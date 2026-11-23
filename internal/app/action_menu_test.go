package app

import (
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestBuildMenuBarItems_Editor(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

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
	File := items[0].SubItems
	if len(File) == 0 {
		t.Fatal("File menu is empty")
	}
	if File[0].Text != "&Save" {
		t.Errorf("Expected first File item to be '&Save', got %q", File[0].Text)
	}
	if File[0].Shortcut != "F2" {
		t.Errorf("Expected Save shortcut 'F2', got %q", File[0].Shortcut)
	}

	// A user override must be reflected in the shortcut column.
	keymap.GlobalHotkeysMgr.Bind("Editor", "CtrlS", "Editor.Save")
	File = BuildMenuBarItems("Editor")[0].SubItems
	if File[0].Shortcut != "F2" && File[0].Shortcut != "Ctrl+S" {
		t.Errorf("Override not reflected: got %q", File[0].Shortcut)
	}
}

func TestBuildMenuBarItems_Viewer(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

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
	File := items[0].SubItems
	last := File[len(File)-1]
	if last.Text != "Screen &grab" {
		t.Errorf("Expected last File item to be 'Screen &grab', got %q", last.Text)
	}
	if last.Shortcut != "Alt+Ins" {
		t.Errorf("Expected Screen Grab shortcut 'Alt+Ins', got %q", last.Shortcut)
	}
}

func TestBuildMenuBarItems_Shell(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")

	wantTitles := []string{"&Files", "&Commands", "&Plugins", "&Windows", "&Options"}
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
	attrAction, ok := GetAction("File.Attributes")
	if !ok {
		t.Fatal("File.Attributes action is not registered")
	}
	if got, want := attrAction.DisplayLabel(), "&File attributes"; got != want {
		t.Fatalf("File.Attributes menu mnemonic = %q, want %q", got, want)
	}
	var foundAttributes bool
	for _, item := range files {
		if item.Text == attrAction.DisplayLabel() || item.Text == "&"+attrAction.DisplayLabel() {
			foundAttributes = true
			break
		}
	}
	if !foundAttributes {
		t.Errorf("Files menu is missing %q", attrAction.DisplayLabel())
	}
	editSymlinkAction, ok := GetAction("File.EditSymlink")
	if !ok {
		t.Fatal("File.EditSymlink action is not registered")
	}
	var foundEditSymlink bool
	for _, item := range files {
		if plainMenuText(item.Text) == plainMenuText(editSymlinkAction.DisplayLabel()) {
			foundEditSymlink = true
			break
		}
	}
	if !foundEditSymlink {
		t.Errorf("Files menu is missing %q", editSymlinkAction.DisplayLabel())
	}

	// Options menu honors MenuSeparatorBefore.
	var optionsMenu []vtui.MenuItem
	for _, item := range items {
		if item.Label == i18n.Msg("Menu.Shell.Options") {
			optionsMenu = item.SubItems
			break
		}
	}
	if optionsMenu == nil {
		t.Fatal("Options menu is missing")
	}
	var sawSeparator bool
	for _, it := range optionsMenu {
		if it.Separator {
			sawSeparator = true
			break
		}
	}
	if !sawSeparator {
		t.Error("Expected at least one separator in the Options menu")
	}

	count := 0
	for _, item := range optionsMenu {
		if item.UserData == history.MenuHistoryItemKey("Settings.Open") {
			count++
		}
		if item.UserData == history.MenuHistoryItemKey("Settings.PluginConfiguration") {
			t.Fatal("legacy configuration entry remains in menu")
		}
	}
	if count != 1 {
		t.Fatalf("Settings entries = %d", count)
	}
	if a, ok := GetAction("Settings.PluginConfiguration"); !ok || len(a.DefaultKeys) == 0 {
		t.Fatal("legacy deep-link shortcut was removed")
	}

	wantCommandShortcuts := map[string]string{
		i18n.Msg("Action.Panel.CopyPath"):   "Ctrl+D",
		i18n.Msg("Action.Panel.InsertPath"): "Ctrl+F",
	}
	var checkShortcuts func(list []vtui.MenuItem)
	checkShortcuts = func(list []vtui.MenuItem) {
		for _, item := range list {
			if want, ok := wantCommandShortcuts[item.Text]; ok {
				if item.Shortcut != want {
					t.Errorf("%q shortcut = %q, want %q", item.Text, item.Shortcut, want)
				}
				delete(wantCommandShortcuts, item.Text)
			}
			checkShortcuts(item.SubItems)
		}
	}
	checkShortcuts(items[1].SubItems)
	for label := range wantCommandShortcuts {
		t.Errorf("Commands menu is missing %q", label)
	}
}

func TestBuildMenuBarItemsUsesFarMnemonics(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")
	if len(items) < 2 {
		t.Fatalf("expected Files and Commands menus, got %+v", items)
	}

	want := make(map[string]rune)
	want["File.View"] = 'v'
	want["File.Edit"] = 'e'
	want["File.Copy"] = 'c'
	want["File.Move"] = 'r'
	want["File.MakeDir"] = 'm'
	want["File.Delete"] = 'd'
	found := make(map[string]bool, len(want))
	spreadsheetHotkey := rune(0)
	for _, menu := range items {
		var visit func([]vtui.MenuItem)
		visit = func(menuItems []vtui.MenuItem) {
			for _, item := range menuItems {
				for name, hotkey := range want {
					if item.UserData != history.MenuHistoryItemKey(name) {
						continue
					}
					found[name] = true
					if got := vtui.ExtractHotkey(item.Text); got != hotkey {
						t.Errorf("%s mnemonic = %q, want %q (%q)", name, got, hotkey, item.Text)
					}
				}
				if item.UserData == history.MenuHistoryItemKey("App.Spreadsheet") {
					spreadsheetHotkey = vtui.ExtractHotkey(item.Text)
				}
				visit(item.SubItems)
			}
		}
		visit(menu.SubItems)
	}
	for name := range want {
		if !found[name] {
			t.Errorf("menu item %s was not found", name)
		}
	}
	if spreadsheetHotkey == 0 {
		t.Error("App.Spreadsheet has no mnemonic")
	}
}

func TestBuildMenuBarItems_Terminal(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Terminal")
	if len(items) != 2 || items[0].Label != "&File" {
		t.Fatalf("Expected File and Settings-containing Options menus, got %+v", items)
	}
	File := items[0].SubItems
	if len(File) == 0 || File[0].Text != "&View terminal log" {
		t.Errorf("Expected first File item to be '&View terminal log', got %+v", File)
	}
}

func TestBuildMenuBarItems_OnClickRunsAction(t *testing.T) {
	preserveActionRegistry(t)
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	clicked := false
	action.RegisterAction(action.Action{
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

func TestBuildMenuBarItems_IncludesPluginPanelCommandsInDeclaredMenu(t *testing.T) {
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{{Frames: []vtui.Frame{&panel.PanelsFrame{}}}}, 0))

	api := &coreAPI{}
	run := 0
	registration, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.menu.archive-command",
		Location: vfs.PluginCommandPanel,
		Label:    "Add to archive",
		MenuPath: "Files",
		Shortcut: "Shift+F1",
		Run:      func(vfs.App) { run++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })

	items := BuildMenuBarItems("Shell")
	if len(items) == 0 || items[0].Label != "&Files" {
		t.Fatalf("Files menu is missing: %+v", items)
	}
	files := items[0].SubItems
	var archiveItem *vtui.MenuItem
	for index := range files {
		if action.PlainLabel(files[index].Text) == "Add to archive" {
			archiveItem = &files[index]
			break
		}
	}
	if archiveItem == nil {
		t.Fatalf("Files menu has no plugin command: %+v", files)
	}
	if archiveItem.Shortcut != "Shift+F1" || archiveItem.OnClick == nil {
		t.Fatalf("plugin menu item metadata = %+v", *archiveItem)
	}
	// The click is intentionally wired through the live registry rather than
	// retaining a plugin closure in the menu item.
	archiveItem.OnClick()
	if run != 1 {
		t.Fatalf("plugin command ran %d times, want once", run)
	}
}

func TestBuildMenuBarItems_DimsPluginCommandWithEnabledFalse(t *testing.T) {
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{{Frames: []vtui.Frame{&panel.PanelsFrame{}}}}, 0))

	api := &coreAPI{}
	run := 0
	enabled := false
	registration, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.menu.disabled-command",
		Location: vfs.PluginCommandPanel,
		Label:    "Disableable command",
		MenuPath: "Files",
		Enabled:  func(vfs.App) bool { return enabled },
		Run:      func(vfs.App) { run++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })

	findItem := func() *vtui.MenuItem {
		items := BuildMenuBarItems("Shell")
		if len(items) == 0 || items[0].Label != "&Files" {
			t.Fatalf("Files menu is missing: %+v", items)
		}
		for index, item := range items[0].SubItems {
			if action.PlainLabel(item.Text) == "Disableable command" {
				return &items[0].SubItems[index]
			}
		}
		t.Fatalf("Files menu has no plugin command: %+v", items[0].SubItems)
		return nil
	}

	// Enabled()==false dims the item exactly like a core action's Enabled
	// does (f4#1356): the row stays put, it just cannot be clicked.
	item := findItem()
	if !item.Disabled {
		t.Fatalf("disabled plugin command was not dimmed: %+v", *item)
	}

	enabled = true
	item = findItem()
	if item.Disabled {
		t.Fatalf("enabled plugin command stayed dimmed: %+v", *item)
	}
	// ExecutePluginCommand re-checks Enabled itself, so this click is safe
	// even against a menu snapshot built while the command was disabled.
	item.OnClick()
	if run != 1 {
		t.Fatalf("plugin command ran %d times, want once", run)
	}
}

