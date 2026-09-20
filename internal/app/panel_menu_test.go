package app

import (
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"testing"
	"time"

	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

func TestPanelsFrame_MenuFitsSmallScreenAndScrolls(t *testing.T) {
	defer paneltest.SwapFrameManager(t)()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 10)
	vtui.FrameManager.Init(scr)

	pf := &panel.PanelsFrame{LastW: 80, LastH: 10}
	items := make([]string, 30)
	for i := range items {
		items[i] = "Item"
	}
	pf.Menu("Long plugin menu", items, nil)

	select {
	case task := <-vtui.FrameManager.TaskChan:
		task()
	case <-time.After(time.Second):
		t.Fatal("menu task was not posted")
	}

	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	if menu.Y1 < 0 || menu.Y2 >= scr.Height() {
		t.Fatalf("menu bounds = (%d,%d)-(%d,%d), screen height = %d", menu.X1, menu.Y1, menu.X2, menu.Y2, scr.Height())
	}
	if menu.ViewHeight >= len(items) || !menu.ShowScrollBar || menu.ScrollBar == nil {
		t.Fatalf("menu viewport = %d items=%d scrollbar=%v bar=%v", menu.ViewHeight, len(items), menu.ShowScrollBar, menu.ScrollBar != nil)
	}
	menu.SetSelectPos(len(items) - 1)
	if menu.TopPos == 0 {
		t.Fatal("selecting the last item did not scroll the menu")
	}
	menu.Close()
}

func TestPanelsFrame_SideMenusExposeDriveHotkeys(t *testing.T) {
	pf := &panel.PanelsFrame{}

	left := pf.LeftMenu().SubItems
	if !findSideDriveMenuItem(left, i18n.Msg("Menu.Left.DriveMenu"), "Alt+F1", appcmd.CmLeftDriveMenu) {
		t.Fatalf("left drive menu has no drive item: %+v", left)
	}

	right := pf.RightMenu().SubItems
	if !findSideDriveMenuItem(right, i18n.Msg("Menu.Right.DriveMenu"), "Alt+F2", appcmd.CmRightDriveMenu) {
		t.Fatalf("right drive menu has no drive item: %+v", right)
	}
}

func findSideDriveMenuItem(items []vtui.MenuItem, label, shortcut string, command int) bool {
	for _, item := range items {
		if plainMenuText(item.Text) == label && item.Shortcut == shortcut && item.Command == command {
			return true
		}
	}
	return false
}

func TestPanelsFrame_SideMenuExposesWorkspaceHotkeys(t *testing.T) {
	items := (&panel.PanelsFrame{}).LeftMenu().SubItems
	for _, tc := range []struct {
		command  int
		label    string
		shortcut string
	}{
		{command: appcmd.CmWorkspaceNew, label: "Action.Workspace.New", shortcut: "Ctrl+N"},
		{command: appcmd.CmWorkspaceClose, label: "Action.Workspace.Close", shortcut: "Ctrl+W"},
	} {
		found := false
		for _, item := range items {
			if item.Command != tc.command {
				continue
			}
			found = true
			if item.Text != i18n.Msg(tc.label) {
				t.Errorf("workspace command %d label = %q, want %q", tc.command, item.Text, i18n.Msg(tc.label))
			}
			if item.Shortcut != tc.shortcut {
				t.Errorf("workspace command %d shortcut = %q, want %q", tc.command, item.Shortcut, tc.shortcut)
			}
		}
		if !found {
			t.Errorf("left menu has no workspace command %d", tc.command)
		}
	}
}

func TestPanelsFrame_GetMenuBarKeepsNativeWorkspaceHotkeys(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHotkeys })

	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	vtui.FrameManager.Push(pf)
	menuFrame := vtui.NewVMenu("test")
	vtui.FrameManager.Push(menuFrame)
	t.Cleanup(func() { vtui.FrameManager.Pop() })

	find := func(command int) *vtui.MenuItem {
		menu := pf.GetMenuBar()
		for i := range menu.Items[0].SubItems {
			item := &menu.Items[0].SubItems[i]
			if item.Command == command {
				return item
			}
		}
		return nil
	}

	if item := find(appcmd.CmWorkspaceNew); item == nil || item.Shortcut != "Ctrl+N" {
		if item == nil {
			t.Fatal("Left menu has no New workspace item after GetMenuBar refresh")
		}
		t.Fatalf("New workspace shortcut after GetMenuBar refresh = %q, want Ctrl+N", item.Shortcut)
	}
	if item := find(appcmd.CmWorkspaceClose); item == nil || item.Shortcut != "Ctrl+W" {
		if item == nil {
			t.Fatal("Left menu has no Close workspace item after GetMenuBar refresh")
		}
		t.Fatalf("Close workspace shortcut after GetMenuBar refresh = %q, want Ctrl+W", item.Shortcut)
	}

	keymap.GlobalHotkeysMgr.Bind("Shell", "CtrlN", "None")
	if item := find(appcmd.CmWorkspaceNew); item == nil || item.Shortcut != "" {
		if item == nil {
			t.Fatal("Left menu lost New workspace item after explicit unbind")
		}
		t.Fatalf("explicitly unbound New workspace shortcut = %q, want empty", item.Shortcut)
	}
}
