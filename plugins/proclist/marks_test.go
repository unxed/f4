//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// markTestSamples are ten fake processes, pids 101..110, sorted by PID in
// every test that uses them (fakePanel), so a pid's display position is
// pid-101.
func markTestSamples() []sample {
	names := []string{"init", "chrome", "Chrome", "bash", "firefox", "chrome", "sshd", "zsh", "chrome", "f4"}
	samples := make([]sample, len(names))
	for i, name := range names {
		samples[i] = sample{pid: 101 + i, name: name, start: uint64(1000 + i), rssKiB: uint64(1024 * (i + 1)), cpuPercent: float64(i)}
	}
	return samples
}

// frozenTestPanel is newTestProcListPanel with its refresh loop stopped, so
// the rows a test applies stay until it applies others. With the loop
// running, a test that pumps the UI queue (to see a toast) would let a real
// snapshot in, and the test's next action would land on a real process.
func frozenTestPanel(t *testing.T) *procListPanel {
	t.Helper()
	p := newTestProcListPanel(t)
	_ = p.Close()
	return p
}

// fakePanel is a frozen panel showing markTestSamples by PID, ascending.
func fakePanel(t *testing.T) *procListPanel {
	t.Helper()
	p := frozenTestPanel(t)
	p.SetFocus(true)
	p.applySamples(markTestSamples())
	p.sortBy(colPID)
	if p.table.SortColumn != displayColumn(p.specs, colPID) || !p.table.SortAscending {
		t.Fatalf("fakePanel: sort = %d asc=%v, want PID ascending", p.table.SortColumn, p.table.SortAscending)
	}
	p.table.SetSelectPos(0)
	return p
}

func keyEvent(vk uint16, mods vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: mods}
}

func press(t *testing.T, p *procListPanel, vk uint16, mods vtinput.ControlKeyState) {
	t.Helper()
	if !p.ProcessKey(keyEvent(vk, mods)) {
		t.Fatalf("key %#x mods %#x was not claimed", vk, mods)
	}
}

// markedPIDs lists the selected processes' pids, lowest first.
func markedPIDs(p *procListPanel) []int {
	var pids []int
	for _, r := range p.table.Rows {
		if pr := r.(procRow); pr.IsSelected() {
			pids = append(pids, pr.s.pid)
		}
	}
	slices.Sort(pids)
	return pids
}

func wantMarked(t *testing.T, p *procListPanel, step string, want ...int) {
	t.Helper()
	if got := markedPIDs(p); !slices.Equal(got, want) {
		t.Fatalf("%s: selected %v, want %v", step, got, want)
	}
}

func wantCursor(t *testing.T, p *procListPanel, step string, pid int) {
	t.Helper()
	if s, ok := p.selectedSample(); !ok || s.pid != pid {
		t.Fatalf("%s: cursor on pid %d (ok=%v), want %d", step, s.pid, ok, pid)
	}
}

func TestInsTogglesTheProcessAndMovesDown(t *testing.T) {
	p := fakePanel(t)
	press(t, p, vtinput.VK_INSERT, 0)
	press(t, p, vtinput.VK_INSERT, 0)
	wantMarked(t, p, "after Ins Ins", 101, 102)
	wantCursor(t, p, "after Ins Ins", 103)

	press(t, p, vtinput.VK_UP, 0)
	press(t, p, vtinput.VK_INSERT, 0)
	wantMarked(t, p, "after Up Ins", 101)
	wantCursor(t, p, "after Up Ins", 103)

	// Ins on the last row toggles it and stays there.
	press(t, p, vtinput.VK_END, 0)
	press(t, p, vtinput.VK_INSERT, 0)
	wantMarked(t, p, "after End Ins", 101, 110)
	wantCursor(t, p, "after End Ins", 110)
}

