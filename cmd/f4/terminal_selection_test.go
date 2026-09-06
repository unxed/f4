package main

import (
	"testing"
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// seedRow writes a plain ASCII string into tv.Lines[row] starting at
// column 0. Preserves existing right-side padding.
func seedRow(tv *TerminalView, row int, text string) {
	attr := DefaultTermAttr
	for i, r := range text {
		if i >= tv.Width {
			return
		}
		tv.Lines[row][i] = vtui.CharInfo{Char: testUint64Rune(r), Attributes: attr}
	}
}

func newSelectableTV(w, h int) *TerminalView {
	tv := NewTerminalView(w, h)
	tv.SetPosition(0, 0, w-1, h-1)
	return tv
}

func TestTerminalSelection_ExtractStreamSingleRow(t *testing.T) {
	tv := newSelectableTV(40, 6)
	defer tv.Close()
	seedRow(tv, 0, "hello world")

	tv.StartSelection(0, 0, false)
	tv.ExtendSelection(4, 0)

	if got := tv.ExtractSelection(); got != "hello" {
		t.Errorf("single-row stream: got %q, want %q", got, "hello")
	}
}

func TestTerminalSelection_ExtractStreamMultiRow(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	seedRow(tv, 0, "line one")
	seedRow(tv, 1, "line two")
	seedRow(tv, 2, "line three")

	// Start mid-row 0, end mid-row 2 — stream picks up first row's
	// tail, all of row 1, and row 2's head.
	tv.StartSelection(5, 0, false)
	tv.ExtendSelection(3, 2)

	got := tv.ExtractSelection()
	want := "one\nline two\nline"
	if got != want {
		t.Errorf("multi-row stream:\n got %q\nwant %q", got, want)
	}
}

func TestTerminalSelection_ExtractBlock(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	seedRow(tv, 0, "abcdefghij")
	seedRow(tv, 1, "0123456789")
	seedRow(tv, 2, "ABCDEFGHIJ")

	tv.StartSelection(2, 0, true) // block
	tv.ExtendSelection(4, 2)

	got := tv.ExtractSelection()
	want := "cde\n234\nCDE"
	if got != want {
		t.Errorf("block: got %q, want %q", got, want)
	}
}

func TestTerminalSelection_SelectWordAt(t *testing.T) {
	tv := newSelectableTV(40, 6)
	defer tv.Close()
	seedRow(tv, 0, "foo bar baz")

	tv.SelectWordAt(5, 0) // click on the 'a' in "bar"
	if got := tv.ExtractSelection(); got != "bar" {
		t.Errorf("word at 'bar': got %q, want %q", got, "bar")
	}

	tv.SelectWordAt(3, 0) // click on space — must not activate
	if got := tv.ExtractSelection(); got != "bar" {
		t.Errorf("clicking whitespace should leave prior selection intact, got %q", got)
	}
}

func TestTerminalSelection_SelectLineAt(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	seedRow(tv, 1, "hello    ") // padded

	tv.SelectLineAt(1)
	if got := tv.ExtractSelection(); got != "hello" {
		t.Errorf("line select trims trailing spaces: got %q, want %q", got, "hello")
	}
}

func TestTerminalSelection_ClearAndEmpty(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	seedRow(tv, 0, "hello")

	if tv.HasSelection() {
		t.Fatal("fresh TerminalView shouldn't have a selection")
	}
	tv.StartSelection(0, 0, false)
	if !tv.HasSelection() {
		t.Fatal("HasSelection should report true after StartSelection")
	}
	if !tv.SelectionIsEmpty() {
		t.Fatal("SelectionIsEmpty should be true when start==end (bare click)")
	}
	tv.ExtendSelection(4, 0)
	if tv.SelectionIsEmpty() {
		t.Fatal("SelectionIsEmpty should be false after extending")
	}
	tv.ClearSelection()
	if tv.HasSelection() {
		t.Fatal("ClearSelection should drop the selection")
	}
}

func TestTerminalSelection_ClearedWhenScreenLifecycleChanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(*TerminalView)
	}{
		{
			name: "full erase",
			change: func(tv *TerminalView) {
				tv.EraseDisplay(2, DefaultTermAttr)
			},
		},
		{
			name: "reset",
			change: func(tv *TerminalView) {
				tv.ResetBuffer(20, 6)
			},
		},
		{
			name: "resize",
			change: func(tv *TerminalView) {
				tv.Resize(21, 7)
			},
		},
		{
			name: "move",
			change: func(tv *TerminalView) {
				tv.SetPosition(1, 0, 20, 5)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tv := newSelectableTV(20, 6)
			defer tv.Close()
			tv.StartSelection(1, 1, false)
			tv.ExtendSelection(4, 1)

			test.change(tv)
			if tv.HasSelection() {
				t.Fatalf("selection survived %s", test.name)
			}
		})
	}

	t.Run("alternate screen", func(t *testing.T) {
		tv := newSelectableTV(20, 6)
		defer tv.Close()
		tv.StartSelection(1, 1, false)
		tv.ExtendSelection(4, 1)
		tv.SetAltScreen(true)
		if tv.HasSelection() {
			t.Fatal("selection survived entering the alternate screen")
		}

		tv.StartSelection(1, 1, false)
		tv.ExtendSelection(4, 1)
		tv.SetAltScreen(false)
		if tv.HasSelection() {
			t.Fatal("selection survived returning to the primary screen")
		}
	})
}

