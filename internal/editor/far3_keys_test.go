package editor

import (
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func newFAR3Editor(t *testing.T, text string) *EditorView {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := NewEditorView(piecetable.New([]byte(text)), nil, "test.txt")
	ev.SetPosition(0, 0, 80, 12)
	t.Cleanup(ev.Close)
	return ev
}

func TestFAR3WordDeletion(t *testing.T) {
	forward := newFAR3Editor(t, "one  two three")
	forward.CursorPos = len("one")
	forward.DeleteWordForward()
	if got, want := forward.GetText(), "onetwo three"; got != want {
		t.Fatalf("forward deletion = %q, want %q", got, want)
	}

	// far2l stops at the spaces before the word and leaves them.
	backward := newFAR3Editor(t, "one  two three")
	backward.CursorPos = len("one  two")
	backward.DeleteWordBackward()
	if got, want := backward.GetText(), "one   three"; got != want {
		t.Fatalf("backward deletion = %q, want %q", got, want)
	}
}

// f4#1729 (DkmS1953): Ctrl+Backspace took the quote in front of a string
// literal with the word, and Ctrl+Del only ate spaces. Both now follow far2l's
// Edit::ProcessKey (KEY_CTRLBS, KEY_CTRLDEL) with the default word dividers.
func TestFAR3WordDeletionStopsAtDividers_Issue1729(t *testing.T) {
	cases := []struct {
		name, text string
		cursor     int
		forward    bool
		want       string
		wantCursor int
	}{
		{"BS keeps the opening quote", `x = "hello`, len(`x = "hello`), false, `x = "`, len(`x = "`)},
		{"BS on a run of dividers", `a == b+++`, len(`a == b+++`), false, `a == b++`, len(`a == b++`)},
		{"BS in Cyrillic", "один два", len("один два"), false, "один ", len("один ")},
		{"Del a word and the spaces after it", `"hello world`, 1, true, `"world`, 1},
		{"Del stops before the closing quote", `say("hi")`, len(`say("`), true, `say("")`, len(`say("`)},
		{"Del spaces up to the next word", "abc    def", 3, true, "abcdef", 3},
		{"Del a divider goes alone", `"hello`, 0, true, ``, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := newFAR3Editor(t, tc.text)
			ev.CursorPos = tc.cursor
			if tc.forward {
				ev.DeleteWordForward()
			} else {
				ev.DeleteWordBackward()
			}
			if got := ev.GetText(); got != tc.want || ev.CursorPos != tc.wantCursor {
				t.Fatalf("text %q cursor %d, want %q cursor %d", got, ev.CursorPos, tc.want, tc.wantCursor)
			}
		})
	}
}

// At the start of a line Ctrl+Backspace is Backspace, and at its end Ctrl+Del
// is Del: both join the lines, as in far2l (editor.cpp).
func TestFAR3WordDeletionJoinsLinesAtTheEdges_Issue1729(t *testing.T) {
	bs := newFAR3Editor(t, "one\ntwo")
	bs.CursorLine, bs.CursorPos = 1, 0
	bs.ProcessKey(keyEvent(vtinput.VK_BACK, vtinput.LeftCtrlPressed))
	if got := bs.GetText(); got != "onetwo" {
		t.Fatalf("Ctrl+Backspace at the start of a line: %q, want %q", got, "onetwo")
	}

	del := newFAR3Editor(t, "one\ntwo")
	del.CursorPos = len("one")
	del.ProcessKey(keyEvent(vtinput.VK_DELETE, vtinput.LeftCtrlPressed))
	if got := del.GetText(); got != "onetwo" {
		t.Fatalf("Ctrl+Del at the end of a line: %q, want %q", got, "onetwo")
	}

	word := newFAR3Editor(t, "say hello")
	word.CursorPos = len("say ")
	word.ProcessKey(keyEvent(vtinput.VK_DELETE, vtinput.LeftCtrlPressed))
	if got := word.GetText(); got != "say " {
		t.Fatalf("Ctrl+Del on a word: %q, want %q", got, "say ")
	}
}

func TestFAR3BlockTransferAndIndent(t *testing.T) {
	ev := newFAR3Editor(t, "one two three")
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorPos = 3
	ev.CopySelectionToCursor()
	if got, want := ev.GetText(), "oneone two three"; got != want {
		t.Fatalf("copied block = %q, want %q", got, want)
	}

	indent := newFAR3Editor(t, "one")
	indent.CursorPos = 1
	indent.ShiftCurrentOrSelectedLines(false)
	if got, want := indent.GetText(), "\tone"; got != want {
		t.Fatalf("indented line = %q, want %q", got, want)
	}
	if indent.CursorPos != 2 {
		t.Fatalf("cursor after indent = %d, want 2", indent.CursorPos)
	}

	unindent := newFAR3Editor(t, "\tone")
	unindent.CursorPos = 2
	unindent.ShiftCurrentOrSelectedLines(true)
	if got, want := unindent.GetText(), "one"; got != want {
		t.Fatalf("unindented line = %q, want %q", got, want)
	}

	selected := newFAR3Editor(t, "one\ntwo\nthree")
	selected.SelActive = true
	selected.SelAnchorOffset = 0
	selected.CursorLine = 1
	selected.CursorPos = 3
	selected.ShiftCurrentOrSelectedLines(false)
	if got, want := selected.GetText(), "\tone\n\ttwo\nthree"; got != want {
		t.Fatalf("indented selected lines = %q, want %q", got, want)
	}
	selected.ShiftCurrentOrSelectedLines(true)
	if got, want := selected.GetText(), "one\ntwo\nthree"; got != want {
		t.Fatalf("unindented selected lines = %q, want %q", got, want)
	}
}

func TestFAR3Bookmarks(t *testing.T) {
	ev := newFAR3Editor(t, "first\nsecond")
	ev.CursorLine = 1
	ev.CursorPos = 2
	ev.SetEditorBookmark(3, true)
	ev.CursorLine = 0
	ev.CursorPos = 0
	ev.SetEditorBookmark(3, false)
	if ev.CursorLine != 1 || ev.CursorPos != 2 {
		t.Fatalf("bookmark restored %d:%d, want 1:2", ev.CursorLine, ev.CursorPos)
	}
}

// The hotkey dispatcher calls DeleteWordBackward itself, before ProcessKey,
// so the join at the start of a line has to be done by the method (f4#1729).
func TestDeleteWordBackwardJoinsLinesAtTheLineStart_Issue1729(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"lf", "one\ntwo", "onetwo"},
		{"crlf", "one\r\ntwo", "onetwo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := newFAR3Editor(t, tc.text)
			ev.CursorLine, ev.CursorPos = 1, 0
			ev.DeleteWordBackward()
			if got := ev.GetText(); got != tc.want {
				t.Fatalf("text %q, want %q", got, tc.want)
			}
			if ev.CursorLine != 0 || ev.CursorPos != len("one") {
				t.Fatalf("cursor %d:%d, want 0:3", ev.CursorLine, ev.CursorPos)
			}
		})
	}

	first := newFAR3Editor(t, "one\ntwo")
	first.DeleteWordBackward()
	if got := first.GetText(); got != "one\ntwo" {
		t.Fatalf("at the very start of the text: %q, want it unchanged", got)
	}
}