// TestShiftArrowRunDecidesItsDirectionOnce is far's Shift+arrow session: the
// first key of a run selects or deselects depending on the row it starts
// from, every key of the run does the same, and any other key ends the run.
func TestShiftArrowRunDecidesItsDirectionOnce(t *testing.T) {
	p := fakePanel(t)
	for range 3 {
		press(t, p, vtinput.VK_DOWN, vtinput.ShiftPressed)
	}
	wantMarked(t, p, "Shift+Down x3", 101, 102, 103)
	wantCursor(t, p, "Shift+Down x3", 104)

	// Up ends the run. The next one starts on selected 103, so it deselects
	// -- and keeps deselecting over 104, which is not selected and which a
	// plain per-row toggle would select.
	press(t, p, vtinput.VK_UP, 0)
	press(t, p, vtinput.VK_DOWN, vtinput.ShiftPressed)
	press(t, p, vtinput.VK_DOWN, vtinput.ShiftPressed)
	wantMarked(t, p, "Up, Shift+Down x2", 101, 102)
	wantCursor(t, p, "Up, Shift+Down x2", 105)

	// A declared key ends the run too, though the host runs it without
	// ProcessKey: the next run decides afresh. Left over, the deselecting run
	// would leave unselected 104 alone.
	ctrlF4 := keyEvent(vtinput.VK_F4, vtinput.LeftCtrlPressed)
	for range 2 { // PID descending, then back to ascending
		if !vfs.DispatchPanelKey(p.PanelKeys(), ctrlF4) {
			t.Fatal("Ctrl+F4 is not declared")
		}
	}
	p.table.SetSelectPos(3)
	press(t, p, vtinput.VK_DOWN, vtinput.ShiftPressed)
	wantMarked(t, p, "Ctrl+F4 x2, then Shift+Down from 104", 101, 102, 104)

	// Shift+Up on the top row still takes the row, with nowhere to move.
	press(t, p, vtinput.VK_HOME, 0)
	press(t, p, vtinput.VK_UP, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+Up on the top row", 102, 104)
	wantCursor(t, p, "Shift+Up on the top row", 101)
}

func TestShiftPageKeysTakeTheWholeStretch(t *testing.T) {
	p := fakePanel(t)
	p.table.SetSelectPos(2)
	press(t, p, vtinput.VK_END, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+End from 103", 103, 104, 105, 106, 107, 108, 109, 110)
	wantCursor(t, p, "Shift+End", 110)

	press(t, p, vtinput.VK_UP, 0)
	press(t, p, vtinput.VK_UP, 0)
	press(t, p, vtinput.VK_HOME, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+Home from 108", 109, 110)
	wantCursor(t, p, "Shift+Home", 101)
}

// TestSelectionBelongsToTheProcess covers procKey: a selection follows its
// process wherever a refresh sorts it, leaves with it, and is not inherited
// by a new process the OS gives the same pid.
func TestSelectionBelongsToTheProcess(t *testing.T) {
	p := fakePanel(t)
	samples := markTestSamples()
	p.marks.set(samples[4].key(), true) // 105 firefox
	p.marks.set(samples[6].key(), true) // 107 sshd
	p.marks.set(samples[8].key(), true) // 109 chrome

	// Next snapshot: 105 is gone; 107's pid now belongs to a new process
	// (same pid, later start); 109 is still there, with other numbers.
	next := append([]sample(nil), samples...)
	next = append(next[:4], next[5:]...)
	next[5].start = 99999 // was 107 sshd
	next[5].name = "sshd"
	next[7].cpuPercent = 50
	p.applySamples(next)
	wantMarked(t, p, "after the refresh", 109)
	if len(p.marks) != 1 {
		t.Fatalf("marks kept %d entries for processes that are gone, want 1", len(p.marks))
	}
}

// TestCursorStaysOnItsProcessAcrossARefresh is what makes F8, Ins and
// Shift+F1/F2 safe under a CPU% sort, which moves the rows on every refresh.
func TestCursorStaysOnItsProcessAcrossARefresh(t *testing.T) {
	p := fakePanel(t)
	p.sortBy(colCPU) // descending: 110 on top
	p.table.SetSelectPos(1)
	wantCursor(t, p, "before the refresh", 109)

	next := markTestSamples()
	next[8].cpuPercent = -1 // 109 drops to the bottom
	p.applySamples(next)
	wantCursor(t, p, "after the refresh", 109)
	if p.table.SelectPos != p.table.ItemCount-1 {
		t.Fatalf("cursor at %d, want the bottom row %d where 109 went", p.table.SelectPos, p.table.ItemCount-1)
	}

	// A process that is gone leaves the cursor on its row.
	next = append(next[:8], next[9:]...)
	p.applySamples(next)
	if p.table.SelectPos != p.table.ItemCount-1 {
		t.Fatalf("cursor at %d after its process exited, want the row it was on, clamped: %d", p.table.SelectPos, p.table.ItemCount-1)
	}
}

// installKeymap makes keymap.GlobalHotkeysMgr bind each event to an action in
// the Shell area, spelled the way the key router spells it, for the test.
func installKeymap(t *testing.T, binds map[*vtinput.InputEvent]string) {
	t.Helper()
	hm := &keymap.HotkeyManager{Bindings: map[string]map[string]string{shellArea: {}}, Defaults: map[string]map[string]string{}}
	for e, action := range binds {
		hm.Bindings[shellArea][keymap.EventToHotkeyString(e)] = action
	}
	previous := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = hm
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
}

// TestGroupKeysAreTheFilePanelsOwnBindings covers the selection keys looked
// up in the keymap: they mean here what they mean in the file panels, on
// whatever key the user put them -- Alt+= stands in for Gray * below.
func TestGroupKeysAreTheFilePanelsOwnBindings(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	invert := keyEvent(vtinput.VK_OEM_PLUS, vtinput.LeftAltPressed)
	installKeymap(t, map[*vtinput.InputEvent]string{
		keyEvent(vtinput.VK_ADD, 0):      "Panel.SelectGroup",
		keyEvent(vtinput.VK_SUBTRACT, 0): "Panel.DeselectGroup",
		invert:                           "Panel.InvertSelection",
		keyEvent(vtinput.VK_ADD, vtinput.LeftCtrlPressed):      "Panel.SelectCurrentExtension",
		keyEvent(vtinput.VK_SUBTRACT, vtinput.LeftCtrlPressed): "Panel.DeselectCurrentExtension",
		keyEvent(vtinput.VK_M, vtinput.LeftCtrlPressed):        "Panel.RestoreSelection",
	})
	p := fakePanel(t)

	// Ctrl+Gray +: every process named like 102 "chrome", in any case.
	p.table.SetSelectPos(1)
	press(t, p, vtinput.VK_ADD, vtinput.LeftCtrlPressed)
	wantMarked(t, p, "Ctrl+Gray +", 102, 103, 106, 109)

	// Gray -: the mask dialog, deselecting what the mask matches.
	press(t, p, vtinput.VK_SUBTRACT, 0)
	answerMaskDialog(t, "CHROME")
	wantMarked(t, p, "Gray - CHROME")

	// Ctrl+M swaps back the selection the last group change replaced, and
	// a second Ctrl+M swaps it out again.
	press(t, p, vtinput.VK_M, vtinput.LeftCtrlPressed)
	wantMarked(t, p, "Ctrl+M", 102, 103, 106, 109)
	press(t, p, vtinput.VK_M, vtinput.LeftCtrlPressed)
	wantMarked(t, p, "Ctrl+M again")

	press(t, p, invert.VirtualKeyCode, invert.ControlKeyState)
	wantMarked(t, p, "Alt+= (invert)", 101, 102, 103, 104, 105, 106, 107, 108, 109, 110)

	press(t, p, vtinput.VK_SUBTRACT, vtinput.LeftCtrlPressed)
	wantMarked(t, p, "Ctrl+Gray - on chrome", 101, 104, 105, 107, 108, 110)

	// Gray +, f4's mask syntax: a list, case-insensitive.
	press(t, p, vtinput.VK_SUBTRACT, vtinput.ShiftPressed)
	press(t, p, vtinput.VK_ADD, 0)
	answerMaskDialog(t, "FIRE*,z*")
	wantMarked(t, p, "Gray + FIRE*,z*", 105, 108)
}

// answerMaskDialog types mask into the open mask dialog and presses OK.
func answerMaskDialog(t *testing.T, mask string) {
	t.Helper()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("no mask dialog open: top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	edit := history.InputBoxEdit(dlg)
	if edit == nil {
		t.Fatal("the mask dialog has no input field")
	}
	if edit.HistoryID != history.FileMasksHistoryID {
		t.Fatalf("mask field history = %q, want the file panels' %q", edit.HistoryID, history.FileMasksHistoryID)
	}
	edit.SetText(mask)
	for _, child := range dlg.GetChildren() {
		if b, ok := child.(*vtui.Button); ok {
			b.OnClick() // OK comes first
			// Nothing runs the frame loop here to take the closed dialog
			// off the stack, so the next one would open under it.
			vtui.FrameManager.RemoveFrame(dlg)
			return
		}
	}
	t.Fatal("the mask dialog has no buttons")
}

func TestGroupKeysWorkOnTheQuickSearchMatches(t *testing.T) {
	p := fakePanel(t)
	p.table.SetSearchText("chrome")
	press(t, p, vtinput.VK_ADD, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+Gray + under a search", 102, 103, 106, 109)

	p.table.ClearSearch()
	press(t, p, vtinput.VK_ADD, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+Gray +", 101, 102, 103, 104, 105, 106, 107, 108, 109, 110)
	press(t, p, vtinput.VK_SUBTRACT, vtinput.ShiftPressed)
	wantMarked(t, p, "Shift+Gray -")
}

// TestGroupKeysWithoutAKeymapAreNotClaimed: a host that never loaded a
// keymap binds nothing, so Gray + is left to the table like any other key.
func TestGroupKeysWithoutAKeymapAreNotClaimed(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = nil
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
	p := fakePanel(t)
	if p.ProcessKey(keyEvent(vtinput.VK_ADD, 0)) {
		t.Fatal("Gray + was claimed with no keymap to bind it")
	}
	wantMarked(t, p, "Gray + with no keymap")
}

func rightMouse(p *procListPanel, pos int, keyDown, moving bool) *vtinput.InputEvent {
	e := &vtinput.InputEvent{
		Type:    vtinput.MouseEventType,
		KeyDown: keyDown,
		MouseX:  testutil.Int16(p.table.X1 + 2),
		MouseY:  testutil.Int16(p.table.Y1 + p.table.MarginTop + pos - p.table.TopPos),
	}
	if keyDown {
		e.ButtonState = vtinput.RightmostButtonPressed
	}
	if moving {
		e.MouseEventFlags = vtinput.MouseMoved
	}
	return e
}

func TestRightMouseButtonSelectsAndDrags(t *testing.T) {
	p := fakePanel(t)
	p.Show(vtui.NewSilentScreenBuf()) // lay the table out

	if !p.ProcessMouse(rightMouse(p, 1, true, false)) {
		t.Fatal("right button press was not claimed")
	}
	wantMarked(t, p, "right click on 102", 102)
	wantCursor(t, p, "right click on 102", 102)
	p.ProcessMouse(rightMouse(p, 3, true, true))
	wantMarked(t, p, "drag to 104", 102, 103, 104)
	p.ProcessMouse(rightMouse(p, 3, false, false))
	if p.rightDrag {
		t.Fatal("the release did not end the drag")
	}

	// A press on a selected row deselects, and its drag carries that.
	p.ProcessMouse(rightMouse(p, 2, true, false))
	p.ProcessMouse(rightMouse(p, 4, true, true))
	p.ProcessMouse(rightMouse(p, 4, false, false))
	wantMarked(t, p, "right drag from selected 103 to 105", 102)
	wantCursor(t, p, "after the drags", 105)

	// The left button is the table's, as before.
	left := rightMouse(p, 0, true, false)
	left.ButtonState = vtinput.FromLeft1stButtonPressed
	p.ProcessMouse(left)
	wantMarked(t, p, "left click", 102)
	wantCursor(t, p, "left click", 101)
}

// TestSelectionSummaryOnTheBottomBorder is far's selected-files line, for
// processes: the count and the memory they hold, centred on the bottom
// border, shortened to the count where the panel is narrow.
func TestSelectionSummaryOnTheBottomBorder(t *testing.T) {
	p := fakePanel(t)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	p.Show(scr)
	if row := testutil.ScreenRow(scr, 19, 0, 39); strings.ContainsRune(row, ':') {
		t.Fatalf("bottom border %q shows a summary with nothing selected", row)
	}

	samples := markTestSamples()
	p.marks.set(samples[0].key(), true) // 1.0 M
	p.marks.set(samples[1].key(), true) // 2.0 M
	p.Show(scr)
	want := fmt.Sprintf(" "+i18n.Msg("ProcList.SelectedInfo")+" ", 2, "3.0 M")
	if got := p.selectionSummary(38); got != want {
		t.Fatalf("selectionSummary = %q, want %q", got, want)
	}
	if row := testutil.ScreenRow(scr, 19, 0, 39); !strings.Contains(row, want) {
		t.Fatalf("bottom border %q does not show %q", row, want)
	}

	short := fmt.Sprintf(" "+i18n.Msg("ProcList.SelectedInfoShort")+" ", 2)
	if got := p.selectionSummary(vtui.StringWidth(want)); got != short {
		t.Fatalf("selectionSummary in %d columns = %q, want %q", vtui.StringWidth(want), got, short)
	}
	if got := p.selectionSummary(3); got != "" {
		t.Fatalf("selectionSummary in 3 columns = %q, want nothing", got)
	}
}
