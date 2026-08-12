package envman

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type managerController struct {
	plugin        *Plugin
	app           vfs.App
	dialog        *managerWindow
	list          *vtui.ListBox
	nameEdit      *vtui.Edit
	enabledEdit   *vtui.Checkbox
	variablesEdit *vtui.MultiLineEdit
	addButton     *managerModeButton
	saveButton    *managerModeButton
	cancelButton  *managerModeButton
	config        Config
	closed        bool
	editing       bool
	editorIndex   int
}

func (plugin *Plugin) openFromMenu(app vfs.App) {
	plugin.openManager(app, false)
}

func (plugin *Plugin) openManager(app vfs.App, quiet bool) {
	if app == nil {
		return
	}
	if err := plugin.reloadConfig(); err != nil {
		plugin.showError(app, err)
	}
	if !quiet {
		current, drift, err := plugin.currentDrift()
		if err != nil {
			plugin.showError(app, err)
			return
		}
		if !drift.Empty() {
			dialog := showEnvManMessage(
				app,
				plugin.text("EnvMan.DriftTitle", "Environment changed", "Окружение изменено"),
				plugin.text("EnvMan.DriftMessage", "The process environment changed outside Environment Manager. How should those changes be handled?", "Окружение процесса было изменено вне Менеджера окружения. Как обработать эти изменения?"),
				[]string{
					plugin.text("EnvMan.DriftContinue", "&Continue", "&Продолжить"),
					plugin.text("EnvMan.DriftCancel", "Cancel", "Отмена"),
					plugin.text("EnvMan.DriftImport", "&Import", "&Импортировать"),
					plugin.text("EnvMan.DriftIgnore", "I&gnore", "И&гнорировать"),
				},
				vtui.MessageInfo,
			)
			if dialog != nil {
				dialog.OnResult = func(choice int) {
					plugin.handleDriftChoice(app, current, drift, choice)
				}
			}
			return
		}
	}
	plugin.openManagerDialog(app)
}

func (plugin *Plugin) handleDriftChoice(app vfs.App, current vfs.ProcessEnvironmentSnapshot, drift Diff, choice int) {
	switch choice {
	case 0:
		// Applying on manager close restores the managed target.
		plugin.openManagerDialog(app)
	case 2:
		entry := importDriftEntry(drift, plugin.text("EnvMan.ImportedProfile", "Imported changes", "Импортированные изменения"), plugin.options)
		plugin.openProfileDialog(app, entry, func(imported Entry) error {
			if err := plugin.mutateConfig(func(config Config) (Config, error) {
				return insertEntry(config, len(config.Entries), imported), nil
			}, false); err != nil {
				return err
			}
			plugin.setLastApplied(current)
			plugin.openManagerDialog(app)
			return nil
		}, nil)
	case 3:
		if err := plugin.ignoreDrift(drift); err != nil {
			plugin.showError(app, err)
			return
		}
		plugin.setLastApplied(current)
		plugin.openManagerDialog(app)
	}
}

func (plugin *Plugin) ignoreDrift(diff Diff) error {
	return plugin.mutateConfig(func(config Config) (Config, error) {
		next := cloneConfig(config)
		seen := make(map[string]struct{}, len(next.IgnoredVariables))
		for _, name := range next.IgnoredVariables {
			seen[normalizeName(name, plugin.options)] = struct{}{}
		}
		appendNames := func(changes []Change) {
			for _, change := range changes {
				key := normalizeName(change.Name, plugin.options)
				if _, exists := seen[key]; exists || isReservedName(change.Name, plugin.options) {
					continue
				}
				seen[key] = struct{}{}
				next.IgnoredVariables = append(next.IgnoredVariables, change.Name)
			}
		}
		appendNames(diff.Added)
		appendNames(diff.Changed)
		appendNames(diff.Removed)
		return next, nil
	}, true)
}

