package app

import (
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const (
	commandPaletteActionName = "App.CommandPalette"
	commandPaletteHistoryID  = "command-palette"
	commandPaletteHistoryMax = 50
	commandPaletteLegacyKey  = "CtrlAltP"
)

type commandPaletteSource uint8

const (
	commandPaletteSourceAction commandPaletteSource = iota
	commandPaletteSourcePlugin
	commandPaletteSourceLegacyPlugin
	commandPaletteSourceUserMenu
)

// commandPaletteEntry is the runtime adapter shared by built-in actions,
// plugin contributions and executable user-menu leaves.
type commandPaletteEntry struct {
	Key                string
	Label              string
	EnglishLabel       string
	Description        string
	EnglishDescription string
	ID                 string
	Category           string
	Shortcut           string
	SearchFields       []string
	Checked            bool
	// run is used by dynamic command providers whose target must be resolved
	// at execution time (workspaces, macros, drives). Static actions and plugin
	// contributions keep using the source-specific fields below.
	run func() bool

	source         commandPaletteSource
	pluginLocation vfs.PluginCommandLocation
	legacyIndex    int
	menuCommands   []string
	panels         *panel.PanelsFrame
}

// ShowCommandPalette opens the palette from every full-screen work area.
// Modal dialogs and menus deliberately remain in control of their own input.
func ShowCommandPalette() bool {
	if vtui.FrameManager == nil {
		return false
	}
	if _, alreadyOpen := vtui.FrameManager.GetTopFrame().(*commandPaletteDialog); alreadyOpen {
		return true
	}
	if menu := vtui.FrameManager.GetActiveMenuBar(); menu != nil && menu.Active {
		// vtui's VMenu fires a clicked item's OnClick synchronously, before the
		// menu bar closes itself (VMenu.FireAction runs ahead of SetExitCode),
		// so a click on this very item still observes menu.Active == true here.
		// Defer and retry once the current input dispatch (and the menu close
		// it triggers) has completed, instead of silently swallowing the click.
		vtui.FrameManager.PostTask(func() { ShowCommandPalette() })
		return true
	}
	if top := vtui.FrameManager.GetTopFrame(); top != nil && top.GetType() == vtui.TypeMenu && top.IsDone() {
		// The retry posted by the branch above can run in the gap between a
		// clicked menu marking itself done and the frame manager removing it:
		// the menu bar is inactive by then, so the branch above lets the call
		// through, and the dead menu is still the top frame. It is a modal
		// VMenu and not a supported owner, so the branch below consumed the
		// retry and the palette never opened. A menu that is done is not an
		// owner to protect; it is a frame on its way out, so go around once
		// more and look again after the cleanup.
		vtui.FrameManager.PostTask(func() { ShowCommandPalette() })
		return true
	}
	if top := vtui.FrameManager.GetTopFrame(); top != nil && top.IsModal() && !commandPaletteModalFrameSupported(top) {
		// Unknown modal owners keep their complete input contract. Consume the
		// palette chord without stacking an unrelated dialog above them.
		return true
	}
	area := macroCurrentArea()
	if !commandPaletteAreaAllowed(area) {
		// Ctrl+Shift+P is deliberately consumed over other modal surfaces; it
		// must never leak through and edit a field or activate a menu item.
		return true
	}

	pf := panel.FindPanelsFrameAnyScreen()
	var fastFindPanel *panel.FileSystemPanel
	fastFindText := ""
	if topPanels, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame); ok && topPanels == pf && !pf.Closed {
		if active := pf.GetActivePanel(); active != nil && active.FastFindMode {
			fastFindPanel = active
			fastFindText = active.FastFindStr
		}
	}
	entries := buildCommandPaletteEntries(area, pf)
	dialog := newCommandPaletteDialog(entries, loadCommandPaletteRecent(), func(entry commandPaletteEntry) {
		// A Window marked Done remains on the stack until the current input
		// dispatch completes. Defer execution so editor/viewer actions see their
		// original frame as the top frame again.
		vtui.FrameManager.PostTask(func() {
			if executeCommandPaletteEntry(entry) {
				rememberCommandPaletteEntry(entry.Key)
			}
		})
	})
	dialog.rebuild = func() []commandPaletteEntry { return buildCommandPaletteEntries(area, pf) }
	vtui.FrameManager.Push(dialog)
	// Pushing any ordinary overlay cancels Fast Find on focus loss. The command
	// palette is the exception: it has just indexed the transient F2 command and
	// must keep that mode alive until the user either runs it, cancels the
	// palette, or chooses an ordinary action (RunAction then performs the normal
	// cancellation). Revalidate the original panel before restoring the snapshot.
	if fastFindPanel != nil && !pf.Closed && pf.GetActivePanel() == fastFindPanel {
		fastFindPanel.FastFindMode = true
		fastFindPanel.FastFindStr = fastFindText
	}
	return true
}

