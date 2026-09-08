package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestCommandPaletteActionIsRegistered(t *testing.T) {
	action, ok := GetAction(commandPaletteActionName)
	if !ok {
		t.Fatalf("%s is not registered", commandPaletteActionName)
	}
	if action.Area != "Common" || !reflect.DeepEqual(action.DefaultKeys, []string{"CtrlShiftP"}) ||
		!reflect.DeepEqual(action.NativeKeys, []string{"CtrlAltP"}) {
		t.Fatalf("palette action = %#v, want Common with CtrlShiftP", action)
	}
}

func TestCommandPaletteLegacyShortcutHonorsExplicitHotkeyOverrides(t *testing.T) {
	previous := GlobalHotkeysMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Bindings: map[string]map[string]string{},
		Defaults: map[string]map[string]string{},
	}
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	e := ParseFarKey(commandPaletteLegacyKey)
	if !commandPaletteLegacyShortcut("Shell", e) {
		t.Fatal("unclaimed Ctrl+Alt+P did not remain available as the legacy fallback")
	}

	GlobalHotkeysMgr.Bindings["Common"] = map[string]string{commandPaletteLegacyKey: "None"}
	if commandPaletteLegacyShortcut("Shell", e) {
		t.Fatal("explicitly silenced Ctrl+Alt+P was reclaimed by the legacy fallback")
	}

	GlobalHotkeysMgr.Bindings["Common"][commandPaletteLegacyKey] = "Panel.TogglePassivePanel"
	if commandPaletteLegacyShortcut("Shell", e) {
		t.Fatal("explicit Ctrl+Alt+P binding was reclaimed by the legacy fallback")
	}
}

func TestCommandPaletteActionApplicability(t *testing.T) {
	if !commandPaletteActionApplies(Action{Area: "Common"}, "Viewer") {
		t.Fatal("Common action is not applicable in Viewer")
	}
	if !commandPaletteActionApplies(Action{Area: "Editor"}, "Editor") {
		t.Fatal("native Editor action is not applicable in Editor")
	}
	if commandPaletteActionApplies(Action{Area: "Editor"}, "Shell") {
		t.Fatal("Editor-only action leaked into Shell")
	}
	if !commandPaletteActionApplies(Action{Area: "Shell"}, "Editor") ||
		!commandPaletteActionApplies(Action{Area: "Shell"}, "Viewer") {
		t.Fatal("Shell settings and panel commands are not globally discoverable")
	}

	const conditionName = "commandPaletteTestUnavailable"
	conditionKey := strings.ToLower(conditionName)
	previous, existed := conditionRegistry[conditionKey]
	conditionRegistry[conditionKey] = func() bool { return false }
	t.Cleanup(func() {
		if existed {
			conditionRegistry[conditionKey] = previous
		} else {
			delete(conditionRegistry, conditionKey)
		}
	})

	conditional := Action{
		Area:         "Editor",
		DefaultAreas: []string{"Terminal"},
		DefaultKeys:  []string{"F2:" + conditionName},
	}
	if commandPaletteActionApplies(conditional, "Terminal") {
		t.Fatal("action with a failed extra-area condition is applicable")
	}
	conditional.DefaultKeys = append(conditional.DefaultKeys, "CtrlF2")
	if !commandPaletteActionApplies(conditional, "Terminal") {
		t.Fatal("unconditional extra-area binding did not make action applicable")
	}
}

func TestCommandPaletteHidesUserMenuFromBusyTerminal(t *testing.T) {
	const condition = "terminalquiet"
	previous := conditionRegistry[condition]
	conditionRegistry[condition] = func() bool { return false }
	t.Cleanup(func() { conditionRegistry[condition] = previous })

	if commandPaletteCanIncludeUserMenu("Terminal") {
		t.Fatal("busy terminal exposes user-menu commands that would send Enter to its PTY")
	}
	if !commandPaletteCanIncludeUserMenu("Editor") {
		t.Fatal("editor unexpectedly hides underlying user-menu commands")
	}
}