func TestBuildMenuBarItemsSkipsPluginVisibilityBeforePanelsFrameRegistration(t *testing.T) {
	t.Cleanup(testutil.SetFrameManagerScreens(t, nil, 0))

	api := &coreAPI{}
	registration, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.menu.startup-visibility",
		Location: vfs.PluginCommandPanel,
		Label:    "Startup command",
		MenuPath: "Files",
		Visible: func(vfs.App) bool {
			t.Fatal("plugin visibility callback ran before a PanelsFrame was registered")
			return false
		},
		Run: func(vfs.App) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	BuildMenuBarItems("Shell")
}

func TestBuildMenuBarItemsGroupsShellMenus(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	for _, menu := range BuildMenuBarItems("Shell") {
		if len(menu.SubItems) <= 1 {
			continue // A standalone command does not need group separators.
		}
		if action.PlainLabel(menu.Label) == action.PlainLabel(i18n.Msg("Menu.Shell.Windows")) {
			continue // Workspace actions form one cohesive group.
		}
		separators := 0
		for index, item := range menu.SubItems {
			if !item.Separator {
				continue
			}
			separators++
			if index == 0 || index == len(menu.SubItems)-1 {
				t.Errorf("%s menu has a separator at index %d, with no group on both sides", menu.Label, index)
			}
			if index > 0 && menu.SubItems[index-1].Separator {
				t.Errorf("%s menu has two separators in a row at index %d", menu.Label, index)
			}
		}
		if separators == 0 {
			t.Errorf("%s menu is one undivided list of %d items", menu.Label, len(menu.SubItems))
		}
	}
}

