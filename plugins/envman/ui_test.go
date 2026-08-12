package envman

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestManagerOperationsPreserveLegacyOrderingSemantics(t *testing.T) {
	config := Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{
			{Kind: KindProfile, Name: "one", Enabled: true},
			{Kind: KindSeparator},
			{Kind: KindProfile, Name: "two"},
		},
	}
	if got := nextProfileIndex(config, 0); got != 2 {
		t.Fatalf("next profile = %d, want 2", got)
	}
	if got := nextProfileIndex(config, 2); got != 2 {
		t.Fatalf("last profile advanced to %d", got)
	}

	duplicated, duplicateIndex, err := duplicateEntry(config, 0)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateIndex != 1 || duplicated.Entries[1].Name != "one" {
		t.Fatalf("duplicate = %#v at %d", duplicated.Entries, duplicateIndex)
	}

	moved, selected, err := moveEntry(Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{
			{Kind: KindProfile, Name: "one"},
			{Kind: KindProfile, Name: "two"},
		},
	}, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if selected != 0 || len(moved.Entries) != 3 || moved.Entries[1].Kind != KindSeparator {
		t.Fatalf("edge move = %#v, selected %d", moved.Entries, selected)
	}
}

func TestImportDriftEntryEscapesLiteralExpansionMarkers(t *testing.T) {
	options := OptionsForGOOS("linux")
	baseline := []string{"PATH=/base", "SELF=old"}
	entry := importDriftEntry(Diff{
		Added:   []Change{{Name: "LITERAL", After: "%UNKNOWN%:$UNKNOWN:${UNKNOWN}"}},
		Changed: []Change{{Name: "SELF", Before: "old", After: "old:%OTHER%:$OTHER"}},
	}, "Imported", options)
	engine, err := NewEngine(baseline, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Evaluate(Config{Version: CurrentConfigVersion, Entries: []Entry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	assertEnvironmentValues(t, result.Environment, map[string]string{
		"PATH":    "/base",
		"LITERAL": "%UNKNOWN%:$UNKNOWN:${UNKNOWN}",
		"SELF":    "old:%OTHER%:$OTHER",
	})
}

func TestProfileHotkeyTogglesFirstMatchingProfileWithoutAdvancing(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{
		{Kind: KindProfile, Name: "first", Enabled: true},
		{Kind: KindProfile, Name: "&Go && tools"},
		{Kind: KindProfile, Name: "also &Go"},
	}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	plugin.store = store
	controller := &managerController{plugin: plugin, config: config}
	controller.list = vtui.NewListBox(0, 0, 30, 5, controller.rows())
	controller.list.SetSelectPos(0)
	if handled := controller.handleKey(&vtinput.InputEvent{KeyDown: true, Char: 'G'}); !handled {
		t.Fatal("profile hotkey was not handled")
	}
	updated := store.Snapshot()
	if !updated.Entries[1].Enabled || updated.Entries[2].Enabled {
		t.Fatalf("hotkey toggle affected wrong profiles: %#v", updated.Entries)
	}
	if controller.list.SelectPos != 1 {
		t.Fatalf("hotkey selection = %d, want 1", controller.list.SelectPos)
	}
	if label, hotkey := profileMenuLabel(config.Entries[1].Name); label != "Go & tools" || hotkey != 'g' {
		t.Fatalf("profile label/hotkey = %q/%q", label, hotkey)
	}
}

func newManagerControllerForTest(t *testing.T, config Config) (*managerController, *Plugin, *envManEditorTestApp, *Store) {
	t.Helper()
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	plugin.store = store
	plugin.envHost = newEnvManTestHost("A=one")
	app := &envManEditorTestApp{}
	if err := plugin.reloadConfig(); err != nil {
		t.Fatal(err)
	}
	dialog := plugin.openManagerDialog(app)
	controller := dialog.controller
	return controller, plugin, app, store
}

func managerTestKey(code uint16, char rune, state vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{KeyDown: true, VirtualKeyCode: code, Char: char, ControlKeyState: state}
}

func TestManagerKeyActionsPersistAndPreserveSelection(t *testing.T) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{
		{Kind: KindProfile, Name: "one"},
		{Kind: KindSeparator},
		{Kind: KindProfile, Name: "two"},
	}}
	controller, _, _, store := newManagerControllerForTest(t, config)
	controller.list.SetSelectPos(0)
	if !controller.handleKey(managerTestKey(vtinput.VK_SPACE, ' ', 0)) {
		t.Fatal("Space toggle was not handled")
	}
	if !store.Snapshot().Entries[0].Enabled || controller.list.SelectPos != 2 {
		t.Fatalf("Space result = %#v, selection %d", store.Snapshot().Entries, controller.list.SelectPos)
	}
	if !controller.handleKey(managerTestKey(vtinput.VK_RETURN, '\r', 0)) {
		t.Fatal("Enter toggle was not handled")
	}
	if !store.Snapshot().Entries[2].Enabled || controller.list.SelectPos != 2 {
		t.Fatalf("Enter result = %#v, selection %d", store.Snapshot().Entries, controller.list.SelectPos)
	}
	if !controller.handleKey(managerTestKey(vtinput.VK_UP, 0, vtinput.LeftCtrlPressed)) {
		t.Fatal("Ctrl+Up move was not handled")
	}
	if store.Snapshot().Entries[1].Name != "two" || controller.list.SelectPos != 1 {
		t.Fatalf("move result = %#v, selection %d", store.Snapshot().Entries, controller.list.SelectPos)
	}

	if !controller.handleKey(managerTestKey(vtinput.VK_INSERT, 0, vtinput.LeftCtrlPressed)) {
		t.Fatal("Ctrl+Ins copy was not handled")
	}
	copied, err := DecodeEntry(vtui.GetClipboard(), controller.plugin.options)
	if err != nil || copied.Name != "two" {
		t.Fatalf("copied entry = %#v, %v", copied, err)
	}
	if !controller.handleKey(managerTestKey(vtinput.VK_DELETE, 0, vtinput.ShiftPressed)) {
		t.Fatal("Shift+Del cut was not handled")
	}
	if len(store.Snapshot().Entries) != 1 {
		t.Fatalf("cut entries = %#v", store.Snapshot().Entries)
	}
	if !controller.handleKey(managerTestKey(vtinput.VK_INSERT, 0, vtinput.ShiftPressed)) {
		t.Fatal("Shift+Ins paste was not handled")
	}
	if len(store.Snapshot().Entries) != 2 || store.Snapshot().Entries[controller.list.SelectPos].Name != "two" {
		t.Fatalf("paste entries = %#v, selection %d", store.Snapshot().Entries, controller.list.SelectPos)
	}
}

func TestManagerListArrowNavigationWrapsWithoutChangingFocus(t *testing.T) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{
		{Kind: KindProfile, Name: "one", Enabled: true},
		{Kind: KindProfile, Name: "two", Enabled: true},
		{Kind: KindProfile, Name: "three", Enabled: true},
	}}
	controller, _, _, _ := newManagerControllerForTest(t, config)
	controller.dialog.SetFocusedItem(controller.list)
	controller.list.SetSelectPos(0)

	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_UP, 0, 0)) {
		t.Fatal("Up at the first profile was not handled")
	}
	if controller.list.SelectPos != 2 || controller.dialog.GetFocusedItem() != controller.list {
		t.Fatalf("Up wrapped to %d with focus %T", controller.list.SelectPos, controller.dialog.GetFocusedItem())
	}
	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_DOWN, 0, 0)) {
		t.Fatal("Down at the last profile was not handled")
	}
	if controller.list.SelectPos != 0 || controller.dialog.GetFocusedItem() != controller.list {
		t.Fatalf("Down wrapped to %d with focus %T", controller.list.SelectPos, controller.dialog.GetFocusedItem())
	}

	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_TAB, '\t', 0)) {
		t.Fatal("Tab did not move to Add profile")
	}
	if controller.editing || controller.dialog.GetFocusedItem() != controller.addButton {
		t.Fatalf("left-pane Tab state/focus = %v/%T, want Add profile", controller.editing, controller.dialog.GetFocusedItem())
	}
	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_TAB, '\t', 0)) || controller.dialog.GetFocusedItem() != controller.list {
		t.Fatalf("second left-pane Tab focus = %T, want ListBox", controller.dialog.GetFocusedItem())
	}
	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_TAB, '\t', vtinput.ShiftPressed)) || controller.dialog.GetFocusedItem() != controller.addButton {
		t.Fatalf("Shift+Tab focus = %T, want Add profile", controller.dialog.GetFocusedItem())
	}
	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_TAB, '\t', 0)) || controller.dialog.GetFocusedItem() != controller.list {
		t.Fatalf("left-pane wrap focus = %T, want ListBox", controller.dialog.GetFocusedItem())
	}

	if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_F4, 0, 0)) || !controller.editing || controller.dialog.GetFocusedItem() != controller.variablesEdit {
		t.Fatalf("F4 editing state/focus = %v/%T, want Variables", controller.editing, controller.dialog.GetFocusedItem())
	}
	for index, want := range []vtui.UIElement{
		controller.saveButton,
		controller.cancelButton,
		controller.nameEdit,
		controller.enabledEdit,
		controller.variablesEdit,
	} {
		if !controller.dialog.ProcessKey(managerTestKey(vtinput.VK_TAB, '\t', 0)) || controller.dialog.GetFocusedItem() != want {
			t.Fatalf("right-pane Tab step %d focus = %T, want %T", index, controller.dialog.GetFocusedItem(), want)
		}
	}
}

