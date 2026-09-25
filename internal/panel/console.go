package panel

import (
	"fmt"
	"os"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// mutedPTY wraps a terminal.PtyBackend to silence automated parser and terminal responses
// (such as CPR, DSR, DA, OSC 52, far2l APC) while mirroring in terminal.ShellModeHost.
// The host terminal provides real responses, so the internal mirror must stay mute.
// Note: mutedPTY intentionally does NOT implement terminal.PtyPixelSizer.
type MutedPTY struct {
	Backend terminal.PtyBackend
}

func (m MutedPTY) Read(p []byte) (int, error)            { return m.Backend.Read(p) }
func (m MutedPTY) Write(p []byte) (int, error)           { return len(p), nil }
func (m MutedPTY) Close() error                          { return m.Backend.Close() }
func (m MutedPTY) SetSize(cols, rows int)                { m.Backend.SetSize(cols, rows) }
func (m MutedPTY) Wait() error                           { return m.Backend.Wait() }
func (m MutedPTY) Run(name string, args ...string) error { return m.Backend.Run(name, args...) }
func (m MutedPTY) IsBusy() bool                          { return m.Backend.IsBusy() }

func (pf *PanelsFrame) IsHostConsoleActive() bool {
	pf.hostConsoleMu.Lock()
	defer pf.hostConsoleMu.Unlock()
	return pf.HostConsoleActive
}

// SetBusy sets the frame's busy state to suppress rendering during host console operations.
func (pf *PanelsFrame) SetBusy(busy bool) {
	pf.Busy = busy
}

// unpaintedTerminalRows reports the rows between the bottom of the terminal
// view and the bottom of the screen that nothing draws while the panels are
// hidden. The layout reserves the keybar row for the whole life of the shell
// so that starting and ending a command does not resize the PTY, but the
// keybar and the command line both stand down while an alternate-screen
// program or a running command owns the terminal -- and whatever is left
// unpainted is filled by vtui's Desktop, whose blue background then reads as
// a stripe below the program's output. The range is empty when one of them
// will paint the row, or when the terminal already reaches the last row.
func unpaintedTerminalRows(altScreen, busy bool, termY2, screenH int) (int, int) {
	if !altScreen && !busy {
		return 0, -1
	}
	first := termY2 + 1
	if first < 0 {
		first = 0
	}
	return first, screenH - 1
}

// consoleStyle returns the console view style effective for this frame.
func (pf *PanelsFrame) consoleStyle() string {
	return terminal.ConsoleViewStyleFor(pf.ShellMode)
}

// overlayLines returns the number of bottom rows reserved for the f4 overlay (0, 1, or 2).
func (pf *PanelsFrame) OverlayLines() int {
	if pf.consoleStyle() != terminal.ConsoleViewFar {
		return 0
	}
	n := 1 // CommandLine
	if pf.ShowKeyBar {
		n++
	}
	return n
}

// updateConsoleOverlayModifiers keeps the manually rendered keybar in sync
// with keyboard events after DrawConsoleOverlay unregisters FrameManager.KeyBar.
// Some terminal hosts report the modifier bit one event late (or omit it on a
// standalone modifier event), so the modifier key's own VK and KeyDown state
// take precedence over ControlKeyState.
func (pf *PanelsFrame) updateConsoleOverlayModifiers(e *vtinput.InputEvent) {
	if e == nil {
		return
	}
	if e.Type == vtinput.FocusEventType {
		pf.consoleOverlayShift = false
		pf.consoleOverlayCtrl = false
		pf.consoleOverlayAlt = false
		return
	}
	if e.Type != vtinput.KeyEventType {
		return
	}

	pf.consoleOverlayShift = e.ControlKeyState&vtinput.ShiftPressed != 0
	pf.consoleOverlayCtrl = e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	pf.consoleOverlayAlt = e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT:
		pf.consoleOverlayShift = e.KeyDown
	case vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL:
		pf.consoleOverlayCtrl = e.KeyDown
	case vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU:
		pf.consoleOverlayAlt = e.KeyDown
	}
}

