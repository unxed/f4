package app

import (
	"strings"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/menuhotkeys"
	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/plughost"
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
		title string
		items []vtui.MenuItem
		// pluginSeparator records that plugin-contributed commands have
		// already been set off from the built-in ones, so a menu fed by
		// several plugins gets one dividing line rather than one per
		// command.
		pluginSeparator bool
		pinned          []vtui.MenuItem
		// subMenus maps a MenuSubPath to the index of its heading in items,
		// so every action of a group lands under the one heading no matter
		// how the registration order interleaves them.
		subMenus map[string]int
	}
	var order []string
	menus := make(map[string]*menu)

	appendAction := func(a action.Action) {
		// Asked before the menu is created, so a group whose every action
		// is hidden does not appear as an empty one.
		if a.Visible != nil && !a.Visible() {
			return
		}
		m := menus[a.MenuPath]
		if m == nil {
			title := i18n.Msg("Menu." + area + "." + a.MenuPath)
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
			UserData: history.MenuHistoryItemKey(a.Name),
		}
		item.Shortcut = keymap.MenuShortcutsForAction(area, a.Name)
		if a.MenuLast {
			if a.MenuSeparatorBefore {
				m.pinned = append(m.pinned, vtui.MenuItem{Separator: true})
			}
			m.pinned = append(m.pinned, item)
			return
		}
		if a.MenuSubPath != "" {
			heading, ok := m.subMenus[a.MenuSubPath]
			if !ok {
				if a.MenuSeparatorBefore {
					m.items = append(m.items, vtui.MenuItem{Separator: true})
				}
				subTitle := i18n.Msg("Menu." + area + "." + a.MenuPath + "." + a.MenuSubPath)
				if strings.HasPrefix(subTitle, "{") {
					subTitle = a.MenuSubPath
				}
				if !strings.Contains(subTitle, "&") {
					subTitle = "&" + subTitle
				}
				m.items = append(m.items, vtui.MenuItem{
					Text:     subTitle,
					UserData: history.MenuHistoryItemKey("submenu:" + a.MenuPath + "." + a.MenuSubPath),
				})
				heading = len(m.items) - 1
				if m.subMenus == nil {
					m.subMenus = make(map[string]int)
				}
				m.subMenus[a.MenuSubPath] = heading
			} else if a.MenuSeparatorBefore {
				m.items[heading].SubItems = append(m.items[heading].SubItems, vtui.MenuItem{Separator: true})
			}
			m.items[heading].SubItems = append(m.items[heading].SubItems, item)
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
			title := i18n.Msg("Menu." + area + "." + command.MenuPath)
			if strings.HasPrefix(title, "{") {
				title = command.MenuPath
			}
			m = &menu{title: title}
			menus[command.MenuPath] = m
			order = append(order, command.MenuPath)
		}
		text := action.PlainLabel(plughost.PluginCommandDisplayLabel(command))
		if !strings.Contains(text, "&") {
			text = "&" + text
		}
		if !m.pluginSeparator {
			m.pluginSeparator = true
			// A menu a plugin created itself opens with its own commands,
			// so there is nothing above them to divide from.
			if len(m.items) > 0 {
				m.items = append(m.items, vtui.MenuItem{Separator: true})
			}
		}
		m.items = append(m.items, vtui.MenuItem{
			Text:     text,
			Shortcut: panel.PluginCommandShortcut(command),
			UserData: history.MenuHistoryItemKey("plugin:" + command.ID),
			OnClick: func() {
				if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
					plughost.ExecutePluginCommand(vfs.PluginCommandPanel, command.ID, pf)
				}
			},
		})
	}

	// The area's own actions first (stable registry order).
	for _, a := range action.All() {
		if a.Name != "Settings.Open" && a.MenuPath != "" && !a.HideFromMenu && a.Area == area {
			appendAction(a)
		}
	}
	if a, ok := GetAction("Settings.Open"); ok {
		appendAction(a)
	}

	// Common actions join only menu groups that already exist in the
	// area, so they cannot create stray top-level menus.
	for _, a := range action.All() {
		if a.Name != "Settings.Open" && a.MenuPath != "" && !a.HideFromMenu && a.Area == "Common" && menus[a.MenuPath] != nil {
			appendAction(a)
		}
	}
	// Panel plugins can opt into an existing generated Shell menu. Keep
	// plugin commands after core actions so the built-in menu remains stable,
	// while still exposing plugin functionality without requiring F11.
	if area == "Shell" {
		// panel.NewPanelsFrame builds its first menu before it is attached to a
		// frame manager. Do not pass a typed-nil *panel.PanelsFrame as vfs.App to
		// plugin visibility callbacks: some plugins inspect the panel state.
		if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
			for _, command := range plughost.PluginCommandsSnapshot(vfs.PluginCommandPanel, pf) {
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
		// Settings leads its submenu without changing top-level menu order.
		for i, item := range items {
			if item.UserData == history.MenuHistoryItemKey("Settings.Open") {
				rest := append([]vtui.MenuItem(nil), items[:i]...)
				rest = append(rest, items[i+1:]...)
				items = append([]vtui.MenuItem{item, {Separator: true}}, rest...)
				break
			}
		}
		result = append(result, vtui.MenuBarItem{Label: m.title, SubItems: normalizeMenuSeparators(items)})
	}
	// Every item above took the first letter of its label, so the menus were
	// full of items that shared a hotkey (#1258).
	menuhotkeys.UniqueBar(result)
	return result
}

// normalizeMenuSeparators removes separators that would draw a line with
// nothing to divide: a leading or trailing one, and any run left behind
// when every item of a group is hidden. A separator belongs to the item
// below it, so it disappears together with that item; without this pass
// the neighbouring groups would silently merge into a doubled line.
func normalizeMenuSeparators(items []vtui.MenuItem) []vtui.MenuItem {
	result := make([]vtui.MenuItem, 0, len(items))
	for _, item := range items {
		if item.Separator && (len(result) == 0 || result[len(result)-1].Separator) {
			continue
		}
		if len(item.SubItems) > 0 {
			item.SubItems = normalizeMenuSeparators(item.SubItems)
		}
		result = append(result, item)
	}
	for len(result) > 0 && result[len(result)-1].Separator {
		result = result[:len(result)-1]
	}
	return result
}