func commandPaletteAreaAllowed(area string) bool {
	switch area {
	case "Shell", "Terminal", "Editor", "Viewer", "Other":
		return true
	default:
		return false
	}
}

// commandPaletteLegacyShortcut is an explicit fallback for terminals that
// still use the legacy byte protocol. In that protocol Ctrl+Shift+P arrives
// as the same Ctrl+P byte used by the passive-panel command, so treating
// Ctrl+P as the palette would break an existing default. Ctrl+Alt+P survives
// as an ESC-prefixed Ctrl+P event and remains available unless the user has
// explicitly assigned or silenced it.
func commandPaletteLegacyShortcut(area string, e *vtinput.InputEvent) bool {
	if e == nil || !e.KeyDown || keymap.EventToHotkeyString(e) != commandPaletteLegacyKey {
		return false
	}
	// A program running in the terminal keeps its keys (#1376).
	if strings.EqualFold(area, "Terminal") && !keymap.ConditionTrue("NoTerminalApp") {
		return false
	}
	if keymap.GlobalHotkeysMgr == nil {
		return true
	}
	return keymap.ConfiguredHotkeyAction(keymap.GlobalHotkeysMgr, area, commandPaletteLegacyKey) == ""
}

func buildCommandPaletteEntries(area string, pf *panel.PanelsFrame) []commandPaletteEntry {
	entries := commandPaletteActionEntries(area)
	entries = append(entries, commandPaletteFrameEntries()...)
	entries = append(entries, commandPaletteWorkspaceEntries()...)
	entries = append(entries, commandPaletteMacroEntries(area)...)
	if pf != nil {
		entries = append(entries, commandPalettePluginEntries(pf)...)
		entries = append(entries, commandPaletteDriveEntries(pf)...)
		entries = append(entries, commandPalettePrefixEntries(area, pf)...)
		// User-menu execution feeds the underlying command line. It is safe in
		// every primary area except a terminal currently owned by a busy term.PTY.
		if commandPaletteCanIncludeUserMenu(area) {
			entries = append(entries, commandPaletteUserMenuEntries(pf)...)
		}
	}
	return entries
}

func commandPaletteCanIncludeUserMenu(area string) bool {
	return area != "Terminal" || keymap.ConditionTrue("TerminalQuiet")
}

func commandPaletteActionEntries(area string) []commandPaletteEntry {
	entries := make([]commandPaletteEntry, 0, action.Len())
	for _, act := range action.All() {
		// The palette used to skip its own launcher act, but that broke the
		// invariant (enforced by TestCommandPaletteResolvesEveryActionGeneratedMenuLeafByID)
		// that every visible menu leaf has a matching palette entry. Listing it
		// is harmless: the running dialog's own alreadyOpen guard makes
		// selecting it a no-op while it is still open, and by the time a
		// deferred re-selection runs the dialog has already been popped, so it
		// simply reopens a fresh palette, same as the CtrlShiftP shortcut would.
		if !commandPaletteActionApplies(act, area) {
			continue
		}
		if act.Visible != nil && !act.Visible() {
			continue
		}

		label := action.PlainLabel(act.DisplayLabel())
		englishLabel := action.PlainLabel(act.Label)
		category := commandPaletteActionCategory(act)
		shortcuts := keymap.MergeShortcuts(
			commandPaletteActionShortcuts(area, act.Name),
			keymap.NativeShortcutsForAction(area, act),
		)
		searchFields := []string{act.Area, act.MenuPath, area}
		translationKeys := append([]string{act.LabelKey, act.DescKey}, act.SearchKeys...)
		translationKeys = append(translationKeys, commandPaletteActionCategoryKeys(act)...)
		searchFields = append(searchFields, commandPaletteTranslations(translationKeys...)...)
		entry := commandPaletteEntry{
			Key:                "act:" + strings.ToLower(act.Name),
			Label:              label,
			EnglishLabel:       englishLabel,
			Description:        act.DisplayDescription(),
			EnglishDescription: act.Description,
			ID:                 act.Name,
			Category:           category,
			Shortcut:           strings.Join(shortcuts, ", "),
			SearchFields:       searchFields,
			source:             commandPaletteSourceAction,
		}
		if act.Checked != nil {
			entry.Checked = act.Checked()
		}
		entries = append(entries, entry)
	}
	return entries
}