func TestManagerMouseWheelRefreshesInlineEditor(t *testing.T) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{
		{Kind: KindProfile, Name: "one", Enabled: true, Variables: []string{"VALUE=one"}},
		{Kind: KindProfile, Name: "two", Enabled: true, Variables: []string{"VALUE=two"}},
	}}
	controller, _, _, _ := newManagerControllerForTest(t, config)
	controller.list.SetSelectPos(0)
	controller.selectionChanged()
	if got := controller.nameEdit.GetText(); got != "one" {
		t.Fatalf("initial inline profile = %q", got)
	}

	if !controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         int16(controller.list.X1 + 1),
		MouseY:         int16(controller.list.Y1 + 1),
		WheelDirection: -1,
	}) {
		t.Fatal("profile-list mouse wheel was not handled")
	}
	if controller.list.SelectPos != 1 {
		t.Fatalf("mouse wheel selection = %d, want 1", controller.list.SelectPos)
	}
	if got := controller.nameEdit.GetText(); got != "two" {
		t.Fatalf("inline profile after mouse wheel = %q, want two", got)
	}
	if got := controller.variablesEdit.GetText(); got != "VALUE=two" {
		t.Fatalf("inline variables after mouse wheel = %q", got)
	}
}

func TestManagerScrollbarMouseScrollsProfileList(t *testing.T) {
	entries := make([]Entry, 40)
	for index := range entries {
		entries[index] = Entry{Kind: KindProfile, Name: fmt.Sprintf("profile-%02d", index), Enabled: true}
	}
	controller, _, _, _ := newManagerControllerForTest(t, Config{Version: CurrentConfigVersion, Entries: entries})
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)
	controller.dialog.Show(screen)
	if controller.list.ScrollBar == nil || !controller.list.ScrollBar.IsVisible() {
		t.Fatal("profile-list scrollbar is not visible")
	}

	if !controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      int16(controller.list.ScrollBar.X1),
		MouseY:      int16(controller.list.ScrollBar.Y2),
	}) {
		t.Fatal("scrollbar down-arrow click was not handled")
	}
	controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:    vtinput.MouseEventType,
		MouseX:  int16(controller.list.ScrollBar.X1),
		MouseY:  int16(controller.list.ScrollBar.Y2),
		KeyDown: false,
	})
	if controller.list.TopPos == 0 {
		t.Fatalf("scrollbar down-arrow click did not scroll the profile list: select=%d value=%d max=%d list=%v scrollbar=%v",
			controller.list.SelectPos, controller.list.ScrollBar.Value, controller.list.ScrollBar.Max,
			[]int{controller.list.X1, controller.list.Y1, controller.list.X2, controller.list.Y2},
			[]int{controller.list.ScrollBar.X1, controller.list.ScrollBar.Y1, controller.list.ScrollBar.X2, controller.list.ScrollBar.Y2})
	}

	controller.list.SetSelectPos(0)
	controller.list.TopPos = 0
	controller.dialog.Show(screen)
	thumbY := controller.list.ScrollBar.Y1 + 1
	controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      int16(controller.list.ScrollBar.X1),
		MouseY:      int16(thumbY),
	})
	controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
		MouseX:          int16(controller.list.ScrollBar.X1),
		MouseY:          int16(thumbY + 3),
	})
	controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:   vtinput.MouseEventType,
		MouseX: int16(controller.list.ScrollBar.X1),
		MouseY: int16(thumbY + 3),
	})
	if controller.list.TopPos == 0 {
		t.Fatal("dragging the scrollbar thumb did not scroll the profile list")
	}
}

