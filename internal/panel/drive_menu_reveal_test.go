package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// pickDriveRow opens the drive menu of side and chooses the row named name.
func pickDriveRow(t *testing.T, pf *PanelsFrame, side int, name string) {
	t.Helper()
	pf.ShowDriveMenu(side)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}
	for i, item := range menu.Items {
		if !item.Separator && strings.Contains(item.Text, name) {
			menu.OnAction(i)
			return
		}
	}
	t.Fatalf("no %q row in the drive menu", name)
}

func newRevealTestFrame(t *testing.T) (*PanelsFrame, string) {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	dir := t.TempDir()
	t.Cleanup(sysinfo.SnapshotDrives())
	sysinfo.SetDrives([]sysinfo.DriveEntry{
		{Name: "Alpha", Factory: func() vfs.VFS { return vfs.NewOSVFS(dir) }},
	})
	return pf, dir
}

// f4#1847: choosing a location for a hidden panel shows the panel.
func TestDriveMenuChoiceShowsTheHiddenSide(t *testing.T) {
	pf, _ := newRevealTestFrame(t)
	pf.ShowLeftPanel = false

	pf.ShowDriveMenu(0)
	if pf.ShowLeftPanel {
		t.Fatal("opening the menu alone showed the panel; Esc must leave it hidden")
	}
	vtui.FrameManager.GetTopFrame().(interface{ Close() }).Close()

	pickDriveRow(t, pf, 0, "Alpha")
	if !pf.ShowLeftPanel || !pf.ShowPanels {
		t.Fatalf("left panel still hidden after choosing a location: left=%v panels=%v", pf.ShowLeftPanel, pf.ShowPanels)
	}
}

// After Ctrl+O hid both panels, the choice shows its own side only: the
// other stays hidden (fromgate, f4#1847, after trying the first fix).
func TestDriveMenuChoiceAfterCtrlOShowsOnlyItsSide(t *testing.T) {
	for _, side := range []int{0, 1} {
		pf, _ := newRevealTestFrame(t)
		pf.TogglePanelsVisibility() // Ctrl+O
		if pf.ShowPanels {
			t.Fatal("Ctrl+O did not hide the panels")
		}
		pickDriveRow(t, pf, side, "Alpha")
		mine, other := pf.ShowLeftPanel, pf.ShowRightPanel
		if side == 1 {
			mine, other = other, mine
		}
		if !pf.ShowPanels || !mine || other {
			t.Fatalf("side %d: panels=%v this side=%v other side=%v, want only this side shown", side, pf.ShowPanels, mine, other)
		}
	}
}

// A side hidden on its own (Ctrl+F1) is shown without touching the other.
func TestDriveMenuChoiceKeepsTheOtherSideAsItWas(t *testing.T) {
	pf, _ := newRevealTestFrame(t)
	pf.ShowLeftPanel, pf.ShowRightPanel = false, false
	pickDriveRow(t, pf, 1, "Alpha")
	if !pf.ShowRightPanel || pf.ShowLeftPanel {
		t.Fatalf("left=%v right=%v, want only the right panel shown", pf.ShowLeftPanel, pf.ShowRightPanel)
	}
}
