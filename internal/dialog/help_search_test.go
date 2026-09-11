package dialog

import (
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func newSearchableHelpForTest(t *testing.T, lines []string) (*vtui.HelpView, *vtui.ScreenBuf) {
	return newSearchableHelpForTestAtSize(t, 80, 25, lines)
}

func newSearchableHelpForTestAtSize(t *testing.T, width, height int, lines []string) (*vtui.HelpView, *vtui.ScreenBuf) {
	t.Helper()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(width, height)
	vtui.FrameManager.Init(scr)
	Engine := vtui.NewHelpEngine(&MemoryHelpVFS{files: map[string]string{}})
	Engine.AddTopic(&vtui.HelpTopic{Name: "Test", Lines: lines})
	oldEngine := vtui.GlobalHelpEngine
	vtui.GlobalHelpEngine = Engine
	t.Cleanup(func() {
		vtui.GlobalHelpEngine = oldEngine
		CurrentHelpSearch = nil
		currentHelpZoom = nil
	})
	view := vtui.NewHelpView(Engine, "Test")
	vtui.FrameManager.Push(view)
	return view, scr
}

func TestVisibleHelpLineRemovesFormatting(t *testing.T) {
	got, centered := visibleHelpLine("^#Title# ~Open file~Viewer@ now")
	if !centered {
		t.Fatal("center marker was not detected")
	}
	if got != "Title Open file now" {
		t.Fatalf("visible line = %q, want %q", got, "Title Open file now")
	}
}

func TestHelpSearchFindsCaseInsensitiveMatchesAndCycles(t *testing.T) {
	view, _ := newSearchableHelpForTest(t, []string{
		"No match here",
		"First Needle",
		"Second needle and NEEDLE",
	})

	for _, r := range "needle" {
		if !HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r}) {
			t.Fatalf("character %q was not consumed", r)
		}
	}
	if got := CurrentHelpSearch.Matches[CurrentHelpSearch.Selected]; got.line != 1 || got.start != 6 {
		t.Fatalf("first match = %#v, want line 1 at rune 6", got)
	}
	if !MoveHelpSearch(view, false) || CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line != 2 || CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].start != 7 {
		t.Fatalf("F3 did not advance to the next line: %#v", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected])
	}
	if !MoveHelpSearch(view, false) || CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].start != 18 {
		t.Fatalf("F3 did not advance to the second occurrence: %#v", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected])
	}
	if !MoveHelpSearch(view, true) || CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].start != 7 {
		t.Fatalf("Shift+F3 did not move backwards: %#v", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected])
	}
}

func TestHelpSearchRendersHighlightAndHint(t *testing.T) {
	view, scr := newSearchableHelpForTest(t, []string{"before Needle after"})
	for _, r := range "needle" {
		HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	view.Show(scr)
	x1, y1, _, y2 := view.GetPosition()
	titleAttr := scr.GetCell((view.X1+view.X2)/2, y1).Attributes
	titleBackground := vtui.GetRGBBack(titleAttr)
	RenderHelpSearch(scr)

	matchCell := scr.GetCell(x1+2+len("before "), y1+1)
	if got, want := vtui.GetRGBFore(matchCell.Attributes), uint32(0xFFFF00); got != want {
		t.Fatalf("current match foreground = %#x, want yellow %#x", got, want)
	}
	if got, want := vtui.GetRGBBack(matchCell.Attributes), vtui.GetRGBBack(vtui.Palette[vtui.ColHelpSelectedLink]); got != want {
		t.Fatalf("current match background = %#x, want selected background %#x", got, want)
	}
	foundHint := false
	for x := x1 + 2; x < view.X2-1; x++ {
		if testutil.Rune(scr.GetCell(x, y2).Char) == 'F' && testutil.Rune(scr.GetCell(x+1, y2).Char) == '3' {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Fatal("search navigation hint was not rendered on the help border")
	}
	if got := scr.GetCell((x1+view.X2)/2, y2).Attributes; got != titleAttr {
		t.Fatalf("bottom hint attr = %#x, want title attr %#x", got, titleAttr)
	}
	foundHighlightedQuery := false
	for x := x1 + 2; x < view.X2; x++ {
		cell := scr.GetCell(x, y1)
		if testutil.Rune(cell.Char) == 'n' && vtui.GetRGBFore(cell.Attributes) == vtui.GetRGBFore(vtui.Palette[vtui.ColHelpLink]) {
			if got := vtui.GetRGBBack(cell.Attributes); got != titleBackground {
				t.Fatalf("query changed title background to %#x, want %#x", got, titleBackground)
			}
			foundHighlightedQuery = true
			break
		}
	}
	if !foundHighlightedQuery {
		t.Fatal("live query was not highlighted in the help title")
	}
}

func TestHelpSearchHotkeysRepeatAndBackspace(t *testing.T) {
	_, _ = newSearchableHelpForTest(t, []string{"one needle", "two needle"})
	for _, r := range "needle" {
		HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: vtinput.LeftCtrlPressed}) {
		t.Fatal("Ctrl+Enter was not consumed by help search")
	}
	if CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line != 1 {
		t.Fatalf("Ctrl+Enter match line = %d, want 1", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line)
	}
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed}) {
		t.Fatal("Ctrl+Shift+Enter was not consumed by help search")
	}
	if CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line != 0 {
		t.Fatalf("Ctrl+Shift+Enter match line = %d, want 0", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line)
	}
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3}) {
		t.Fatal("F3 was not consumed by help search")
	}
	if CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line != 1 {
		t.Fatalf("F3 match line = %d, want 1", CurrentHelpSearch.Matches[CurrentHelpSearch.Selected].line)
	}
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK}) {
		t.Fatal("Backspace was not consumed by help search")
	}
	if got := string(CurrentHelpSearch.Query); got != "needl" {
		t.Fatalf("query after Backspace = %q, want needl", got)
	}
}

