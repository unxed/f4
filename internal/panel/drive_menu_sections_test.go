package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// driveMenuSectionsSetup points the settings at a temporary directory with one
// bookmark in slot 3 and two plugin tools, and opens the drive menu.
func driveMenuSectionsSetup(t *testing.T, hidden []string) (*PanelsFrame, *vtui.VMenu, string) {
	t.Helper()
	cfg := t.TempDir()
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}
	place := t.TempDir()
	if err := os.WriteFile(BookmarksFilePath(),
		[]byte("[3]\nPath="+place+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveDisabledDriveTools(DriveToolsVisibilityFilePath(), hidden); err != nil {
		t.Fatal(err)
	}
	restore := sysinfo.SnapshotDrives()
	t.Cleanup(restore)
	sysinfo.SetDrives([]sysinfo.DriveEntry{
		{Name: "Alpha", Factory: func() vfs.VFS { return nil }},
		{Name: "Beta", Factory: func() vfs.VFS { return nil }},
	})
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	pf.ShowDriveMenu(0)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}
	return pf, menu, place
}

func ruleRows(menu *vtui.VMenu) map[string]int {
	rows := map[string]int{}
	for i, item := range menu.Items {
		if item.Separator && item.Text != "" {
			rows[strings.TrimSpace(item.Text)] = i
		}
	}
	return rows
}

// f4#1148 (mitek1977-coder): the sections of the drive menu go in the order
// of the pages of the drive chooser: Tools, Bookmarks, Links.
func TestDriveMenuBookmarksComeAfterTheTools(t *testing.T) {
	_, menu, _ := driveMenuSectionsSetup(t, nil)
	rules := ruleRows(menu)
	tools, okT := rules[strings.TrimSpace(i18n.Msg("Drive.Tools"))]
	bookmarks, okB := rules[strings.TrimSpace(i18n.Msg("Bookmarks.Title"))]
	links, okL := rules[strings.TrimSpace(i18n.Msg("Drive.Links"))]
	if !okT || !okB || !okL {
		t.Fatalf("rules %v: want Tools, Bookmarks and Links", rules)
	}
	if tools >= bookmarks || bookmarks >= links {
		t.Fatalf("rows Tools %d, Bookmarks %d, Links %d: want them in that order", tools, bookmarks, links)
	}
}

// f4#1148: with every tool hidden on the Tools page the menu has no Tools rule
// with nothing under it.
func TestDriveMenuHasNoToolsRuleWithoutTools(t *testing.T) {
	_, menu, _ := driveMenuSectionsSetup(t, []string{driveToolKeyOtherPanel, driveToolKeyTemporaryPanel, driveToolKeyRegistry, "Alpha", "Beta"})
	if _, ok := ruleRows(menu)[strings.TrimSpace(i18n.Msg("Drive.Tools"))]; ok {
		t.Fatal("the Tools rule is shown with every tool hidden")
	}
}

// f4#1148: Del on a bookmark row asks first, as on a link; Cancel keeps it,
// Delete removes it.
func TestDriveMenuDelOnBookmarkAsks(t *testing.T) {
	_, menu, place := driveMenuSectionsSetup(t, nil)
	row := -1
	for i, item := range menu.Items {
		if strings.HasPrefix(item.Text, "&3") {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no row for bookmark slot 3")
	}
	del := func() *vtui.Window {
		t.Helper()
		menu.SetSelectPos(row)
		before := vtui.FrameManager.GetTopFrame()
		before.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})
		q, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if !ok || vtui.Frame(q) == before || q.OnResult == nil {
			t.Fatalf("Del on the bookmark asked nothing (top %T)", vtui.FrameManager.GetTopFrame())
		}
		return q
	}
	slot3 := func() string {
		set, err := LoadBookmarks(BookmarksFilePath())
		if err != nil {
			t.Fatal(err)
		}
		return set[3].Path
	}

	q := del()
	q.OnResult(1) // Cancel
	vtui.FrameManager.Pop()
	if slot3() != place {
		t.Fatal("Cancel removed the bookmark")
	}
	q = del()
	q.OnResult(0) // Delete
	vtui.FrameManager.Pop()
	if slot3() != "" {
		t.Fatal("Delete kept the bookmark")
	}
	drainDriveMenuTasks()
}

// drainDriveMenuTasks runs the reopen the deletion posted, so nothing is left
// in the UI queue for the next test.
func drainDriveMenuTasks() {
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			return
		}
	}
}