// consoleOverlayLabels selects the same precedence as vtui.KeyBar: Shift,
// then Ctrl, then Alt, then the unmodified row.
func consoleOverlayLabels(labels *vtui.KeySet, shift, ctrl, alt bool) vtui.KeyBarLabels {
	if labels == nil {
		return vtui.KeyBarLabels{}
	}
	if shift {
		return labels.Shift
	}
	if ctrl {
		return labels.Ctrl
	}
	if alt {
		return labels.Alt
	}
	return labels.Normal
}

// overlayKeybarSlots lays the keybar out exactly the way vtui.KeyBar does, so
// the console overlay and the panel keybar agree on slot width and label
// truncation. The overlay used to hardcode five columns per label, which is why
// it showed "RenMo" where the real keybar had room for "Rename or move".
// Column math only, no drawing: unit-testable and shared by both emitters.
func OverlayKeybarSlots(labels vtui.KeyBarLabels, width int) []terminal.OverlayKeySlot {
	if width <= 0 {
		return nil
	}
	slotWidth := width / 12
	if slotWidth < 3 {
		slotWidth = 3
	}
	slots := make([]terminal.OverlayKeySlot, 0, 12)
	for i := 0; i < 12; i++ {
		x := i * slotWidth
		if x > width-1 {
			break
		}
		num := fmt.Sprintf("%d", i+1)
		labelX := x + len([]rune(num))
		labelW := slotWidth - len([]rune(num)) - 1
		if i == 11 {
			// The last slot swallows the rounding remainder, as in vtui.
			labelW = width - labelX
		}
		if available := width - labelX; labelW > available {
			labelW = available
		}
		if labelW < 0 {
			labelW = 0
		}
		label := []rune(labels[i])
		if len(label) > labelW {
			label = label[:labelW]
		}
		slots = append(slots, terminal.OverlayKeySlot{
			Col:   x,
			Num:   num,
			Label: fmt.Sprintf("%-*s", labelW, string(label)),
		})
	}
	return slots
}

// buildConsoleOverlayContent collects the overlay text from the live UI state.
func (pf *PanelsFrame) buildConsoleOverlayContent() terminal.ConsoleOverlayContent {
	ov := terminal.ConsoleOverlayContent{Lines: pf.OverlayLines()}

	var sb strings.Builder
	for _, ci := range pf.BuildPrompt() {
		sb.WriteString(vtui.CellString(ci.Char))
	}
	if pf.CmdLine != nil && pf.CmdLine.Edit != nil {
		sb.WriteString(pf.CmdLine.Edit.GetText())
	}
	ov.Cmd = sb.String()
	// Edit keeps its caret position private, so the cursor is parked at the end
	// of the typed text. Exact placement needs an accessor in vtui.
	ov.CursorCol = len([]rune(ov.Cmd))

	if pf.ShowKeyBar && ov.Lines >= 2 {
		if labels := pf.GetKeyLabels(); labels != nil {
			active := consoleOverlayLabels(labels, pf.consoleOverlayShift, pf.consoleOverlayCtrl, pf.consoleOverlayAlt)
			ov.Keys = OverlayKeybarSlots(active, pf.LastW)
		}
	}

	if vtui.FrameManager != nil {
		if ac, ok := vtui.FrameManager.GetTopFrame().(*vtui.AutoCompleteMenu); ok && ac != nil && ac.HasMatches() {
			x1, y1, x2, y2 := ac.GetPosition()
			ov.Popup = &terminal.OverlayPopupContent{
				X:         x1,
				Y:         y1,
				Width:     x2 - x1 + 1,
				Height:    y2 - y1 + 1,
				SelectPos: ac.SelectPos(),
				Items:     ac.Matches,
			}
		}
	}
	return ov
}

// ConsoleOverlayUsesWinAPI reports whether the overlay must be painted with the
// Windows Console API instead of ANSI: under the winapi renderer the visible
// screen buffer is not a VT stream and escape sequences would land in it as
// literal text.
func ConsoleOverlayUsesWinAPI() bool {
	if terminal.SelectedTTYBackend != "winapi" && terminal.SelectedTTYBackend != "win32" {
		return false
	}
	return terminal.WinConsoleOverlayAvailable()
}

// consoleViewActive reports whether the frame currently shows a console view
// (host console with a live shell, or the no-terminal.PTY console) rather than panels.
func (pf *PanelsFrame) ConsoleViewActive() bool {
	switch pf.ShellMode {
	case terminal.ShellModeHost:
		return pf.IsHostConsoleActive()
	case terminal.ShellModeSimpleInline:
		return !pf.ShowPanels
	}
	return false
}

