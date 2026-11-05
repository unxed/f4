package editor

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func persistentKey(ev *EditorView, vk uint16, state vtinput.ControlKeyState, ch rune) {
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: state, Char: ch})
}

func selectedText(t *testing.T, ev *EditorView) string {
	t.Helper()
	start, end := ev.GetSelectionRange()
	if !ev.SelActive || end <= start {
		return ""
	}
	data, err := ev.Pt.GetRange(start, end-start)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// f4#1729 (gchet): with persistent blocks a selected block stays when the
// cursor moves away, typing works at the cursor and leaves it, edits before
// it move it along, and Ctrl+P copies it to the cursor.
func TestPersistentBlockStaysWhileTheCursorMoves_Issue1729(t *testing.T) {
	old := config.App.EditorPersistentBlocks
	t.Cleanup(func() { config.App.EditorPersistentBlocks = old })
	config.App.EditorPersistentBlocks = true

	ev := newFAR3Editor(t, "hello world\nsecond line\n")
	for i := 0; i < 5; i++ {
		persistentKey(ev, vtinput.VK_RIGHT, vtinput.ShiftPressed, 0)
	}
	persistentKey(ev, vtinput.VK_DOWN, 0, 0)
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("after Down the block is %q, want %q", got, "hello")
	}

	persistentKey(ev, 0, 0, 'X')
	if got, want := ev.GetText(), "hello world\nseconXd line\n"; got != want {
		t.Fatalf("typing gave %q, want %q", got, want)
	}
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("after typing below it the block is %q, want %q", got, "hello")
	}

	ev.CursorLine, ev.CursorPos = 0, 0
	persistentKey(ev, 0, 0, 'Y')
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("after typing before it the block is %q, want it moved along", got)
	}

	ev.CursorLine, ev.CursorPos = 1, 0
	ev.CopySelectionToCursor()
	if got, want := ev.GetText(), "Yhello world\nhelloseconXd line\n"; got != want {
		t.Fatalf("Ctrl+P gave %q, want %q", got, want)
	}
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("Ctrl+P cleared the persistent block: got %q", got)
	}
	ev.MoveSelectionToCursor()
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("Ctrl+M cleared the persistent block: got %q", got)
	}

	ev.ClearSelection()
	if ev.SelActive {
		t.Fatal("Ctrl+U kept the block")
	}
}

// Without the option the selection goes away as before.
func TestSelectionWithoutPersistentBlocksGoesWithTheCursor(t *testing.T) {
	old := config.App.EditorPersistentBlocks
	t.Cleanup(func() { config.App.EditorPersistentBlocks = old })
	config.App.EditorPersistentBlocks = false

	ev := newFAR3Editor(t, "hello world\nsecond line\n")
	for i := 0; i < 5; i++ {
		persistentKey(ev, vtinput.VK_RIGHT, vtinput.ShiftPressed, 0)
	}
	persistentKey(ev, vtinput.VK_DOWN, 0, 0)
	if ev.SelActive {
		t.Fatalf("the selection %q stayed after Down", selectedText(t, ev))
	}
}

// A click elsewhere moves the cursor and leaves a persistent block; a drag
// makes a new one.
func TestPersistentBlockSurvivesAClick_Issue1729(t *testing.T) {
	old := config.App.EditorPersistentBlocks
	t.Cleanup(func() { config.App.EditorPersistentBlocks = old })
	config.App.EditorPersistentBlocks = true

	ev := newFAR3Editor(t, "hello world\nsecond line\n")
	for i := 0; i < 5; i++ {
		persistentKey(ev, vtinput.VK_RIGHT, vtinput.ShiftPressed, 0)
	}
	persistentKey(ev, vtinput.VK_DOWN, 0, 0)

	// The editor sits at 0,0 (newFAR3Editor): its text starts on row 1,
	// so row 2 is the second line.
	mouse := func(x, y int16, state uint32, flags uint32) {
		ev.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: state != 0,
			MouseX: x, MouseY: y, ButtonState: state, MouseEventFlags: flags})
	}
	mouse(2, 2, vtinput.FromLeft1stButtonPressed, 0)
	mouse(2, 2, 0, 0)
	if ev.CursorLine != 1 || ev.CursorPos != 2 {
		t.Errorf("the click put the cursor at %d:%d, want 1:2", ev.CursorLine, ev.CursorPos)
	}
	if got := selectedText(t, ev); got != "hello" {
		t.Fatalf("after a click the block is %q, want %q", got, "hello")
	}
}

