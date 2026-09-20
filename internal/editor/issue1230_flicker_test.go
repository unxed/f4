package editor

import (
	"reflect"
	"testing"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// newFlickerEditor returns an editor whose Colorer highlighter has the colours
// of four lines cached and no worker, so a line with no fresh colours stays
// uncoloured for as long as the test looks at it.
func newFlickerEditor(t *testing.T) (*EditorView, *ColorerHighlighter) {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	ev := NewEditorView(piecetable.New([]byte("first\nsecond\nthird\nfourth")), nil, "test.txt")
	t.Cleanup(ev.Close)

	ch := &ColorerHighlighter{session: &colorer.Session{}}
	// Registered after ev.Close, so it runs first: the empty session must not
	// reach the real release path.
	t.Cleanup(func() { ch.session = nil })
	for line := 0; line < 4; line++ {
		ch.storeAttrs(line, []uint64{uint64(line + 1)}, uint64(10+line), nil)
	}
	ev.Highlighter = ch
	// What a drawn frame records: the colours above belong to four lines.
	ch.noteLineCount(ev.Li.LineCount())
	return ev, ch
}

// TestIssue1230TypingKeepsTheOldColoursUntilNewOnesArrive is the regression
// test for the follow-up to issue #1230: colours are computed by a worker, so
// an edit that dropped the cached colours below it left those lines plain until
// the worker answered, and the screen blinked on every keystroke. The old
// colours are still the best guess for those lines and have to stay on screen.
func TestIssue1230TypingKeepsTheOldColoursUntilNewOnesArrive(t *testing.T) {
	ev, ch := newFlickerEditor(t)
	ev.CursorLine, ev.CursorPos = 1, 0

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'x'})

	if got := ev.Pt.String(); got != "first\nxsecond\nthird\nfourth" {
		t.Fatalf("text after typing = %q", got)
	}
	for line := 0; line < 4; line++ {
		want := []uint64{uint64(line + 1)}
		if got := ch.HighlightLine(line, "", 0); !reflect.DeepEqual(got, want) {
			t.Errorf("line %d colours right after typing = %v, want the old %v", line, got, want)
		}
		if got := ch.GetLineBackground(line, 99); got != uint64(10+line) {
			t.Errorf("line %d background right after typing = %d, want the old %d", line, got, 10+line)
		}
	}
}

// TestIssue1230EnterMovesTheOldColoursWithTheText covers the same for Enter:
// the text below the cursor moves down a line, and so must the colours drawn
// for it in the meantime, or the old #1230 garbling would show for a moment.
func TestIssue1230EnterMovesTheOldColoursWithTheText(t *testing.T) {
	ev, ch := newFlickerEditor(t)
	ev.CursorLine, ev.CursorPos = 1, 0

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})

	if got := ev.Pt.String(); got != "first\n\nsecond\nthird\nfourth" {
		t.Fatalf("text after Enter = %q", got)
	}
	// Line 0 is above the edit; line 1 is the empty half of the split line; the
	// old lines 1, 2 and 3 now sit on 2, 3 and 4.
	want := map[int][]uint64{0: {1}, 2: {2}, 3: {3}, 4: {4}}
	for line, attrs := range want {
		if got := ch.HighlightLine(line, "", 0); !reflect.DeepEqual(got, attrs) {
			t.Errorf("line %d colours right after Enter = %v, want %v", line, got, attrs)
		}
	}
}

// TestIssue1230FreshColoursReplaceTheOldOnes checks the other half: once the
// worker has coloured a line, the kept colours must not come back.
func TestIssue1230FreshColoursReplaceTheOldOnes(t *testing.T) {
	ev, ch := newFlickerEditor(t)
	ev.CursorLine, ev.CursorPos = 1, 0

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'x'})
	ch.storeAttrs(2, []uint64{77}, 88, nil)

	if got := ch.HighlightLine(2, "", 0); !reflect.DeepEqual(got, []uint64{77}) {
		t.Errorf("line 2 colours = %v, want the fresh [77]", got)
	}
	if got := ch.GetLineBackground(2, 99); got != 88 {
		t.Errorf("line 2 background = %d, want the fresh 88", got)
	}

	// A hard drop, as a file type or scheme change does, keeps nothing.
	ch.DropFrom(0)
	for line := 0; line < 4; line++ {
		if got := ch.HighlightLine(line, "", 0); got != nil {
			t.Errorf("line %d colours after DropFrom(0) = %v, want none", line, got)
		}
	}
}

// TestIssue1230UndoKeepsTheColoursUntilNewOnesArrive: Undo and Redo dropped
// every colour at once, so the whole screen blinked plain, as reported for
// Undo. The colours of the text they replace stay until fresh ones arrive.
func TestIssue1230UndoKeepsTheColoursUntilNewOnesArrive(t *testing.T) {
	ev, ch := newFlickerEditor(t)
	ev.CursorLine, ev.CursorPos = 1, 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'x'})
	// The worker has coloured the edited text.
	for line := 0; line < 4; line++ {
		ch.storeAttrs(line, []uint64{uint64(100 + line)}, uint64(200+line), nil)
	}

	ev.Undo()

	if got := ev.Pt.String(); got != "first\nsecond\nthird\nfourth" {
		t.Fatalf("text after Undo = %q", got)
	}
	for line := 0; line < 4; line++ {
		if got := ch.HighlightLine(line, "", 0); !reflect.DeepEqual(got, []uint64{uint64(100 + line)}) {
			t.Errorf("line %d colours right after Undo = %v, want the kept [%d]", line, got, 100+line)
		}
	}

	ev.Redo()
	for line := 0; line < 4; line++ {
		if got := ch.HighlightLine(line, "", 0); got == nil {
			t.Errorf("line %d blinked plain right after Redo", line)
		}
	}
}

// Undoing an Enter takes a line away: the colours below it move up with the
// text, and the ones above stay.
func TestIssue1230UndoOfEnterMovesTheKeptColoursWithTheText(t *testing.T) {
	ev, ch := newFlickerEditor(t)
	ev.CursorLine, ev.CursorPos = 1, 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	for line := 0; line < 5; line++ {
		ch.storeAttrs(line, []uint64{uint64(10 + line)}, 0, nil)
	}

	ev.Undo()

	if got := ev.Pt.String(); got != "first\nsecond\nthird\nfourth" {
		t.Fatalf("text after Undo = %q", got)
	}
	want := map[int][]uint64{0: {10}, 1: {12}, 2: {13}, 3: {14}}
	for line, attrs := range want {
		if got := ch.HighlightLine(line, "", 0); !reflect.DeepEqual(got, attrs) {
			t.Errorf("line %d colours after Undo = %v, want %v", line, got, attrs)
		}
	}
}
