package main

import (
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// BuildMenuBarItems generates the top-level menu structure for an area
// from the action registry. Every action with a MenuPath set appears in
// the corresponding top-level menu; items inside a menu follow
// registration order, with Common-area actions appended after the
// area's own, except that any action with MenuLast set is pinned after
// every other item in its group instead. The shortcut column reflects
// the *active* bindings, including user overrides from hotkeys.ini.
func BuildMenuBarItems(area string) []vtui.MenuBarItem {
	type menu struct {
		title  string
		items  []vtui.MenuItem
		pinned []vtui.MenuItem
	}
	var order []string
	menus := make(map[string]*menu)

	appendAction := func(a Action) {
		// Asked before the menu is created, so a group whose every action
		// is hidden does not appear as an empty one.
		if a.Visible != nil && !a.Visible() {
			return
		}
		m := menus[a.MenuPath]
		if m == nil {
			title := Msg("Menu." + area + "." + a.MenuPath)
			if strings.HasPrefix(title, "{") {
				title = a.MenuPath
			}
			m = &menu{title: title}
			menus[a.MenuPath] = m
			order = append(order, a.MenuPath)
		}
		text := a.DisplayLabel()
		if !strings.Contains(text, "&") {
			text = "&" + text // first letter becomes the menu hotkey
		}
		if a.Checked != nil && a.Checked() {
			text = "√ " + text
		}
		item := vtui.MenuItem{
			Text:     text,
			OnClick:  func() { RunAction(a.Name) },
			UserData: menuHistoryItemKey(a.Name),
		}
		item.Shortcut = MenuShortcutsForAction(area, a.Name)
		if a.MenuLast {
			if a.MenuSeparatorBefore {
				m.pinned = append(m.pinned, vtui.MenuItem{Separator: true})
			}
			m.pinned = append(m.pinned, item)
			return
		}
		if a.MenuSeparatorBefore {
			m.items = append(m.items, vtui.MenuItem{Separator: true})
		}
		m.items = append(m.items, item)
	}

	appendPluginCommand := func(command vfs.PluginCommand) {
		m := menus[command.MenuPath]
		if m == nil {
			title := Msg("Menu." + area + "." + command.MenuPath)
			if strings.HasPrefix(title, "{") {
				title = command.MenuPath
			}
			m = &menu{title: title}
			menus[command.MenuPath] = m
			order = append(order, command.MenuPath)
		}
		text := plainLabel(pluginCommandDisplayLabel(command))
		if !strings.Contains(text, "&") {
			text = "&" + text
		}
		m.items = append(m.items, vtui.MenuItem{
			Text:     text,
			Shortcut: pluginCommandShortcut(command),
			UserData: menuHistoryItemKey("plugin:" + command.ID),
			OnClick: func() {
				if pf := findPanelsFrameAnyScreen(); pf != nil {
					executeRegisteredPluginCommand(vfs.PluginCommandPanel, command.ID, pf)
				}
			},
		})
	}

	// The area's own actions first (stable registry order).
	for _, a := range GetOrderedActions() {
		if a.MenuPath != "" && !a.HideFromMenu && a.Area == area {
			appendAction(a)
		}
	}
	// Common actions join only menu groups that already exist in the
	// area, so they cannot create stray top-level menus.
	for _, a := range GetOrderedActions() {
		if a.MenuPath != "" && !a.HideFromMenu && a.Area == "Common" && menus[a.MenuPath] != nil {
			appendAction(a)
		}
	}
	// Panel plugins can opt into an existing generated Shell menu. Keep
	// plugin commands after core actions so the built-in menu remains stable,
	// while still exposing plugin functionality without requiring F11.
	if area == "Shell" {
		// NewPanelsFrame builds its first menu before it is attached to a
		// frame manager. Do not pass a typed-nil *PanelsFrame as vfs.App to
		// plugin visibility callbacks: some plugins inspect the panel state.
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			for _, command := range pluginCommandsSnapshot(vfs.PluginCommandPanel, pf) {
				if command.MenuPath != "" {
					appendPluginCommand(command)
				}
			}
		}
	}

	result := make([]vtui.MenuBarItem, 0, len(order))
	for _, path := range order {
		m := menus[path]
		items := make([]vtui.MenuItem, 0, len(m.items)+len(m.pinned))
		items = append(items, m.items...)
		items = append(items, m.pinned...)
		result = append(result, vtui.MenuBarItem{Label: m.title, SubItems: items})
	}
	return result
}
