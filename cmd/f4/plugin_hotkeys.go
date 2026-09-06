package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// Plugin menu entries are actions too, but their lifetime is controlled by a
// plugin registration rather than by the built-in action registry. Keeping a
// separate namespace lets hotkeys.ini refer to them without leaving stale
// Action values behind when an RPC plugin disconnects.
func pluginCommandActionName(id string) string { return "Plugin.Command." + id }

func legacyPluginActionName(index int) string {
	return "Plugin.Legacy." + strconv.Itoa(index)
}

func isPluginActionName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "plugin.command.") || strings.HasPrefix(name, "plugin.legacy.")
}

func pluginActionForName(name string) (Action, bool) {
	rawName := strings.TrimSpace(name)
	lowerName := strings.ToLower(rawName)
	switch {
	case strings.HasPrefix(lowerName, "plugin.command."):
		id := strings.TrimSpace(rawName[len("Plugin.Command."):])
		pluginCommandRegistry.RLock()
		registered, ok := pluginCommandRegistry.byID[strings.ToLower(id)]
		if ok {
			registered.command = clonePluginCommand(registered.command)
		}
		pluginCommandRegistry.RUnlock()
		if !ok {
			return Action{}, false
		}
		command := registered.command
		actionName := pluginCommandActionName(command.ID)
		return Action{
			Name:        actionName,
			Area:        "Shell",
			Label:       pluginCommandDisplayLabel(command),
			Description: pluginCommandDisplayDescription(command),
			Handler:     func() bool { return runPluginHotkeyAction(actionName) },
		}, true
	case strings.HasPrefix(lowerName, "plugin.legacy."):
		index, err := strconv.Atoi(strings.TrimSpace(rawName[len("Plugin.Legacy."):]))
		if err != nil || index < 0 {
			return Action{}, false
		}
		items := pluginMenuItemsSnapshot()
		if index >= len(items) {
			return Action{}, false
		}
		item := items[index]
		actionName := item.ActionName
		if actionName == "" {
			actionName = legacyPluginActionName(index)
		}
		return Action{
			Name:        actionName,
			Area:        "Shell",
			Label:       item.Label,
			Description: "Run the selected plugin command",
			Handler:     func() bool { return runPluginHotkeyAction(actionName) },
		}, true
	default:
		return Action{}, false
	}
}

func runPluginHotkeyAction(name string) bool {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(strings.ToLower(name), "plugin.command.") {
		id := strings.TrimSpace(name[len("Plugin.Command."):])
		pluginCommandRegistry.RLock()
		registered, ok := pluginCommandRegistry.byID[strings.ToLower(id)]
		if ok {
			registered.command = clonePluginCommand(registered.command)
		}
		pluginCommandRegistry.RUnlock()
		if !ok {
			return false
		}
		pf := findPanelsFrame()
		if pf == nil {
			return false
		}
		return executeRegisteredPluginCommand(registered.command.Location, registered.command.ID, pf)
	}

	if strings.HasPrefix(strings.ToLower(name), "plugin.legacy.") {
		index, err := strconv.Atoi(strings.TrimSpace(name[len("Plugin.Legacy."):]))
		if err != nil || index < 0 {
			return false
		}
		items := pluginMenuItemsSnapshot()
		if index >= len(items) || items[index].Handler == nil {
			return false
		}
		if pf := findPanelsFrame(); pf != nil {
			items[index].Handler(pf)
			return true
		}
	}
	return false
}

func pluginActionShortcut(name string) string {
	if GlobalHotkeysMgr == nil {
		return ""
	}
	if key := GlobalHotkeysMgr.GetKeyForAction("Shell", name); key != "" {
		return FormatKeyForUI(key)
	}
	return ""
}

func pluginCommandShortcut(command vfs.PluginCommand) string {
	if shortcut := pluginActionShortcut(pluginCommandActionName(command.ID)); shortcut != "" {
		return shortcut
	}
	return command.Shortcut
}

func pluginActionConfiguredBinding(name string) (string, string) {
	if GlobalHotkeysMgr == nil {
		return "", ""
	}
	for _, area := range []string{"Shell", "Common"} {
		var keys []string
		for key, binding := range GlobalHotkeysMgr.Bindings[area] {
			namePart := strings.SplitN(binding, ":", 2)[0]
			if strings.EqualFold(namePart, name) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) != 0 {
			return area, keys[0]
		}
	}
	return "", ""
}

func pluginActionConfiguredKey(name string) string {
	_, key := pluginActionConfiguredBinding(name)
	return key
}

func pluginActionDefaultShortcut(name string) string {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(lowerName, "plugin.command.") {
		return ""
	}
	id := strings.TrimSpace(name[len("Plugin.Command."):])
	pluginCommandRegistry.RLock()
	registered, ok := pluginCommandRegistry.byID[strings.ToLower(id)]
	if ok {
		shortcut := registered.command.Shortcut
		pluginCommandRegistry.RUnlock()
		return shortcut
	}
	pluginCommandRegistry.RUnlock()
	return ""
}

func pluginMenuItemShortcut(item PluginMenuItem) string {
	return pluginActionShortcut(item.ActionName)
}

func assignPluginHotkey(menu *vtui.VMenu, index int, actionName string) {
	hm := GlobalHotkeysMgr
	if hm == nil || vtui.FrameManager == nil || !isPluginActionName(actionName) {
		return
	}
	oldArea, oldKey := pluginActionConfiguredBinding(actionName)
	frame := NewHotkeyAssignFrame(hm, actionName, "Shell", func() {
		newArea, newKey := pluginActionConfiguredBinding(actionName)
		if oldArea != "" && (oldArea != newArea || oldKey != newKey) {
			hm.Unbind(oldArea, oldKey)
		}
		hm.Save()
		if menu != nil && index >= 0 && index < len(menu.Items) {
			menu.Items[index].Shortcut = pluginActionShortcut(actionName)
		}
		vtui.FrameManager.Redraw()
	})
	vtui.FrameManager.Push(frame)
}

// pluginHotkeyActionsSnapshot includes commands that are currently hidden from
// the F11 menu as well. A user can therefore assign a shortcut once and keep
// it when moving to another drive or when a plugin changes its visibility.
func pluginHotkeyActionsSnapshot() []Action {
	pluginCommandRegistry.RLock()
	commandIDs := append([]string(nil), pluginCommandRegistry.order...)
	pluginCommandRegistry.RUnlock()

	actions := make([]Action, 0, len(commandIDs)+len(pluginMenuItemsSnapshot()))
	for _, id := range commandIDs {
		pluginCommandRegistry.RLock()
		registered, ok := pluginCommandRegistry.byID[id]
		pluginCommandRegistry.RUnlock()
		if !ok {
			continue
		}
		if action, ok := pluginActionForName(pluginCommandActionName(registered.command.ID)); ok {
			actions = append(actions, action)
		}
	}
	for index, item := range pluginMenuItemsSnapshot() {
		name := item.ActionName
		if name == "" {
			name = legacyPluginActionName(index)
		}
		if action, ok := pluginActionForName(name); ok {
			actions = append(actions, action)
		}
	}
	return actions
}