func TestManagerCheckboxClickTogglesWithoutEditing(t *testing.T) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{
		{Kind: KindProfile, Name: "one", Enabled: true},
		{Kind: KindProfile, Name: "two", Enabled: false},
	}}
	controller, _, _, store := newManagerControllerForTest(t, config)

	if !controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      int16(controller.list.X1 + 1),
		MouseY:      int16(controller.list.Y1),
	}) {
		t.Fatal("checkbox click was not handled")
	}
	if store.Snapshot().Entries[0].Enabled {
		t.Fatal("checkbox click did not disable the first profile")
	}
	if controller.editing {
		t.Fatal("checkbox click unexpectedly entered edit mode")
	}

	controller.dialog.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX:      int16(controller.list.X1 + 5),
		MouseY:      int16(controller.list.Y1 + 1),
	})
	if controller.list.SelectPos != 1 || store.Snapshot().Entries[1].Enabled {
		t.Fatal("clicking the profile label should select it without toggling")
	}
}

func TestManagerInlineEditorUsesCompactLayout(t *testing.T) {
	controller, _, _, _ := newManagerControllerForTest(t, Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{{Kind: KindProfile, Name: "one", Enabled: true}},
	})
	var nameLabel *vtui.Text
	for _, child := range controller.dialog.GetChildren() {
		text, ok := child.(*vtui.Text)
		if ok && text.GetText() == controller.plugin.text("EnvMan.ProfileName", "&Name:", "&Имя:") {
			nameLabel = text
			break
		}
	}
	if nameLabel == nil {
		t.Fatal("Name label was not found")
	}
	_, labelY, labelX2, _ := nameLabel.GetPosition()
	editX1, editY, editX2, _ := controller.nameEdit.GetPosition()
	enabledX1, enabledY, _, _ := controller.enabledEdit.GetPosition()
	_, dialogY1, _, dialogY2 := controller.dialog.GetPosition()
	if labelY != dialogY1+3 || controller.list.Y1 != dialogY1+3 {
		t.Fatalf("content starts at Name/List y=%d/%d, want one blank row below header at y=%d", labelY, controller.list.Y1, dialogY1+3)
	}
	if editX1 != labelX2+2 {
		t.Fatalf("Name edit starts at %d after label ending at %d; want one blank cell", editX1, labelX2)
	}
	if labelY != editY || enabledY != editY || enabledX1 != editX2+2 {
		t.Fatalf("compact header positions: labelY=%d edit=%d..%d@y%d enabled=%d@y%d", labelY, editX1, editX2, editY, enabledX1, enabledY)
	}

	_, _, _, variablesY2 := controller.variablesEdit.GetPosition()
	if got := controller.list.Y2; got != variablesY2 {
		t.Fatalf("profile-list input background ends at y=%d, Variables ends at y=%d", got, variablesY2)
	}
	if controller.list.X1 != controller.dialog.X1+2 || controller.list.X2 != controller.dialog.X1+controller.dialog.splitOffset-2 {
		t.Fatalf("profile list bounds = %d..%d, want the full input surface", controller.list.X1, controller.list.X2)
	}
	_, saveY, _, _ := controller.saveButton.GetPosition()
	_, addY, _, _ := controller.addButton.GetPosition()
	if addY != saveY {
		t.Fatalf("Add profile y=%d, Save/Cancel row y=%d", addY, saveY)
	}
	if variablesY2 != saveY-2 {
		t.Fatalf("Variables ends at y=%d, Save is y=%d; want one blank row", variablesY2, saveY)
	}
	if saveY != dialogY2-2 {
		t.Fatalf("Save is y=%d, bottom border y=%d; want one blank row after buttons", saveY, dialogY2)
	}
}

func TestManagerEditorAndDialogKeyRouting(t *testing.T) {
	t.Run("F4 dialog and Alt-F4 editor", func(t *testing.T) {
		config := Config{Version: CurrentConfigVersion, Entries: []Entry{{Kind: KindProfile, Name: "one"}}}
		controller, _, app, _ := newManagerControllerForTest(t, config)
		controller.list.SetSelectPos(0)
		controller.handleKey(managerTestKey(vtinput.VK_F4, 0, 0))
		if !controller.editing || vtui.FrameManager.GetTopFrame() != controller.dialog || controller.dialog.GetFocusedItem() != controller.variablesEdit {
			t.Fatalf("F4 did not activate the inline editor: editing=%v top=%T focus=%T", controller.editing, vtui.FrameManager.GetTopFrame(), controller.dialog.GetFocusedItem())
		}
		controller.cancelInlineEdit()
		controller.handleKey(managerTestKey(vtinput.VK_F4, 0, vtinput.LeftAltPressed))
		if len(app.requests) != 1 {
			t.Fatalf("Alt-F4 editor requests = %d", len(app.requests))
		}
	})
	t.Run("AlwaysUseEditor and duplicate", func(t *testing.T) {
		config := Config{Version: CurrentConfigVersion, AlwaysUseEditor: true, Entries: []Entry{{Kind: KindProfile, Name: "one"}}}
		controller, _, app, store := newManagerControllerForTest(t, config)
		controller.list.SetSelectPos(0)
		controller.handleKey(managerTestKey(vtinput.VK_F4, 0, 0))
		controller.handleKey(managerTestKey(vtinput.VK_F5, 0, 0))
		if len(app.requests) != 2 || len(store.Snapshot().Entries) != 2 || controller.list.SelectPos != 1 {
			t.Fatalf("editor/duplicate result: requests %d entries %#v selection %d", len(app.requests), store.Snapshot().Entries, controller.list.SelectPos)
		}
	})
	t.Run("Insert delete settings and environment editors", func(t *testing.T) {
		config := Config{Version: CurrentConfigVersion, Entries: []Entry{{Kind: KindProfile, Name: "one"}}}
		controller, _, app, store := newManagerControllerForTest(t, config)
		controller.list.SetSelectPos(0)
		controller.handleKey(managerTestKey(vtinput.VK_INSERT, 0, 0))
		if vtui.FrameManager.GetTopFrame() == controller.dialog {
			t.Fatal("Insert did not open the profile dialog")
		}
		vtui.FrameManager.Pop()
		controller.addButton.OnClick()
		if vtui.FrameManager.GetTopFrame() == controller.dialog {
			t.Fatal("Add profile button did not open the profile dialog")
		}
		vtui.FrameManager.Pop()
		controller.handleKey(managerTestKey(vtinput.VK_DELETE, 0, 0))
		confirmation, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if !ok || vtui.Frame(confirmation) == vtui.Frame(controller.dialog) {
			t.Fatalf("delete confirmation = %T", vtui.FrameManager.GetTopFrame())
		}
		confirmation.SetExitCode(0)
		if len(store.Snapshot().Entries) != 0 {
			t.Fatalf("confirmed delete entries = %#v", store.Snapshot().Entries)
		}
		controller.handleKey(managerTestKey(vtinput.VK_F2, 0, 0))
		if vtui.FrameManager.GetTopFrame() == controller.dialog {
			t.Fatal("F2 did not open settings")
		}
		vtui.FrameManager.Pop()
		controller.handleKey(managerTestKey(vtinput.VK_F3, 0, vtinput.ShiftPressed))
		controller.handleKey(managerTestKey(vtinput.VK_F4, 0, vtinput.ShiftPressed))
		if len(app.requests) != 2 {
			t.Fatalf("environment view/edit requests = %d", len(app.requests))
		}
	})
}