// A vertical block (Alt+arrows) is persistent too: it stays when the cursor
// moves on, typing works at the cursor, and a line added above it moves it
// down with its text.
func TestPersistentVerticalBlock_Issue1729(t *testing.T) {
	old := config.App.EditorPersistentBlocks
	t.Cleanup(func() { config.App.EditorPersistentBlocks = old })
	config.App.EditorPersistentBlocks = true

	ev := newFAR3Editor(t, "abcdef\nghijkl\nmnopqr\n")
	for i := 0; i < 3; i++ {
		persistentKey(ev, vtinput.VK_RIGHT, vtinput.LeftAltPressed, 0)
	}
	persistentKey(ev, vtinput.VK_DOWN, vtinput.LeftAltPressed, 0)
	persistentKey(ev, vtinput.VK_DOWN, 0, 0)
	if !ev.RectSelActive {
		t.Fatal("the vertical block went away when the cursor moved on")
	}

	block := func() string {
		vtui.SetClipboard("")
		ev.CopySelection()
		return vtui.GetClipboard()
	}
	want := block()
	if want == "" {
		t.Fatal("the vertical block copies nothing")
	}

	persistentKey(ev, 0, 0, 'X')
	if got := ev.GetText(); got != "abcdef\nghijkl\nmnoXpqr\n" {
		t.Fatalf("typing gave %q", got)
	}
	if got := block(); got != want {
		t.Fatalf("after typing below it the block copies %q, want %q", got, want)
	}

	ev.CursorLine, ev.CursorPos = 0, 0
	persistentKey(ev, vtinput.VK_RETURN, 0, 0)
	if got := block(); got != want {
		t.Fatalf("after a line above it the block copies %q, want %q", got, want)
	}

	// A click elsewhere moves the cursor and leaves the block.
	ev.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: 4, MouseY: 4, ButtonState: vtinput.FromLeft1stButtonPressed})
	ev.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: 4, MouseY: 4})
	if !ev.RectSelActive {
		t.Fatal("a click dropped the vertical block")
	}
	if got := block(); got != want {
		t.Fatalf("after a click the block copies %q, want %q", got, want)
	}
}

func TestPersistentVerticalBlockTransferAndLineEndings_Issue1729(t *testing.T) {
	old := config.App.EditorPersistentBlocks
	t.Cleanup(func() { config.App.EditorPersistentBlocks = old })
	config.App.EditorPersistentBlocks = true

	for _, tc := range []struct {
		name, eol string
	}{
		{name: "LF", eol: "\n"},
		{name: "CRLF", eol: "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := strings.Join([]string{"abcdef", "ghijkl", "mnopqr"}, tc.eol)
			for _, move := range []bool{false, true} {
				ev := newFAR3Editor(t, original)
				ev.StartRectSelection(0, 1)
				ev.CursorLine, ev.CursorPos = 1, 3
				ev.rectFrozen = true
				ev.rectEndLine, ev.rectEndCol = 1, 3
				ev.rectLines = ev.Li.LineCount()
				ev.CursorLine, ev.CursorPos = 2, 1
				if move {
					ev.MoveSelectionToCursor()
				} else {
					ev.CopySelectionToCursor()
				}

				if !ev.RectSelActive {
					t.Fatal("vertical block was cleared by transfer")
				}
				vtui.SetClipboard("")
				ev.CopySelection()
				if got := vtui.GetClipboard(); got != "bc\nhi" {
					t.Fatalf("selected rectangle after transfer = %q, want %q", got, "bc\nhi")
				}
				want := "abcdef" + tc.eol + "ghijkl" + tc.eol + "mbcnopqr" + tc.eol + " hi"
				if move {
					want = "adef" + tc.eol + "gjkl" + tc.eol + "mbcnopqr" + tc.eol + " hi"
				}
				if got := ev.GetText(); got != want {
					t.Fatalf("transferred text = %q, want %q", got, want)
				}
			}
		})
	}
}
