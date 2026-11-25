package app

import (
	"strings"
	"sync"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// GrabberFrame is the modal, full-screen "screen grabber" reached via
// Alt+Ins (same as far/far2l). It freezes a snapshot of whatever is
// currently on screen, lets the user drive a text cursor with the
// keyboard, extend a rectangular selection with Shift-modified
// navigation, and copy the selected text to the clipboard on
// Enter / Ctrl+Ins. Esc cancels without touching the clipboard.
//
// A single-cell selection collapses onto the cursor when navigation
// runs without Shift, matching far2l's _reset_area semantics.
type GrabberFrame struct {
	vtui.BaseFrame
	mu      sync.Mutex
	snap    [][]vtui.CharInfo
	snapW   int
	snapH   int
	curX    int
	curY    int
	anchorX int
	anchorY int
	hasSnap bool

	// Held-modifier tracking. Some GUI hosts (e.g. gogpu) don't
	// reliably carry ShiftPressed/etc in ControlKeyState on
	// non-character keys, so we keep our own bookkeeping via
	// VK_SHIFT / VK_CONTROL / VK_MENU key events and OR it with
	// whatever the event's mask reports.
	shiftHeld bool
	ctrlHeld  bool
	altHeld   bool

	// mouseSelecting is true while the left button is held after a
	// press inside the grabber, so motion events extend the
	// rectangular selection until the button is released.
	mouseSelecting bool

	// restoreKeyBar puts back the key bar that belonged to the frame below
	// the grabber. While the grabber is active the key bar must not receive
	// drag motion: it would steal the pointer update and leave the selection
	// rectangle one row short (#1289).
	restoreKeyBar func()
}

var pendingViewerMouseSelection struct {
	frame *viewer.ViewerView
	x, y  int16
}

// NewGrabberFrame constructs an empty grabber. The screen snapshot is
// taken on the first Show(scr) pass — that's the earliest moment we
// see the ScreenBuf with the pre-grabber render already applied by
// the frames beneath us.
func NewGrabberFrame() *GrabberFrame {
	g := &GrabberFrame{}
	g.Modal = true
	g.SetVisible(true)
	w := vtui.FrameManager.GetScreenSize()
	h := vtui.FrameManager.GetScreenHeight()
	g.SetPosition(0, 0, w-1, h-1)
	return g
}

// OpenGrabber pushes a fresh grabber onto the active screen's stack.
// Callers wire this to Alt+Ins in every frame that could hold input
// focus (panel.PanelsFrame, editor.EditorView, viewer.ViewerView, …).
func OpenGrabber() {
	g := NewGrabberFrame()
	// Freeze the screen as the user last saw it. Some frames (the history
	// dialogs) finish painting in FrameManager.OnRender, after every frame's
	// Show and only while they are the top frame, so the pass that shows the
	// grabber would repaint them without that layer (#1237). The buffer keeps
	// the last composed frame until the next render starts.
	if scr := vtui.FrameManager.Screen(); scr != nil {
		g.mu.Lock()
		g.snapshot(scr)
		g.mu.Unlock()
	}
	g.suppressUnderlyingKeyBar()
	vtui.FrameManager.Push(g)
	vtui.FrameManager.Redraw()
}

// handleForcedMouseSelectionEvent turns Shift+left-click anywhere, or an
// unmodified left-click in the text viewer, into a screen grabber gesture.
// Other plain left-clicks keep their normal meaning (including moving a
// window), while Ctrl/Alt combinations remain available to their handlers.
//
// The first press is consumed by FrameManager.EventFilter before regular
// frame dispatch, so the new grabber has to receive that same press after its
// initial snapshot. Later motion/release events are routed through the normal
// FrameManager mouse-capture path.
func handleForcedMouseSelectionEvent(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.MouseEventType {
		return false
	}
	if pendingViewerMouseSelection.frame != nil {
		pending := pendingViewerMouseSelection
		if !e.KeyDown || e.ButtonState&vtinput.FromLeft1stButtonPressed == 0 {
			pendingViewerMouseSelection.frame = nil
		} else if e.MouseEventFlags&vtinput.MouseMoved != 0 &&
			vtui.FrameManager != nil && vtui.FrameManager.Screen() != nil &&
			vtui.FrameManager.GetTopFrame() == pending.frame {
			pendingViewerMouseSelection.frame = nil
			press := *e
			press.MouseX, press.MouseY = pending.x, pending.y
			press.MouseEventFlags = 0
			press.ButtonState = vtinput.FromLeft1stButtonPressed
			openGrabberForMouseSelection(&press, e)
			return true
		}
	}
	if !e.KeyDown || e.MouseEventFlags&vtinput.MouseMoved != 0 ||
		e.ButtonState != vtinput.FromLeft1stButtonPressed {
		return false
	}
	// Let the visible function-key bar keep first refusal. Its mouse handler
	// turns a click into the corresponding (possibly Shift-modified) F-key
	// event; opening the grabber here would make Shift+F10, for example,
	// impossible to invoke with the mouse (#1289).
	if keyBarContainsMouse(e) {
		return false
	}
	if vtui.FrameManager == nil || vtui.FrameManager.Screen() == nil {
		return false
	}

	const modifierMask = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed |
		vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed
	mods := e.ControlKeyState & modifierMask
	shiftSelection := mods == vtinput.ShiftPressed
	if mods == 0 {
		if vv, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok && viewerTextAreaAtMouse(vv, e) {
			pendingViewerMouseSelection.frame = vv
			pendingViewerMouseSelection.x, pendingViewerMouseSelection.y = e.MouseX, e.MouseY
		}
	}
	if !shiftSelection {
		return false
	}
	if _, alreadyOpen := vtui.FrameManager.GetTopFrame().(*GrabberFrame); alreadyOpen {
		return false
	}

	openGrabberForMouseSelection(e, nil)
	return true
}

func openGrabberForMouseSelection(press, motion *vtinput.InputEvent) {
	grabber := NewGrabberFrame()
	vtui.FrameManager.Push(grabber)
	// Push only changes the frame stack. Snapshot and handle the press now so
	// the first drag motion cannot arrive before the selection has an anchor.
	grabber.Show(vtui.FrameManager.Screen())
	grabber.ProcessMouse(press)
	if motion != nil {
		grabber.ProcessMouse(motion)
	}
	vtui.FrameManager.Redraw()
}

// viewerTextAreaAtMouse limits the no-modifier shortcut to the viewer's text
// viewport. Its title bar, scrollbar, and other frames retain their own mouse
// gestures; Shift+drag remains the explicit whole-screen selection gesture.
func viewerTextAreaAtMouse(frame vtui.Frame, e *vtinput.InputEvent) bool {
	vv, ok := frame.(*viewer.ViewerView)
	if !ok || vv.HexMode || vv.DecodeMode || e == nil {
		return false
	}
	x1, y1, x2, y2 := vv.GetPosition()
	return int(e.MouseX) >= x1 && int(e.MouseX) < x2 &&
		int(e.MouseY) > y1 && int(e.MouseY) <= y2
}

// suppressUnderlyingKeyBar keeps the full-screen grabber in charge of every
// mouse event, including motion over the visible function-key row. The
// original bar is restored when the grabber exits, and the guard in the
// restore closure avoids overwriting a bar installed by a different frame.
func (g *GrabberFrame) suppressUnderlyingKeyBar() {
	if vtui.FrameManager == nil {
		return
	}
	if g.restoreKeyBar == nil && vtui.FrameManager.KeyBar != nil {
		previous := vtui.FrameManager.KeyBar
		g.restoreKeyBar = func() {
			if vtui.FrameManager != nil && vtui.FrameManager.KeyBar == nil {
				vtui.FrameManager.KeyBar = previous
			}
		}
	}
	vtui.FrameManager.KeyBar = nil
}

func (g *GrabberFrame) restoreUnderlyingKeyBar() {
	if g.restoreKeyBar != nil {
		g.restoreKeyBar()
		g.restoreKeyBar = nil
	}
}

func keyBarContainsMouse(e *vtinput.InputEvent) bool {
	if e == nil || vtui.FrameManager == nil || vtui.FrameManager.KeyBar == nil ||
		!vtui.FrameManager.KeyBar.IsVisible() {
		return false
	}
	x1, y1, x2, y2 := vtui.FrameManager.KeyBar.GetPosition()
	x, y := int(e.MouseX), int(e.MouseY)
	return x >= x1 && x <= x2 && y >= y1 && y <= y2
}

// actionScreenGrab is the context-aware terminal.App.ScreenGrab handler. Invoking the
// action again while the grabber owns the screen has the same toggle semantics
// as its native Alt+Ins key instead of stacking a second snapshot frame.
func actionScreenGrab() bool {
	if vtui.FrameManager == nil {
		return false
	}
	if grabber, ok := vtui.FrameManager.GetTopFrame().(*GrabberFrame); ok {
		return grabber.ProcessKey(keymap.ParseFarKey("AltIns"))
	}
	OpenGrabber()
	return true
}

// VetoActionKey leaves physical Alt+Ins to GrabberFrame.ProcessKey. macro.MacroManager
// otherwise resolves the Common terminal.App.ScreenGrab action before a focused frame's
// normal key handler gets a chance to close the grabber.
func (g *GrabberFrame) VetoActionKey(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_INSERT {
		return false
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	return alt && !ctrl && !shift
}

// rect returns the normalized selection rectangle in snapshot
// coordinates: left ≤ right, top ≤ bottom, inclusive on both sides.
func (g *GrabberFrame) rect() (l, r, t, b int) {
	l, r = g.anchorX, g.curX
	if l > r {
		l, r = r, l
	}
	t, b = g.anchorY, g.curY
	if t > b {
		t, b = b, t
	}
	return
}

func (g *GrabberFrame) snapshot(scr *vtui.ScreenBuf) {
	w, h := scr.Width(), scr.Height()
	g.snapW, g.snapH = w, h
	g.snap = make([][]vtui.CharInfo, h)
	for y := 0; y < h; y++ {
		row := make([]vtui.CharInfo, w)
		for x := 0; x < w; x++ {
			row[x] = scr.GetCell(x, y)
		}
		g.snap[y] = row
	}
	g.clampCursor()
	g.hasSnap = true
}

func (g *GrabberFrame) clampCursor() {
	if g.snapW == 0 || g.snapH == 0 {
		return
	}
	if g.curX < 0 {
		g.curX = 0
	}
	if g.curY < 0 {
		g.curY = 0
	}
	if g.curX > g.snapW-1 {
		g.curX = g.snapW - 1
	}
	if g.curY > g.snapH-1 {
		g.curY = g.snapH - 1
	}
	if g.anchorX < 0 {
		g.anchorX = 0
	}
	if g.anchorY < 0 {
		g.anchorY = 0
	}
	if g.anchorX > g.snapW-1 {
		g.anchorX = g.snapW - 1
	}
	if g.anchorY > g.snapH-1 {
		g.anchorY = g.snapH - 1
	}
}

// Show paints the grabber overlay. On the first pass we snapshot the
// underlying render, then on every pass we blit the snapshot back
// (covering whatever the underlying frames drew this cycle) and
// invert cells inside the selection rect.
func (g *GrabberFrame) Show(scr *vtui.ScreenBuf) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.suppressUnderlyingKeyBar()

	if !g.hasSnap || scr.Width() != g.snapW || scr.Height() != g.snapH {
		g.snapshot(scr)
		g.SetPosition(0, 0, g.snapW-1, g.snapH-1)
	}

	for y := 0; y < g.snapH; y++ {
		scr.Write(0, y, g.snap[y])
	}

	l, r, t, b := g.rect()
	for y := t; y <= b; y++ {
		for x := l; x <= r; x++ {
			ci := g.snap[y][x]
			ci.Attributes = terminal.InvertAttrColors(ci.Attributes)
			scr.Write(x, y, []vtui.CharInfo{ci})
		}
	}

	scr.SetCursorPos(g.curX, g.curY)
	scr.SetCursorVisible(true)
	scr.SetCursorShape(vtui.CursorShapeBlock)
}

func (g *GrabberFrame) moveCursor(dx, dy int, extend bool) {
	if g.snapW == 0 || g.snapH == 0 {
		return
	}
	nx := g.curX + dx
	ny := g.curY + dy
	if nx < 0 {
		nx = 0
	}
	if ny < 0 {
		ny = 0
	}
	if nx > g.snapW-1 {
		nx = g.snapW - 1
	}
	if ny > g.snapH-1 {
		ny = g.snapH - 1
	}
	g.curX, g.curY = nx, ny
	if !extend {
		g.anchorX, g.anchorY = nx, ny
	}
}

func (g *GrabberFrame) jump(x, y int, extend bool) {
	if g.snapW == 0 || g.snapH == 0 {
		return
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x > g.snapW-1 {
		x = g.snapW - 1
	}
	if y > g.snapH-1 {
		y = g.snapH - 1
	}
	g.curX, g.curY = x, y
	if !extend {
		g.anchorX, g.anchorY = x, y
	}
}

// copyText extracts the current selection from the snapshot: one
// screen row per output line, trailing spaces trimmed, wide-char
// filler cells skipped so CJK/emoji don't emit stray U+FFFF runes.
func (g *GrabberFrame) copyText() string {
	l, r, t, b := g.rect()
	var sb strings.Builder
	for y := t; y <= b; y++ {
		var line strings.Builder
		for x := l; x <= r; x++ {
			line.WriteString(vtui.CellString(g.snap[y][x].Char))
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		if y < b {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func (g *GrabberFrame) copyAndExit() {
	text := g.copyText()
	if text != "" {
		// vtui.SetClipboard can take up to ~4s when Far2lEnabled=true
		// (two 2s waits for IPC replies) and can block for a similar
		// stretch when it shells out to wl-copy/xclip on Linux. Do
		// the write off the UI goroutine so the grabber closes
		// instantly and the app stays responsive; the clipboard
		// eventually settles in the background.
		terminal.SetClipboardAsync(text)
	}
	g.restoreUnderlyingKeyBar()
	g.SetExitCode(1)
}

func (g *GrabberFrame) cancel() {
	g.restoreUnderlyingKeyBar()
	g.SetExitCode(-1)
}

func (g *GrabberFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	// Track modifier hold state ourselves — gui hosts sometimes
	// omit ShiftPressed/etc from ControlKeyState on non-character
	// key events, and X11-based hosts deliver the modifier as its
	// left/right VK variant rather than the generic one.
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT:
		g.shiftHeld = e.KeyDown
		return true
	case vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL:
		g.ctrlHeld = e.KeyDown
		return true
	case vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU:
		g.altHeld = e.KeyDown
		return true
	}

	if !e.KeyDown {
		return true
	}

	ctrl := g.ctrlHeld || (e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0
	shift := g.shiftHeld || (e.ControlKeyState&vtinput.ShiftPressed) != 0
	alt := g.altHeld || (e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed)) != 0

	// Alt+Ins on an already-open grabber toggles it off.
	if e.VirtualKeyCode == vtinput.VK_INSERT && alt {
		g.cancel()
		vtui.FrameManager.Redraw()
		return true
	}

	// Big-step deltas match far2l: Ctrl+←/→ = ±10 cols, Ctrl+↑/↓ = ±5 rows.
	const bigX, bigY = 10, 5

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE:
		g.cancel()
	case vtinput.VK_LEFT:
		if ctrl {
			g.moveCursor(-bigX, 0, shift)
		} else {
			g.moveCursor(-1, 0, shift)
		}
	case vtinput.VK_RIGHT:
		if ctrl {
			g.moveCursor(bigX, 0, shift)
		} else {
			g.moveCursor(1, 0, shift)
		}
	case vtinput.VK_UP:
		if ctrl {
			g.moveCursor(0, -bigY, shift)
		} else {
			g.moveCursor(0, -1, shift)
		}
	case vtinput.VK_DOWN:
		if ctrl {
			g.moveCursor(0, bigY, shift)
		} else {
			g.moveCursor(0, 1, shift)
		}
	case vtinput.VK_HOME:
		if ctrl {
			g.jump(0, 0, shift)
		} else {
			g.jump(0, g.curY, shift)
		}
	case vtinput.VK_END:
		if ctrl {
			g.jump(g.snapW-1, g.snapH-1, shift)
		} else {
			g.jump(g.snapW-1, g.curY, shift)
		}
	case vtinput.VK_PRIOR:
		g.jump(g.curX, 0, shift)
	case vtinput.VK_NEXT:
		g.jump(g.curX, g.snapH-1, shift)
	case vtinput.VK_A:
		g.anchorX, g.anchorY = 0, 0
		g.curX, g.curY = g.snapW-1, g.snapH-1
	case vtinput.VK_U:
		g.anchorX, g.anchorY = g.curX, g.curY
	case vtinput.VK_RETURN:
		g.copyAndExit()
	case vtinput.VK_INSERT:
		if ctrl {
			g.copyAndExit()
		}
	}
	vtui.FrameManager.Redraw()
	return true
}

// ProcessMouse drives the grabber's rectangular selection with the
// pointer. Pressing the left button anchors the selection, dragging
// with the button held extends the rectangle, and releasing the left
// button stops the selection, leaving the highlighted rectangle in
// place so it can be copied with Enter / Ctrl+Ins like any
// keyboard-driven selection.
func (g *GrabberFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	// Wheel events are not used by the grabber.
	if e.WheelDirection != 0 {
		return true
	}
	if g.snapW == 0 || g.snapH == 0 {
		return true
	}

	x, y := int(e.MouseX), int(e.MouseY)
	leftDown := e.ButtonState&vtinput.FromLeft1stButtonPressed != 0

	// While selecting, pointer motion extends the rectangle. Check
	// this before the release test so a drag reported without the
	// button bit (some backends) still extends instead of ending.
	if g.mouseSelecting && e.MouseEventFlags&vtinput.MouseMoved != 0 {
		g.jump(x, y, true)
		vtui.FrameManager.Redraw()
		return true
	}

	// Releasing the left button — or any event without the button
	// held — stops the selection, freezing the highlighted rectangle
	// until the next press. This makes the area stay fixed through
	// any later mouse movement.
	if g.mouseSelecting && (!e.KeyDown || !leftDown) {
		g.mouseSelecting = false
		vtui.FrameManager.Redraw()
		return true
	}

	// Button press: anchor the selection (or extend on re-press).
	if leftDown {
		g.jump(x, y, g.mouseSelecting)
		g.mouseSelecting = true
		vtui.FrameManager.Redraw()
	}
	return true
}

func (g *GrabberFrame) GetType() vtui.FrameType                { return vtui.TypeUser + 4 }
func (g *GrabberFrame) GetTitle() string                       { return "Screen Grabber" }
func (g *GrabberFrame) IsModal() bool                          { return true }
func (g *GrabberFrame) HasShadow() bool                        { return false }
func (g *GrabberFrame) HandleCommand(cmd int, args any) bool   { return false }
func (g *GrabberFrame) HandleBroadcast(cmd int, args any) bool { return false }
func (g *GrabberFrame) Valid(cmd int) bool                     { return true }
func (g *GrabberFrame) GetKeyLabels() *vtui.KeySet             { return nil }

func (g *GrabberFrame) ResizeConsole(w, h int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.SetPosition(0, 0, w-1, h-1)
	// Force resnapshot on next Show — the frozen buffer no longer
	// matches the physical screen.
	g.hasSnap = false
}