// isTopFrame reports whether pf itself is the frame currently on top of the
// active screen's stack. consoleViewActive() alone tracks only pf.showPanels,
// which does NOT flip when some other frame -- an editor or viewer opened via
// F3/F4 while the console view is showing, a dialog -- gets pushed on top
// of pf. We also treat an active AutoCompleteMenu as part of the panels
// frame if it is directly on top of it, so the overlay isn't frozen while
// autocomplete hints are visible during typing.
func (pf *PanelsFrame) IsTopFrame() bool {
	if vtui.FrameManager == nil || len(vtui.FrameManager.Screens) == 0 {
		return false
	}
	idx := vtui.FrameManager.ActiveIdx
	if idx < 0 || idx >= len(vtui.FrameManager.Screens) {
		return false
	}
	frames := vtui.FrameManager.Screens[idx].Frames
	if len(frames) == 0 {
		return false
	}
	top := frames[len(frames)-1]
	if top == vtui.Frame(pf) {
		return true
	}
	if _, ok := top.(*vtui.AutoCompleteMenu); ok {
		if len(frames) >= 2 && frames[len(frames)-2] == vtui.Frame(pf) {
			return true
		}
	}
	return false
}

// drawConsoleOverlay paints the f4 command line and keybar over the console.
func (pf *PanelsFrame) DrawConsoleOverlay() {
	if pf.OverlayLines() == 0 || !pf.ConsoleViewActive() {
		return
	}
	// zoin-bot: the console overlay owns the physical bottom rows; leaving the
	// frame-manager keybar registered would repaint a second copy afterwards.
	pf.suppressFrameManagerKeyBar()
	ov := pf.buildConsoleOverlayContent()
	// The single most useful line in a Wine bug report: which emitter ran and
	// what geometry it believed in. "Overlay drew at the top of the window"
	// is either a wrong pf.lastH (ansi) or a wrong srWindow (winapi), and
	// this tells the two apart without guessing.
	p := terminal.ProbeConsole()
	vtui.DebugLog("OVERLAY: backend=%q winapi=%v lastW=%d lastH=%d lines=%d keys=%d csbi=%v win=%dx%d",
		terminal.SelectedTTYBackend, ConsoleOverlayUsesWinAPI(), pf.LastW, pf.LastH, ov.Lines, len(ov.Keys),
		p.OK, p.WinCols(), p.WinRows())
	if ConsoleOverlayUsesWinAPI() {
		terminal.WinDrawConsoleOverlay(ov)
		return
	}
	pf.emitAnsiConsoleOverlay(ov)
}

// zoin-bot: suppressFrameManagerKeyBar prevents the normal ScreenBuf renderer from
// drawing a second keybar while the console overlay paints directly to the
// host terminal. The next panel redraw registers it again when appropriate.
func (pf *PanelsFrame) suppressFrameManagerKeyBar() {
	if vtui.FrameManager != nil && vtui.FrameManager.KeyBar == pf.KeyBar {
		vtui.FrameManager.KeyBar = nil
	}
}

// clearConsoleOverlay takes the overlay off the screen and gives the cursor back
// to the spot where the previous output ended. Without it, a command's output
// scrolls a copy of the command line into the console history.
func (pf *PanelsFrame) clearConsoleOverlay() {
	n := pf.OverlayLines()
	if n == 0 {
		return
	}
	if ConsoleOverlayUsesWinAPI() {
		terminal.WinClearConsoleOverlay(n)
		return
	}
	h := pf.LastH
	if h <= 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("\x1b7")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "\x1b[%d;1H\x1b[0m\x1b[2K", h-n+1+i)
	}
	sb.WriteString("\x1b8")
	vtui.WritePassthrough([]byte(sb.String()))
}

// drawHostConsoleOverlay is kept as the name used by the host console call sites.
func (pf *PanelsFrame) drawHostConsoleOverlay() {
	pf.DrawConsoleOverlay()
}