func TestManagerInlineEditorSavesAndCancelsInPlace(t *testing.T) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{{
		Kind: KindProfile, Name: "one", Enabled: true, Variables: []string{"A=one"},
	}}}
	controller, _, _, store := newManagerControllerForTest(t, config)
	controller.list.SetSelectPos(0)
	if !controller.nameEdit.IsDisabled() || !controller.enabledEdit.IsDisabled() || !controller.variablesEdit.IsDisabled() ||
		controller.nameEdit.CanFocus() || controller.enabledEdit.CanFocus() || controller.variablesEdit.CanFocus() ||
		!controller.saveButton.IsDisabled() || !controller.cancelButton.IsDisabled() ||
		controller.saveButton.IsVisible() || controller.cancelButton.IsVisible() ||
		controller.addButton.IsDisabled() || !controller.addButton.IsVisible() {
		t.Fatal("browse mode exposed interactive preview controls")
	}
	controller.handleKey(managerTestKey(vtinput.VK_F4, 0, 0))
	if controller.dialog.bottomHint != controller.dialog.editHint {
		t.Fatal("inline editor did not switch the bottom-border key hint")
	}
	if controller.list.IsDisabled() == false || !controller.variablesEdit.CanFocus() ||
		!controller.saveButton.IsVisible() || !controller.cancelButton.IsVisible() ||
		!controller.addButton.IsDisabled() || controller.addButton.IsVisible() {
		t.Fatal("edit mode did not activate the right pane and its action buttons")
	}
	controller.nameEdit.SetText("renamed")
	controller.enabledEdit.State = 0
	controller.variablesEdit.SetText("A=two\nB=three")
	if !controller.saveInlineEdit() {
		t.Fatal("inline save failed")
	}
	saved := store.Snapshot().Entries[0]
	if saved.Name != "renamed" || saved.Enabled || len(saved.Variables) != 2 || saved.Variables[1] != "B=three" {
		t.Fatalf("saved inline profile = %#v", saved)
	}
	if controller.editing || controller.dialog.GetFocusedItem() != controller.list || !controller.nameEdit.IsDisabled() || controller.nameEdit.CanFocus() ||
		controller.list.IsDisabled() || controller.saveButton.IsVisible() || controller.cancelButton.IsVisible() ||
		controller.addButton.IsDisabled() || !controller.addButton.IsVisible() {
		t.Fatal("inline editor did not return focus to the profile list")
	}
	if controller.dialog.bottomHint != controller.dialog.listHint {
		t.Fatal("inline save did not restore the list key hint")
	}

	controller.beginInlineEdit(0, saved)
	controller.nameEdit.SetText("discarded")
	controller.cancelInlineEdit()
	if got := store.Snapshot().Entries[0].Name; got != "renamed" {
		t.Fatalf("cancel persisted name %q", got)
	}
	if got := controller.nameEdit.GetText(); got != "renamed" {
		t.Fatalf("cancel left stale editor text %q", got)
	}
}

