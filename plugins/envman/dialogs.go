package envman

import (
	"errors"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func (plugin *Plugin) openProfileDialog(app vfs.App, initial Entry, onSave func(Entry) error, onCancel func()) *vtui.Window {
	width, height := 76, 22
	if vtui.FrameManager != nil {
		if maximum := vtui.FrameManager.GetScreenSize() - 2; maximum > 32 && width > maximum {
			width = maximum
		}
		if maximum := vtui.FrameManager.GetScreenHeight() - 2; maximum > 14 && height > maximum {
			height = maximum
		}
	}
	dialog := vtui.NewCenteredDialog(width, height, " "+plugin.text("EnvMan.ProfileTitle", "Environment profile", "Профиль окружения")+" ")
	dialog.ShowClose = true
	x := dialog.X1 + 2
	y := dialog.Y1 + 2

	dialog.AddItem(vtui.NewText(x, y, plugin.text("EnvMan.ProfileName", "&Name:", "&Имя:"), 0))
	nameEdit := vtui.NewEdit(x+12, y, width-16, initial.Name)
	nameEdit.SetGrowMode(vtui.GrowHiX)
	dialog.AddItem(nameEdit)
	y += 2

	enabled := vtui.NewCheckbox(x, y, plugin.text("EnvMan.ProfileEnabled", "Profile is &enabled", "Профиль &включён"), false)
	if initial.Enabled {
		enabled.State = 1
	}
	dialog.AddItem(enabled)
	y += 2

	dialog.AddItem(vtui.NewText(x, y, plugin.text("EnvMan.ProfileVariables", "Variables (NAME=value; an empty value unsets):", "Переменные (ИМЯ=значение; пустое значение удаляет):"), 0))
	y++
	variablesHeight := dialog.Y2 - y - 4
	if variablesHeight < 3 {
		variablesHeight = 3
	}
	variables := vtui.NewMultiLineEdit(x, y, width-4, variablesHeight, strings.Join(initial.Variables, "\n"))
	variables.SetGrowMode(vtui.GrowHiX | vtui.GrowHiY)
	dialog.AddItem(variables)

	saveButton := vtui.NewButton(0, 0, plugin.text("EnvMan.Save", "&Save", "&Сохранить"))
	cancelButton := vtui.NewButton(0, 0, plugin.text("EnvMan.Cancel", "Cancel", "Отмена"))
	saveButton.IsDefault = true
	dialog.AddItem(saveButton)
	dialog.AddItem(cancelButton)
	buttons := vtui.NewHBoxLayout(dialog.X1+2, dialog.Y2-2, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(saveButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Apply()
	saveButton.SetGrowMode(vtui.GrowAll)
	cancelButton.SetGrowMode(vtui.GrowAll)

	cancelled := true
	saveButton.OnClick = func() {
		next := Entry{
			Kind:      KindProfile,
			Name:      strings.TrimSpace(nameEdit.GetText()),
			Enabled:   enabled.State == 1,
			Variables: variables.GetLines(),
		}
		if err := (Config{Version: CurrentConfigVersion, Entries: []Entry{next}}).Validate(plugin.options); err != nil {
			vtui.ShowMessageOn(
				dialog,
				" "+plugin.text("EnvMan.ProfileErrorTitle", "Profile error", "Ошибка профиля")+" ",
				err.Error(),
				[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
			)
			return
		}
		cancelled = false
		if onSave != nil {
			if err := onSave(next); err != nil {
				cancelled = true
				vtui.ShowMessageOn(
					dialog,
					" "+plugin.text("EnvMan.ProfileErrorTitle", "Profile error", "Ошибка профиля")+" ",
					err.Error(),
					[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
				)
				return
			}
		}
		dialog.Close()
	}
	cancelButton.OnClick = dialog.Close
	dialog.OnResult = func(int) {
		if cancelled && onCancel != nil {
			onCancel()
		}
	}
	pushEnvManDialog(app, dialog)
	return dialog
}

func (plugin *Plugin) configure(app vfs.App) {
	if err := plugin.reloadConfig(); err != nil {
		plugin.showError(app, err)
	}
	plugin.openConfigDialog(app, true)
}

func (plugin *Plugin) openConfigDialog(app vfs.App, apply bool, onSaved ...func(Config)) *vtui.Window {
	config := plugin.snapshotConfig()
	width, height := 76, 15
	if vtui.FrameManager != nil {
		if maximum := vtui.FrameManager.GetScreenSize() - 2; maximum > 32 && width > maximum {
			width = maximum
		}
		if maximum := vtui.FrameManager.GetScreenHeight() - 2; maximum > 11 && height > maximum {
			height = maximum
		}
	}
	dialog := vtui.NewCenteredDialog(width, height, " "+plugin.text("EnvMan.SettingsTitle", "Environment Manager settings", "Настройки Менеджера окружения")+" ")
	dialog.ShowClose = true
	x := dialog.X1 + 2
	y := dialog.Y1 + 2

	dialog.AddItem(vtui.NewText(x, y, plugin.text("EnvMan.IgnoredVariables", "Ignored variables (comma-separated):", "Игнорируемые переменные (через запятую):"), 0))
	y++
	ignored := vtui.NewEdit(x, y, width-4, strings.Join(config.IgnoredVariables, ", "))
	ignored.SetGrowMode(vtui.GrowHiX)
	dialog.AddItem(ignored)
	y += 2

	alwaysEditor := vtui.NewCheckbox(x, y, plugin.text("EnvMan.AlwaysEditor", "Always edit profiles in the f4 &editor", "Всегда редактировать профили в &редакторе f4"), false)
	if config.AlwaysUseEditor {
		alwaysEditor.State = 1
	}
	dialog.AddItem(alwaysEditor)
	y += 2

	dialog.AddItem(vtui.NewText(x, y, plugin.text("EnvMan.PrefixInfo", "Command prefix: envman", "Префикс команд: envman"), 0))

	saveButton := vtui.NewButton(0, 0, plugin.text("EnvMan.Save", "&Save", "&Сохранить"))
	cancelButton := vtui.NewButton(0, 0, plugin.text("EnvMan.Cancel", "Cancel", "Отмена"))
	saveButton.IsDefault = true
	var importFar3Button *vtui.Button
	if far3ImportSupported() {
		importFar3Button = vtui.NewButton(0, 0, plugin.text("EnvMan.ImportFar3", "&Import from Far Manager 3...", "&Импорт из Far Manager 3..."))
		dialog.AddItem(importFar3Button)
	}
	dialog.AddItem(saveButton)
	dialog.AddItem(cancelButton)
	buttons := vtui.NewHBoxLayout(dialog.X1+2, dialog.Y2-2, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	if importFar3Button != nil {
		buttons.Add(importFar3Button, vtui.Margins{}, vtui.AlignTop)
	}
	buttons.Add(saveButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Apply()
	saveButton.SetGrowMode(vtui.GrowAll)
	cancelButton.SetGrowMode(vtui.GrowAll)
	if importFar3Button != nil {
		importFar3Button.SetGrowMode(vtui.GrowAll)
		importFar3Button.OnClick = func() {
			plugin.importFar3FromConfigDialog(dialog, ignored, alwaysEditor, apply, onSaved)
		}
	}

	saveButton.OnClick = func() {
		names := splitIgnoredVariables(ignored.GetText(), plugin.options)
		err := plugin.mutateConfig(func(current Config) (Config, error) {
			next := cloneConfig(current)
			next.IgnoredVariables = names
			next.AlwaysUseEditor = alwaysEditor.State == 1
			return next, nil
		}, apply)
		if err != nil {
			vtui.ShowMessageOn(
				dialog,
				" "+plugin.text("EnvMan.SettingsErrorTitle", "Settings error", "Ошибка настроек")+" ",
				err.Error(),
				[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
			)
			return
		}
		latest := plugin.snapshotConfig()
		for _, callback := range onSaved {
			if callback != nil {
				callback(latest)
			}
		}
		dialog.Close()
	}
	cancelButton.OnClick = dialog.Close
	pushEnvManDialog(app, dialog)
	return dialog
}

func splitIgnoredVariables(value string, options EngineOptions) []string {
	parts := strings.FieldsFunc(value, func(char rune) bool {
		return char == ',' || char == ';' || char == '\n' || char == '\r' || char == '\t' || char == ' '
	})
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		key := normalizeName(name, options)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	return result
}

func (plugin *Plugin) editEntry(app vfs.App, entry Entry, onSave func(Entry) error) {
	encoded, err := EncodeEntry(entry)
	if err != nil {
		plugin.showError(app, err)
		return
	}
	plugin.openEntryEditorContent(app, []byte(encoded), 0, false, onSave)
}

func (plugin *Plugin) openEntryEditorContent(app vfs.App, content []byte, cursorLine int, modified bool, onSave func(Entry) error) {
	host, ok := app.(vfs.TextEditorHost)
	if !ok {
		plugin.showError(app, errors.New(plugin.text("EnvMan.ErrorNoEditor", "this f4 view does not expose the text editor", "это окно f4 не предоставляет текстовый редактор")))
		return
	}
	err := host.OpenTextEditor(vfs.TextEditorRequest{
		DisplayTitle: plugin.text("EnvMan.EntryEditorTitle", "Environment profile", "Профиль окружения"),
		Content:      append([]byte(nil), content...),
		Modified:     modified,
		CursorLine:   cursorLine,
		Temporary:    true,
		OnClose: func(content []byte, closeErr error) {
			if closeErr != nil {
				plugin.showError(app, closeErr)
				return
			}
			decoded, decodeErr := DecodeEntry(string(content), plugin.options)
			if decodeErr != nil {
				plugin.offerEditorRecovery(app, decodeErr, func() {
					plugin.openEntryEditorContent(app, content, editorErrorLine(decodeErr), true, onSave)
				})
				return
			}
			if onSave != nil {
				if saveErr := onSave(decoded); saveErr != nil {
					plugin.offerEditorRecovery(app, saveErr, func() {
						plugin.openEntryEditorContent(app, content, 0, true, onSave)
					})
				}
			}
		},
	})
	if err != nil {
		plugin.showError(app, err)
	}
}

func (plugin *Plugin) viewEnvironment(app vfs.App) {
	plugin.openEnvironmentEditor(app, false)
}

func (plugin *Plugin) editEnvironment(app vfs.App) {
	plugin.openEnvironmentEditor(app, true)
}

func (plugin *Plugin) openEnvironmentEditor(app vfs.App, apply bool) {
	plugin.mu.Lock()
	environmentHost := plugin.envHost
	plugin.mu.Unlock()
	if environmentHost == nil {
		plugin.showError(app, plugin.notInitializedError())
		return
	}
	encoded, err := EncodeEnvironment(snapshotLines(environmentHost.SnapshotProcessEnvironment()), plugin.options)
	if err != nil {
		plugin.showError(app, err)
		return
	}
	plugin.openEnvironmentEditorContent(app, apply, []byte(encoded), 0, false)
}

func (plugin *Plugin) openEnvironmentEditorContent(app vfs.App, apply bool, content []byte, cursorLine int, modified bool) {
	host, ok := app.(vfs.TextEditorHost)
	if !ok {
		plugin.showError(app, errors.New(plugin.text("EnvMan.ErrorNoEditor", "this f4 view does not expose the text editor", "это окно f4 не предоставляет текстовый редактор")))
		return
	}
	title := plugin.text("EnvMan.EnvironmentViewTitle", "Process environment", "Окружение процесса")
	if apply {
		title = plugin.text("EnvMan.EnvironmentEditorTitle", "Edit process environment", "Редактирование окружения процесса")
	}
	err := host.OpenTextEditor(vfs.TextEditorRequest{
		DisplayTitle: title,
		Content:      append([]byte(nil), content...),
		Modified:     modified,
		CursorLine:   cursorLine,
		Temporary:    true,
		OnClose: func(content []byte, closeErr error) {
			if closeErr != nil {
				plugin.showError(app, closeErr)
				return
			}
			if !apply {
				return
			}
			environment, explicit, decodeErr := plugin.decodeEnvironmentDocument(string(content))
			if decodeErr != nil {
				plugin.offerEditorRecovery(app, decodeErr, func() {
					plugin.openEnvironmentEditorContent(app, apply, content, editorErrorLine(decodeErr), true)
				})
				return
			}
			if applyErr := plugin.applyEditedEnvironment(environment, explicit); applyErr != nil {
				plugin.offerEditorRecovery(app, applyErr, func() {
					plugin.openEnvironmentEditorContent(app, apply, content, 0, true)
				})
				return
			}
			app.RefreshAll()
		},
	})
	if err != nil {
		plugin.showError(app, err)
	}
}

func editorErrorLine(err error) int {
	var lineError *LineError
	if errors.As(err, &lineError) && lineError.Line > 0 {
		return lineError.Line - 1
	}
	return 0
}

func (plugin *Plugin) offerEditorRecovery(app vfs.App, err error, reopen func()) *vtui.Window {
	if app == nil || err == nil {
		return nil
	}
	dialog := showEnvManMessage(
		app,
		plugin.text("EnvMan.EditorErrorTitle", "Environment text error", "Ошибка текста окружения"),
		err.Error(),
		[]string{
			plugin.text("EnvMan.EditorReopen", "&Reopen", "&Открыть снова"),
			plugin.text("EnvMan.EditorDiscard", "&Discard", "&Отбросить"),
		},
		vtui.MessageWarn,
	)
	if dialog != nil {
		dialog.OnResult = func(choice int) {
			if choice == 0 && reopen != nil {
				reopen()
			}
		}
	}
	return dialog
}
