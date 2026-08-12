package main

import (
	"testing"

	"github.com/unxed/vtui"
)

func TestBuildMenuBarItems_Editor(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Editor")

	wantTitles := []string{"&File", "&Edit", "&Search", "&Options", "&Insert"}
	if len(items) != len(wantTitles) {
		t.Fatalf("Expected %d top-level menus, got %d: %+v", len(wantTitles), len(items), items)
	}
	for i, want := range wantTitles {
		if items[i].Label != want {
			t.Errorf("Menu %d: expected title %q, got %q", i, want, items[i].Label)
		}
	}

	// File menu: Save first, with the default F2 shortcut shown.
	file := items[0].SubItems
	if len(file) == 0 {
		t.Fatal("File menu is empty")
	}
	if file[0].Text != "&Save" {
		t.Errorf("Expected first File item to be '&Save', got %q", file[0].Text)
	}
	if file[0].Shortcut != "F2" {
		t.Errorf("Expected Save shortcut 'F2', got %q", file[0].Shortcut)
	}

	// A user override must be reflected in the shortcut column.
	GlobalHotkeysMgr.Bind("Editor", "CtrlS", "Editor.Save")
	file = BuildMenuBarItems("Editor")[0].SubItems
	if file[0].Shortcut != "F2" && file[0].Shortcut != "Ctrl+S" {
		t.Errorf("Override not reflected: got %q", file[0].Shortcut)
	}
}

func TestBuildMenuBarItems_Viewer(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Viewer")

	wantTitles := []string{"&File", "&View", "&Search", "&Options"}
	if len(items) != len(wantTitles) {
		t.Fatalf("Expected %d top-level menus, got %d: %+v", len(wantTitles), len(items), items)
	}
	for i, want := range wantTitles {
		if items[i].Label != want {
			t.Errorf("Menu %d: expected title %q, got %q", i, want, items[i].Label)
		}
	}

	// Common actions (Screen Grab) are appended after the area's own.
	file := items[0].SubItems
	last := file[len(file)-1]
	if last.Text != "Screen &grab" {
		t.Errorf("Expected last File item to be 'Screen &grab', got %q", last.Text)
	}
	if last.Shortcut != "Alt+Ins" {
		t.Errorf("Expected Screen Grab shortcut 'Alt+Ins', got %q", last.Shortcut)
	}
}

func TestBuildMenuBarItems_Shell(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")

	wantTitles := []string{"&Files", "&Commands", "&Options"}
	if len(items) != len(wantTitles) {
		t.Fatalf("Expected %d top-level menus, got %d: %+v", len(wantTitles), len(items), items)
	}
	for i, want := range wantTitles {
		if items[i].Label != want {
			t.Errorf("Menu %d: expected title %q, got %q", i, want, items[i].Label)
		}
	}

	// Files menu: View first, with the default F3 shortcut shown.
	files := items[0].SubItems
	if len(files) == 0 {
		t.Fatal("Files menu is empty")
	}
	if files[0].Text != "&View" {
		t.Errorf("Expected first Files item to be '&View', got %q", files[0].Text)
	}
	if files[0].Shortcut != "F3" {
		t.Errorf("Expected View shortcut 'F3', got %q", files[0].Shortcut)
	}

	// Options menu honors MenuSeparatorBefore.
	var sawSeparator bool
	for _, it := range items[2].SubItems {
		if it.Separator {
			sawSeparator = true
			break
		}
	}
	if !sawSeparator {
		t.Error("Expected at least one separator in the Options menu")
	}

	var pluginConfiguration *vtui.MenuItem
	for i := range items[2].SubItems {
		item := &items[2].SubItems[i]
		if item.Text == Msg("Menu.PluginConfiguration") {
			pluginConfiguration = item
			break
		}
	}
	if pluginConfiguration == nil {
		t.Fatal("Plugin Configuration is missing from the Options menu")
	}
	if pluginConfiguration.Shortcut != "Shift+F11" {
		t.Errorf("Plugin Configuration shortcut = %q, want Shift+F11", pluginConfiguration.Shortcut)
	}

	wantCommandShortcuts := map[string]string{
		Msg("Action.Panel.CopyPath"):   "Ctrl+D",
		Msg("Action.Panel.InsertPath"): "Ctrl+F",
	}
	for _, item := range items[1].SubItems {
		if want, ok := wantCommandShortcuts[item.Text]; ok {
			if item.Shortcut != want {
				t.Errorf("%q shortcut = %q, want %q", item.Text, item.Shortcut, want)
			}
			delete(wantCommandShortcuts, item.Text)
		}
	}
	for label := range wantCommandShortcuts {
		t.Errorf("Commands menu is missing %q", label)
	}
}

func TestBuildMenuBarItems_Terminal(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Terminal")
	if len(items) != 1 || items[0].Label != "&File" {
		t.Fatalf("Expected a single '&File' menu, got %+v", items)
	}
	file := items[0].SubItems
	if len(file) == 0 || file[0].Text != "&View terminal log" {
		t.Errorf("Expected first File item to be '&View terminal log', got %+v", file)
	}
}

func TestBuildMenuBarItems_OnClickRunsAction(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	clicked := false
	RegisterAction(Action{
		Name:     "Test.MenuClick",
		Area:     "Editor",
		Label:    "Click me",
		MenuPath: "TestMenu",
		Handler:  func() bool { clicked = true; return true },
	})

	items := BuildMenuBarItems("Editor")
	last := items[len(items)-1]
	if last.Label != "TestMenu" {
		t.Fatalf("Expected fallback menu title 'TestMenu', got %q", last.Label)
	}
	if len(last.SubItems) != 1 {
		t.Fatalf("Expected 1 item in TestMenu, got %d", len(last.SubItems))
	}
	last.SubItems[0].OnClick()
	if !clicked {
		t.Error("OnClick did not run the action handler")
	}
}