func TestManagerVisualStatesAndOverflowFollowDialogTheme(t *testing.T) {
	entries := make([]Entry, 40)
	for index := range entries {
		entries[index] = Entry{Kind: KindProfile, Name: fmt.Sprintf("profile-%02d", index), Enabled: index != 0}
	}
	controller, _, _, _ := newManagerControllerForTest(t, Config{Version: CurrentConfigVersion, Entries: entries})
	dialog := controller.dialog
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)

	oldText := vtui.Palette[vtui.ColDialogText]
	oldEdit := vtui.Palette[vtui.ColDialogEdit]
	oldSelected := vtui.Palette[vtui.ColDialogSelectedButton]
	oldBox := vtui.Palette[vtui.ColDialogBox]
	oldTitle := vtui.Palette[vtui.ColDialogBoxTitle]
	oldHint := vtui.Palette[vtui.ColDialogHighlightBoxTitle]
	t.Cleanup(func() {
		vtui.Palette[vtui.ColDialogText] = oldText
		vtui.Palette[vtui.ColDialogEdit] = oldEdit
		vtui.Palette[vtui.ColDialogSelectedButton] = oldSelected
		vtui.Palette[vtui.ColDialogBox] = oldBox
		vtui.Palette[vtui.ColDialogBoxTitle] = oldTitle
		vtui.Palette[vtui.ColDialogHighlightBoxTitle] = oldHint
	})
	normal := vtui.SetRGBBoth(0, 0xE0D0C0, 0x102030)
	edit := vtui.SetRGBBoth(0, 0xD0C0B0, 0x080C10)
	selected := vtui.SetRGBBoth(0, 0xF0E0D0, 0x304050)
	box := vtui.SetRGBBoth(0, 0x405060, 0x102030)
	title := vtui.SetRGBBoth(0, 0x607080, 0x102030)
	hint := vtui.SetRGBBoth(0, 0xA0B0C0, 0x102030)
	vtui.Palette[vtui.ColDialogText] = normal
	vtui.Palette[vtui.ColDialogEdit] = edit
	vtui.Palette[vtui.ColDialogSelectedButton] = selected
	vtui.Palette[vtui.ColDialogBox] = box
	vtui.Palette[vtui.ColDialogBoxTitle] = title
	vtui.Palette[vtui.ColDialogHighlightBoxTitle] = hint
	controller.list.SetSelectPos(1)
	controller.selectionChanged()
	controller.list.SetFocus(true)
	dialog.Show(screen)
	x1, y1, x2, y2 := dialog.GetPosition()
	splitX := x1 + dialog.splitOffset
	leftHeaderX := x1 + 1 + ((splitX-1)-(x1+1)+1-(runewidth.StringWidth(dialog.profilesTitle)+2))/2
	rightHeaderX := splitX + 1 + ((x2-1)-(splitX+1)+1-(runewidth.StringWidth(dialog.detailsTitle)+2))/2
	if cell := screen.GetCell(leftHeaderX, y1+1); cell.Attributes != selected || rune(cell.Char) != ' ' {
		t.Fatalf("browse-mode Profiles left margin = %q/%#x, want active blank/%#x", rune(cell.Char), cell.Attributes, selected)
	}
	if cell := screen.GetCell(leftHeaderX+runewidth.StringWidth(dialog.profilesTitle)+1, y1+1); cell.Attributes != selected || rune(cell.Char) != ' ' {
		t.Fatalf("browse-mode Profiles right margin = %q/%#x", rune(cell.Char), cell.Attributes)
	}
	if got := screen.GetCell(rightHeaderX, y1+1).Attributes; got != title {
		t.Fatalf("browse-mode details margin = %#x, want inactive %#x", got, title)
	}
	if got := screen.GetCell(controller.list.X1, controller.list.Y1+1).Attributes; got != selected {
		t.Fatalf("focused current-profile color = %#x, want %#x", got, selected)
	}
	previewCell := screen.GetCell(controller.variablesEdit.X1, controller.variablesEdit.Y1)
	if got, want := vtui.GetRGBFore(previewCell.Attributes), vtui.GetRGBFore(edit); got != want {
		t.Fatalf("preview text foreground = %#x, want unchanged %#x", got, want)
	}
	if got, want := vtui.GetRGBBack(previewCell.Attributes), vtui.GetRGBBack(managerInputBackground(false)); got != want {
		t.Fatalf("preview background = %#x, want light inactive %#x", got, want)
	}
	if got := vtui.GetRGBBack(screen.GetCell(controller.variablesEdit.X1, controller.variablesEdit.Y1-1).Attributes); got != vtui.GetRGBBack(normal) {
		t.Fatalf("Variables label background = %#x, want unchanged dialog background %#x", got, vtui.GetRGBBack(normal))
	}
	if got := vtui.GetRGBBack(screen.GetCell(controller.list.X1, controller.list.Y1-1).Attributes); got != vtui.GetRGBBack(normal) {
		t.Fatalf("blank row below Profiles background = %#x, want dialog %#x", got, vtui.GetRGBBack(normal))
	}
	if got := vtui.GetRGBBack(screen.GetCell(controller.nameEdit.X1, controller.nameEdit.Y1-1).Attributes); got != vtui.GetRGBBack(normal) {
		t.Fatalf("blank row below Profile details background = %#x, want dialog %#x", got, vtui.GetRGBBack(normal))
	}
	if got := vtui.GetRGBBack(screen.GetCell(controller.enabledEdit.X1, controller.enabledEdit.Y1).Attributes); got != vtui.GetRGBBack(normal) {
		t.Fatalf("Enabled background = %#x, want unchanged dialog background %#x", got, vtui.GetRGBBack(normal))
	}
	if cell := screen.GetCell(controller.list.X2, controller.list.Y1); rune(cell.Char) != rune(vtui.ScrollUpArrow) ||
		cell.Attributes != managerHintWithBackground(box, managerInputBackground(true)) {
		t.Fatalf("active scrollbar top cell = %q/%#x", rune(cell.Char), cell.Attributes)
	}
	controller.dialog.SetFocusedItem(controller.addButton)
	dialog.Show(screen)
	if got, want := screen.GetCell(controller.list.X1, controller.list.Y1+1).Attributes, inactiveManagerCursorAttr(normal); got != want {
		t.Fatalf("ListBox cursor while Add profile is focused = %#x, want neutral %#x", got, want)
	}
	controller.dialog.SetFocusedItem(controller.list)
	controller.beginInlineEdit(1, entries[1])
	dialog.Show(screen)
	if got := screen.GetCell(leftHeaderX, y1+1).Attributes; got != title {
		t.Fatalf("edit-mode Profiles margin = %#x, want inactive %#x", got, title)
	}
	editingHeaderX := splitX + 1 + ((x2-1)-(splitX+1)+1-(runewidth.StringWidth(dialog.editingTitle)+2))/2
	if cell := screen.GetCell(editingHeaderX, y1+1); cell.Attributes != selected || rune(cell.Char) != ' ' {
		t.Fatalf("edit-mode right header margin = %q/%#x, want active blank/%#x", rune(cell.Char), cell.Attributes, selected)
	}

	if got, want := screen.GetCell(controller.variablesEdit.X1, controller.variablesEdit.Y1).Attributes, edit; got != want {
		t.Fatalf("active Variables attr = %#x, want standard input %#x", got, want)
	}
	inactiveBackground := managerInputBackground(false)
	if got, want := screen.GetCell(controller.list.X1, controller.list.Y1).Attributes, managerHintWithBackground(subduedManagerRowAttr(normal), inactiveBackground); got != want {
		t.Fatalf("inactive row color = %#x, want %#x", got, want)
	}
	if got, want := screen.GetCell(controller.list.X1, controller.list.Y1+1).Attributes, inactiveManagerCursorAttr(normal); got != want {
		t.Fatalf("unfocused current-profile color = %#x, want neutral %#x", got, want)
	}
	if got, want := screen.GetCell(controller.list.X1, controller.list.Y1+2).Attributes, managerHintWithBackground(normal, inactiveBackground); got != want {
		t.Fatalf("ordinary inactive-pane row color = %#x, want %#x", got, want)
	}
	if !controller.list.ShowScrollBar || controller.list.ItemCount <= controller.list.ViewHeight {
		t.Fatal("overflowing profile list did not enable its scrollbar")
	}
	if cell := screen.GetCell(controller.list.X2, controller.list.Y1); rune(cell.Char) != rune(vtui.ScrollUpArrow) ||
		cell.Attributes != managerHintWithBackground(box, inactiveBackground) {
		t.Fatalf("inactive scrollbar top cell = %q/%#x", rune(cell.Char), cell.Attributes)
	}

	parts := parseManagerHint(dialog.bottomHint)
	if len(parts) == 0 || parts[0].label == "" {
		t.Fatalf("parsed manager hint = %#v", parts)
	}
	start := -1
	wantActive := managerHintWithBackground(hint, managerHintBackground())
	for x := x1 + 1; x < x2; x++ {
		if screen.GetCell(x, y2).Attributes == wantActive {
			start = x
			break
		}
	}
	if start < 0 {
		t.Fatal("manager hint strip contains no key text")
	}
	if got := screen.GetCell(start, y2).Attributes; got != wantActive {
		t.Fatalf("hotkey attr = %#x, want %#x", got, wantActive)
	}
	labelX := start + runewidth.StringWidth(parts[0].key) + 1
	wantLabel := managerHintWithBackground(title, managerHintBackground())
	if got := screen.GetCell(labelX, y2).Attributes; got != wantLabel {
		t.Fatalf("hotkey description attr = %#x, want %#x", got, wantLabel)
	}
}

func TestManagerInactiveInputBackgroundUsesGentleLift(t *testing.T) {
	old := vtui.Palette[vtui.ColDialogEdit]
	t.Cleanup(func() { vtui.Palette[vtui.ColDialogEdit] = old })
	vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, 0xD0D0D0, 0)
	if got, want := vtui.GetRGBBack(managerInputBackground(false)), uint32(0x141414); got != want {
		t.Fatalf("inactive input background = %#x, want subdued lift %#x", got, want)
	}
}