func TestCommandPaletteActionShortcutsAreDeterministic(t *testing.T) {
	const falseCondition = "commandpaletteshortcutfalse"
	conditionRegistry[falseCondition] = func() bool { return false }
	t.Cleanup(func() { delete(conditionRegistry, falseCondition) })
	previous := GlobalHotkeysMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Defaults: map[string]map[string]string{},
		Bindings: map[string]map[string]string{
			"Shell": {
				"CtrlB": "Test.PaletteAction:Condition",
				"CtrlA": "Test.PaletteAction",
				"CtrlC": "Test.PaletteAction:" + falseCondition,
			},
			"Common": {"AltZ": "Test.PaletteAction"},
		},
	}
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	want := []string{"Alt+Z", "Ctrl+A", "Ctrl+B"}
	if got := commandPaletteActionShortcuts("Shell", "test.paletteaction"); !reflect.DeepEqual(got, want) {
		t.Fatalf("shortcuts = %v, want %v", got, want)
	}
}

func TestCommandPaletteActionShortcutsIgnoreUnrelatedConditions(t *testing.T) {
	const unrelatedCondition = "commandpaletteunrelatedpanic"
	conditionRegistry[unrelatedCondition] = func() bool {
		panic("an unrelated shortcut condition was evaluated")
	}
	t.Cleanup(func() { delete(conditionRegistry, unrelatedCondition) })

	previous := GlobalHotkeysMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Defaults: map[string]map[string]string{},
		Bindings: map[string]map[string]string{
			"Shell": {
				"CtrlP": "Test.PaletteAction",
				"Esc":   "Other.Action:" + unrelatedCondition,
			},
		},
	}
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	want := []string{"Ctrl+P"}
	if got := commandPaletteActionShortcuts("Shell", "test.paletteaction"); !reflect.DeepEqual(got, want) {
		t.Fatalf("shortcuts = %v, want %v", got, want)
	}
}

func TestCommandPaletteIncludesBothPluginLocationsAndReResolves(t *testing.T) {
	api := &coreAPI{}
	runs := 0
	registrations := make([]vfs.Registration, 0, 2)
	for _, command := range []vfs.PluginCommand{
		{ID: "test.command-palette.panel", Location: vfs.PluginCommandPanel, Label: "Panel contribution", Run: func(vfs.App) { runs++ }},
		{ID: "test.command-palette.config", Location: vfs.PluginCommandConfig, Label: "Configuration contribution", Run: func(vfs.App) { runs++ }},
	} {
		registration, err := api.RegisterPluginCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		registrations = append(registrations, registration)
	}
	t.Cleanup(func() {
		for _, registration := range registrations {
			registration.Unregister()
		}
	})

	pf := &PanelsFrame{}
	entries := commandPalettePluginEntries(pf)
	byID := make(map[string]commandPaletteEntry)
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	panel, panelOK := byID["test.command-palette.panel"]
	config, configOK := byID["test.command-palette.config"]
	if !panelOK || !configOK {
		t.Fatalf("plugin entries missing: panel=%v config=%v", panelOK, configOK)
	}
	if panel.Category == config.Category {
		t.Fatalf("panel and configuration commands have the same category %q", panel.Category)
	}
	func() {
		// Install the deliberately minimal PanelsFrame in an isolated manager so
		// workspace validation stays meaningful without borrowing another test's
		// active screen.
		restoreFrameManager := swapFrameManager(t)
		defer restoreFrameManager()
		defer setFrameManagerScreensForTest(t, []*vtui.AppScreen{{Number: 1, Frames: []vtui.Frame{pf}}}, 0)()

		if !executeCommandPaletteEntry(panel) || runs != 1 {
			t.Fatalf("live plugin command ran %d times", runs)
		}
		registrations[0].Unregister()
		if executeCommandPaletteEntry(panel) || runs != 1 {
			t.Fatal("unregistered plugin command was invoked from a stale palette entry")
		}
	}()
}

