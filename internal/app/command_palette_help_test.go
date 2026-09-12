package app

import (
	"github.com/unxed/f4/internal/keymap"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func helpPaletteEntriesByID(entries []commandPaletteEntry) map[string]commandPaletteEntry {
	result := make(map[string]commandPaletteEntry, len(entries))
	for _, entry := range entries {
		result[entry.ID] = entry
	}
	return result
}

func TestCommandPaletteHelpProviderFiltersFrameworkFallbacks(t *testing.T) {
	initFrameworkActionTestScreen(t)
	previousHelp := vtui.GlobalHelpEngine
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = nil
	t.Cleanup(func() {
		vtui.GlobalHelpEngine = previousHelp
		keymap.GlobalHotkeysMgr = previousHotkeys
		dialog.ResetHelpState()
	})

	Engine := vtui.NewHelpEngine(nil)
	Engine.AddTopic(&vtui.HelpTopic{Name: "Contents", Lines: []string{"Contents"}})
	vtui.GlobalHelpEngine = Engine
	menu := vtui.NewMenuBar([]string{"&File"})
	menu.Items[0].SubItems = []vtui.MenuItem{{Text: "&Open"}}
	host := &frameworkActionTestFrame{title: "Files", menu: menu}
	vtui.FrameManager.Push(host)
	help := vtui.NewHelpView(Engine, "Contents")
	vtui.FrameManager.Push(help)

	entries := commandPaletteFrameEntries()
	byID := helpPaletteEntriesByID(entries)
	want := map[string]bool{"Help.Close": true, "Help.Zoom": true, "Help.Contents": true}
	for id := range byID {
		if !want[id] {
			t.Errorf("unexpected root Help command %s", id)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("missing root Help commands: %v", want)
	}
	if byID["Help.Close"].Shortcut != "Esc, F10" || byID["Help.Zoom"].Shortcut != "F5" {
		t.Fatalf("Help shortcuts close=%q zoom=%q", byID["Help.Close"].Shortcut, byID["Help.Zoom"].Shortcut)
	}
	for _, entry := range entries {
		if entry.Description != entry.Label {
			t.Errorf("Help %s description %q is not localized label %q", entry.ID, entry.Description, entry.Label)
		}
	}

	for _, entry := range commandPaletteActionEntries("Other") {
		if entry.ID == "App.Help" || entry.ID == "App.MainMenu" {
			t.Errorf("modal Help exposed unusable framework action %s", entry.ID)
		}
	}
	for _, actionName := range []string{"App.Help", "App.MainMenu", "Workspace.New", "Workspace.Close", "Workspace.List"} {
		action, _ := GetAction(actionName)
		if got := keymap.NativeShortcutsForAction("Other", action); len(got) != 0 {
			t.Errorf("modal Help advertised native %s shortcut: %v", actionName, got)
		}
	}
	if actionContextHelp() {
		t.Fatal("App.Help opened nested Help over an existing HelpView")
	}
	if vtui.FrameManager.GetTopFrame() != help {
		t.Fatalf("nested help guard changed top frame to %T", vtui.FrameManager.GetTopFrame())
	}
}

func TestCommandPaletteModalWhitelistConsumesUnknownAndAllowsSupportedFrames(t *testing.T) {
	initFrameworkActionTestScreen(t)
	unknown := &frameworkActionTestFrame{title: "Unknown modal"}
	unknown.Modal = true
	vtui.FrameManager.Push(unknown)
	if commandPaletteModalFrameSupported(unknown) {
		t.Fatal("unknown modal frame was accepted by the command-palette whitelist")
	}
	if !ShowCommandPalette() {
		t.Fatal("unknown modal did not consume the command-palette request")
	}
	if vtui.FrameManager.GetTopFrame() != unknown {
		t.Fatalf("command palette stacked over unknown modal as %T", vtui.FrameManager.GetTopFrame())
	}
	vtui.FrameManager.Pop()
	vtui.FrameManager.SyncCurrentScreen()

	previousHelp := vtui.GlobalHelpEngine
	Engine := vtui.NewHelpEngine(nil)
	Engine.AddTopic(&vtui.HelpTopic{Name: "Contents", Lines: []string{"Contents"}})
	vtui.GlobalHelpEngine = Engine
	t.Cleanup(func() { vtui.GlobalHelpEngine = previousHelp })
	help := vtui.NewHelpView(Engine, "Contents")
	if !commandPaletteModalFrameSupported(help) ||
		!commandPaletteModalFrameSupported(&GrabberFrame{}) ||
		!commandPaletteModalFrameSupported(&ArkanoidFrame{}) {
		t.Fatal("Help/Grabber/Arkanoid modal whitelist is incomplete")
	}
	vtui.FrameManager.Push(help)
	if !ShowCommandPalette() {
		t.Fatal("supported Help modal rejected the command palette")
	}
	if _, ok := vtui.FrameManager.GetTopFrame().(*commandPaletteDialog); !ok {
		t.Fatalf("supported Help modal top = %T, want command palette", vtui.FrameManager.GetTopFrame())
	}
}

func TestCommandPaletteHelpProviderExecutesLiveStateExactly(t *testing.T) {
	initFrameworkActionTestScreen(t)
	previousHelp := vtui.GlobalHelpEngine
	t.Cleanup(func() {
		vtui.GlobalHelpEngine = previousHelp
		dialog.ResetHelpState()
	})

	Engine := vtui.NewHelpEngine(nil)
	Engine.AddTopic(&vtui.HelpTopic{Name: "Root", Lines: []string{"Root"}})
	Engine.AddTopic(&vtui.HelpTopic{Name: "Second", Lines: []string{"needle one", "needle two"}})
	Engine.AddTopic(&vtui.HelpTopic{Name: "Contents", Lines: []string{"Contents"}})
	vtui.GlobalHelpEngine = Engine
	help := vtui.NewHelpView(Engine, "Root")
	vtui.FrameManager.Push(help)
	help.SwitchTopic("Second")
	for _, char := range "needle" {
		if !dialog.HandleHelpSearchHotkey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char}) {
			t.Fatalf("Help search did not consume %q", char)
		}
	}

	entries := commandPaletteHelpEntries(help)
	byID := helpPaletteEntriesByID(entries)
	wantIDs := []string{
		"Help.Close", "Help.Zoom", "Help.Contents", "Help.Back",
		"Help.FindNext", "Help.FindPrevious", "Help.ClearSearch",
	}
	if len(byID) != len(wantIDs) {
		t.Fatalf("active-query Help commands = %#v", byID)
	}
	for _, id := range wantIDs {
		if _, ok := byID[id]; !ok {
			t.Errorf("active-query Help command %s is missing", id)
		}
	}
	if byID["Help.Close"].Shortcut != "F10" || byID["Help.ClearSearch"].Shortcut != "Esc" {
		t.Fatalf("query-owned Escape shortcuts close=%q clear=%q", byID["Help.Close"].Shortcut, byID["Help.ClearSearch"].Shortcut)
	}
	if results := rankCommandPaletteEntries(entries, "следующий результат поиска в справке", nil); len(results) == 0 || results[0].ID != "Help.FindNext" {
		t.Fatalf("Russian Help query = %#v", results)
	}

	if !executeCommandPaletteEntry(byID["Help.FindNext"]) || dialog.CurrentHelpSearch.Selected != 1 {
		t.Fatalf("Help.FindNext selected %d, want 1", dialog.CurrentHelpSearch.Selected)
	}
	if !executeCommandPaletteEntry(byID["Help.FindPrevious"]) || dialog.CurrentHelpSearch.Selected != 0 {
		t.Fatalf("Help.FindPrevious selected %d, want 0", dialog.CurrentHelpSearch.Selected)
	}
	dialog.CurrentHelpSearch.Query = []rune("absent")
	dialog.CurrentHelpSearch.Matches = nil
	if executeCommandPaletteEntry(byID["Help.FindNext"]) {
		t.Fatal("Help.FindNext reported success with no live match")
	}
	dialog.CurrentHelpSearch.Query = []rune("needle")
	dialog.UpdateHelpSearch(help)

	before := [4]int{}
	before[0], before[1], before[2], before[3] = help.GetPosition()
	if !executeCommandPaletteEntry(byID["Help.Zoom"]) {
		t.Fatal("Help.Zoom failed")
	}
	// ToggleHelpZoom leaves one extra row below the maximized window so the
	// Help controls stay inside the visible frame, hence height-4 and not
	// height-3. internal/dialog/help_search_test.go checks the same bound.
	if got := [4]int{help.X1, help.Y1, help.X2, help.Y2}; got != [4]int{0, 0, vtui.FrameManager.GetScreenSize() - 1, vtui.FrameManager.GetScreenHeight() - 4} {
		t.Fatalf("zoomed Help bounds = %v", got)
	}
	if !executeCommandPaletteEntry(byID["Help.Zoom"]) {
		t.Fatal("Help.Zoom restore failed")
	}
	if got := [4]int{help.X1, help.Y1, help.X2, help.Y2}; !reflect.DeepEqual(got, before) {
		t.Fatalf("restored Help bounds = %v, want %v", got, before)
	}

	if !executeCommandPaletteEntry(byID["Help.ClearSearch"]) || dialog.CurrentHelpSearch != nil {
		t.Fatal("Help.ClearSearch did not clear the live query")
	}
	if executeCommandPaletteEntry(byID["Help.FindNext"]) {
		t.Fatal("captured Help.FindNext ran after its query disappeared")
	}
	if !executeCommandPaletteEntry(byID["Help.Back"]) {
		t.Fatal("Help.Back failed")
	}
	if historyLen, ok := dialog.NestedHelpLen(reflect.ValueOf(help), "history"); !ok || historyLen != 0 {
		t.Fatalf("Help.Back history = %d, readable=%v", historyLen, ok)
	}
	if !executeCommandPaletteEntry(byID["Help.Contents"]) || !strings.Contains(help.GetTitle(), "Contents") {
		t.Fatalf("Help.Contents title = %q", help.GetTitle())
	}
	if !executeCommandPaletteEntry(byID["Help.Close"]) || !help.IsDone() {
		t.Fatal("Help.Close did not close HelpView")
	}
}