func TestManagerHintBackgroundMatchesOnlyRenderedTextWidth(t *testing.T) {
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(50, 5)
	oldText := vtui.Palette[vtui.ColDialogText]
	oldTitle := vtui.Palette[vtui.ColDialogBoxTitle]
	oldHint := vtui.Palette[vtui.ColDialogHighlightBoxTitle]
	t.Cleanup(func() {
		vtui.Palette[vtui.ColDialogText] = oldText
		vtui.Palette[vtui.ColDialogBoxTitle] = oldTitle
		vtui.Palette[vtui.ColDialogHighlightBoxTitle] = oldHint
	})
	base := vtui.SetRGBBoth(0, 0x505050, 0x202020)
	vtui.Palette[vtui.ColDialogText] = base
	vtui.Palette[vtui.ColDialogBoxTitle] = vtui.SetRGBBoth(0, 0x808080, 0x202020)
	vtui.Palette[vtui.ColDialogHighlightBoxTitle] = vtui.SetRGBBoth(0, 0xA0A0A0, 0x202020)
	screen.FillRect(5, 2, 44, 2, '─', base)

	const hint = "F4 Edit  F2 Settings"
	drawManagerHint(screen, 5, 44, 2, hint)
	contentWidth := managerHintWidth(parseManagerHint(hint)) + 2
	available := 38 // inside x=6..43
	start := 6 + (available-contentWidth)/2
	end := start + contentWidth - 1
	wantBackground := vtui.GetRGBBack(managerHintBackground())
	for x := start; x <= end; x++ {
		if got := vtui.GetRGBBack(screen.GetCell(x, 2).Attributes); got != wantBackground {
			t.Fatalf("hint background at x=%d = %#x, want %#x", x, got, wantBackground)
		}
	}
	if cell := screen.GetCell(start, 2); rune(cell.Char) != ' ' {
		t.Fatalf("left hint margin = %q, want a space", rune(cell.Char))
	}
	if cell := screen.GetCell(end, 2); rune(cell.Char) != ' ' {
		t.Fatalf("right hint margin = %q, want a space", rune(cell.Char))
	}
	if cell := screen.GetCell(start+1, 2); rune(cell.Char) != 'F' {
		t.Fatalf("hint text begins at %q after left margin, want F", rune(cell.Char))
	}
	if cell := screen.GetCell(start-1, 2); rune(cell.Char) != '─' || cell.Attributes != base {
		t.Fatalf("left cell outside hint was changed to %q/%#x", rune(cell.Char), cell.Attributes)
	}
	if cell := screen.GetCell(end+1, 2); rune(cell.Char) != '─' || cell.Attributes != base {
		t.Fatalf("right cell outside hint was changed to %q/%#x", rune(cell.Char), cell.Attributes)
	}
}

func TestManagerDividerMovesWithDialog(t *testing.T) {
	controller, _, _, _ := newManagerControllerForTest(t, Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{{Kind: KindProfile, Name: "one", Enabled: true}},
	})
	dialog := controller.dialog
	x1, y1, x2, y2 := dialog.GetPosition()
	originalSplit := x1 + dialog.splitOffset
	const delta = 7
	dialog.SetPosition(x1+delta, y1, x2+delta, y2)

	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)
	dialog.Show(screen)
	newSplit := originalSplit + delta
	if cell := screen.GetCell(newSplit, y1+1); rune(cell.Char) != '│' || cell.Attributes != vtui.Palette[vtui.ColDialogBox] {
		t.Fatalf("moved divider cell = %q/%#x at x=%d", rune(cell.Char), cell.Attributes, newSplit)
	}
	if cell := screen.GetCell(originalSplit, y1+1); rune(cell.Char) == '│' {
		t.Fatalf("divider remained at its old absolute x=%d", originalSplit)
	}
}

func TestEnvManDialogsPassLayoutValidation(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	type dialogContainer interface {
		vtui.Container
		GetPosition() (int, int, int, int)
	}
	scenarios := map[string]func() dialogContainer{
		"manager": func() dialogContainer {
			return plugin.openManagerDialog(app)
		},
		"profile": func() dialogContainer {
			return plugin.openProfileDialog(app, Entry{
				Kind:      KindProfile,
				Name:      "development",
				Enabled:   true,
				Variables: []string{"PATH=%PATH%:/tools", "MODE=debug"},
			}, nil, nil)
		},
		"settings": func() dialogContainer {
			return plugin.openConfigDialog(app, false)
		},
	}
	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			vtui.SetDefaultPalette()
			screen := vtui.NewSilentScreenBuf()
			screen.AllocBuf(120, 60)
			vtui.FrameManager.Init(screen)
			dialog := build()
			rules := vtui.DefaultLayoutRules
			if name == "manager" {
				rules.MaxWidth = 120
			}
			for _, err := range vtui.ValidateLayoutWithRules(dialog, rules) {
				t.Errorf("layout validation failed: %v", err)
			}
			if name == "manager" {
				x1, _, x2, _ := dialog.GetPosition()
				wantWidth, _ := managerDialogSize(120, 60)
				if width := x2 - x1 + 1; width != wantWidth {
					t.Errorf("manager width = %d, want %d", width, wantWidth)
				}
				closeButtonFound := false
				for _, child := range dialog.GetChildren() {
					button, ok := child.(*vtui.Button)
					if !ok {
						continue
					}
					if button.GetText() == plugin.text("EnvMan.Close", "&Close", "&Закрыть") {
						closeButtonFound = true
					}
				}
				if closeButtonFound {
					t.Error("manager still has a bottom Close button")
				}
				manager := dialog.(*managerWindow)
				if manager.bottomHint == "" {
					t.Error("manager bottom-border hint is empty")
				}
				if manager.listHint != plugin.text("EnvMan.ManagerKeys", "Ins Add  Del Delete  F4 Edit  F5 Copy  Space Toggle  Ctrl+Up/Down Move  F2 Settings", "Ins Добавить  Del Удалить  F4 Правка  F5 Копия  Space Переключить  Ctrl+↑/↓ Переместить  F2 Настройки") {
					t.Error("manager stored a pre-truncated bottom-border hint")
				}
			}
		})
	}
}

func TestManagerDialogUsesSeventyPercentAboveMinimum(t *testing.T) {
	tests := []struct {
		name         string
		screenW      int
		screenH      int
		wantW, wantH int
	}{
		{name: "classic minimum", screenW: 80, screenH: 25, wantW: 78, wantH: 23},
		{name: "large screen", screenW: 200, screenH: 60, wantW: 140, wantH: 42},
		{name: "only width exceeds minimum", screenW: 160, screenH: 30, wantW: 112, wantH: 23},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			width, height := managerDialogSize(test.screenW, test.screenH)
			if width != test.wantW || height != test.wantH {
				t.Fatalf("manager size = %dx%d, want %dx%d", width, height, test.wantW, test.wantH)
			}
		})
	}
}