func TestCommandPalettePluginMetadataUsesCurrentLanguageAndAllLanguageAliases(t *testing.T) {
	oldLanguage := AppConfig.Language
	oldFallbackLanguage := AppConfig.FallbackLanguage
	t.Cleanup(func() {
		AppConfig.Language = oldLanguage
		AppConfig.FallbackLanguage = oldFallbackLanguage
		InitLang()
	})
	AppConfig.Language = "en"
	AppConfig.FallbackLanguage = ""
	InitLang()

	api := &coreAPI{}
	registration, err := api.RegisterPluginCommand(vfs.PluginCommand{
		ID:                    "test.command-palette.localized-plugin",
		Location:              vfs.PluginCommandPanel,
		Label:                 "English fallback label",
		LabelKey:              "Archive.Command.Extract",
		LocalizedLabels:       map[string]string{"es": "Extraer archivos del complemento"},
		Description:           "English fallback description",
		DescriptionKey:        "Archive.Command.Extract.Desc",
		LocalizedDescriptions: map[string]string{"es": "Extraer el archivo seleccionado"},
		SearchKeys:            []string{"NetFox.ConnectionTitle"},
		SearchTerms:           []string{"object metadata alias"},
		Shortcut:              "Ctrl+Alt+X",
		Run:                   func(vfs.App) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	findEntry := func() commandPaletteEntry {
		for _, entry := range commandPalettePluginEntries(&PanelsFrame{}) {
			if entry.ID == "test.command-palette.localized-plugin" {
				return entry
			}
		}
		t.Fatal("localized plugin command is missing from the palette")
		return commandPaletteEntry{}
	}

	entry := findEntry()
	if entry.Label != "Extract files" || entry.Description != "Extract the selected archive to the passive panel" {
		t.Fatalf("English display metadata = %#v", entry)
	}
	if entry.EnglishLabel != "English fallback label" || entry.EnglishDescription != "English fallback description" || entry.Shortcut != "Ctrl+Alt+X" {
		t.Fatalf("fallback/shortcut metadata = %#v", entry)
	}
	for _, query := range []string{
		"Извлечь файлы",
		"Извлечь выбранный архив в пассивную панель",
		"Настройка подключения",
		"Extraer archivos del complemento",
		"Extraer el archivo seleccionado",
		"object metadata alias",
	} {
		results := rankCommandPaletteEntries([]commandPaletteEntry{entry}, query, nil)
		if len(results) != 1 || results[0].Label != "Extract files" {
			t.Errorf("query %q returned %#v", query, results)
		}
	}

	AppConfig.Language = "ru"
	InitLang()
	entry = findEntry()
	if entry.Label != "Извлечь файлы" || entry.Description != "Извлечь выбранный архив в пассивную панель" {
		t.Fatalf("Russian display metadata = %#v", entry)
	}
}

func TestCommandPaletteFlattensExecutableUserMenuLeaves(t *testing.T) {
	source := commandPaletteUserMenuSource{
		mode:  MenuModeLocal,
		title: "Local menu",
		path:  `C:\work\FarMenu.ini`,
		items: []UserMenuItem{
			{HotKey: "--"},
			{Label: "&Tools", Submenu: []UserMenuItem{
				{HotKey: "R", Label: "&Run checks", Commands: []string{"REM ignored", "go test ./..."}},
				{HotKey: "C", Label: "Comments", Commands: []string{":: ignored"}},
			}},
		},
	}
	entries := flattenCommandPaletteUserMenu(source, &PanelsFrame{})
	if len(entries) != 1 {
		t.Fatalf("flattened entries = %#v, want one executable leaf", entries)
	}
	entry := entries[0]
	if entry.Label != "Run checks" || entry.Description != "Tools > Run checks" || entry.Shortcut != "R" {
		t.Fatalf("flattened entry = %#v", entry)
	}
	joined := strings.Join(entry.SearchFields, " ")
	if !strings.Contains(joined, "go test ./...") || !strings.Contains(strings.ToLower(joined), "farmenu.ini") {
		t.Fatalf("user-menu search metadata = %q", joined)
	}
}

type commandPaletteHistoryStore map[string][]string

func (store commandPaletteHistoryStore) LoadHistory(id string) []string {
	return append([]string(nil), store[id]...)
}

func (store commandPaletteHistoryStore) SaveHistory(id string, values []string) {
	store[id] = append([]string(nil), values...)
}

func TestCommandPaletteMRUDeduplicatesAndCapsHistory(t *testing.T) {
	store := commandPaletteHistoryStore{}
	for index := 0; index < 60; index++ {
		store[commandPaletteHistoryID] = append(store[commandPaletteHistoryID], fmt.Sprintf("action:%02d", index))
	}
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = store
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	rememberCommandPaletteEntry("action:12")
	history := store[commandPaletteHistoryID]
	if len(history) != commandPaletteHistoryMax || history[0] != "action:12" {
		t.Fatalf("MRU = %v entries starting with %q", len(history), history[0])
	}
	count := 0
	for _, key := range history {
		if key == "action:12" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("MRU contains selected key %d times", count)
	}
}

type commandPalettePrimaryFrame struct {
	vtui.BaseFrame
	pluginIntercepted bool
}

func (*commandPalettePrimaryFrame) GetType() vtui.FrameType { return vtui.TypeUser + 1 }
func (frame *commandPalettePrimaryFrame) InterceptPluginKey(*vtinput.InputEvent) bool {
	frame.pluginIntercepted = true
	return true
}

func TestCommandPaletteHotkeyPrecedesPluginsAndDoesNotStack(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(100, 30)
	vtui.FrameManager.Init(screen)

	host := &commandPalettePrimaryFrame{}
	vtui.FrameManager.Push(host)
	previousHotkeys, previousMacro := GlobalHotkeysMgr, MacroMgr
	GlobalHotkeysMgr = &HotkeyManager{
		Defaults: map[string]map[string]string{"Common": {"CtrlShiftP": commandPaletteActionName}},
		Bindings: map[string]map[string]string{"Common": {"CtrlShiftP": commandPaletteActionName}},
	}
	manager := &MacroManager{Macros: make(map[string]map[string][]*vtinput.InputEvent)}
	MacroMgr = manager
	t.Cleanup(func() {
		GlobalHotkeysMgr = previousHotkeys
		MacroMgr = previousMacro
	})

	event := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_P, Char: 'P',
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	}
	if !manager.Filter(event) {
		t.Fatal("Ctrl+Shift+P was not consumed")
	}
	if host.pluginIntercepted {
		t.Fatal("plugin intercepted the palette hotkey")
	}
	if _, ok := vtui.FrameManager.GetTopFrame().(*commandPaletteDialog); !ok {
		t.Fatalf("top frame = %T, want command palette", vtui.FrameManager.GetTopFrame())
	}
	frameCount := len(vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Frames)
	if !manager.Filter(event) {
		t.Fatal("second Ctrl+Shift+P was not consumed")
	}
	if got := len(vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Frames); got != frameCount {
		t.Fatalf("second hotkey stacked another palette: frames %d -> %d", frameCount, got)
	}
}

func TestFastFindDoesNotVetoModifiedPrintablePaletteKey(t *testing.T) {
	t.Cleanup(swapFrameManager(t))

	panel := &FileSystemPanel{fastFindMode: true}
	frame := &PanelsFrame{showPanels: true, panels: [2]Panel{panel, nil}}
	modified := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_P, Char: 'P',
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	}
	if frame.VetoActionKey(modified) {
		t.Fatal("Fast Find vetoed Ctrl+Shift+P carrying Char='P'")
	}
	plain := *modified
	plain.ControlKeyState = 0
	plain.Char = 'p'
	if !frame.VetoActionKey(&plain) {
		t.Fatal("Fast Find stopped owning ordinary printable input")
	}
}