// emitAnsiConsoleOverlay renders the overlay directly to the host terminal
// using minimal ANSI escape sequences without involving ScreenBuf.
func (pf *PanelsFrame) emitAnsiConsoleOverlay(ov terminal.ConsoleOverlayContent) {
	h := pf.LastH
	if h <= 0 {
		return
	}
	n := ov.Lines
	cmdRow := h - n + 1 // 1-based row index

	var sb strings.Builder
	// 1. Save cursor position
	sb.WriteString("\x1b7")

	// 2. Draw CommandLine
	fmt.Fprintf(&sb, "\x1b[%d;1H\x1b[0m\x1b[2K", cmdRow)
	sb.WriteString(ov.Cmd)

	// 3. Draw KeyBar if visible
	if len(ov.Keys) > 0 {
		fmt.Fprintf(&sb, "\x1b[%d;1H\x1b[0m\x1b[2K", h)
		for _, k := range ov.Keys {
			// Slots carry their own start column now: rounding leftovers make
			// the widths uneven, so walking them by concatenation would drift.
			fmt.Fprintf(&sb, "\x1b[%d;%dH", h, k.Col+1)
			// Matches vtui's real KeyBar palette (ColKeyBarNum/ColKeyBarText:
			// "LightGray on DarkGray / DarkGray on Teal"). This used to be
			// swapped, which made the overlay look like a different, alien
			// keybar sitting next to the real one instead of matching it.
			fmt.Fprintf(&sb, "\x1b[0;37;40m%s\x1b[0;30;46m%s", k.Num, k.Label)
		}
	}

	// 4. Restore cursor position and visibility
	sb.WriteString("\x1b[0m\x1b8")

	// Without a terminal.PTY there is no shell to own the cursor, so f4 parks it in its
	// own command line: that blinking caret is what tells the user the console
	// is waiting for a command rather than hung.
	if pf.ShellMode == terminal.ShellModeSimpleInline {
		fmt.Fprintf(&sb, "\x1b[%d;%dH\x1b[?25h", cmdRow, ov.CursorCol+1)
	}
	vtui.WritePassthrough([]byte(sb.String()))
}

// syncAutoCompleteSuppression keeps CommandLine.AutoCompleteSuppressed in
// step with where the popup can actually be drawn safely. terminal.WinDrawConsoleOverlay
// (console_overlay_windows.go) paints AutoCompleteMenu directly with the
// Windows Console API, so it's safe there. The ANSI console-view path has no
// such renderer yet -- pushing the menu there would go through vtui's normal
// full-frame Show(), which is exactly the leak documented in WINE.md
// §2c/§2j.5 (one frame of panels/keybar flashed onto the live console).
// Suppress only in that gap; panels mode and the winapi console view are
// unaffected.
func (pf *PanelsFrame) syncAutoCompleteSuppression() {
	if pf.CmdLine == nil {
		return
	}
	pf.CmdLine.AutoCompleteSuppressed = pf.ConsoleViewActive() && !ConsoleOverlayUsesWinAPI()
}

// handleHostConsoleTab completes a TAB in the f4-owned command line before it
// can be forwarded to the shell behind a host-console overlay. The normal
// autocomplete menu is safe on the WinAPI overlay because it has a native
// renderer. ANSI host consoles cannot repaint a popup over the shell's screen
// without a readable backing buffer, so they accept the first selectable item
// directly and redraw only the overlay.
func (pf *PanelsFrame) handleHostConsoleTab(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_TAB {
		return false
	}
	if e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed|vtinput.ShiftPressed) != 0 {
		return false
	}
	if pf.CmdLine == nil || pf.CmdLine.IsEmpty() || !config.App.CommandLineAutoComplete || vtui.FrameManager == nil {
		return false
	}

	if ac, ok := vtui.FrameManager.GetTopFrame().(*vtui.AutoCompleteMenu); ok && ac != nil {
		if ac.Edit != pf.CmdLine.Edit || !ac.HasMatches() {
			return false
		}
		ac.ProcessKey(e)
		pf.drawHostConsoleOverlay()
		return true
	}

	ac := vtui.NewAutoCompleteMenu(pf.CmdLine.Edit)
	if !ac.HasMatches() {
		return false
	}
	if ConsoleOverlayUsesWinAPI() {
		vtui.FrameManager.Push(ac)
	} else {
		// There is no safe ANSI readback for restoring the shell output under a
		// popup. Complete the same selected item without painting the popup.
		ac.ProcessKey(e)
	}
	pf.drawHostConsoleOverlay()
	return true
}

