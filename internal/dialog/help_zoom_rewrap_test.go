package dialog

import (
	"testing"

	"github.com/unxed/vtinput"
)

// f4 #378 (follow-up): a long help line is broken at spaces to fit the
// window's width, and Far/far2l re-break it when the window is zoomed wider
// (F5) or narrower again. montoner0 reported on 2026-09-26 that after
// pressing F5 to maximize Help, the text keeps the narrow window's line
// breaks instead of reflowing into the newly available width. This test
// drives the exact F5 path a user takes -- HandleHelpSearchHotkey, which is
// wired into vtui.FrameManager.EventFilter in production -- rather than
// calling vtui.HelpView.SetPosition directly, to catch a re-wrap that works
// on one resize path but not on this one.
func TestHelpRewrapsAfterF5ZoomToggle(t *testing.T) {
	long := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	view, scr := newSearchableHelpForTestAtSize(t, 100, 25, []string{"first", long, "last"})

	// Render once so RenderHelpFrame's enableHelpZoom flips on ShowZoom, the
	// same as the real F1 help window before any resize.
	view.Show(scr)
	RenderHelpFrame(scr, view)

	rowsBefore := len(view.CurrentTopic().Lines)
	if rowsBefore <= 3 {
		t.Fatalf("the narrow window laid the topic out in %d rows, want the long line broken into more than one row", rowsBefore)
	}

	f5 := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F5}
	if !HandleHelpSearchHotkey(f5) {
		t.Fatal("F5 was not consumed by the Help zoom toggle")
	}
	if currentHelpZoom == nil {
		t.Fatal("F5 did not zoom the Help window")
	}

	view.Show(scr)
	RenderHelpFrame(scr, view)

	rowsAfter := len(view.CurrentTopic().Lines)
	if rowsAfter >= rowsBefore {
		t.Fatalf("after F5 zoomed Help wider, the topic still lays out in %d rows (was %d before zoom): the long line was not re-wrapped to the new width", rowsAfter, rowsBefore)
	}
}
