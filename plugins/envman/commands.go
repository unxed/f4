package envman

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func (plugin *Plugin) handlePrefix(app vfs.App, raw string) {
	operation := strings.TrimSpace(raw)
	if operation == "" {
		plugin.openManager(app, true)
		return
	}
	if strings.EqualFold(operation, "e") {
		plugin.editEnvironment(app)
		return
	}
	if operation[0] == '+' || operation[0] == '-' || operation[0] == '*' {
		if err := plugin.setProfileState(operation[0], strings.TrimSpace(operation[1:])); err != nil {
			plugin.showError(app, err)
		}
		return
	}
	if operation[0] == '<' || operation[0] == '>' {
		plugin.runFileCommand(app, operation[0], strings.TrimSpace(operation[1:]))
		return
	}
	plugin.showError(app, fmt.Errorf("%s: %q", plugin.text("EnvMan.ErrorUnknownCommand", "unknown Environment Manager command", "неизвестная команда Менеджера окружения"), operation))
}

func (plugin *Plugin) setProfileState(operation byte, name string) error {
	if name == "" {
		return errors.New(plugin.text("EnvMan.ErrorProfileNameEmpty", "profile name is empty", "имя профиля не задано"))
	}
	found := false
	return plugin.mutateConfig(func(config Config) (Config, error) {
		next := cloneConfig(config)
		for index := range next.Entries {
			entry := &next.Entries[index]
			if entry.Kind != KindProfile || entry.Name != name {
				continue
			}
			found = true
			switch operation {
			case '+':
				entry.Enabled = true
			case '-':
				entry.Enabled = false
			case '*':
				entry.Enabled = !entry.Enabled
			default:
				return config, fmt.Errorf("unsupported profile operation %q", operation)
			}
			break
		}
		if !found {
			return config, fmt.Errorf("%s: %s", plugin.text("EnvMan.ErrorProfileNotFound", "profile not found", "профиль не найден"), name)
		}
		return next, nil
	}, true)
}

func (plugin *Plugin) runFileCommand(app vfs.App, operation byte, rawPath string) {
	filesystem := app.GetActivePanelVFS()
	path, err := resolveVFSPath(filesystem, filesystemPath(filesystem), rawPath)
	if err != nil {
		plugin.showError(app, fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorPath", "invalid environment file path", "недопустимый путь к файлу окружения"), err))
		return
	}
	title := plugin.text("EnvMan.ProgressTitle", "Environment Manager", "Менеджер окружения")
	start := plugin.text("EnvMan.ProgressFile", "Processing environment file...", "Обработка файла окружения...")
	app.RunProgressTask(title, start, false, func(ctx context.Context, _ func(string, int)) error {
		return plugin.performFileCommand(ctx, filesystem, path, operation)
	}, func(taskErr error) {
		if taskErr != nil {
			plugin.showError(app, taskErr)
			return
		}
		if operation == '>' {
			app.RefreshAll()
		}
	})
}

func (plugin *Plugin) performFileCommand(ctx context.Context, filesystem vfs.VFS, path string, operation byte) error {
	switch operation {
	case '<':
		data, err := readVFSFile(ctx, filesystem, path, maxEnvironmentFileSize)
		if err != nil {
			return fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorImport", "could not import environment", "не удалось импортировать окружение"), err)
		}
		text, err := decodeEnvironmentBytes(data)
		if err != nil {
			return fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorImport", "could not import environment", "не удалось импортировать окружение"), err)
		}
		environment, explicit, err := plugin.decodeEnvironmentDocument(text)
		if err != nil {
			return fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorImport", "could not import environment", "не удалось импортировать окружение"), err)
		}
		return plugin.applyEditedEnvironment(environment, explicit)
	case '>':
		plugin.mu.Lock()
		environmentHost := plugin.envHost
		plugin.mu.Unlock()
		if environmentHost == nil {
			return plugin.notInitializedError()
		}
		lines := plugin.exportableEnvironment(snapshotLines(environmentHost.SnapshotProcessEnvironment()))
		encoded, err := EncodeEnvironment(lines, plugin.options)
		if err != nil {
			return fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorExport", "could not export environment", "не удалось экспортировать окружение"), err)
		}
		data := []byte(encoded)
		if err := writeVFSFile(ctx, filesystem, path, data); err != nil {
			return fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorExport", "could not export environment", "не удалось экспортировать окружение"), err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported file operation %q", operation)
	}
}

func (plugin *Plugin) applyEditedEnvironment(environment []string, explicit map[string]struct{}) error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	plugin.mu.Lock()
	environmentHost := plugin.envHost
	options := cloneOptions(plugin.options)
	plugin.mu.Unlock()
	if environmentHost == nil {
		return plugin.notInitializedError()
	}
	current := environmentHost.SnapshotProcessEnvironment()
	target, err := plugin.editedEnvironmentTarget(snapshotLines(current), environment, explicit)
	if err != nil {
		return err
	}
	diff, err := DiffEnvironment(snapshotLines(current), target, options)
	if err != nil {
		return err
	}
	diff = editableDiff(diff, options)
	if diff.Empty() {
		return nil
	}
	_, err = environmentHost.ApplyProcessEnvironment(environmentChanges(diff))
	if err != nil {
		return err
	}
	return nil
}