func TestTerminalSelection_HighlightInvertsCells(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	seedRow(tv, 0, "hello world")

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(20, 6)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	tv.SetVisible(true)
	tv.StartSelection(2, 0, false)
	tv.ExtendSelection(4, 0)
	tv.Show(scr)

	baseFG := vtui.GetRGBFore(DefaultTermAttr)
	baseBG := vtui.GetRGBBack(DefaultTermAttr)
	for x := 2; x <= 4; x++ {
		attr := scr.GetCell(x, 0).Attributes
		if vtui.GetRGBFore(attr) != baseBG || vtui.GetRGBBack(attr) != baseFG {
			t.Errorf("cell (%d,0) not inverted: fg=%06x bg=%06x, want fg=%06x bg=%06x",
				x, vtui.GetRGBFore(attr), vtui.GetRGBBack(attr), baseBG, baseFG)
		}
	}
	if attr := scr.GetCell(1, 0).Attributes; vtui.GetRGBFore(attr) == baseBG {
		t.Error("cell (1,0) outside selection should not be inverted")
	}
	if attr := scr.GetCell(5, 0).Attributes; vtui.GetRGBFore(attr) == baseBG {
		t.Error("cell (5,0) outside selection should not be inverted")
	}
}

func TestTerminalSelection_InTerminalArea(t *testing.T) {
	tv := newSelectableTV(20, 6)
	defer tv.Close()
	tv.SetPosition(10, 5, 29, 10) // move it off origin

	cases := []struct {
		x, y int
		want bool
	}{
		{10, 5, true},
		{29, 10, true},
		{9, 5, false},
		{30, 10, false},
		{20, 4, false},
		{20, 11, false},
	}
	for _, c := range cases {
		if got := tv.InTerminalArea(c.x, c.y); got != c.want {
			t.Errorf("InTerminalArea(%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

// panelsFrameWithMouseSelect returns a PanelsFrame configured for
// hidden-panels terminal-selection tests: 80x25 grid, no mouse
// tracking, no altScreen, a live termView, and a fake PTY.
func panelsFrameWithMouseSelect(t *testing.T) (*PanelsFrame, *fakePTY) {
	t.Helper()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))
	pf.showPanels = false
	pf.termView.SetPosition(0, 0, 79, 22)
	pf.termView.clipboardWriter = func(string) {}
	// zoin-bot: these tests exercise the terminal/PTy mouse path. A visible
	// f4 command line has its own paste path and is covered separately below.
	pf.cmdLine.SetVisible(false)
	pty := &fakePTY{}
	pf.pty = pty
	return pf, pty
}

type fakePTY struct {
	writes []byte
	busy   bool // reported by IsBusy; a busy PTY receives raw keys
}

func (p *fakePTY) Read(b []byte) (int, error)            { return 0, nil }
func (p *fakePTY) Write(b []byte) (int, error)           { p.writes = append(p.writes, b...); return len(b), nil }
func (p *fakePTY) Close() error                          { return nil }
func (p *fakePTY) SetSize(cols, rows int)                {}
func (p *fakePTY) Wait() error                           { return nil }
func (p *fakePTY) Run(name string, args ...string) error { return nil }
func (p *fakePTY) IsBusy() bool                          { return p.busy }

// TestPanelsFrame_TerminalMouseSelect_Drag exercises the full click →
// drag → release pipeline through PanelsFrame.ProcessMouse. Asserts
// on selection state; the release-time clipboard write happens on a
// goroutine and depends on external tools (xclip / wl-copy / far2l
// IPC), so we don't assert on GetClipboard here — that path is
// covered separately in the vtui clipboard tests.
func TestPanelsFrame_TerminalMouseSelect_Drag(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	seedRow(tv, 0, "hello world")

	// LMB down at (2, 0)
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      2, MouseY: 0,
	})
	if !tv.HasSelection() {
		t.Fatal("expected HasSelection=true after LMB down")
	}
	if !pf.termSelDragging {
		t.Fatal("expected termSelDragging=true after LMB down inside terminal area")
	}
	// Drag to (6, 0)
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
		MouseX:          6, MouseY: 0,
	})
	if got := tv.ExtractSelection(); got != "llo w" {
		t.Errorf("after drag: got %q, want %q", got, "llo w")
	}

	// Release button — drag flag drops, selection stays (xterm-style).
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		ButtonState: 0,
		MouseX:      6, MouseY: 0,
	})
	if pf.termSelDragging {
		t.Fatal("release should clear termSelDragging")
	}
	if !tv.HasSelection() {
		t.Fatal("release should keep the highlight (xterm-style)")
	}
	if got := tv.ExtractSelection(); got != "llo w" {
		t.Errorf("post-release extract: got %q, want %q", got, "llo w")
	}
}

