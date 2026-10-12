package app

import (
	"strings"
	"sync"
	"time"

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

// menuRowEnabled remembers, per menu row, the function that says whether the
// row can be used right now. BuildMenuBarItems fills it as it builds; the
// panels frame caches the built rows between frames, and
// refreshMenuRowStates uses these functions to keep the dimmed flags of the
// cached rows current (f4#1814).
var (
	menuRowEnabledMu sync.Mutex
	menuRowEnabled   = map[history.MenuHistoryItemKey]func() bool{}
)

// pluginMenuKeyPrefix is the part of a menu row's key that tells a row fed by
// a plugin command from one fed by the action registry. It is what
// refreshMenuRowStates keys the two schedules on: a plugin's answer can cost
// a read of the file under the cursor (f4#1832), so those rows are asked
// only while the bar is up.
const pluginMenuKeyPrefix = "plugin:"

func rememberMenuRowEnabled(key history.MenuHistoryItemKey, enabled func() bool) {
	if enabled == nil {
		return
	}
	menuRowEnabledMu.Lock()
	menuRowEnabled[key] = enabled
	menuRowEnabledMu.Unlock()
}

// menuRowStamp is what the rows' Enabled functions look at, as cheaply as it
// can be read: which panels frame and which panel is active, where its cursor
// stands, how many rows it lists, which built menu is being refreshed and
// whether the bar is up at all. Two calls with the same stamp are answered
// from the first (f4#1832).
type menuRowStamp struct {
	frame  *panel.PanelsFrame
	active int
	fsp    *panel.FileSystemPanel
	tree   *panel.TreePanel
	cursor int
	count  int
	items  *vtui.MenuBarItem
	// menuOpen reports that the menu bar is up (F9, a click on the top line,
	// an Alt hotkey): the rows nobody can reach while it is down are asked
	// only then, see refreshMenuRowStates.
	menuOpen bool
}

// menuRowRefreshEvery bounds how stale a row can be when the stamp did not
// change but something the stamp does not cover did, such as a selection made
// without moving the cursor.
var menuRowRefreshEvery = 250 * time.Millisecond

var menuRowLast struct {
	stamp menuRowStamp
	at    time.Time
	valid bool
}

// pluginMenuLast is the same throttle for the plugin rows alone, without the
// interval: they are asked once per opening of the bar and left alone until
// it goes down and comes up again. A plugin's answer can read the file under
// the cursor (the archive plugin scans up to sfxProbeLimit of it), and doing
// that every menuRowRefreshEvery would freeze an open menu on a big file
// just as badly as a cursor step froze the panel (f4#1832).
var pluginMenuLast struct {
	stamp menuRowStamp
	valid bool
}

func currentMenuRowStamp(items []vtui.MenuBarItem) menuRowStamp {
	stamp := menuRowStamp{}
	if len(items) > 0 {
		stamp.items = &items[0]
	}
	pf := panel.FindPanelsFrame()
	if pf == nil {
		return stamp
	}
	stamp.frame, stamp.active = pf, pf.ActiveIdx
	stamp.tree = focusedTreePanel(pf)
	stamp.menuOpen = pf.MenuBar != nil && pf.MenuBar.Active
	if fsp := pf.GetActivePanel(); fsp != nil {
		stamp.fsp, stamp.cursor, stamp.count = fsp, fsp.GetCursorIndex(), len(fsp.Entries)
	}
	return stamp
}

// refreshMenuRowStates sets the Disabled flag of every row that has an
// Enabled function to its answer now. GetMenuBar asks for this on every key
// and every frame, and the answers cost a walk of the listing per row (the
// selection) or a plugin's own check, so the rows are asked again only when
// the cursor, the panel or the menu changed, or after menuRowRefreshEvery
// for what that stamp does not cover (f4#1814, f4#1832).
//
// Two groups, two schedules. The rows of the action registry follow the
// cursor as above. The rows a plugin contributes are asked only while the
// bar is up, once per opening: while it is down no one can reach them, and
// their answer can read the whole file under the cursor (the archive plugin
// scans it for a stubbed archive, up to sfxProbeLimit), which is what froze
// the panel on every cursor step in a folder of videos (f4#1832). Nothing
// runs on a dimmed flag: RunAction and ExecutePluginCommand both re-check
// Enabled themselves before anything happens.
func refreshMenuRowStates(items []vtui.MenuBarItem) {
	menuRowEnabledMu.Lock()
	defer menuRowEnabledMu.Unlock()
	if len(menuRowEnabled) == 0 {
		return
	}
	stamp := currentMenuRowStamp(items)
	now := time.Now()
	coreDue := !menuRowLast.valid || menuRowLast.stamp != stamp ||
		now.Sub(menuRowLast.at) >= menuRowRefreshEvery
	pluginDue := stamp.menuOpen && (!pluginMenuLast.valid || pluginMenuLast.stamp != stamp)
	if !stamp.menuOpen {
		// The bar went down. The next opening is a new one and is asked
		// afresh even when nothing but the clock moved since, so the flag
		// that says "this opening has been asked" is cleared here.
		pluginMenuLast.valid = false
	}
	if !coreDue && !pluginDue {
		return
	}
	if coreDue {
		menuRowLast.stamp, menuRowLast.at, menuRowLast.valid = stamp, now, true
	}
	if pluginDue {
		pluginMenuLast.stamp, pluginMenuLast.valid = stamp, true
	}
	// The rows ask the panels for their selection one after another with
	// nothing changing in between: let the listing be walked once each.
	if pf := panel.FindPanelsFrame(); pf != nil {
		for _, p := range pf.Panels {
			if fsp, ok := p.(*panel.FileSystemPanel); ok {
				defer fsp.MemoizeSelection()()
			}
		}
	}
	var refresh func(rows []vtui.MenuItem)
	refresh = func(rows []vtui.MenuItem) {
		for i := range rows {
			if key, ok := rows[i].UserData.(history.MenuHistoryItemKey); ok {
				if enabled := menuRowEnabled[key]; enabled != nil && rowGroupDue(key, coreDue, pluginDue) {
					rows[i].Disabled = !enabled()
				}
			}
			refresh(rows[i].SubItems)
		}
	}
	for i := range items {
		refresh(items[i].SubItems)
	}
}

// rowGroupDue reports whether the group key belongs to is due this call:
// a plugin row follows the bar, every other row follows the cursor.
func rowGroupDue(key history.MenuHistoryItemKey, coreDue, pluginDue bool) bool {
	if strings.HasPrefix(string(key), pluginMenuKeyPrefix) {
		return pluginDue
	}
	return coreDue
}

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
			text = menuhotkeys.Auto(text) // first letter becomes the menu hotkey
		}
		if a.Checked != nil && a.Checked() {
			text = "√ " + text
		}
		item := vtui.MenuItem{
			Text: text,
			// RunAction re-checks Enabled itself, so a stale Disabled flag
			// (the action's context changed while this dropdown sat open)
			// can never let the click through; this flag only decides how
			// the row looks and whether vtui lets it get that far at all.
			Disabled: a.Enabled != nil && !a.Enabled(),
			OnClick:  func() { RunAction(a.Name) },
			UserData: history.MenuHistoryItemKey(a.Name),
		}
		rememberMenuRowEnabled(history.MenuHistoryItemKey(a.Name), a.Enabled)
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
					subTitle = menuhotkeys.Auto(subTitle)
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

	appendPluginCommand := func(command vfs.PluginCommand, app vfs.App) {
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
			text = menuhotkeys.Auto(text)
		}
		if !m.pluginSeparator {
			m.pluginSeparator = true
			// A menu a plugin created itself opens with its own commands,
			// so there is nothing above them to divide from.
			if len(m.items) > 0 {
				m.items = append(m.items, vtui.MenuItem{Separator: true})
			}
		}
		if command.Enabled != nil {
			rememberMenuRowEnabled(history.MenuHistoryItemKey(pluginMenuKeyPrefix+command.ID), func() bool { return command.Enabled(app) })
		}
		m.items = append(m.items, vtui.MenuItem{
			Text: text,
			// ExecutePluginCommand re-checks Enabled itself (same rationale
			// as the core-action comment above): this flag only decides how
			// the row looks and whether vtui lets the click through at all.
			Disabled: command.Enabled != nil && !command.Enabled(app),
			Shortcut: panel.PluginCommandShortcut(command),
			UserData: history.MenuHistoryItemKey(pluginMenuKeyPrefix + command.ID),
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
					appendPluginCommand(command, pf)
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
