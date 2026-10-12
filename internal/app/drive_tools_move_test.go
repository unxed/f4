package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// keepDriveToolsOrderFile starts a test with no stored tool order and puts the
// file back as it was afterwards. The profile directory is resolved once per
// process, so the tests share the file and must not leave anything in it.
func keepDriveToolsOrderFile(t *testing.T) {
	t.Helper()
	path := panel.DriveToolsOrderFilePath()
	before, readErr := os.ReadFile(path)
	_ = os.Remove(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
}

// f4#1148: Ctrl+Up and Ctrl+Down on a tool row of the drive menu move it and
// store the order.
func TestDriveMenuCtrlUpDownMovesTheToolRow(t *testing.T) {
	keepDriveToolsOrderFile(t)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldDrives := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(oldDrives) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Alpha tool"}, {Name: "Beta tool"}, {Name: "Gamma tool"}})
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
	row := func(name string) int {
		for i, item := range menu.Items {
			if strings.Contains(item.Text, name) {
				return i
			}
		}
		t.Fatalf("no row %q", name)
		return -1
	}
	pump := func() {
		for {
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}
	press := func(vk uint16) bool {
		handled := menu.ProcessKey(&vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
		pump() // the menu is closed and opened again in the new order
		return handled
	}
	stored := func() []string {
		t.Helper()
		order, err := panel.LoadDriveToolsOrder(panel.DriveToolsOrderFilePath())
		if err != nil {
			t.Fatal(err)
		}
		return order
	}
	index := func(order []string, key string) int {
		for i, k := range order {
			if k == key {
				return i
			}
		}
		return -1
	}

	// A plugin's row moves up past its neighbour.
	menu.SetSelectPos(row("Beta tool"))
	if !press(vtinput.VK_UP) {
		t.Fatal("Ctrl+Up was not consumed by the drive menu")
	}
	order := stored()
	if index(order, "Beta tool") < 0 || index(order, "Beta tool") > index(order, "Alpha tool") {
		t.Fatalf("stored order after Ctrl+Up on Beta = %q, want Beta before Alpha", order)
	}

	// The menu is rebuilt in that order, and a built-in row moves down past a
	// plugin's the same way (f4#1148: every row of Tools can be moved).
	menu, ok = paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatalf("the menu did not come back: %T", vtui.FrameManager.GetTopFrame())
	}
	if row("Beta tool") > row("Alpha tool") {
		t.Fatalf("the menu does not show Beta above Alpha after the move")
	}
	otherLabel := strings.ReplaceAll(i18n.Msg("Panel.Other"), "&", "")
	findOther := func() int {
		for i, item := range menu.Items {
			if strings.Contains(strings.ReplaceAll(item.Text, "&", ""), otherLabel) {
				return i
			}
		}
		return -1
	}
	otherRow := findOther()
	if otherRow < 0 {
		t.Fatal("no Other panel row")
	}
	menu.SetSelectPos(otherRow)
	// Down until it has passed Beta (a platform may list more built-in rows
	// than another, the Windows registry for one).
	for i := 0; i < 8 && index(stored(), "@other-panel") < index(stored(), "Beta tool"); i++ {
		press(vtinput.VK_DOWN)
		menu, ok = paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame())
		if !ok {
			t.Fatalf("the menu did not come back: %T", vtui.FrameManager.GetTopFrame())
		}
		if j := findOther(); j >= 0 {
			menu.SetSelectPos(j)
		}
	}
	order = stored()
	if index(order, "@other-panel") < index(order, "Beta tool") {
		t.Fatalf("Other panel is still above Beta after repeated Ctrl+Down: %q", order)
	}
}

// With "sort plugins by hotkey" on, the plugin tools sit in name order and a
// move would be undone at once; the menu says so instead of doing nothing
// (f4#1148).
func TestDriveMenuCtrlUpOnSortedToolsExplainsWhy(t *testing.T) {
	keepDriveToolsOrderFile(t)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldDrives := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(oldDrives) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Alpha tool"}, {Name: "Beta tool"}})
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	oldOptions := config.App.DriveMenuOptions
	config.App.DriveMenuOptions = config.DefaultDriveMenuOptions | config.DriveMenuSortPluginsByHotkey
	t.Cleanup(func() { config.App.DriveMenuOptions = oldOptions })

	pf.ShowDriveMenu(0)
	menu, ok := paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatalf("drive menu not opened: %T", vtui.FrameManager.GetTopFrame())
	}
	for i, item := range menu.Items {
		if strings.Contains(item.Text, "Beta tool") {
			menu.SetSelectPos(i)
		}
	}
	if !menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_UP,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+Up was not consumed")
	}
	if _, stillMenu := paneltest.DriveMenuFromFrame(vtui.FrameManager.GetTopFrame()); stillMenu {
		t.Fatal("nothing told the user why the tool did not move")
	}
	order, err := panel.LoadDriveToolsOrder(panel.DriveToolsOrderFilePath())
	if err != nil || len(order) != 0 {
		t.Fatalf("an order was stored although the tools are sorted by name: %q %v", order, err)
	}
}