func TestPanelsFrame_TerminalMouseSelect_KeyDownClearsHighlight(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	tv.StartSelection(2, 0, false)
	tv.ExtendSelection(6, 0)

	pf.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'x',
	})
	if tv.HasSelection() {
		t.Fatal("a key-down should clear the terminal mouse highlight")
	}
}

// TestPanelsFrame_TerminalMouseSelect_EscapeDismissesWithoutPTY covers
// #881: Esc on a highlighted selection only drops the highlight. Neither
// the press nor its release reaches the shell, so nothing behaves like a
// line reset. Once the highlight is gone Esc is an ordinary key again.
func TestPanelsFrame_TerminalMouseSelect_EscapeDismissesWithoutPTY(t *testing.T) {
	pf, pty := panelsFrameWithMouseSelect(t)
	pty.busy = true // raw keys go straight to the PTY, as with a running command
	tv := pf.termView
	tv.StartSelection(2, 0, false)
	tv.ExtendSelection(6, 0)

	esc := func(down bool) *vtinput.InputEvent {
		return &vtinput.InputEvent{
			Type:           vtinput.KeyEventType,
			KeyDown:        down,
			VirtualKeyCode: vtinput.VK_ESCAPE,
			Char:           0x1b,
		}
	}

	if !pf.ProcessKey(esc(true)) {
		t.Fatal("Esc on a highlighted selection should be consumed")
	}
	if tv.HasSelection() {
		t.Fatal("Esc should clear the terminal mouse highlight")
	}
	if !pf.termSelEscHeld {
		t.Fatal("the dismissing Esc press should arm key-up swallowing")
	}
	if len(pty.writes) != 0 {
		t.Fatalf("Esc press must not reach the PTY, got %q", pty.writes)
	}

	if !pf.ProcessKey(esc(false)) {
		t.Fatal("the release of the dismissing Esc should be consumed")
	}
	if pf.termSelEscHeld {
		t.Fatal("key-up swallowing should disarm after one release")
	}
	if len(pty.writes) != 0 {
		t.Fatalf("Esc release must not reach the PTY, got %q", pty.writes)
	}

	// With no highlight left, Esc goes to the shell as before.
	pf.ProcessKey(esc(true))
	if len(pty.writes) == 0 {
		t.Fatal("Esc without a selection should still be forwarded to the PTY")
	}
}

// TestPanelsFrame_TerminalMouseSelect_ModifiedEscapeNotSwallowed keeps
// Alt+Esc / Ctrl+Esc / Shift+Esc on the regular any-key path: they clear
// the highlight but are not treated as the dismiss gesture.
func TestPanelsFrame_TerminalMouseSelect_ModifiedEscapeNotSwallowed(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	tv.StartSelection(2, 0, false)
	tv.ExtendSelection(6, 0)

	pf.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_ESCAPE,
		Char:            0x1b,
		ControlKeyState: vtinput.LeftAltPressed,
	})
	if tv.HasSelection() {
		t.Fatal("Alt+Esc should clear the highlight like any other key")
	}
	if pf.termSelEscHeld {
		t.Fatal("Alt+Esc must not arm key-up swallowing")
	}
}

