package panel

import (
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/vfs"
)

// PluginMenuVisibilityFilePath is the file that lists the F11 menu entries the
// user has hidden, one action name per line (f4#918). It sits beside the
// drive-tool visibility file and has the same format.
func PluginMenuVisibilityFilePath() string {
	if config.IsPortableProfile() {
		return filepath.Join(config.GetF4ConfigDir(), "settings", "plugin-menu-visibility.txt")
	}
	configDir, _ := config.UserConfigDir()
	return filepath.Join(configDir, "f4", "settings", "plugin-menu-visibility.txt")
}

// hiddenPluginMenuEntries reads the hidden action names. A name whose plugin
// is not loaded stays in the file, so the entry is still hidden when the
// plugin comes back.
func hiddenPluginMenuEntries() map[string]bool {
	names, _ := LoadDisabledDriveTools(PluginMenuVisibilityFilePath())
	hidden := make(map[string]bool, len(names))
	for _, name := range names {
		hidden[name] = true
	}
	return hidden
}

// PluginMenuEntriesSnapshot is every entry the F11 menu could show, hidden or
// not, in menu order. Settings lists these to let the user choose.
func PluginMenuEntriesSnapshot() []PluginMenuEntry {
	var pf *PanelsFrame
	if frame := FindPanelsFrameAnyScreen(); frame != nil {
		pf = frame
	}
	items := plughost.PluginMenuItemsSnapshot()
	var commands []vfs.PluginCommand
	if pf != nil {
		commands = commandsForPluginMenu(plughost.PluginCommandsSnapshot(vfs.PluginCommandPanel, pf))
	}
	return buildPluginMenuEntries(items, commands)
}

// SavePluginMenuHidden stores the hidden action names.
func SavePluginMenuHidden(names []string) error {
	return SaveDisabledDriveTools(PluginMenuVisibilityFilePath(), names)
}

// LoadPluginMenuHidden is the stored list of hidden action names, in order.
func LoadPluginMenuHidden() ([]string, error) {
	return LoadDisabledDriveTools(PluginMenuVisibilityFilePath())
}

// SetPluginMenuEntryHidden hides or shows one entry and stores the list at
// once. The other names in the file, those of plugins not loaded now included,
// are kept.
func SetPluginMenuEntryHidden(actionName string, hidden bool) error {
	names, err := LoadPluginMenuHidden()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(names)+1)
	present := false
	for _, name := range names {
		if name == actionName {
			present = true
			if !hidden {
				continue
			}
		}
		out = append(out, name)
	}
	if hidden && !present {
		out = append(out, actionName)
	}
	return SavePluginMenuHidden(out)
}

// pluginConfigCommandFor finds the command that configures the tool whose menu
// command has the given ID. A plugin names the two as a pair in one namespace,
// "<namespace>.open" and "<namespace>.configure" (f4.envman.open and
// f4.envman.configure, visren.open and visren.configure, and so on), and the
// configuration one is registered at the PluginCommandConfig location.
func pluginConfigCommandFor(commandID string, app vfs.App) (vfs.PluginCommand, bool) {
	dot := strings.LastIndex(commandID, ".")
	if dot <= 0 {
		return vfs.PluginCommand{}, false
	}
	want := strings.ToLower(commandID[:dot] + ".configure")
	for _, command := range plughost.PluginCommandsSnapshot(vfs.PluginCommandConfig, app) {
		if strings.ToLower(command.ID) == want {
			return command, true
		}
	}
	return vfs.PluginCommand{}, false
}