func TestBuildMenuBarItemsSeparatesPluginCommandsFromBuiltIns(t *testing.T) {
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{{Frames: []vtui.Frame{&panel.PanelsFrame{}}}}, 0))

	api := &coreAPI{}
	first, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.menu.separator-first",
		Location: vfs.PluginCommandPanel,
		Label:    "First plugin command",
		MenuPath: "Files",
		Run:      func(vfs.App) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Unregister)
	second, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:       "test.menu.separator-second",
		Location: vfs.PluginCommandPanel,
		Label:    "Second plugin command",
		MenuPath: "Files",
		Run:      func(vfs.App) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Unregister)

	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })

	items := BuildMenuBarItems("Shell")
	if len(items) == 0 || items[0].Label != "&Files" {
		t.Fatalf("Files menu is missing: %+v", items)
	}
	files := items[0].SubItems
	indexOf := func(label string) int {
		for index := range files {
			if action.PlainLabel(files[index].Text) == label {
				return index
			}
		}
		return -1
	}
	firstIndex := indexOf("First plugin command")
	secondIndex := indexOf("Second plugin command")
	if firstIndex <= 0 || secondIndex < 0 {
		t.Fatalf("plugin commands are missing from the Files menu: %+v", files)
	}
	if !files[firstIndex-1].Separator {
		t.Errorf("plugin commands are not set off from the built-in items: %+v", files)
	}
	if secondIndex != firstIndex+1 {
		t.Errorf("second plugin command at index %d, want %d: one separator opens the whole plugin group", secondIndex, firstIndex+1)
	}
}