func TestProfileDialogSaveFailureKeepsEditedDialogOpen(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	dialog := plugin.openProfileDialog(app, Entry{
		Kind: KindProfile, Name: "profile", Enabled: true, Variables: []string{"A=one"},
	}, func(Entry) error {
		return errors.New("injected save failure")
	}, nil)
	if dialog == nil {
		t.Fatal("profile dialog was not created")
	}
	for _, child := range dialog.GetChildren() {
		button, ok := child.(*vtui.Button)
		if ok && button.IsDefault {
			button.OnClick()
			if dialog.IsDone() {
				t.Fatal("profile dialog closed after a failed save")
			}
			return
		}
	}
	t.Fatal("profile save button was not found")
}

func TestEditedEnvironmentTargetPreservesProtectedAndAbsentIgnoredValues(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.IgnoredVariables = []string{"IGNORED"}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	plugin.store = store

	edited, explicit, err := plugin.decodeEnvironmentDocument("EDITED=new\n")
	if err != nil {
		t.Fatal(err)
	}
	target, err := plugin.editedEnvironmentTarget(
		[]string{"EDITED=old", "IGNORED=keep", "BAD-NAME=keep", "PROMPT=keep"},
		edited,
		explicit,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"EDITED":   "new",
		"IGNORED":  "keep",
		"BAD-NAME": "keep",
		"PROMPT":   "keep",
	}
	assertEnvironmentValues(t, target, want)

	edited, explicit, err = plugin.decodeEnvironmentDocument("EDITED=new\nIGNORED=\n")
	if err != nil {
		t.Fatal(err)
	}
	target, err = plugin.editedEnvironmentTarget(
		[]string{"EDITED=old", "IGNORED=keep"},
		edited,
		explicit,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertEnvironmentValues(t, target, map[string]string{"EDITED": "new"})
}

func TestExportableEnvironmentOmitsIgnoredReservedAndNonportableNames(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.IgnoredVariables = []string{"IGNORED"}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	plugin.store = store

	encoded, err := EncodeEnvironment(plugin.exportableEnvironment([]string{
		"Z=last",
		"IGNORED=hidden",
		"BAD-NAME=hidden",
		"PROMPT=hidden",
		"A=first",
	}), plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != "A=first\nZ=last\n" {
		t.Fatalf("export = %q", encoded)
	}
}

func TestEnvironmentFileDecoderUsesBOMAndOEMFallback(t *testing.T) {
	utf16, err := vfs.EncodeBytes([]byte("A=one\n"), 1200)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeEnvironmentBytes(utf16)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "A=one\n" {
		t.Fatalf("UTF-16 decode = %q", decoded)
	}

	oemInput := []byte{0x80, '\n'}
	wantOEM, err := vfs.DecodeBytes(oemInput, 22222)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeEnvironmentBytes(oemInput)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != string(wantOEM) {
		t.Fatalf("OEM decode = %q, want %q", decoded, wantOEM)
	}
}

type envManEditorTestApp struct {
	requests []vfs.TextEditorRequest
	messages [][]string
	active   vfs.VFS
	refresh  int
}

type rejectingEnvironmentHost struct {
	snapshot vfs.ProcessEnvironmentSnapshot
}

func (host *rejectingEnvironmentHost) SnapshotProcessEnvironment() vfs.ProcessEnvironmentSnapshot {
	return cloneEnvironmentSnapshot(host.snapshot)
}

func (host *rejectingEnvironmentHost) ApplyProcessEnvironment([]vfs.ProcessEnvironmentChange) (vfs.ProcessEnvironmentSnapshot, error) {
	return cloneEnvironmentSnapshot(host.snapshot), errors.New("injected environment apply failure")
}

func (app *envManEditorTestApp) GetActivePanelVFS() vfs.VFS { return app.active }
func (*envManEditorTestApp) GetPassivePanelVFS() vfs.VFS    { return nil }
func (*envManEditorTestApp) GetSelectedNames() []string     { return nil }
func (*envManEditorTestApp) GetSelectedName() string        { return "" }
func (app *envManEditorTestApp) RefreshAll()                { app.refresh++ }
func (*envManEditorTestApp) SetPendingSelection(string)     {}
func (*envManEditorTestApp) RunProgressTask(_ string, _ string, _ bool, task func(context.Context, func(string, int)) error, done func(error)) {
	err := task(context.Background(), func(string, int) {})
	done(err)
}
func (*envManEditorTestApp) RunAdvancedProgressTask(string, bool, func(context.Context, vfs.TaskReporter) error, func(error)) {
}
func (app *envManEditorTestApp) Message(_, _ string, buttons []string) int {
	app.messages = append(app.messages, append([]string(nil), buttons...))
	return 0
}
func (*envManEditorTestApp) InputBox(string, string, string, func(string)) {}
func (*envManEditorTestApp) Menu(string, []string, func(int))              {}
func (app *envManEditorTestApp) OpenTextEditor(request vfs.TextEditorRequest) error {
	request.Content = append([]byte(nil), request.Content...)
	app.requests = append(app.requests, request)
	return nil
}

func TestEntryEditorParseFailureReopensExactContentAtErrorLine(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	plugin.openEntryEditorContent(app, []byte("initial"), 0, false, nil)
	invalid := []byte("=Name=test\n=Enabled=1\n\nGOOD=one\nnot-an-assignment\ntrailing  ")
	app.requests[0].OnClose(invalid, nil)
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("recovery frame = %T", vtui.FrameManager.GetTopFrame())
	}
	t.Cleanup(vtui.FrameManager.CloseActiveScreen)
	dialog.SetExitCode(0)
	if len(app.requests) != 2 {
		t.Fatalf("editor requests = %d", len(app.requests))
	}
	reopened := app.requests[1]
	if string(reopened.Content) != string(invalid) {
		t.Fatalf("reopened content = %q", reopened.Content)
	}
	if reopened.CursorLine != 4 || !reopened.Modified {
		t.Fatalf("reopened cursor/modified = %d/%v", reopened.CursorLine, reopened.Modified)
	}
}

func TestEntryEditorSaveFailureCanReopenExactContent(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	plugin.openEntryEditorContent(app, []byte("initial"), 0, false, func(Entry) error {
		return errors.New("injected save failure")
	})
	edited := []byte("=Name=test\n=Enabled=1\n\nA=one\n")
	app.requests[0].OnClose(edited, nil)
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("save recovery frame = %T", vtui.FrameManager.GetTopFrame())
	}
	dialog.SetExitCode(0)
	if len(app.requests) != 2 || string(app.requests[1].Content) != string(edited) || !app.requests[1].Modified {
		t.Fatalf("save recovery request = %#v", app.requests)
	}
}