func TestPanelsFrame_TerminalMouseSelect_AltShiftBlockCopies(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	seedRow(tv, 0, "abcdefghij")
	seedRow(tv, 1, "0123456789")
	seedRow(tv, 2, "ABCDEFGHIJ")

	copied := make(chan string, 1)
	tv.clipboardWriter = func(text string) { copied <- text }
	mods := vtinput.LeftAltPressed | vtinput.ShiftPressed
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      2, MouseY: 0, ControlKeyState: mods,
	})
	if !tv.selBlock {
		t.Fatal("Alt+Shift+LMB should start a rectangular terminal selection")
	}

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
		MouseX:          4, MouseY: 2, ControlKeyState: mods,
	})
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		MouseX: 4, MouseY: 2,
	})

	select {
	case got := <-copied:
		if want := "cde\n234\nCDE"; got != want {
			t.Errorf("terminal Alt+Shift block copy = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal rectangular selection was not copied on release")
	}
	if !tv.HasSelection() {
		t.Fatal("terminal selection should remain highlighted after release")
	}
}

func TestPanelsFrame_TerminalMouseSelect_ShiftDoubleClickCopiesWord(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	seedRow(tv, 0, "foo bar baz")

	// Model the second press of a Shift+double-click. The frame's click
	// counter has already seen the first press, while the terminal backend
	// reports the modifier and double-click flag on this press.
	pf.termSelClickN = 1
	pf.termSelClickAt = time.Now()
	pf.termSelClickX, pf.termSelClickY = 5, 0
	copied := make(chan string, 1)
	tv.clipboardWriter = func(text string) { copied <- text }

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		ControlKeyState: vtinput.ShiftPressed,
		MouseEventFlags: vtinput.DoubleClick,
		MouseX:          5, MouseY: 0,
	})
	if !pf.termSelDragging {
		t.Fatal("double-click selection must remain active until button release")
	}

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		ButtonState: 0, ControlKeyState: vtinput.ShiftPressed,
		MouseX: 5, MouseY: 0,
	})

	select {
	case got := <-copied:
		if got != "bar" {
			t.Fatalf("copied word = %q, want %q", got, "bar")
		}
	case <-time.After(time.Second):
		t.Fatal("Shift+double-click did not copy the selected word")
	}
}

func TestPanelsFrame_TerminalMouseSelect_ShiftTripleClickCopiesLine(t *testing.T) {
	pf, _ := panelsFrameWithMouseSelect(t)
	tv := pf.termView
	seedRow(tv, 0, "foo bar baz")

	// Model the third press of a Shift+triple-click after the first two
	// presses have established the click sequence.
	pf.termSelClickN = 2
	pf.termSelClickAt = time.Now()
	pf.termSelClickX, pf.termSelClickY = 5, 0
	copied := make(chan string, 1)
	tv.clipboardWriter = func(text string) { copied <- text }

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		ControlKeyState: vtinput.ShiftPressed,
		MouseEventFlags: vtui.TripleClick,
		MouseX:          5, MouseY: 0,
	})
	if !pf.termSelDragging {
		t.Fatal("triple-click selection must remain active until button release")
	}

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		ButtonState: 0, ControlKeyState: vtinput.ShiftPressed,
		MouseX: 5, MouseY: 0,
	})

	select {
	case got := <-copied:
		if got != "foo bar baz" {
			t.Fatalf("copied line = %q, want %q", got, "foo bar baz")
		}
	case <-time.After(time.Second):
		t.Fatal("Shift+triple-click did not copy the selected line")
	}
}

