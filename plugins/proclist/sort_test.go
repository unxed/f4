//go:build linux || windows || darwin

package proclist

import (
	"slices"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// shownPIDs is the order the table shows the processes in.
func shownPIDs(p *procListPanel) []int {
	var pids []int
	p.eachShown(func(_ int, s sample) { pids = append(pids, s.pid) })
	return pids
}

func wantOrder(t *testing.T, p *procListPanel, step string, want ...int) {
	t.Helper()
	if got := shownPIDs(p); !slices.Equal(got, want) {
		t.Fatalf("%s: order %v, want %v", step, got, want)
	}
}

// TestSortKeysPickTheirColumnAndDirection covers Ctrl+F3..Ctrl+F7: each key
// sorts its column the way that column is wanted first, the same key again
// reverses it, and the cursor stays on its process throughout.
func TestSortKeysPickTheirColumnAndDirection(t *testing.T) {
	p := fakePanel(t)
	p.table.SetSelectPos(4) // 105 firefox
	ctrl := vtinput.LeftCtrlPressed

	// Names ignore case; equal names go by PID.
	press(t, p, vtinput.VK_F3, ctrl)
	wantOrder(t, p, "Ctrl+F3", 104, 102, 103, 106, 109, 110, 105, 101, 107, 108)
	wantCursor(t, p, "Ctrl+F3", 105)
	press(t, p, vtinput.VK_F3, ctrl)
	wantOrder(t, p, "Ctrl+F3 again", 108, 107, 101, 105, 110, 109, 106, 103, 102, 104)

	// Memory and CPU start with the biggest on top.
	press(t, p, vtinput.VK_F6, ctrl)
	wantOrder(t, p, "Ctrl+F6", 110, 109, 108, 107, 106, 105, 104, 103, 102, 101)
	press(t, p, vtinput.VK_F6, ctrl)
	wantOrder(t, p, "Ctrl+F6 again", 101, 102, 103, 104, 105, 106, 107, 108, 109, 110)
	press(t, p, vtinput.VK_F5, ctrl)
	wantOrder(t, p, "Ctrl+F5", 110, 109, 108, 107, 106, 105, 104, 103, 102, 101)
	wantCursor(t, p, "Ctrl+F5", 105)

	press(t, p, vtinput.VK_F4, ctrl)
	wantOrder(t, p, "Ctrl+F4", 101, 102, 103, 104, 105, 106, 107, 108, 109, 110)

	// Unsorted is the order the snapshot came in.
	shuffled := markTestSamples()
	shuffled[0], shuffled[9] = shuffled[9], shuffled[0]
	p.applySamples(shuffled)
	press(t, p, vtinput.VK_F7, ctrl)
	if col, _ := p.sortedBy(); col != sortUnsorted {
		t.Fatalf("Ctrl+F7: sorted by %d, want unsorted", col)
	}
	wantOrder(t, p, "Ctrl+F7", 110, 102, 103, 104, 105, 106, 107, 108, 109, 101)
	wantCursor(t, p, "Ctrl+F7", 105)
}

// TestSortKeysOfAHiddenColumnAreDimmed: a column hidden in ProcList.Config
// has nothing to sort by, so its key is dimmed on the keybar and does
// nothing, and its menu row is disabled.
func TestSortKeysOfAHiddenColumnAreDimmed(t *testing.T) {
	store := &settingsStore{current: DefaultSettings()}
	store.current.VisibleColumns = []string{"pid", "name", "mem"}
	controller, err := newProcListPanel(vfs.PanelContext{Bounds: [4]int{0, 0, 39, 19}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	p := controller.(*procListPanel)
	p.applySamples(markTestSamples())
	p.sortBy(colMem)

	for _, k := range p.PanelKeys() {
		if k.VK != vtinput.VK_F5 || k.Mods != vtinput.LeftCtrlPressed {
			continue
		}
		if k.Enabled == nil || k.Enabled() {
			t.Fatal("Ctrl+F5 (CPU%) is enabled with the CPU% column hidden")
		}
	}
	press(t, p, vtinput.VK_F5, vtinput.LeftCtrlPressed)
	if col, ascending := p.sortedBy(); col != colMem || ascending {
		t.Fatalf("after Ctrl+F5 with CPU%% hidden: sorted by %d asc=%v, want memory, descending, untouched", col, ascending)
	}
}

// TestHeaderClickStartsAColumnLikeItsKey: Table sorts a header click
// ascending; the panel turns a newly picked column to its first direction,
// so clicking Mem puts the biggest on top, as Ctrl+F6 does.
func TestHeaderClickStartsAColumnLikeItsKey(t *testing.T) {
	p := fakePanel(t)
	p.Show(vtui.NewSilentScreenBuf())
	p.table.SetSelectPos(4) // 105

	// Columns: PID(8) | Name | Mem(10) | CPU%(7), one separator between each.
	memX := p.table.X1 + p.table.GetContentWidth() - 7 - 1 - 10 + 3
	click := &vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      testutil.Int16(memX),
		MouseY:      testutil.Int16(p.table.Y1 + 1), // the header, under the QuickSearch line
	}
	p.ProcessMouse(click)
	if col, ascending := p.sortedBy(); col != colMem || ascending {
		t.Fatalf("after a click on Mem: sorted by %d asc=%v, want memory, descending", col, ascending)
	}
	wantCursor(t, p, "after a click on Mem", 105)
	p.ProcessMouse(click)
	if col, ascending := p.sortedBy(); col != colMem || !ascending {
		t.Fatalf("after a second click on Mem: sorted by %d asc=%v, want memory, ascending", col, ascending)
	}
}

func TestSortMenuMarksTheModeInForceAndSorts(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := fakePanel(t)

	press(t, p, vtinput.VK_F12, vtinput.LeftCtrlPressed)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("Ctrl+F12 opened %T, want the sort menu", vtui.FrameManager.GetTopFrame())
	}
	if len(menu.Items) != len(sortModes) {
		t.Fatalf("sort menu has %d rows, want %d", len(menu.Items), len(sortModes))
	}
	pidRow := menu.Items[1]
	if want := sortMarker(true) + " " + i18n.Msg("ProcList.Sort.PID"); pidRow.Text != want || pidRow.Shortcut != "Ctrl+F4" {
		t.Fatalf("PID row = %q / %q, want %q / Ctrl+F4", pidRow.Text, pidRow.Shortcut, want)
	}
	if menu.SelectPos != 1 {
		t.Fatalf("sort menu opens on row %d, want the PID row in force", menu.SelectPos)
	}

	menu.OnAction(3) // memory
	if col, ascending := p.sortedBy(); col != colMem || ascending {
		t.Fatalf("menu row Memory: sorted by %d asc=%v, want memory, descending", col, ascending)
	}

	vtui.FrameManager.RemoveFrame(menu)
	p.sortBy(sortUnsorted)
	press(t, p, vtinput.VK_F12, vtinput.LeftCtrlPressed)
	menu = vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if got, want := menu.Items[len(sortModes)-1].Text, sortCheckMark+" "+i18n.Msg("Menu.SortUnsorted"); got != want {
		t.Fatalf("unsorted row = %q, want %q", got, want)
	}
}

func TestCompareNamesIgnoresCase(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"bash", "Bash", 0},
		{"Xorg", "bash", 1},
		{"f4", "firefox", -1},
		{"chrome", "chromedriver", -1},
		{"Ärger", "ärger", 0},
		{"", "a", -1},
	}
	for _, c := range cases {
		got := compareNames(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("compareNames(%q, %q) = %d, want the sign of %d", c.a, c.b, got, c.want)
		}
	}
}