func commandPaletteActionApplies(action action.Action, area string) bool {
	if strings.EqualFold(action.Area, "Common") || strings.EqualFold(action.Area, area) {
		return true
	}
	// Shell commands operate on the panel.PanelsFrame found beneath an editor or
	// viewer and include application settings, sorting and plugin management.
	// Keeping them global is what makes the palette an escape hatch for
	// commands otherwise hidden in another area's menu bar.
	if strings.EqualFold(action.Area, "Shell") && commandPaletteAreaAllowed(area) {
		return true
	}
	for _, extraArea := range action.DefaultAreas {
		if !strings.EqualFold(extraArea, area) {
			continue
		}
		// Extra-area bindings may be conditional (notably Shell actions exposed
		// in Terminal only while no AltScreen application owns the keyboard).
		hasApplicableKey := false
		for _, keySpec := range action.DefaultKeys {
			_, condition, _ := strings.Cut(keySpec, ":")
			if condition == "" || keymap.ConditionTrue(condition) {
				hasApplicableKey = true
				break
			}
		}
		return hasApplicableKey
	}
	return false
}

func commandPaletteActionCategory(act action.Action) string {
	for _, key := range commandPaletteActionCategoryKeys(act) {
		if category := i18n.Msg(key); category != "" && !strings.HasPrefix(category, "{") {
			return action.PlainLabel(category)
		}
	}
	if act.MenuPath != "" {
		return act.MenuPath
	}
	return act.Area
}

func commandPaletteActionCategoryKeys(action action.Action) []string {
	var keys []string
	if strings.HasPrefix(action.Name, "Workspace.") {
		keys = append(keys, "CommandPalette.CategoryWorkspace")
	}
	if action.MenuPath != "" {
		keys = append(keys,
			"Menu."+action.Area+"."+action.MenuPath,
			"Menu."+action.MenuPath,
		)
	}
	return keys
}