func TestHelpSearchEscapeClosesHelpImmediately(t *testing.T) {
	view, _ := newSearchableHelpForTest(t, []string{"one needle"})
	for _, r := range "needle" {
		HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	if HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE}) {
		t.Fatal("Escape was consumed before HelpView could close")
	}
	if CurrentHelpSearch != nil {
		t.Fatal("Escape left Help search state active")
	}
	view.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE})
	if vtui.FrameManager.GetTopFrame() != view || !view.IsDone() {
		t.Fatal("Escape did not close Help while clearing the search")
	}
}

func TestHelpBackspaceDoesNotCloseRootTopic(t *testing.T) {
	view, _ := newSearchableHelpForTest(t, []string{"root help"})
	backspace := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK}
	if !HandleHelpSearchHotkey(backspace) {
		t.Fatal("Backspace on the root Help topic was not consumed")
	}
	if view.IsDone() || vtui.FrameManager.GetTopFrame() != view {
		t.Fatal("Backspace closed the root Help window")
	}
}

func TestHelpBackspaceStillReturnsToPreviousTopic(t *testing.T) {
	view, _ := newSearchableHelpForTest(t, []string{"root help"})
	vtui.GlobalHelpEngine.AddTopic(&vtui.HelpTopic{Name: "Second", Lines: []string{"second topic"}})
	view.SwitchTopic("Second")
	backspace := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK}
	if HandleHelpSearchHotkey(backspace) {
		t.Fatal("Backspace with topic history should be passed to HelpView")
	}
	view.ProcessKey(backspace)
	historyLen, ok := NestedHelpLen(reflect.ValueOf(view), "history")
	if view.IsDone() || !ok || historyLen != 0 {
		t.Fatalf("Backspace did not return to the previous topic: history=%d, %v; done=%v", historyLen, ok, view.IsDone())
	}
}

