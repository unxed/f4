package main

import (
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestPluginCommandHotkeyUsesConfiguredShortcutAndRunsCommand(t *testing.T) {
	previousHotkeys := GlobalHotkeysMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Bindings: map[string]map[string]string{
			"Shell": {"CtrlF9": "Plugin.Command.test.hotkey"},
		},
		Defaults: map[string]map[string]string{},
	}
	t.Cleanup(func() { GlobalHotkeysMgr = previousHotkeys })

	called := 0
	registration, err := (&coreAPI{}).RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.hotkey",
		Location: vfs.PluginCommandPanel,
		Label:    "Plugin hotkey command",
		Shortcut: "F1",
		Run: func(vfs.App) {
			called++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	if got := pluginCommandShortcut(vfs.PluginCommand{ID: "test.hotkey", Shortcut: "F1"}); got != "Ctrl+F9" {
		t.Fatalf("configured plugin shortcut = %q, want Ctrl+F9", got)
	}
	action, ok := GetAction("Plugin.Command.test.hotkey")
	if !ok || action.Label != "Plugin hotkey command" || action.Area != "Shell" {
		t.Fatalf("plugin hotkey action = %#v, registered=%v", action, ok)
	}

	restoreManager := swapFrameManager(t)
	defer restoreManager()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := &PanelsFrame{}
	defer setFrameManagerScreensForTest(t, []*vtui.AppScreen{{Frames: []vtui.Frame{pf}}}, 0)()
	if !RunAction(action.Name) {
		t.Fatal("configured plugin action was not dispatched")
	}
	if called != 1 {
		t.Fatalf("plugin command calls = %d, want 1", called)
	}
}

func TestBuildHotkeyRowsIncludesUnassignedPluginCommand(t *testing.T) {
	previousHotkeys := GlobalHotkeysMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Bindings: map[string]map[string]string{},
		Defaults: map[string]map[string]string{},
	}
	t.Cleanup(func() { GlobalHotkeysMgr = previousHotkeys })

	registration, err := (&coreAPI{}).RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.hotkey-row",
		Location: vfs.PluginCommandPanel,
		Label:    "Plugin row command",
		Shortcut: "Shift+F6",
		Run:      func(vfs.App) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	for _, row := range buildHotkeyRows(GlobalHotkeysMgr) {
		if row.Action == "Plugin.Command.test.hotkey-row" {
			if row.Label != "Plugin row command" || row.Key != "Shift+F6" || !row.Editable {
				t.Fatalf("plugin hotkey row = %#v", row)
			}
			return
		}
	}
	t.Fatal("unassigned plugin command is missing from the hotkey dialog")
}
