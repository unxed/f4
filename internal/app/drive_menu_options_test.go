package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/settings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestDriveMenuPlatformRowsAlignColumns(t *testing.T) {
	options := config.DriveMenuShowType | config.DriveMenuShowLabel | config.DriveMenuShowFilesystem | config.DriveMenuShowSize
	rows := []panel.DriveMenuPlatformRow{
		{Base: "C:", Kind: "fixed", Label: "Win10", Filesystem: "NTFS", Total: "953.0 GiB", Free: "300.6 GiB"},
		{Base: "K:", Kind: "network", Label: "DISK-K", Filesystem: "NTFS", Total: "13.8 TiB", Free: "1.2 TiB"},
	}
	lines := panel.DriveMenuPlatformRowsText(rows, options)
	if len(lines) != 2 {
		t.Fatalf("got %d rows, want 2: %q", len(lines), lines)
	}
	for _, column := range []string{"fixed", "Win10", "NTFS"} {
		if got, want := strings.Index(lines[0], column), strings.Index(lines[1], map[string]string{
			"fixed": "network", "Win10": "DISK-K", "NTFS": "NTFS",
		}[column]); got != want {
			t.Fatalf("column %q starts at %d and %d, want equal starts", column, got, want)
		}
	}
	if !strings.Contains(lines[0], "953.0 GiB | 300.6 GiB") || !strings.Contains(lines[1], " 13.8 TiB |   1.2 TiB") {
		t.Fatalf("size columns are not aligned/right-justified: %q", lines)
	}
}

func TestDriveMenuPhysicalDiskHasNoTypeDescription(t *testing.T) {
	got := panel.DriveMenuPlatformItemText(sysinfo.DriveEntry{Name: "Physical Disks"}, config.DriveMenuShowType)
	if got != "Physical Disks" {
		t.Fatalf("physical disk row = %q, want no type suffix", got)
	}
}

func TestDriveMenuOptionsDialogSizeIsContentBased(t *testing.T) {
	width, height := panel.DriveMenuOptionsDialogSize()
	if width >= 78 {
		t.Fatalf("options dialog width = %d, want narrower than the old fixed width", width)
	}
	if height != len(panel.DriveMenuOptionSpecs)+7 {
		t.Fatalf("options dialog height = %d, want %d", height, len(panel.DriveMenuOptionSpecs)+7)
	}
	longest := 0
	for _, spec := range panel.DriveMenuOptionSpecs {
		label, _, _ := vtui.ParseAmpersandString(i18n.Msg(spec.Label))
		if w := vtui.StringWidth(label); w > longest {
			longest = w
		}
	}
	if width < longest+8 {
		t.Fatalf("options dialog width = %d, does not fit longest checkbox label of %d", width, longest)
	}
}

func TestDriveMenuOptions_DefaultsAndFormatting(t *testing.T) {
	if config.ParseDriveMenuOptions("") != config.DefaultDriveMenuOptions {
		t.Fatalf("empty options did not use defaults: %#x", config.ParseDriveMenuOptions(""))
	}
	if config.ParseDriveMenuOptions("not-a-number") != config.DefaultDriveMenuOptions {
		t.Fatalf("invalid options did not use defaults")
	}
	if got := panel.DriveMenuPlatformItemText(sysinfo.DriveEntry{Name: "/ Root"}, config.DriveMenuShowType|config.DriveMenuShowFilesystem); !strings.Contains(got, "/") {
		t.Fatalf("root row lost its name: %q", got)
	}
	if got := panel.DriveMenuSize(1024*1024*3, false); got != "3 MiB" {
		t.Fatalf("integer drive size = %q, want 3 MiB", got)
	}
	if got := panel.DriveMenuSize(1024*1024*3, true); !strings.Contains(got, "3.0") {
		t.Fatalf("decimal drive size = %q, want a decimal value", got)
	}
}

func TestPanelsFrame_DriveMenu_F9OpensOptions(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// With no tools registered there is nothing for the tools window to list
	// (f4#918), so F9 goes straight to the drive options.
	oldDrives := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(oldDrives) })
	sysinfo.SetDrives(nil)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	oldOptions := config.App.DriveMenuOptions
	config.App.DriveMenuOptions = config.DefaultDriveMenuOptions
	t.Cleanup(func() { config.App.DriveMenuOptions = oldOptions })

	pf.ShowDriveMenu(0)
	menu, ok := paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatalf("drive menu not opened: %T", vtui.FrameManager.GetTopFrame())
	}
	if !strings.Contains(i18n.Msg("Drive.BottomHint"), "F9") {
		t.Fatalf("drive menu hint does not advertise F9: %q", i18n.Msg("Drive.BottomHint"))
	}
	if !menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F9,
	}) {
		t.Fatal("F9 was not consumed by the drive menu")
	}
	if vtui.FrameManager.GetTopFrame() == menu {
		t.Fatal("F9 did not open the drive options dialog")
	}
	dlg, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatalf("drive options frame is not a container: %T", vtui.FrameManager.GetTopFrame())
	}
	center, ok := dlg.(*settings.Center)
	if !ok || center.Category() != "drives.options" {
		t.Fatal("F9 must open the Drive options page of the Drive chooser window")
	}
	center.Show(vtui.NewSilentScreenBuf())
	vtui.FrameManager.Pop()
	menu.Close()
}

// With drive tools registered, F9 opens the same settings window at once, with
// the Tools page among its pages and no window of the tools in between
// (f4#1148).
func TestPanelsFrame_DriveMenu_F9OpensTheSettingsWindowWithAToolsPage(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldDrives := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(oldDrives) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "&A Alpha drive"}})
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	oldOptions := config.App.DriveMenuOptions
	config.App.DriveMenuOptions = config.DefaultDriveMenuOptions
	t.Cleanup(func() { config.App.DriveMenuOptions = oldOptions })

	pf.ShowDriveMenu(0)
	menu, ok := paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatalf("drive menu not opened: %T", vtui.FrameManager.GetTopFrame())
	}
	if !menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F9}) {
		t.Fatal("F9 was not consumed by the drive menu")
	}
	center, ok := vtui.FrameManager.GetTopFrame().(*settings.Center)
	if !ok || center.Category() != "drives.options" {
		t.Fatalf("F9 opened %T, want the Drive chooser settings window on its first page", vtui.FrameManager.GetTopFrame())
	}
}
