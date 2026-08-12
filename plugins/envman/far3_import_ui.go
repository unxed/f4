package envman

import (
	"fmt"
	"strings"

	"github.com/unxed/vtui"
)

func (plugin *Plugin) importFar3FromConfigDialog(
	dialog *vtui.Window,
	ignored *vtui.Edit,
	alwaysEditor *vtui.Checkbox,
	apply bool,
	onSaved []func(Config),
) {
	candidates, err := far3ImportLoader()
	if err != nil {
		plugin.showFar3ImportError(dialog, err)
		return
	}
	if len(candidates) == 0 {
		plugin.showFar3ImportError(dialog, fmt.Errorf("%s", plugin.text(
			"EnvMan.Far3NotFound",
			"Far Manager 3 Environment Manager settings were not found",
			"Настройки Менеджера окружения Far Manager 3 не найдены",
		)))
		return
	}

	choose := func(candidate far3ImportCandidate) {
		plugin.confirmFar3Import(dialog, ignored, alwaysEditor, apply, onSaved, candidate)
	}
	if len(candidates) == 1 {
		choose(candidates[0])
		return
	}

	buttons := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		buttons = append(buttons, "&"+plugin.far3SourceText(candidate.Source))
	}
	buttons = append(buttons, plugin.text("EnvMan.Cancel", "Cancel", "Отмена"))
	chooser := vtui.ShowMessageOnEx(
		dialog,
		" "+plugin.text("EnvMan.Far3Title", "Import from Far Manager 3", "Импорт из Far Manager 3")+" ",
		plugin.text(
			"EnvMan.Far3ChooseSource",
			"Environment Manager settings exist in both registry views. Choose the Far Manager installation to import.",
			"Настройки Менеджера окружения найдены в обеих ветках реестра. Выберите установку Far Manager для импорта.",
		),
		buttons,
		vtui.MessageInfo,
	)
	if chooser != nil {
		chooser.OnResult = func(choice int) {
			if choice >= 0 && choice < len(candidates) {
				choose(candidates[choice])
			}
		}
	}
}

func (plugin *Plugin) confirmFar3Import(
	dialog *vtui.Window,
	ignored *vtui.Edit,
	alwaysEditor *vtui.Checkbox,
	apply bool,
	onSaved []func(Config),
	candidate far3ImportCandidate,
) {
	if err := validateFar3ImportCandidate(candidate, plugin.options); err != nil {
		plugin.showFar3ImportError(dialog, err)
		return
	}
	prompt := vtui.ShowMessageOnEx(
		dialog,
		" "+plugin.text("EnvMan.Far3Title", "Import from Far Manager 3", "Импорт из Far Manager 3")+" ",
		fmt.Sprintf(
			plugin.text(
				"EnvMan.Far3Prompt",
				"Found %d entries in the %s. Append them to the current profiles or replace all profiles? Ignored variables and the editor preference will also be imported.",
				"Найдено записей: %d в ветке «%s». Добавить их к текущим профилям или заменить все профили? Исключения и настройка редактора также будут импортированы.",
			),
			len(candidate.Entries),
			plugin.far3SourceText(candidate.Source),
		),
		[]string{
			plugin.text("EnvMan.Far3Append", "&Append", "&Добавить"),
			plugin.text("EnvMan.Far3Replace", "&Replace", "&Заменить"),
			plugin.text("EnvMan.Cancel", "Cancel", "Отмена"),
		},
		vtui.MessageInfo,
	)
	if prompt == nil {
		return
	}
	prompt.OnResult = func(choice int) {
		if choice != 0 && choice != 1 {
			return
		}
		replace := choice == 1
		err := plugin.mutateConfig(func(current Config) (Config, error) {
			// Preserve edits already made in this open settings dialog before
			// applying the selected Far import mode.
			current.IgnoredVariables = splitIgnoredVariables(ignored.GetText(), plugin.options)
			current.AlwaysUseEditor = alwaysEditor.State == 1
			return mergeFar3Import(current, candidate, replace, plugin.options), nil
		}, apply)
		if err != nil {
			plugin.showFar3ImportError(dialog, err)
			return
		}
		latest := plugin.snapshotConfig()
		ignored.SetText(strings.Join(latest.IgnoredVariables, ", "))
		if latest.AlwaysUseEditor {
			alwaysEditor.State = 1
		} else {
			alwaysEditor.State = 0
		}
		for _, callback := range onSaved {
			if callback != nil {
				callback(latest)
			}
		}
		vtui.ShowMessageOnEx(
			dialog,
			" "+plugin.text("EnvMan.Far3Title", "Import from Far Manager 3", "Импорт из Far Manager 3")+" ",
			fmt.Sprintf(
				plugin.text("EnvMan.Far3Success", "Imported %d entries from Far Manager 3.", "Импортировано записей из Far Manager 3: %d."),
				len(candidate.Entries),
			),
			[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
			vtui.MessageInfo,
		)
	}
}

func (plugin *Plugin) showFar3ImportError(dialog *vtui.Window, err error) {
	if dialog == nil || err == nil {
		return
	}
	vtui.ShowMessageOnEx(
		dialog,
		" "+plugin.text("EnvMan.Far3ErrorTitle", "Far Manager 3 import error", "Ошибка импорта из Far Manager 3")+" ",
		err.Error(),
		[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
		vtui.MessageWarn,
	)
}

func (plugin *Plugin) far3SourceText(source string) string {
	switch source {
	case far3SourceRegistry64:
		return plugin.text("EnvMan.Far3Source64", "64-bit registry", "64-битная ветка реестра")
	case far3SourceRegistry32:
		return plugin.text("EnvMan.Far3Source32", "32-bit registry", "32-битная ветка реестра")
	default:
		return plugin.text("EnvMan.Far3SourceRegistry", "Windows registry", "реестр Windows")
	}
}