func TestEnvironmentEditorApplyFailureCanReopenExactContent(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	plugin.store = store
	plugin.envHost = &rejectingEnvironmentHost{snapshot: vfs.ProcessEnvironmentSnapshot{
		Variables: []vfs.ProcessEnvironmentVariable{{Name: "A", Value: "old"}},
	}}
	app := &envManEditorTestApp{}
	plugin.openEnvironmentEditorContent(app, true, []byte("A=old\n"), 0, false)
	edited := []byte("A=new\n")
	app.requests[0].OnClose(edited, nil)
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("apply recovery frame = %T", vtui.FrameManager.GetTopFrame())
	}
	dialog.SetExitCode(0)
	if len(app.requests) != 2 || string(app.requests[1].Content) != string(edited) || !app.requests[1].Modified {
		t.Fatalf("apply recovery request = %#v", app.requests)
	}
}

func TestEditorParseFailureCanBeDiscarded(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	plugin.openEntryEditorContent(app, []byte("initial"), 0, false, nil)
	app.requests[0].OnClose([]byte("invalid"), nil)
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("recovery frame = %T", vtui.FrameManager.GetTopFrame())
	}
	t.Cleanup(vtui.FrameManager.CloseActiveScreen)
	dialog.SetExitCode(1)
	if len(app.requests) != 1 {
		t.Fatalf("discard opened %d editor requests", len(app.requests))
	}
}

func TestManagerListFollowsDialogTheme(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	dialog := plugin.openManagerDialog(&envManEditorTestApp{})
	t.Cleanup(vtui.FrameManager.CloseActiveScreen)

	var list *vtui.ListBox
	for _, child := range dialog.GetChildren() {
		if candidate, ok := child.(*vtui.ListBox); ok {
			list = candidate
			break
		}
	}
	if list == nil {
		t.Fatal("manager list was not found")
	}
	if list.ColorTextIdx != vtui.ColDialogText ||
		list.ColorSelectedTextIdx != vtui.ColDialogSelectedButton ||
		list.ColorItemSelectTextIdx != vtui.ColDialogHighlightText ||
		list.ColorItemSelectCursorIdx != vtui.ColDialogHighlightSelectedButton ||
		list.ColorTitleIdx != vtui.ColDialogHighlightText ||
		list.ColorBoxIdx != vtui.ColDialogBox {
		t.Fatalf("manager list palette indices = text:%d selected:%d item:%d cursor:%d title:%d box:%d",
			list.ColorTextIdx, list.ColorSelectedTextIdx, list.ColorItemSelectTextIdx,
			list.ColorItemSelectCursorIdx, list.ColorTitleIdx, list.ColorBoxIdx)
	}
	if list.ScrollBar == nil || list.ScrollBar.ColorIdx != vtui.ColDialogBox {
		t.Fatal("manager list scrollbar does not use the dialog palette")
	}
	if !list.AlwaysShowCursor {
		t.Fatal("manager list does not preserve the current-profile highlight without focus")
	}
	// This test exercises changes to the ordinary list text color. Persistent
	// selection highlighting has its own visual-state assertions.
	list.AlwaysShowCursor = false

	old := vtui.Palette[vtui.ColDialogText]
	t.Cleanup(func() { vtui.Palette[vtui.ColDialogText] = old })
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)
	list.SetFocus(false)

	first := vtui.SetRGBBoth(0, 0x102030, 0x405060)
	vtui.Palette[vtui.ColDialogText] = first
	list.Show(screen)
	if got := screen.GetCell(list.X1, list.Y1).Attributes; got != first {
		t.Fatalf("initial list color = %#x, want %#x", got, first)
	}

	second := vtui.SetRGBBoth(0, 0x708090, 0xA0B0C0)
	vtui.Palette[vtui.ColDialogText] = second
	list.Show(screen)
	if got := screen.GetCell(list.X1, list.Y1).Attributes; got != second {
		t.Fatalf("list retained stale theme color %#x, want %#x", got, second)
	}
}

func TestEnvironmentEditorParseFailureReopensExactContentAtErrorLine(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	app := &envManEditorTestApp{}
	plugin.openEnvironmentEditorContent(app, true, []byte("A=one\n"), 0, false)
	invalid := []byte("A=one\nnot-an-assignment\ntrailing  ")
	app.requests[0].OnClose(invalid, nil)
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("recovery frame = %T", vtui.FrameManager.GetTopFrame())
	}
	t.Cleanup(vtui.FrameManager.CloseActiveScreen)
	dialog.SetExitCode(0)
	if len(app.requests) != 2 {
		t.Fatalf("editor requests = %d", len(app.requests))
	}
	reopened := app.requests[1]
	if string(reopened.Content) != string(invalid) || reopened.CursorLine != 1 || !reopened.Modified {
		t.Fatalf("reopened = content %q, cursor %d, modified %v", reopened.Content, reopened.CursorLine, reopened.Modified)
	}
}

func initEnvManTestUI() {
	vtui.SetDefaultPalette()
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)
	vtui.FrameManager.Init(screen)
}

func TestDirectEnvironmentEditDoesNotAdvanceLastApplied(t *testing.T) {
	directory := t.TempDir()
	host := newEnvManTestHost("A=old")
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plugin.Close() })
	before := cloneEnvironmentSnapshot(plugin.lastApplied)
	environment, explicit, err := plugin.decodeEnvironmentDocument("A=new\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.applyEditedEnvironment(environment, explicit); err != nil {
		t.Fatal(err)
	}
	if plugin.lastApplied.Generation != before.Generation {
		t.Fatalf("last applied generation advanced from %d to %d", before.Generation, plugin.lastApplied.Generation)
	}
	_, drift, err := plugin.currentDrift()
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Changed) != 1 || drift.Changed[0].Name != "A" {
		t.Fatalf("direct edit drift = %#v", drift)
	}
}

func assertEnvironmentValues(t *testing.T, environment []string, want map[string]string) {
	t.Helper()
	state, err := newEnvironmentState(environment, runtimeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.values) != len(want) {
		t.Fatalf("environment = %#v, want %#v", environment, want)
	}
	for name, value := range want {
		got, exists := state.lookup(name)
		if !exists || got != value {
			t.Fatalf("%s = %q/%v, want %q", name, got, exists, value)
		}
	}
}

var _ vfs.App = (*envManEditorTestApp)(nil)
var _ vfs.TextEditorHost = (*envManEditorTestApp)(nil)