func editableDiff(diff Diff, options EngineOptions) Diff {
	filter := func(changes []Change) []Change {
		result := make([]Change, 0, len(changes))
		for _, change := range changes {
			if !isReservedName(change.Name, options) && validateVariableName(change.Name) == nil {
				result = append(result, change)
			}
		}
		return result
	}
	return Diff{Added: filter(diff.Added), Changed: filter(diff.Changed), Removed: filter(diff.Removed)}
}

func (plugin *Plugin) callNonInteractive(ctx context.Context, filesystem vfs.VFS, base string, arguments []any) ([]any, error) {
	if len(arguments) != 1 {
		return nil, errors.New(plugin.text("EnvMan.ErrorMacroArguments", "Environment Manager macro call expects one string operation", "Макрокоманда Менеджера окружения ожидает одну строковую операцию"))
	}
	operation, ok := arguments[0].(string)
	if !ok {
		return nil, errors.New(plugin.text("EnvMan.ErrorMacroType", "Environment Manager macro operation must be a string", "Операция макрокоманды Менеджера окружения должна быть строкой"))
	}
	operation = strings.TrimSpace(operation)
	if operation == "" || strings.EqualFold(operation, "e") {
		return nil, fmt.Errorf("%s: %q", plugin.text("EnvMan.ErrorMacroInteractive", "macro operation requires the interactive Environment Manager UI", "операция макрокоманды требует интерактивного интерфейса Менеджера окружения"), operation)
	}
	switch operation[0] {
	case '+', '-', '*':
		if err := plugin.setProfileState(operation[0], strings.TrimSpace(operation[1:])); err != nil {
			return nil, err
		}
	case '<', '>':
		path, err := resolveVFSPath(filesystem, base, strings.TrimSpace(operation[1:]))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", plugin.text("EnvMan.ErrorPath", "invalid environment file path", "недопустимый путь к файлу окружения"), err)
		}
		if err := plugin.performFileCommand(ctx, filesystem, path, operation[0]); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%s: %q", plugin.text("EnvMan.ErrorMacroUnknown", "unknown Environment Manager macro operation", "неизвестная макрооперация Менеджера окружения"), operation)
	}
	return []any{true}, nil
}

func filesystemPath(filesystem vfs.VFS) string {
	if filesystem == nil {
		return ""
	}
	return filesystem.GetPath()
}

func (plugin *Plugin) showError(app vfs.App, err error) {
	if app == nil || err == nil {
		return
	}
	showEnvManMessage(
		app,
		plugin.text("EnvMan.ErrorTitle", "Environment Manager error", "Ошибка Менеджера окружения"),
		err.Error(),
		[]string{plugin.text("EnvMan.OK", "&OK", "&ОК")},
		vtui.MessageWarn,
	)
}