func TestHelpSearchHighlightsAllVisibleMatches(t *testing.T) {
	view, scr := newSearchableHelpForTest(t, []string{"needle and NEEDLE"})
	for _, r := range "needle" {
		HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	view.Show(scr)
	RenderHelpSearch(scr)
	x1, y1, _, _ := view.GetPosition()
	first := scr.GetCell(x1+2, y1+1)
	second := scr.GetCell(x1+2+len("needle and "), y1+1)
	if got, want := vtui.GetRGBFore(first.Attributes), uint32(0xFFFF00); got != want {
		t.Fatalf("current match foreground = %#x, want yellow %#x", got, want)
	}
	if got, want := vtui.GetRGBFore(second.Attributes), vtui.GetRGBFore(vtui.Palette[vtui.ColHelpLink]); got != want {
		t.Fatalf("other match foreground = %#x, want %#x", got, want)
	}
	if got, want := vtui.GetRGBBack(first.Attributes), vtui.GetRGBBack(vtui.Palette[vtui.ColHelpSelectedLink]); got != want {
		t.Fatalf("current match background = %#x, want %#x", got, want)
	}
	if got, want := vtui.GetRGBBack(second.Attributes), vtui.GetRGBBack(vtui.Palette[vtui.ColHelpText]); got != want {
		t.Fatalf("other match background = %#x, want unchanged %#x", got, want)
	}
}

func TestHelpSearchHighlightFollowsManualScrolling(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "ordinary line"
	}
	lines[0] = "needle at the top"
	view, scr := newSearchableHelpForTest(t, lines)
	vtui.FrameManager.Pop()
	wrapped := &struct{ *vtui.HelpView }{HelpView: view}
	vtui.FrameManager.Push(wrapped)
	for _, r := range "needle" {
		HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}

	wrapped.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT})
	wrapped.Show(scr)
	RenderHelpSearch(scr)

	scrollTop, ok := helpViewScrollTop(wrapped)
	if !ok || scrollTop == 0 {
		t.Fatalf("HelpView scrollTop = %d, want a positive value after PageDown", scrollTop)
	}
	if CurrentHelpSearch.scrollTop != scrollTop {
		t.Fatalf("search scrollTop = %d, actual HelpView scrollTop = %d", CurrentHelpSearch.scrollTop, scrollTop)
	}
	x1, y1, _, _ := view.GetPosition()
	staleCell := scr.GetCell(x1+2, y1+1)
	if got := vtui.GetRGBFore(staleCell.Attributes); got == vtui.GetRGBFore(vtui.Palette[vtui.ColDialogHighlightText]) || got == vtui.GetRGBFore(vtui.Palette[vtui.ColHelpLink]) {
		t.Fatal("highlight from the old scroll position remained over the first visible row")
	}
}

func TestHelpSearchReadsScrollPositionFromEmbeddedHelpView(t *testing.T) {
	view, _ := newSearchableHelpForTest(t, make([]string, 40))
	wrapped := &struct{ *vtui.HelpView }{HelpView: view}
	wrapped.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT})
	want, ok := helpViewScrollTop(view)
	if !ok || want == 0 {
		t.Fatalf("direct HelpView scrollTop = %d, want positive", want)
	}
	if got, ok := helpViewScrollTop(wrapped); !ok || got != want {
		t.Fatalf("embedded HelpView scrollTop = %d, %v; want %d, true", got, ok, want)
	}
}

func TestHelpShowsZoomButtonAndRestoresPreviousBounds(t *testing.T) {
	view, scr := newSearchableHelpForTestAtSize(t, 80, 40, []string{"Help text"})
	view.Show(scr)
	RenderHelpSearch(scr)
	if !view.ShowZoom {
		t.Fatal("Help zoom support was not enabled")
	}
	x1, y1, x2, y2 := view.GetPosition()
	if got := testutil.Rune(scr.GetCell(x2-6, y1).Char); got != vtui.UIStrings.ZoomSymbol {
		t.Fatalf("zoom button symbol = %q, want %q", got, vtui.UIStrings.ZoomSymbol)
	}
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: testutil.Int16(x2 - 6), MouseY: testutil.Int16(y1),
	}) {
		t.Fatal("zoom button click was not handled")
	}
	maxX1, maxY1, maxX2, maxY2 := view.GetPosition()
	if maxX1 != 0 || maxY1 != 0 || maxX2 != 79 || maxY2 != 36 || currentHelpZoom == nil {
		t.Fatalf("zoomed Help bounds=(%d,%d)-(%d,%d), zoom state=%v, want (0,0)-(79,36)", maxX1, maxY1, maxX2, maxY2, currentHelpZoom)
	}
	_, _, zoomedX2, _ := view.GetPosition()
	if !HandleHelpSearchHotkey(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: testutil.Int16(zoomedX2 - 6), MouseY: 0,
	}) {
		t.Fatal("restore button click was not handled")
	}
	gotX1, gotY1, gotX2, gotY2 := view.GetPosition()
	if gotX1 != x1 || gotY1 != y1 || gotX2 != x2 || gotY2 != y2 || currentHelpZoom != nil {
		t.Fatalf("restored bounds=(%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)", gotX1, gotY1, gotX2, gotY2, x1, y1, x2, y2)
	}
}
