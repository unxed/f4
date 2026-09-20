package editor

import (
	"reflect"
	"testing"
)

func TestEditor_MultiCursor_AddCursorSelectsWordFirst(t *testing.T) {
	ev := multiCursorEditor(t, "alpha beta alpha")
	ev.CursorLine = 0
	ev.CursorPos = 2 // inside the first "alpha"

	ev.AddCursorAtNextOccurrence()

	if !ev.SelActive {
		t.Fatal("the first press did not select the word under the caret")
	}
	if start, end := ev.GetSelectionRange(); start != 0 || end != 5 {
		t.Errorf("selection = [%d %d), want the word [0 5)", start, end)
	}
	if ev.MultiCursor() {
		t.Errorf("the first press already added carets: %v", extraCaretOffsets(ev))
	}
}

func TestEditor_MultiCursor_AddCursorWalksTheCopies(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb aa bb aa")
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.AddCursorAtNextOccurrence() // selects "aa" at 0
	ev.AddCursorAtNextOccurrence() // adds the copy at 6
	ev.AddCursorAtNextOccurrence() // adds the copy at 12

	// The newest copy is the primary caret, so the view follows it.
	if start, end := ev.GetSelectionRange(); start != 12 || end != 14 {
		t.Errorf("primary selection = [%d %d), want [12 14)", start, end)
	}
	if got, want := extraCaretSelections(ev), [][2]int{{0, 2}, {6, 8}}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra selections = %v, want %v", got, want)
	}
}

// Once every copy has a caret, pressing again changes nothing rather than
// adding a caret on top of one that is already there.
func TestEditor_MultiCursor_AddCursorStopsWhenAllTaken(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb aa")
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.AddCursorAtNextOccurrence()
	ev.AddCursorAtNextOccurrence()
	before := extraCaretSelections(ev)

	ev.AddCursorAtNextOccurrence()
	ev.AddCursorAtNextOccurrence()

	if got := extraCaretSelections(ev); !reflect.DeepEqual(got, before) {
		t.Errorf("extra selections = %v, want them unchanged at %v", got, before)
	}
}

// The search wraps: a copy before the selection is found once the end of the
// file has been passed.
func TestEditor_MultiCursor_AddCursorWrapsAround(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb aa")
	ev.CursorLine = 0
	ev.CursorPos = 6 // inside the second "aa"

	ev.AddCursorAtNextOccurrence() // selects the copy at 6
	ev.AddCursorAtNextOccurrence() // the only other copy is behind it

	if start, end := ev.GetSelectionRange(); start != 0 || end != 2 {
		t.Errorf("primary selection = [%d %d), want the wrapped copy [0 2)", start, end)
	}
	if got, want := extraCaretSelections(ev), [][2]int{{6, 8}}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra selections = %v, want %v", got, want)
	}
}

func TestEditor_MultiCursor_SelectAllOccurrences(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb\naa cc aa")
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.SelectAllOccurrences()

	// The caret that was already on a copy stays the primary one, so the
	// view does not jump.
	if start, end := ev.GetSelectionRange(); start != 0 || end != 2 {
		t.Errorf("primary selection = [%d %d), want [0 2)", start, end)
	}
	if got, want := extraCaretSelections(ev), [][2]int{{6, 8}, {12, 14}}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra selections = %v, want %v", got, want)
	}
}

// Copies found this way are ordinary carets: typing replaces every one of them
// in a single undoable change.
func TestEditor_MultiCursor_SelectAllThenTypeReplacesEveryCopy(t *testing.T) {
	ev := multiCursorEditor(t, "foo bar\nfoo baz\nfoo")
	ev.CursorLine = 0
	ev.CursorPos = 1

	ev.SelectAllOccurrences()
	typeCharAtCarets(ev, 'X')

	if got, want := ev.Pt.String(), "X bar\nX baz\nX"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}

	ev.Undo()
	if got, want := ev.Pt.String(), "foo bar\nfoo baz\nfoo"; got != want {
		t.Errorf("buffer after undo = %q, want %q", got, want)
	}
}

// The scan pulls the text out in chunks; a copy lying across a chunk boundary
// still has to be found.
func TestEditor_MultiCursor_SearchCrossesChunkBoundaries(t *testing.T) {
	needle := "needle"
	filler := make([]byte, editorCaretSearchChunk-3)
	for i := range filler {
		filler[i] = '.'
	}
	text := needle + string(filler) + needle
	ev := multiCursorEditor(t, text)
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.AddCursorAtNextOccurrence() // selects the copy at 0
	ev.AddCursorAtNextOccurrence()

	wantStart := len(needle) + len(filler)
	if start, end := ev.GetSelectionRange(); start != wantStart || end != wantStart+len(needle) {
		t.Errorf("primary selection = [%d %d), want [%d %d)", start, end, wantStart, wantStart+len(needle))
	}
}

// "Not this one": the copy added last moves on to the next one and the carets
// before it stay (#317).
func TestEditor_MultiCursor_SkipOccurrenceMovesThePrimaryOn(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb aa bb aa bb aa")
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.AddCursorAtNextOccurrence() // selects "aa" at 0
	ev.AddCursorAtNextOccurrence() // adds the copy at 6, which is now primary
	ev.SkipOccurrence()            // 6 is not wanted: on to 12

	if start, end := ev.GetSelectionRange(); start != 12 || end != 14 {
		t.Errorf("primary selection = [%d %d), want [12 14)", start, end)
	}
	if got, want := extraCaretSelections(ev), [][2]int{{0, 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra selections = %v, want %v: the skipped copy must not keep a caret", got, want)
	}

	// Copies that already have a caret are passed over, and the search wraps.
	ev.SkipOccurrence() // 18
	ev.SkipOccurrence() // wraps past 0, which has a caret, to 6
	if start, end := ev.GetSelectionRange(); start != 6 || end != 8 {
		t.Errorf("after wrapping the primary selection = [%d %d), want [6 8)", start, end)
	}
}

// Going back takes the last copy away and returns to the one before it.
func TestEditor_MultiCursor_RemoveLastOccurrenceGoesBack(t *testing.T) {
	ev := multiCursorEditor(t, "aa bb aa bb aa")
	ev.CursorLine = 0
	ev.CursorPos = 0

	ev.AddCursorAtNextOccurrence() // selects "aa" at 0
	ev.AddCursorAtNextOccurrence() // 6
	ev.AddCursorAtNextOccurrence() // 12, primary

	ev.RemoveLastOccurrence()
	if start, end := ev.GetSelectionRange(); start != 6 || end != 8 {
		t.Errorf("primary selection = [%d %d), want the previous copy [6 8)", start, end)
	}
	if got, want := extraCaretSelections(ev), [][2]int{{0, 2}}; !reflect.DeepEqual(got, want) {
		t.Errorf("extra selections = %v, want %v", got, want)
	}

	ev.RemoveLastOccurrence()
	if start, end := ev.GetSelectionRange(); start != 0 || end != 2 {
		t.Errorf("primary selection = [%d %d), want [0 2)", start, end)
	}
	if ev.MultiCursor() {
		t.Errorf("carets are left after going back to the first copy: %v", extraCaretOffsets(ev))
	}

	// With one caret there is nothing to take back.
	ev.RemoveLastOccurrence()
	if start, end := ev.GetSelectionRange(); start != 0 || end != 2 {
		t.Errorf("a lone caret moved: [%d %d)", start, end)
	}
}
