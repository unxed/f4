package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#1148 (mitek1977-coder): the Tools page lists the built-in tools first and
// everything in the drive menu's own order, and hiding a built-in tool there
// takes it out of the menu.
func TestDriveMenuToolChoicesFollowTheMenu(t *testing.T) {
	cfg := t.TempDir()
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldOptions := config.App.DriveMenuOptions
	t.Cleanup(func() { config.App.DriveMenuOptions = oldOptions })
	config.App.DriveMenuOptions &^= config.DriveMenuSortPluginsByHotkey
	defer sysinfo.SnapshotDrives()()
	sysinfo.SetDrives([]sysinfo.DriveEntry{
		{Name: "Alpha", Factory: func() vfs.VFS { return nil }},
		{Name: "Beta", Factory: func() vfs.VFS { return nil }},
	})

	keys := func() string {
		var out []string
		for _, c := range DriveMenuToolChoices() {
			if c.Key != driveToolKeyRegistry {
				out = append(out, c.Key)
			}
		}
		return fmt.Sprint(out)
	}
	if got := keys(); got != "[@other-panel @temporary-panel Alpha Beta]" {
		t.Fatalf("default order %s", got)
	}
	// Ctrl+Up/Down in the menu stored Beta first and Other panel after Alpha.
	if err := SaveDriveToolsOrder(DriveToolsOrderFilePath(), []string{"Beta", driveToolKeyTemporaryPanel, "Alpha", driveToolKeyOtherPanel}); err != nil {
		t.Fatal(err)
	}
	if got := keys(); got != "[Beta @temporary-panel Alpha @other-panel]" {
		t.Fatalf("stored order %s", got)
	}

	// Hidden on the Tools page, Other panel is gone from the menu.
	if err := SaveDisabledDriveTools(DriveToolsVisibilityFilePath(), []string{driveToolKeyOtherPanel}); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowDriveMenu(0)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}
	other := strings.TrimSpace(strings.ReplaceAll(i18n.Msg("Panel.Other"), "&", ""))
	for _, item := range menu.Items {
		if strings.TrimSpace(strings.ReplaceAll(item.Text, "&", "")) == other {
			t.Fatal("the hidden Other panel is still in the drive menu")
		}
	}
}