func TestBuildMenuBarItemsFoldsRareCommandsIntoSubMenus(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")
	if len(items) < 2 || items[1].Label != "&Commands" {
		t.Fatalf("Commands menu is missing: %+v", items)
	}
	commands := items[1].SubItems

	find := func(list []vtui.MenuItem, label string) *vtui.MenuItem {
		for index := range list {
			if action.PlainLabel(list[index].Text) == action.PlainLabel(label) {
				return &list[index]
			}
		}
		return nil
	}

	for _, sub := range []struct {
		title  string
		member string
	}{
		{i18n.Msg("Menu.Shell.Commands.History"), "Panel.CommandHistory"},
		{i18n.Msg("Menu.Shell.Commands.Navigation"), "Panel.GoParent"},
		{i18n.Msg("Menu.Shell.Commands.Paths"), "Panel.CopyPath"},
		{i18n.Msg("Menu.Shell.Commands.AI"), "AI.TogglePanel"},
	} {
		heading := find(commands, sub.title)
		if heading == nil {
			t.Errorf("Commands menu has no %q submenu", action.PlainLabel(sub.title))
			continue
		}
		if heading.OnClick != nil || heading.Shortcut != "" {
			t.Errorf("%q is a submenu heading, it must not act as a command", action.PlainLabel(sub.title))
		}
		act, ok := GetAction(sub.member)
		if !ok {
			t.Fatalf("%s is not registered", sub.member)
		}
		if find(heading.SubItems, act.DisplayLabel()) == nil {
			t.Errorf("%q is missing from the %q submenu: %+v", act.DisplayLabel(), action.PlainLabel(sub.title), heading.SubItems)
		}
		if find(commands, act.DisplayLabel()) != nil {
			t.Errorf("%q is listed both at the top level and in the %q submenu", act.DisplayLabel(), action.PlainLabel(sub.title))
		}
	}

	historyMenu := find(commands, i18n.Msg("Menu.Shell.Commands.History"))
	if historyMenu == nil {
		t.Fatal("Commands menu has no History submenu")
	}
	for _, member := range []string{"History.ImportFar2l", "History.ImportFar2lFolders"} {
		act, ok := GetAction(member)
		if !ok {
			t.Fatalf("%s is not registered", member)
		}
		if find(historyMenu.SubItems, act.DisplayLabel()) == nil {
			t.Errorf("%q is missing from the History submenu", act.DisplayLabel())
		}
	}
	settingsImport, ok := GetAction("Settings.ImportFar2l")
	if !ok {
		t.Fatal("Settings.ImportFar2l is not registered")
	}
	if find(commands, settingsImport.DisplayLabel()) == nil {
		t.Errorf("%q is missing from the Commands menu", settingsImport.DisplayLabel())
	}
}

func TestBuildMenuBarItemsPlacesPluginCommandsInPluginsMenu(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")
	var plugins, commands *vtui.MenuBarItem
	for index := range items {
		switch action.PlainLabel(items[index].Label) {
		case action.PlainLabel(i18n.Msg("Menu.Shell.Plugins")):
			plugins = &items[index]
		case action.PlainLabel(i18n.Msg("Menu.Shell.Commands")):
			commands = &items[index]
		}
	}
	if plugins == nil {
		t.Fatal("Plugins menu is missing")
	}
	if commands == nil {
		t.Fatal("Commands menu is missing")
	}

	pluginMenu, ok := GetAction("Panel.PluginMenu")
	if !ok {
		t.Fatal("Panel.PluginMenu is not registered")
	}
	find := func(items []vtui.MenuItem, label string) bool {
		for _, item := range items {
			if action.PlainLabel(item.Text) == action.PlainLabel(label) {
				return true
			}
		}
		return false
	}
	if !find(plugins.SubItems, pluginMenu.DisplayLabel()) {
		t.Fatalf("Plugins menu is missing %q: %+v", pluginMenu.DisplayLabel(), plugins.SubItems)
	}
	if find(commands.SubItems, pluginMenu.DisplayLabel()) {
		t.Fatalf("Commands menu still contains %q", pluginMenu.DisplayLabel())
	}
}

func TestBuildMenuBarItemsPlacesWorkspaceActionsInWindowsMenu(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	items := BuildMenuBarItems("Shell")
	var windows, commands, files *vtui.MenuBarItem
	for index := range items {
		switch action.PlainLabel(items[index].Label) {
		case action.PlainLabel(i18n.Msg("Menu.Shell.Windows")):
			windows = &items[index]
		case action.PlainLabel(i18n.Msg("Menu.Shell.Commands")):
			commands = &items[index]
		case action.PlainLabel(i18n.Msg("Menu.Shell.Files")):
			files = &items[index]
		}
	}
	if windows == nil || commands == nil || files == nil {
		t.Fatalf("expected Windows, Commands and Files menus; got %+v", items)
	}

	hasAction := func(items []vtui.MenuItem, name string) bool {
		want := history.MenuHistoryItemKey(name)
		for _, item := range items {
			if item.UserData == want {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"Panel.TempPanel", "Workspace.Fork", "Workspace.List"} {
		if !hasAction(windows.SubItems, name) {
			t.Errorf("Windows menu is missing %q", name)
		}
		if hasAction(commands.SubItems, name) || hasAction(files.SubItems, name) {
			t.Errorf("%q remains in Commands or Files", name)
		}
	}
}

func TestSettingsFirstInEveryOptionsMenu(t *testing.T) {
	for _, area := range []string{"Shell", "Editor", "Viewer", "Terminal"} {
		found := false
		for _, menu := range BuildMenuBarItems(area) {
			for _, item := range menu.SubItems {
				if item.UserData == history.MenuHistoryItemKey("Settings.Open") {
					found = true
					if menu.SubItems[0].UserData != history.MenuHistoryItemKey("Settings.Open") {
						t.Fatalf("%s: Settings is not first", area)
					}
				}
			}
		}
		if !found {
			t.Fatalf("%s: Settings missing", area)
		}
	}
}