func (plugin *Plugin) openManagerDialog(app vfs.App) *managerWindow {
	config := plugin.snapshotConfig()
	width, height := managerDialogSize(80, 25)
	if vtui.FrameManager != nil {
		width, height = managerDialogSize(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	}
	title := " " + plugin.text("EnvMan.ManagerTitle", "Environment profiles", "Профили окружения") + " "
	base := vtui.NewCenteredDialog(width, height, title)
	leftWidth := width / 3
	if leftWidth < 24 {
		leftWidth = 24
	}
	if leftWidth > 34 {
		leftWidth = 34
	}
	listX := base.X1 + 2
	splitX := listX + leftWidth
	rightX := splitX + 2
	rightWidth := base.X2 - rightX - 1
	footerText := plugin.text("EnvMan.ManagerKeys", "Ins Add  Del Delete  F4 Edit  F5 Copy  Space Toggle  Ctrl+Up/Down Move  F2 Settings", "Ins Добавить  Del Удалить  F4 Правка  F5 Копия  Space Переключить  Ctrl+↑/↓ Переместить  F2 Настройки")
	editHint := plugin.text("EnvMan.ManagerEditKeys", "Ctrl+Enter Save  Esc Cancel", "Ctrl+Enter Сохранить  Esc Отмена")
	dialog := &managerWindow{
		Window:        base,
		bottomHint:    footerText,
		listHint:      footerText,
		editHint:      editHint,
		splitOffset:   splitX - base.X1,
		profilesTitle: plugin.text("EnvMan.ManagerProfilesPane", "Profiles", "Профили"),
		detailsTitle:  plugin.text("EnvMan.ManagerDetailsPane", "Profile details", "Содержимое профиля"),
		editingTitle:  plugin.text("EnvMan.ManagerEditingPane", "Editing profile", "Редактирование профиля"),
	}
	dialog.ShowClose = true
	controller := &managerController{plugin: plugin, app: app, dialog: dialog, config: config}
	dialog.controller = controller

	listPanelLeft := listX
	listPanelRight := splitX - 2
	listPanelTop := dialog.Y1 + 2
	listPanelBottom := dialog.Y2 - 4
	list := vtui.NewListBox(
		listPanelLeft,
		listPanelTop+1,
		listPanelRight-listPanelLeft+1,
		listPanelBottom-listPanelTop,
		controller.rows(),
	)
	useDialogListColors(list)
	bindListBoxScrollbar(list)
	list.ShowScrollBar = true
	list.AlwaysShowCursor = true
	list.SetGrowMode(vtui.GrowHiY)
	list.OnKeyDown = controller.handleKey
	list.OnSelect = func(int) { controller.selectionChanged() }
	controller.list = list
	dialog.AddItem(list)

	y := dialog.Y1 + 3
	nameLabelText := plugin.text("EnvMan.ProfileName", "&Name:", "&Имя:")
	nameLabel := vtui.NewText(rightX, y, nameLabelText, 0)
	dialog.AddItem(nameLabel)
	cleanNameLabel, _, _ := vtui.ParseAmpersandString(nameLabelText)
	nameEditX := rightX + runewidth.StringWidth(cleanNameLabel) + 1
	enabledText := plugin.text("EnvMan.ManagerEnabled", "&Enabled", "&Включён")
	cleanEnabled, _, _ := vtui.ParseAmpersandString(enabledText)
	enabledWidth := 4 + runewidth.StringWidth(cleanEnabled)
	enabledX := rightX + rightWidth - enabledWidth
	nameEditWidth := enabledX - nameEditX - 1
	nameEdit := vtui.NewEdit(nameEditX, y, nameEditWidth, "")
	nameEdit.SetGrowMode(vtui.GrowHiX)
	dialog.AddItem(nameEdit)

	enabled := vtui.NewCheckbox(enabledX, y, enabledText, false)
	dialog.AddItem(enabled)
	y += 2

	variablesLabel := runewidth.Truncate(
		plugin.text("EnvMan.ProfileVariables", "Variables (NAME=value; an empty value unsets):", "Переменные (ИМЯ=значение; пустое значение удаляет):"),
		rightWidth,
		"…",
	)
	dialog.AddItem(vtui.NewText(rightX, y, variablesLabel, 0))
	y++
	variablesHeight := dialog.Y2 - y - 3
	if variablesHeight < 4 {
		variablesHeight = 4
	}
	variables := vtui.NewMultiLineEdit(rightX, y, rightWidth, variablesHeight, "")
	variables.SetGrowMode(vtui.GrowHiX | vtui.GrowHiY)
	dialog.AddItem(variables)

	addButton := newManagerModeButton(vtui.NewButton(0, 0, plugin.text("EnvMan.AddProfile", "&Add profile", "&Добавить профиль")))
	saveButton := newManagerModeButton(vtui.NewButton(0, 0, plugin.text("EnvMan.Save", "&Save", "&Сохранить")))
	cancelButton := newManagerModeButton(vtui.NewButton(0, 0, plugin.text("EnvMan.Cancel", "Cancel", "Отмена")))
	addButton.OnClick = func() {
		index, _, selected := controller.selected()
		controller.addProfile(index, selected)
	}
	saveButton.IsDefault = true
	saveButton.OnClick = func() { controller.saveInlineEdit() }
	cancelButton.OnClick = controller.cancelInlineEdit
	dialog.AddItem(addButton)
	dialog.AddItem(saveButton)
	dialog.AddItem(cancelButton)
	addLayout := vtui.NewHBoxLayout(listPanelLeft, dialog.Y2-2, listPanelRight-listPanelLeft+1, 1)
	addLayout.HorizontalAlign = vtui.AlignCenter
	addLayout.Add(addButton, vtui.Margins{}, vtui.AlignTop)
	addLayout.Apply()
	buttons := vtui.NewHBoxLayout(rightX, dialog.Y2-2, rightWidth, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(saveButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Apply()
	addButton.SetGrowMode(vtui.GrowAll)
	saveButton.SetGrowMode(vtui.GrowAll)
	cancelButton.SetGrowMode(vtui.GrowAll)

	controller.nameEdit = nameEdit
	controller.enabledEdit = enabled
	controller.variablesEdit = variables
	controller.addButton = addButton
	controller.saveButton = saveButton
	controller.cancelButton = cancelButton
	controller.refresh(0)

	dialog.OnResult = func(int) {
		if controller.closed {
			return
		}
		controller.closed = true
		if err := plugin.applyStoredConfig(); err != nil {
			plugin.showError(app, err)
			return
		}
		app.RefreshAll()
	}
	pushEnvManDialog(app, dialog)
	return dialog
}

func managerDialogSize(screenWidth, screenHeight int) (int, int) {
	const minimumWidth, minimumHeight = 78, 23
	width, height := minimumWidth, minimumHeight
	if candidate := screenWidth * 70 / 100; candidate > width {
		width = candidate
	}
	if candidate := screenHeight * 70 / 100; candidate > height {
		height = candidate
	}
	if maximum := screenWidth - 2; maximum > 0 && width > maximum {
		width = maximum
	}
	if maximum := screenHeight - 2; maximum > 0 && height > maximum {
		height = maximum
	}
	return width, height
}

// useDialogListColors keeps the profile list in the same semantic palette
// family as its containing dialog. ListBox is backed by Table, whose defaults
// intentionally use the panel-oriented ColTable palette.
func useDialogListColors(list *vtui.ListBox) {
	list.ColorTextIdx = vtui.ColDialogText
	list.ColorSelectedTextIdx = vtui.ColDialogSelectedButton
	list.ColorItemSelectTextIdx = vtui.ColDialogHighlightText
	list.ColorItemSelectCursorIdx = vtui.ColDialogHighlightSelectedButton
	list.ColorTitleIdx = vtui.ColDialogHighlightText
	list.ColorBoxIdx = vtui.ColDialogBox
	if list.ScrollBar != nil {
		list.ScrollBar.ColorIdx = vtui.ColDialogBox
	}
}

// NewListBox embeds a value-copy of Table, so the ScrollBar callback created
// by NewTable still points at the discarded original ScrollView. Rebind it to
// the live ListBox until vtui can do this as part of ListBox construction.
func bindListBoxScrollbar(list *vtui.ListBox) {
	if list == nil || list.ScrollBar == nil {
		return
	}
	list.ScrollBar.OnScroll = func(value int) {
		list.ScrollBy(value - list.TopPos)
	}
}

func (controller *managerController) rows() []string {
	rows := make([]string, 0, len(controller.config.Entries))
	for _, entry := range controller.config.Entries {
		if entry.Kind == KindSeparator {
			rows = append(rows, controller.plugin.text("EnvMan.SeparatorRow", "──────────────── group ────────────────", "──────────────── группа ───────────────"))
			continue
		}
		marker := controller.plugin.text("EnvMan.DisabledMarker", "[ ]", "[ ]")
		if entry.Enabled {
			marker = controller.plugin.text("EnvMan.EnabledMarker", "[x]", "[x]")
		}
		label, _ := profileMenuLabel(entry.Name)
		rows = append(rows, marker+" "+label)
	}
	if len(rows) == 0 {
		rows = append(rows, controller.plugin.text("EnvMan.EmptyList", "<no profiles — press Ins to add one>", "<профилей нет — нажмите Ins, чтобы добавить>"))
	}
	return rows
}

func (controller *managerController) refresh(selection int) {
	controller.list.Items = controller.rows()
	controller.list.UpdateRows()
	rows := make([]vtui.TableRow, len(controller.list.Items))
	for index, text := range controller.list.Items {
		inactive := false
		if index < len(controller.config.Entries) {
			entry := controller.config.Entries[index]
			inactive = entry.Kind == KindSeparator || !entry.Enabled
		}
		rows[index] = managerListRow{text: text, inactive: inactive, index: index, list: controller.list}
	}
	controller.list.SetRows(rows)
	if len(controller.config.Entries) == 0 {
		controller.list.SetSelectPos(0)
		if !controller.editing {
			controller.loadInlineEditor(0, Entry{}, false)
		}
		return
	}
	if selection < 0 {
		selection = 0
	}
	if selection >= len(controller.config.Entries) {
		selection = len(controller.config.Entries) - 1
	}
	controller.list.SetSelectPos(selection)
	if !controller.editing {
		controller.loadInlineEditor(selection, controller.config.Entries[selection], true)
	}
}

func (controller *managerController) selectionChanged() {
	controller.editing = false
	controller.list.SetDisabled(false)
	controller.dialog.bottomHint = controller.dialog.listHint
	index, entry, selected := controller.selected()
	controller.loadInlineEditor(index, entry, selected)
}

func (controller *managerController) loadInlineEditor(index int, entry Entry, selected bool) {
	if controller.nameEdit == nil || controller.enabledEdit == nil || controller.variablesEdit == nil {
		return
	}
	profile := selected && entry.Kind == KindProfile
	if profile {
		controller.nameEdit.SetText(entry.Name)
		if entry.Enabled {
			controller.enabledEdit.State = 1
		} else {
			controller.enabledEdit.State = 0
		}
		controller.variablesEdit.SetLines(entry.Variables)
		controller.editorIndex = index
	} else {
		controller.nameEdit.SetText("")
		controller.enabledEdit.State = 0
		controller.variablesEdit.SetText("")
	}
	controller.setInlineEditorEnabled(profile && controller.editing)
}

func (controller *managerController) setInlineEditorEnabled(enabled bool) {
	if controller.nameEdit == nil {
		return
	}
	// Preview controls stay disabled for every input path. managerWindow.Show
	// redraws them without vtui's foreground dimming before applying the
	// inactive-pane background.
	controller.nameEdit.SetDisabled(!enabled)
	controller.enabledEdit.SetDisabled(!enabled)
	controller.variablesEdit.SetDisabled(!enabled)
	controller.nameEdit.SetCanFocus(enabled)
	controller.enabledEdit.SetCanFocus(enabled)
	controller.variablesEdit.SetCanFocus(enabled)
	controller.addButton.SetDisabled(enabled)
	controller.addButton.setModeVisible(!enabled)
	controller.saveButton.SetDisabled(!enabled)
	controller.cancelButton.SetDisabled(!enabled)
	controller.saveButton.setModeVisible(enabled)
	controller.cancelButton.setModeVisible(enabled)
}

func (controller *managerController) beginInlineEdit(index int, entry Entry) {
	if entry.Kind != KindProfile || controller.nameEdit == nil {
		return
	}
	controller.editorIndex = index
	controller.editing = true
	controller.dialog.bottomHint = controller.dialog.editHint
	controller.nameEdit.SetText(entry.Name)
	if entry.Enabled {
		controller.enabledEdit.State = 1
	} else {
		controller.enabledEdit.State = 0
	}
	controller.variablesEdit.SetLines(entry.Variables)
	controller.setInlineEditorEnabled(true)
	controller.list.SetDisabled(true)
	controller.dialog.SetFocusedItem(controller.variablesEdit)
}

func (controller *managerController) saveInlineEdit() bool {
	if controller.nameEdit == nil || !controller.editing {
		return false
	}
	nextEntry := Entry{
		Kind:      KindProfile,
		Name:      strings.TrimSpace(controller.nameEdit.GetText()),
		Enabled:   controller.enabledEdit.State == 1,
		Variables: controller.variablesEdit.GetLines(),
	}
	if err := (Config{Version: CurrentConfigVersion, Entries: []Entry{nextEntry}}).Validate(controller.plugin.options); err != nil {
		controller.showError(err)
		return false
	}
	next, err := replaceEntry(controller.config, controller.editorIndex, nextEntry)
	if err != nil {
		controller.showError(err)
		return false
	}
	if err := controller.plugin.saveConfig(next); err != nil {
		controller.showError(err)
		return false
	}
	controller.config = cloneConfig(next)
	controller.editing = false
	controller.dialog.bottomHint = controller.dialog.listHint
	controller.setInlineEditorEnabled(false)
	controller.list.SetDisabled(false)
	controller.refresh(controller.editorIndex)
	controller.dialog.SetFocusedItem(controller.list)
	return true
}

func (controller *managerController) cancelInlineEdit() {
	controller.editing = false
	controller.dialog.bottomHint = controller.dialog.listHint
	controller.list.SetDisabled(false)
	controller.refresh(controller.editorIndex)
	controller.dialog.SetFocusedItem(controller.list)
}

func (controller *managerController) selected() (int, Entry, bool) {
	index := controller.list.SelectPos
	if index < 0 || index >= len(controller.config.Entries) {
		return 0, Entry{}, false
	}
	return index, cloneEntry(controller.config.Entries[index]), true
}

func (controller *managerController) save(next Config, selection int) bool {
	if err := controller.persist(next, selection); err != nil {
		controller.showError(err)
		return false
	}
	return true
}

func (controller *managerController) persist(next Config, selection int) error {
	if err := controller.plugin.saveConfig(next); err != nil {
		return err
	}
	controller.config = cloneConfig(next)
	controller.refresh(selection)
	return nil
}

func (controller *managerController) showError(err error) {
	vtui.ShowMessageOn(
		controller.dialog,
		" "+controller.plugin.text("EnvMan.ErrorTitle", "Environment Manager error", "Ошибка Менеджера окружения")+" ",
		err.Error(),
		[]string{controller.plugin.text("EnvMan.OK", "&OK", "&ОК")},
	)
}

func (controller *managerController) handleKey(event *vtinput.InputEvent) bool {
	if event == nil || !event.KeyDown {
		return false
	}
	control := event.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := event.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := event.ControlKeyState&vtinput.ShiftPressed != 0
	plain := !control && !alt && !shift
	index, entry, selected := controller.selected()
	if plain && (event.VirtualKeyCode == vtinput.VK_UP || event.VirtualKeyCode == vtinput.VK_DOWN) && controller.list.ItemCount > 0 {
		last := controller.list.ItemCount - 1
		if event.VirtualKeyCode == vtinput.VK_UP && controller.list.SelectPos == 0 {
			controller.list.SetSelectPos(last)
			controller.selectionChanged()
			return true
		}
		if event.VirtualKeyCode == vtinput.VK_DOWN && controller.list.SelectPos == last {
			controller.list.SetSelectPos(0)
			controller.selectionChanged()
			return true
		}
	}

	if !control && !alt && (event.VirtualKeyCode == vtinput.VK_ADD || event.Char == '+' || event.VirtualKeyCode == vtinput.VK_OEM_PLUS && shift) {
		return controller.setEnabled(index, selected, true, true)
	}
	if plain && (event.VirtualKeyCode == vtinput.VK_SUBTRACT || event.VirtualKeyCode == vtinput.VK_OEM_MINUS || event.Char == '-') {
		return controller.setEnabled(index, selected, false, true)
	}
	if plain && event.VirtualKeyCode == vtinput.VK_SPACE {
		return controller.toggle(index, selected, true)
	}
	if plain && event.VirtualKeyCode == vtinput.VK_RETURN {
		return controller.toggle(index, selected, false)
	}
	if plain && event.VirtualKeyCode == vtinput.VK_INSERT {
		controller.addProfile(index, selected)
		return true
	}
	if plain && event.VirtualKeyCode == vtinput.VK_DELETE && selected {
		controller.delete(index, entry)
		return true
	}
	if event.VirtualKeyCode == vtinput.VK_F4 && selected && (plain || alt) {
		if alt || controller.config.AlwaysUseEditor {
			controller.editWithEditor(index, entry)
		} else if plain {
			controller.beginInlineEdit(index, entry)
		}
		return plain || alt
	}
	if plain && event.VirtualKeyCode == vtinput.VK_F5 && selected {
		next, duplicateIndex, err := duplicateEntry(controller.config, index)
		if err != nil {
			controller.showError(err)
			return true
		}
		if controller.save(next, duplicateIndex) && entry.Kind == KindProfile {
			if controller.config.AlwaysUseEditor {
				controller.editWithEditor(duplicateIndex, next.Entries[duplicateIndex])
			} else {
				controller.beginInlineEdit(duplicateIndex, next.Entries[duplicateIndex])
			}
		}
		return true
	}
	if control && !alt && !shift && selected && (event.VirtualKeyCode == vtinput.VK_UP || event.VirtualKeyCode == vtinput.VK_DOWN) {
		delta := -1
		if event.VirtualKeyCode == vtinput.VK_DOWN {
			delta = 1
		}
		next, destination, err := moveEntry(controller.config, index, delta)
		if err != nil {
			controller.showError(err)
		} else {
			controller.save(next, destination)
		}
		return true
	}
	if shift && !control && !alt && event.VirtualKeyCode == vtinput.VK_F3 {
		controller.plugin.viewEnvironment(controller.app)
		return true
	}
	if shift && !control && !alt && event.VirtualKeyCode == vtinput.VK_F4 {
		controller.dialog.Close()
		controller.plugin.editEnvironment(controller.app)
		return true
	}
	if shift && !control && !alt && event.VirtualKeyCode == vtinput.VK_DELETE && selected {
		controller.cut(index, entry)
		return true
	}
	if control && !alt && !shift && event.VirtualKeyCode == vtinput.VK_INSERT && selected {
		controller.copy(entry)
		return true
	}
	if shift && !control && !alt && event.VirtualKeyCode == vtinput.VK_INSERT {
		controller.paste(index, selected)
		return true
	}
	if plain && event.VirtualKeyCode == vtinput.VK_F2 {
		controller.plugin.openConfigDialog(controller.app, false, func(config Config) {
			controller.config = cloneConfig(config)
			controller.refresh(controller.list.SelectPos)
		})
		return true
	}
	if plain && event.Char != 0 {
		if hotkeyIndex := findProfileHotkey(controller.config, event.Char); hotkeyIndex >= 0 {
			controller.list.SetSelectPos(hotkeyIndex)
			return controller.toggle(hotkeyIndex, true, false)
		}
	}
	return false
}

func profileMenuLabel(name string) (string, rune) {
	runes := []rune(name)
	var label strings.Builder
	var hotkey rune
	for index := 0; index < len(runes); index++ {
		if runes[index] != '&' {
			label.WriteRune(runes[index])
			continue
		}
		if index+1 >= len(runes) {
			label.WriteRune('&')
			continue
		}
		index++
		if runes[index] == '&' {
			label.WriteRune('&')
			continue
		}
		if hotkey == 0 {
			hotkey = unicode.ToLower(runes[index])
		}
		label.WriteRune(runes[index])
	}
	return label.String(), hotkey
}

func findProfileHotkey(config Config, pressed rune) int {
	pressed = unicode.ToLower(pressed)
	for index, entry := range config.Entries {
		if entry.Kind != KindProfile {
			continue
		}
		_, hotkey := profileMenuLabel(entry.Name)
		if hotkey != 0 && hotkey == pressed {
			return index
		}
	}
	return -1
}

func (controller *managerController) setEnabled(index int, selected, enabled, advance bool) bool {
	if !selected {
		return true
	}
	if controller.config.Entries[index].Kind != KindProfile {
		if advance {
			controller.refresh(nextProfileIndex(controller.config, index))
		}
		return true
	}
	next, err := setEntryEnabled(controller.config, index, enabled)
	if err != nil {
		controller.showError(err)
		return true
	}
	if advance {
		index = nextProfileIndex(next, index)
	}
	controller.save(next, index)
	return true
}

func (controller *managerController) toggle(index int, selected, advance bool) bool {
	if !selected {
		return true
	}
	if controller.config.Entries[index].Kind != KindProfile {
		if advance {
			controller.refresh(nextProfileIndex(controller.config, index))
		}
		return true
	}
	next, err := toggleEntry(controller.config, index)
	if err != nil {
		controller.showError(err)
		return true
	}
	if advance {
		index = nextProfileIndex(next, index)
	}
	controller.save(next, index)
	return true
}

func (controller *managerController) addProfile(index int, selected bool) {
	insertAt := len(controller.config.Entries)
	if selected {
		insertAt = index
	}
	entry := Entry{
		Kind:    KindProfile,
		Name:    controller.plugin.text("EnvMan.NewProfile", "New profile", "Новый профиль"),
		Enabled: true,
	}
	controller.plugin.openProfileDialog(controller.app, entry, func(saved Entry) error {
		return controller.persist(insertEntry(controller.config, insertAt, saved), insertAt)
	}, nil)
}

func (controller *managerController) delete(index int, entry Entry) {
	name := entry.Name
	if entry.Kind == KindSeparator {
		name = controller.plugin.text("EnvMan.SeparatorName", "group separator", "разделитель группы")
	}
	message := fmt.Sprintf(controller.plugin.text("EnvMan.DeletePrompt", "Delete %q?", "Удалить %q?"), name)
	confirm := vtui.ShowMessageOnEx(
		controller.dialog,
		" "+controller.plugin.text("EnvMan.DeleteTitle", "Delete entry", "Удаление элемента")+" ",
		message,
		[]string{
			controller.plugin.text("EnvMan.Delete", "&Delete", "&Удалить"),
			controller.plugin.text("EnvMan.Cancel", "Cancel", "Отмена"),
		},
		vtui.MessageWarn,
	)
	if confirm == nil {
		return
	}
	confirm.OnResult = func(choice int) {
		if choice != 0 {
			return
		}
		next, _, err := deleteEntry(controller.config, index)
		if err != nil {
			controller.showError(err)
			return
		}
		controller.save(next, index)
	}
}

func (controller *managerController) editWithEditor(index int, entry Entry) {
	controller.plugin.editEntry(controller.app, entry, func(saved Entry) error {
		next, err := replaceEntry(controller.config, index, saved)
		if err != nil {
			return err
		}
		return controller.persist(next, index)
	})
}

func (controller *managerController) copy(entry Entry) bool {
	encoded, err := EncodeEntry(entry)
	if err != nil {
		controller.showError(err)
		return false
	}
	vtui.SetClipboard(encoded)
	return true
}

func (controller *managerController) cut(index int, entry Entry) {
	if !controller.copy(entry) {
		return
	}
	next, _, err := deleteEntry(controller.config, index)
	if err != nil {
		controller.showError(err)
		return
	}
	controller.save(next, index)
}

func (controller *managerController) paste(index int, selected bool) {
	entry, err := DecodeEntry(vtui.GetClipboard(), controller.plugin.options)
	if err != nil {
		controller.showError(fmt.Errorf("%s: %w", controller.plugin.text("EnvMan.ErrorClipboard", "clipboard does not contain an Environment Manager entry", "буфер обмена не содержит элемент Менеджера окружения"), err))
		return
	}
	insertAt := len(controller.config.Entries)
	if selected {
		insertAt = index + 1
	}
	controller.save(insertEntry(controller.config, insertAt, entry), insertAt)
}

func pushEnvManDialog(app vfs.App, dialog vtui.Frame) {
	if vtui.FrameManager == nil || dialog == nil {
		return
	}
	if anchor, ok := app.(vtui.Frame); ok {
		vtui.FrameManager.PushToFrameScreen(anchor, dialog)
		return
	}
	vtui.FrameManager.Push(dialog)
}