func commandPaletteActionShortcuts(area, actionName string) []string {
	if keymap.GlobalHotkeysMgr == nil {
		return nil
	}
	active := keymap.GlobalHotkeysMgr.GetActiveBindings()
	areas := []string{area}
	if !strings.EqualFold(area, "Common") {
		areas = append(areas, "Common")
	}
	seen := make(map[string]bool)
	var keys []string
	for _, bindingArea := range areas {
		for key, binding := range active[bindingArea] {
			name, condition, _ := strings.Cut(binding, ":")
			if !strings.EqualFold(name, actionName) {
				continue
			}
			if condition != "" && !keymap.ConditionTrue(condition) {
				continue
			}
			if !seen[key] {
				seen[key] = true
				keys = append(keys, keymap.FormatKeyForUI(key))
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func commandPalettePluginEntries(pf *panel.PanelsFrame) []commandPaletteEntry {
	var entries []commandPaletteEntry
	for _, location := range []vfs.PluginCommandLocation{vfs.PluginCommandPanel, vfs.PluginCommandConfig} {
		categoryKey := "CommandPalette.CategoryPlugin"
		category := i18n.Msg("CommandPalette.CategoryPlugin")
		if location == vfs.PluginCommandConfig {
			categoryKey = "CommandPalette.CategoryPluginConfig"
			category = i18n.Msg("CommandPalette.CategoryPluginConfig")
		}
		for _, command := range plughost.PluginCommandsSnapshot(location, pf) {
			label := action.PlainLabel(plughost.PluginCommandDisplayLabel(command))
			description := plughost.PluginCommandDisplayDescription(command)
			if description == "" {
				description = command.ID
			}
			englishDescription := command.Description
			if englishDescription == "" {
				englishDescription = command.ID
			}
			searchFields := []string{category, command.Label, command.Description}
			searchFields = append(searchFields, plughost.PluginCommandSearchTerms(command)...)
			translationKeys := append([]string{categoryKey}, plughost.PluginCommandTranslationKeys(command)...)
			searchFields = append(searchFields, commandPaletteTranslations(translationKeys...)...)
			entries = append(entries, commandPaletteEntry{
				Key:                fmt.Sprintf("plugin:%d:%s", location, strings.ToLower(command.ID)),
				Label:              label,
				EnglishLabel:       action.PlainLabel(command.Label),
				Description:        description,
				EnglishDescription: englishDescription,
				ID:                 command.ID,
				Category:           category,
				Shortcut:           panel.PluginCommandShortcut(command),
				SearchFields:       searchFields,
				source:             commandPaletteSourcePlugin,
				pluginLocation:     location,
				panels:             pf,
			})
		}
	}
	for index, item := range plughost.PluginMenuItemsSnapshot() {
		actionName := item.ActionName
		if actionName == "" {
			actionName = keymap.LegacyPluginActionName(index)
		}
		label := action.PlainLabel(item.Label)
		searchFields := []string{i18n.Msg("CommandPalette.CategoryLegacyPlugin")}
		searchFields = append(searchFields, commandPaletteTranslations(
			"CommandPalette.CategoryPlugin",
			"CommandPalette.CategoryLegacyPlugin",
		)...)
		entries = append(entries, commandPaletteEntry{
			Key:          fmt.Sprintf("legacy-plugin:%s:%d", normalizeCommandPaletteText(label), index),
			Label:        label,
			EnglishLabel: label,
			Category:     i18n.Msg("CommandPalette.CategoryPlugin"),
			Shortcut:     panel.PluginActionShortcut(actionName),
			SearchFields: searchFields,
			source:       commandPaletteSourceLegacyPlugin,
			legacyIndex:  index,
			panels:       pf,
		})
	}
	return entries
}

type commandPaletteUserMenuSource struct {
	mode  panel.MenuMode
	title string
	path  string
	items []panel.UserMenuItem
}

func commandPaletteUserMenuEntries(pf *panel.PanelsFrame) []commandPaletteEntry {
	if pf == nil {
		return nil
	}
	var sources []commandPaletteUserMenuSource
	seenSources := make(map[string]bool)
	for _, mode := range []panel.MenuMode{panel.MenuModeLocal, panel.MenuModeFar, panel.MenuModeMain} {
		items, title, path, ok := panel.LoadMenuForMode(pf, mode)
		if !ok || len(items) == 0 || path == "" {
			continue
		}
		canonical := commandPaletteCanonicalPath(path)
		if seenSources[canonical] {
			continue
		}
		seenSources[canonical] = true
		sources = append(sources, commandPaletteUserMenuSource{mode: mode, title: title, path: path, items: items})
	}

	var entries []commandPaletteEntry
	for _, source := range sources {
		entries = append(entries, flattenCommandPaletteUserMenu(source, pf)...)
	}
	return entries
}

func flattenCommandPaletteUserMenu(source commandPaletteUserMenuSource, pf *panel.PanelsFrame) []commandPaletteEntry {
	var entries []commandPaletteEntry
	var walk func(items []panel.UserMenuItem, labels []string, indexes []int)
	walk = func(items []panel.UserMenuItem, labels []string, indexes []int) {
		for index := range items {
			item := items[index]
			if item.IsSeparator() {
				continue
			}
			label := action.PlainLabel(item.Label)
			pathLabels := append(append([]string(nil), labels...), label)
			pathIndexes := append(append([]int(nil), indexes...), index)
			if item.IsSubmenu() {
				walk(item.Submenu, pathLabels, pathIndexes)
				continue
			}
			if !commandPaletteMenuHasExecutableCommands(item.Commands) {
				continue
			}
			indexParts := make([]string, len(pathIndexes))
			for i, pathIndex := range pathIndexes {
				indexParts[i] = fmt.Sprintf("%d", pathIndex)
			}
			breadcrumb := strings.Join(pathLabels, " > ")
			searchFields := []string{breadcrumb, source.path, strings.Join(item.Commands, " ")}
			searchFields = append(searchFields, commandPaletteTranslations(
				"CommandPalette.CategoryUserMenu",
				commandPaletteUserMenuTitleKey(source.mode),
			)...)
			entries = append(entries, commandPaletteEntry{
				Key:                "user-menu:" + commandPaletteCanonicalPath(source.path) + ":" + strings.Join(indexParts, "."),
				Label:              label,
				EnglishLabel:       label,
				Description:        breadcrumb,
				EnglishDescription: breadcrumb,
				ID:                 item.HotKey,
				Category:           fmt.Sprintf("%s: %s", i18n.Msg("CommandPalette.CategoryUserMenu"), action.PlainLabel(source.title)),
				Shortcut:           item.HotKey,
				SearchFields:       searchFields,
				source:             commandPaletteSourceUserMenu,
				menuCommands:       append([]string(nil), item.Commands...),
				panels:             pf,
			})
		}
	}
	walk(source.items, nil, nil)
	return entries
}

func commandPaletteUserMenuTitleKey(mode panel.MenuMode) string {
	switch mode {
	case panel.MenuModeLocal:
		return "UserMenu.LocalMenuTitle"
	case panel.MenuModeFar, panel.MenuModeMain:
		return "UserMenu.MainMenuTitle"
	default:
		return ""
	}
}

func commandPaletteMenuHasExecutableCommands(commands []string) bool {
	for _, command := range commands {
		trimmed := strings.TrimSpace(command)
		if trimmed != "" && !panel.IsMenuComment(trimmed) {
			return true
		}
	}
	return false
}

func commandPaletteCanonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return filepath.ToSlash(path)
}

func executeCommandPaletteEntry(entry commandPaletteEntry) bool {
	// The Fast Find toggle is the only palette command that operates inside the
	// transient search itself. Every other selection leaves that mode, including
	// dynamic/plugin commands that do not pass through RunAction.
	if !strings.EqualFold(entry.ID, "FastFind.ToggleMatchMode") {
		if pf := panel.FindPanelsFrame(); pf != nil && pf.CancelFastFind() && vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}
	if entry.run != nil {
		return entry.run()
	}
	switch entry.source {
	case commandPaletteSourceAction:
		return RunAction(entry.ID)
	case commandPaletteSourcePlugin:
		return plughost.ExecutePluginCommand(entry.pluginLocation, entry.ID, entry.panels)
	case commandPaletteSourceLegacyPlugin:
		items := plughost.PluginMenuItemsSnapshot()
		if entry.legacyIndex >= 0 && entry.legacyIndex < len(items) && items[entry.legacyIndex].Handler != nil {
			items[entry.legacyIndex].Handler(entry.panels)
			return true
		}
	case commandPaletteSourceUserMenu:
		if entry.panels != nil && commandPaletteMenuHasExecutableCommands(entry.menuCommands) {
			return panel.ExecuteMenuCommandsWithResult(entry.panels, entry.menuCommands)
		}
	}
	return false
}

func loadCommandPaletteRecent() []string {
	if vtui.GlobalHistoryProvider == nil {
		return nil
	}
	return vtui.GlobalHistoryProvider.LoadHistory(commandPaletteHistoryID)
}

func rememberCommandPaletteEntry(key string) {
	if vtui.GlobalHistoryProvider == nil || key == "" {
		return
	}
	history := vtui.GlobalHistoryProvider.LoadHistory(commandPaletteHistoryID)
	result := make([]string, 0, min(len(history)+1, commandPaletteHistoryMax))
	result = append(result, key)
	for _, previous := range history {
		if strings.EqualFold(previous, key) {
			continue
		}
		result = append(result, previous)
		if len(result) == commandPaletteHistoryMax {
			break
		}
	}
	vtui.GlobalHistoryProvider.SaveHistory(commandPaletteHistoryID, result)
}
