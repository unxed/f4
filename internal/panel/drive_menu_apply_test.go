package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#1148 (mitek1977-coder): Apply in the drive chooser's settings window,
// opened by F9 in the drive menu, rebuilds the menu under it from the new
// settings, and the window stays open on top.
func TestDriveMenuRebuiltUnderSettingsOnApply(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	defer sysinfo.SnapshotDrives()()
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Beta tool", Factory: func() vfs.VFS { return nil }}})
	oldOptions := config.App.DriveMenuOptions
	oldOpen := OpenSettingsCategoryOnly
	t.Cleanup(func() {
		config.App.DriveMenuOptions = oldOptions
		OpenSettingsCategoryOnly = oldOpen
		driveMenuBelowSettings = nil
	})
	config.App.DriveMenuOptions = config.DefaultDriveMenuOptions

	pf.ShowDriveMenu(0)
	old, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}
	settings := vtui.NewCenteredDialog(40, 10, "Drive chooser")
	OpenSettingsCategoryOnly = func(string) bool {
		vtui.FrameManager.Push(settings)
		return true
	}
	pf.openDriveMenuTools(0, old)
	if vtui.FrameManager.GetTopFrame() != settings {
		t.Fatal("F9 did not open the settings window")
	}

	hasTool := func(m *vtui.VMenu) bool {
		for _, item := range m.Items {
			if strings.Contains(item.Text, "Beta tool") {
				return true
			}
		}
		return false
	}
	if !hasTool(old) {
		t.Fatal("the tool is not in the menu to begin with")
	}
	// Apply turns the tools off.
	config.App.DriveMenuOptions &^= config.DriveMenuShowPlugins
	RefreshDriveMenuBelow(settings)

	frames := vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx)
	if frames[len(frames)-1] != settings {
		t.Fatal("the settings window is no longer on top after Apply")
	}
	rebuilt, ok := frames[len(frames)-2].(*DriveMenuFrame)
	if !ok {
		t.Fatalf("under the settings window is %T, want the drive menu", frames[len(frames)-2])
	}
	if rebuilt.VMenu == old {
		t.Fatal("the drive menu was not rebuilt")
	}
	if hasTool(rebuilt.VMenu) {
		t.Error("the rebuilt drive menu still lists the tool turned off by Apply")
	}
	for _, f := range frames {
		if m, ok := f.(*DriveMenuFrame); ok && m.VMenu == old {
			t.Error("the old drive menu is still on the stack")
		}
	}

	// A second Apply rebuilds the menu that is there now.
	config.App.DriveMenuOptions |= config.DriveMenuShowPlugins
	RefreshDriveMenuBelow(settings)
	frames = vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx)
	again, ok := frames[len(frames)-2].(*DriveMenuFrame)
	if !ok || again == rebuilt || !hasTool(again.VMenu) {
		t.Error("a second Apply did not rebuild the menu with the tool back")
	}
}