// enterHostConsole switches the physical terminal to the primary screen and activates
// live passthrough of terminal.PTY output directly to the host console.
func (pf *PanelsFrame) EnterHostConsole() {
	if pf.ShellMode != terminal.ShellModeHost {
		return
	}
	pf.hostConsoleMu.Lock()
	if pf.HostConsoleActive {
		pf.hostConsoleMu.Unlock()
		return
	}
	pf.HostConsoleActive = true
	pf.hostConsoleMu.Unlock()
	pf.syncAutoCompleteSuppression()

	pf.SetBusy(true)
	pf.suppressFrameManagerKeyBar()
	vtui.SetAltScreen(false)

	n := pf.OverlayLines()
	if n > 0 && pf.LastH > n {
		scrollBottom := pf.LastH - n
		vtui.WritePassthrough([]byte(fmt.Sprintf("\x1b[1;%dr", scrollBottom)))
		pf.drawHostConsoleOverlay()
	}
}

// leaveHostConsole restores the alternate screen buffer and returns visual control to f4 panels.
func (pf *PanelsFrame) LeaveHostConsole() {
	if pf.ShellMode != terminal.ShellModeHost {
		return
	}
	pf.hostConsoleMu.Lock()
	if !pf.HostConsoleActive {
		pf.hostConsoleMu.Unlock()
		return
	}
	pf.HostConsoleActive = false
	pf.resetHostConsoleReplyState()
	pf.hostConsoleMu.Unlock()
	pf.syncAutoCompleteSuppression()

	// A host-console application can receive a mouse-down record without a
	// matching release reaching f4 (notably through native Windows console
	// input). FrameManager keeps routing all later mouse events to the frame
	// that handled that down event until it sees a release. Queue a neutral
	// release while returning control to the panels so a stale capture cannot
	// block the menu bar or modal dialogs.
	if vtui.FrameManager != nil {
		vtui.FrameManager.PostEvent(vtinput.InputEvent{
			Type:        vtinput.MouseEventType,
			MouseX:      -1,
			MouseY:      -1,
			KeyDown:     false,
			ButtonState: 0,
		})
	}

	// Protective reset sequence to clean up any terminal modes left by child applications
	var resetSeq strings.Builder
	if pf.TermView != nil && pf.TermView.UseAltScreen {
		resetSeq.WriteString("\x1b[?1049l")
	}
	// In Windows Terminal, sending VT mouse tracking disable sequences
	// (1000/1002/1003/1006) before switching back to the alt screen
	// confuses WT's internal input routing: mouse buttons get swapped
	// and the Shift modifier gets stuck.  Skip them when running inside
	// WT; the basic resets (scroll region, attributes, cursor) are safe.
	if os.Getenv("WT_SESSION") == "" {
		resetSeq.WriteString("\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l")
	}
	resetSeq.WriteString("\x1b[r\x1b[0m\x1b[?25h")
	vtui.WritePassthrough([]byte(resetSeq.String()))

	vtui.SetAltScreen(true)

	// The reset above only undoes what f4 sent. Whatever ran in the host
	// console spoke to the terminal directly, and the mouse tracking it (or,
	// on Windows, the console host on its behalf) turned off stays off until
	// somebody asks for it again -- so f4 asks here, before the redraw, and
	// gets its clicks back.
	restoreHostInputModes()

	if vtui.FrameManager != nil && vtui.FrameManager.Screen() != nil {
		vtui.FrameManager.Screen().HardReset()
	}
	pf.SetBusy(false)
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// HostConsoleLogFallback selects the data source for F3/F4 (Terminal.ViewLog/
// EditLog). pf.termView only ever sees bytes that came through a real terminal.PTY;
// under terminal.ShellModeSimpleInline (issue #513 / WINE.md -- Wine has no usable
// ConPTY) commands run with inherited stdio straight into the host console
// buffer, never touching termView, so its log is permanently empty there.
// Read the host console buffer itself instead. Every other shell mode keeps
// using termView exactly as before (nil here).
func (pf *PanelsFrame) HostConsoleLogFallback() func() []byte {
	if pf.ShellMode != terminal.ShellModeSimpleInline || !ConsoleOverlayUsesWinAPI() {
		return nil
	}
	lines := pf.OverlayLines()
	return func() []byte { return terminal.ReadHostConsoleFullText(lines) }
}
