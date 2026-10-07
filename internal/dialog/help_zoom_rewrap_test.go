package dialog

import (
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
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
	generated := &vtui.HelpTopic{}
	AppendGeneratedHelpAction(generated, "F3", "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda")
	lines := append([]string{"first"}, generated.Lines...)
	lines = append(lines, "last")
	view, scr := newSearchableHelpForTestAtSize(t, 100, 25, lines)

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

// f4 #378 (follow-up): the other resize path a real user hits is the terminal
// window itself being resized (SIGWINCH), which vtui's FrameManager.Resize
// forwards to every open frame's ResizeConsole. On its own, a live resize
// while Help is at its ordinary (non-maximized) size is not informative here:
// vtui.HelpView.ResizeConsole always re-centers at its normal, width-capped
// size regardless of the terminal's width, so nothing is expected to change
// and TestHelpRewrapsAfterF5ZoomToggle above already covers the case where
// the window's width genuinely changes.
//
// The real, reproducible bug is a live resize *while Help is zoomed*:
// ResizeConsole has no idea the window is maximized -- that state
// (currentHelpZoom) lives entirely in this package, outside vtui -- so a
// resize recenters the window back to its ordinary width, silently
// discarding the zoom and, with it, the wider re-wrap montoner0 expected to
// still be there.
func TestHelpZoomSurvivesLiveTerminalResize(t *testing.T) {
	long := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	view, scr := newSearchableHelpForTestAtSize(t, 100, 25, []string{"first", long, "last"})

	f5 := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F5}
	if !HandleHelpSearchHotkey(f5) {
		t.Fatal("F5 was not consumed by the Help zoom toggle")
	}

	view.Show(scr)
	RenderHelpFrame(scr, view)
	rowsZoomed := len(view.CurrentTopic().Lines)

	vtui.FrameManager.Resize(220, 25)

	view.Show(scr)
	RenderHelpFrame(scr, view)
	x1, _, x2, _ := view.GetPosition()
	if x1 != 0 || x2 != 219 {
		t.Fatalf("after the terminal resized wider while Help was zoomed, the window sits at %d..%d instead of filling the new width 0..219: the resize silently un-zoomed it", x1, x2)
	}

	rowsAfter := len(view.CurrentTopic().Lines)
	if rowsAfter > rowsZoomed {
		t.Fatalf("after the terminal resized wider while Help was zoomed, the topic laid out in %d rows (was %d right after zooming): the long line was re-wrapped back to a narrower width instead of the new, wider one", rowsAfter, rowsZoomed)
	}
}