// TestPanelsFrame_TerminalMouseSelect_ReleaseHostShapes locks in
// support for the four distinct release-event shapes across our
// backends. See the giant comment inside handleTerminalMouseSelection
// for the taxonomy.
func TestPanelsFrame_TerminalMouseSelect_ReleaseHostShapes(t *testing.T) {
	cases := []struct {
		name    string
		release *vtinput.InputEvent
	}{
		{
			"Wayland/Windows-console: ButtonState=0, KeyDown=false",
			&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: false,
				ButtonState: 0,
				MouseX:      6, MouseY: 0,
			},
		},
		{
			"Windows-console: ButtonState=0, KeyDown=true (release inferred by ButtonState==0)",
			&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: true,
				ButtonState: 0,
				MouseX:      6, MouseY: 0,
			},
		},
		{
			"X11/purex11/tty-SGR: ButtonState=LMB left in place, KeyDown=false",
			&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: false,
				ButtonState: vtinput.FromLeft1stButtonPressed,
				MouseX:      6, MouseY: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, _ := panelsFrameWithMouseSelect(t)
			tv := pf.termView
			seedRow(tv, 0, "hello world")

			pf.ProcessMouse(&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: true,
				ButtonState: vtinput.FromLeft1stButtonPressed,
				MouseX:      2, MouseY: 0,
			})
			pf.ProcessMouse(&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: true,
				ButtonState:     vtinput.FromLeft1stButtonPressed,
				MouseEventFlags: vtinput.MouseMoved,
				MouseX:          6, MouseY: 0,
			})
			pf.ProcessMouse(tc.release)
			if pf.termSelDragging {
				t.Fatalf("release didn't clear termSelDragging: %+v", tc.release)
			}
			if got := tv.ExtractSelection(); got != "llo w" {
				t.Fatalf("selection lost through release: got %q, want %q", got, "llo w")
			}
		})
	}
}

// TestPanelsFrame_TerminalMouseSelect_DragHostShapes locks in support
// for the two motion-event shapes we've seen: Wayland-style with the
// button code still in ButtonState, and X11-style where motion carries
// only the MouseMoved flag.
func TestPanelsFrame_TerminalMouseSelect_DragHostShapes(t *testing.T) {
	cases := []struct {
		name string
		drag *vtinput.InputEvent
	}{
		{
			"Wayland: ButtonState=LMB, KeyDown=false, MouseMoved",
			&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: false,
				ButtonState:     vtinput.FromLeft1stButtonPressed,
				MouseEventFlags: vtinput.MouseMoved,
				MouseX:          6, MouseY: 0,
			},
		},
		{
			"X11: ButtonState=0, KeyDown=false, MouseMoved",
			&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: false,
				ButtonState:     0,
				MouseEventFlags: vtinput.MouseMoved,
				MouseX:          6, MouseY: 0,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, _ := panelsFrameWithMouseSelect(t)
			tv := pf.termView
			seedRow(tv, 0, "hello world")

			pf.ProcessMouse(&vtinput.InputEvent{
				Type: vtinput.MouseEventType, KeyDown: true,
				ButtonState: vtinput.FromLeft1stButtonPressed,
				MouseX:      2, MouseY: 0,
			})
			pf.ProcessMouse(tc.drag)
			if got := tv.ExtractSelection(); got != "llo w" {
				t.Fatalf("drag didn't extend: got %q, want %q", got, "llo w")
			}
		})
	}
}

func TestPanelsFrame_TerminalMouseSelect_RightClickPastes(t *testing.T) {
	pf, pty := panelsFrameWithMouseSelect(t)
	pf.termView.clipboardReader = func() string { return "pasted" }

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.RightmostButtonPressed,
		MouseX:      5, MouseY: 5,
	})
	if string(pty.writes) != "pasted" {
		t.Errorf("RMB paste sent %q, want %q", string(pty.writes), "pasted")
	}
}

func TestPanelsFrame_TerminalMouseSelect_RightClickPasteBracketed(t *testing.T) {
	pf, pty := panelsFrameWithMouseSelect(t)
	pf.termView.BracketedPasteMode = true
	pf.termView.clipboardReader = func() string { return "pasted" }

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.RightmostButtonPressed,
		MouseX:      5, MouseY: 5,
	})
	want := "\x1b[200~pasted\x1b[201~"
	if string(pty.writes) != want {
		t.Errorf("bracketed paste sent %q, want %q", string(pty.writes), want)
	}
}

func TestPanelsFrame_TerminalMouseSelect_RightClickPastesIntoVisibleCommandLine(t *testing.T) {
	pf, pty := panelsFrameWithMouseSelect(t)
	pf.cmdLine.SetVisible(true)
	pf.shellMode = ShellModeOwn
	pf.termView.clipboardReader = func() string { return "echo pasted" }

	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.RightmostButtonPressed,
		MouseX:      5, MouseY: 5,
	})

	if got := pf.cmdLine.Edit.GetText(); got != "echo pasted" {
		t.Fatalf("visible command line after RMB paste = %q, want %q", got, "echo pasted")
	}
	if len(pty.writes) != 0 {
		t.Fatalf("visible command line RMB paste wrote directly to PTY: %q", string(pty.writes))
	}
}
