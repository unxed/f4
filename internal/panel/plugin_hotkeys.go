package panel

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// pluginMenuBottomHint is drawn on the lower border of the F11 menu, so that the
// hotkey assignment on F4 (and removal on Del) is not known only to those who
// read the ticket it was asked for in.
const pluginMenuBottomHint = " Del F4 F9 "

// declaredHotkeyString turns the shortcut a plugin declares for a command
// ("Shift+F1") into the string the hotkey manager spells that key with
// ("ShiftF1"), or "" when it does not name a key.
func declaredHotkeyString(declared string) string {
	declared = strings.ReplaceAll(strings.TrimSpace(declared), "+", "")
	if declared == "" {
		return ""
	}
	e := keymap.ParseFarKey(declared)
	if e == nil || e.VirtualKeyCode == 0 && e.Char == 0 {
		return ""
	}
	return keymap.EventToHotkeyString(e)
}

// PluginDefaultKeyOff reports whether the user removed the default hotkey a
// plugin brings with it (config PluginDefaultHotkeysOff).
func PluginDefaultKeyOff(key string) bool {
	if key == "" {
		return false
	}
	for _, off := range strings.Split(config.App.PluginDefaultHotkeysOff, ";") {
		if strings.EqualFold(strings.TrimSpace(off), key) {
			return true
		}
	}
	return false
}

// pluginLabelHotkeyOffKey is the PluginDefaultHotkeysOff entry that switches off
// the letter a plugin marks with an ampersand in the label of the entry named
// actionName. It cannot clash with a key spelling: those never hold a colon.
func pluginLabelHotkeyOffKey(actionName string) string {
	return "amp:" + actionName
}

// pluginLabelHotkey is the letter a plugin label marks with an ampersand, or
// zero when it marks none or the user switched it off.
func pluginLabelHotkey(actionName, label string) rune {
	if PluginDefaultKeyOff(pluginLabelHotkeyOffKey(actionName)) {
		return 0
	}
	_, hotkey, _ := vtui.ParseAmpersandString(label)
	return pluginMenuHotkeyRune(string(hotkey))
}

// SetPluginDefaultKeyOff removes a plugin's default hotkey from use, or gives
// it back, and saves the setting.
func SetPluginDefaultKeyOff(key string, off bool) {
	if key == "" || PluginDefaultKeyOff(key) == off {
		return
	}
	var kept []string
	for _, k := range strings.Split(config.App.PluginDefaultHotkeysOff, ";") {
		if k = strings.TrimSpace(k); k != "" && !strings.EqualFold(k, key) {
			kept = append(kept, k)
		}
	}
	if off {
		kept = append(kept, key)
	}
	config.App.PluginDefaultHotkeysOff = strings.Join(kept, ";")
	config.SaveConfig()
}

func PluginActionForName(name string) (action.Action, bool) {
	rawName := strings.TrimSpace(name)
	lowerName := strings.ToLower(rawName)
	switch {
	case strings.HasPrefix(lowerName, "plugin.command."):
		id := strings.TrimSpace(rawName[len("Plugin.Command."):])
		command, ok := plughost.PluginCommandByID(id)
		if !ok {
			return action.Action{}, false
		}
		actionName := keymap.PluginCommandActionName(command.ID)
		return action.Action{
			Name:        actionName,
			Area:        "Shell",
			Label:       plughost.PluginCommandDisplayLabel(command),
			Description: plughost.PluginCommandDisplayDescription(command),
			Handler:     func() bool { return runPluginHotkeyAction(actionName) },
		}, true
	case strings.HasPrefix(lowerName, "plugin.legacy."):
		index, err := strconv.Atoi(strings.TrimSpace(rawName[len("Plugin.Legacy."):]))
		if err != nil || index < 0 {
			return action.Action{}, false
		}
		items := plughost.PluginMenuItemsSnapshot()
		if index >= len(items) {
			return action.Action{}, false
		}
		item := items[index]
		actionName := item.ActionName
		if actionName == "" {
			actionName = keymap.LegacyPluginActionName(index)
		}
		return action.Action{
			Name:        actionName,
			Area:        "Shell",
			Label:       item.Label,
			Description: "Run the selected plugin command",
			Handler:     func() bool { return runPluginHotkeyAction(actionName) },
		}, true
	default:
		return action.Action{}, false
	}
}

func runPluginHotkeyAction(name string) bool {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(strings.ToLower(name), "plugin.command.") {
		id := strings.TrimSpace(name[len("Plugin.Command."):])
		command, ok := plughost.PluginCommandByID(id)
		if !ok {
			return false
		}
		pf := FindPanelsFrame()
		if pf == nil {
			return false
		}
		return plughost.ExecutePluginCommand(command.Location, command.ID, pf)
	}

	if strings.HasPrefix(strings.ToLower(name), "plugin.legacy.") {
		index, err := strconv.Atoi(strings.TrimSpace(name[len("Plugin.Legacy."):]))
		if err != nil || index < 0 {
			return false
		}
		items := plughost.PluginMenuItemsSnapshot()
		if index >= len(items) || items[index].Handler == nil {
			return false
		}
		if pf := FindPanelsFrame(); pf != nil {
			items[index].Handler(pf)
			return true
		}
	}
	return false
}

func PluginActionShortcut(name string) string {
	if keymap.GlobalHotkeysMgr == nil {
		return ""
	}
	if key := keymap.GlobalHotkeysMgr.GetKeyForAction("Shell", name); key != "" {
		return keymap.FormatKeyForUI(key)
	}
	return ""
}

func PluginCommandShortcut(command vfs.PluginCommand) string {
	if shortcut := PluginActionShortcut(keymap.PluginCommandActionName(command.ID)); shortcut != "" {
		return shortcut
	}
	return command.Shortcut
}

// A plugin menu hot key is a single letter or digit. The F11 menu dispatches
// it through the same ampersand accelerator machinery every other vtui menu
// uses, so a key that cannot be typed as one character -- Del, Enter, Ctrl+F9
// -- is not a menu hot key at all and must never be stored as one.
func pluginMenuHotkeyRune(key string) rune {
	runes := []rune(strings.TrimSpace(key))
	if len(runes) != 1 {
		return 0
	}
	if unicode.IsLetter(runes[0]) || unicode.IsDigit(runes[0]) {
		return unicode.ToUpper(runes[0])
	}
	return 0
}

func IsPluginMenuHotkey(key string) bool { return pluginMenuHotkeyRune(key) != 0 }

// pluginMenuEntry is one row of the F11 menu: the accelerator it owns is kept
// apart from a chord such as Ctrl+F9, which the hotkey manager dispatches
// globally and the menu only displays.
type PluginMenuEntry struct {
	Label      string
	ActionName string
	// CommandID is the registered plugin command behind the entry; empty for a
	// row a plugin added with RegisterPluginMenuItem.
	CommandID string
	Declared  string // shortcut declared by the plugin itself
	Chord     string
	Hotkey    string
}

// applyBinding splits what is currently configured for the entry into an
// accelerator and a display-only chord.
func (e *PluginMenuEntry) applyBinding() {
	e.Hotkey, e.Chord = "", ""
	if key := PluginActionConfiguredKey(e.ActionName); key != "" {
		if r := pluginMenuHotkeyRune(key); r != 0 {
			e.Hotkey = string(r)
		} else {
			e.Chord = keymap.FormatKeyForUI(key)
		}
		return
	}
	declared := strings.TrimSpace(e.Declared)
	if PluginDefaultKeyOff(declaredHotkeyString(declared)) {
		return // the user removed the plugin's own default
	}
	if r := pluginMenuHotkeyRune(declared); r != 0 {
		e.Hotkey = string(r)
		return
	}
	e.Chord = declared
}

// Shortcut is what the left-hand column shows for the entry.
func (e PluginMenuEntry) Shortcut() string {
	if e.Hotkey != "" {
		return e.Hotkey
	}
	return e.Chord
}

// resolvePluginMenuHotkeys makes every accelerator in the menu unique. Far can
// cycle through rows sharing a letter; a menu that prints the letters in their
// own column cannot, and two identical letters in that column are worse than
// one row without an accelerator.
func resolvePluginMenuHotkeys(entries []PluginMenuEntry) {
	used := make(map[rune]bool, len(entries))
	claim := func(key string) string {
		r := pluginMenuHotkeyRune(key)
		if r == 0 || used[r] {
			return ""
		}
		used[r] = true
		return string(r)
	}
	// Assigned letters go first: a letter the user picked with F4 must not be
	// lost to an ampersand that happens to sit higher up the menu.
	for i := range entries {
		entries[i].Hotkey = claim(entries[i].Hotkey)
	}
	// Ampersand markers coming from the plugin labels fill in the rest.
	for i := range entries {
		if entries[i].Hotkey != "" || entries[i].Chord != "" {
			continue
		}
		if hotkey := pluginLabelHotkey(entries[i].ActionName, entries[i].Label); hotkey != 0 {
			entries[i].Hotkey = claim(string(hotkey))
		}
	}
}

func buildPluginMenuEntries(items []plughost.PluginMenuItem, commands []vfs.PluginCommand) []PluginMenuEntry {
	entries := make([]PluginMenuEntry, 0, len(items)+len(commands))
	for index, item := range items {
		actionName := item.ActionName
		if actionName == "" {
			actionName = keymap.LegacyPluginActionName(index)
		}
		entries = append(entries, PluginMenuEntry{Label: item.Label, ActionName: actionName})
	}
	for _, command := range commands {
		entries = append(entries, PluginMenuEntry{
			Label:      plughost.PluginCommandDisplayLabel(command),
			ActionName: keymap.PluginCommandActionName(command.ID),
			CommandID:  command.ID,
			Declared:   command.Shortcut,
		})
	}
	RefreshPluginMenuEntries(entries)
	return entries
}

// refreshPluginMenuEntries re-reads the bindings for the whole menu. Assigning
// a letter takes it away from whoever held it before, so a single row cannot be
// updated on its own.
func RefreshPluginMenuEntries(entries []PluginMenuEntry) {
	for i := range entries {
		entries[i].applyBinding()
	}
	resolvePluginMenuHotkeys(entries)
}

// pluginMenuShortcutWidth keeps the column one character wide even when nothing
// is assigned yet, so the first F4 assignment does not shift every label.
func pluginMenuShortcutWidth(entries []PluginMenuEntry) int {
	width := 1
	for _, entry := range entries {
		if w := runewidth.StringWidth(entry.Shortcut()); w > width {
			width = w
		}
	}
	return width
}

// pluginMenuItemText renders the shortcut in a stable column before the
// command name. A single unmodified character remains an ampersand hotkey so
// it can also activate the item while the F11 menu is open. Longer chords are
// display-only metadata; their actual dispatch happens in the hotkey manager.
func PluginMenuItemText(label, shortcut string, shortcutWidth int) string {
	cleanLabel, _, _ := vtui.ParseAmpersandString(label)
	shortcut = strings.TrimSpace(shortcut)
	if shortcutWidth < runewidth.StringWidth(shortcut) {
		shortcutWidth = runewidth.StringWidth(shortcut)
	}
	prefix := strings.Repeat(" ", shortcutWidth-runewidth.StringWidth(shortcut))
	if shortcut != "" {
		if len([]rune(shortcut)) == 1 && !unicode.IsSpace([]rune(shortcut)[0]) {
			prefix += "&" + shortcut
		} else {
			prefix += shortcut
		}
	}
	return prefix + " " + cleanLabel
}

func pluginActionConfiguredBinding(name string) (string, string) {
	return keymap.ConfiguredHotkeyBinding(keymap.GlobalHotkeysMgr, name)
}

func PluginActionConfiguredKey(name string) string {
	_, key := pluginActionConfiguredBinding(name)
	return key
}

func PluginActionDefaultShortcut(name string) string {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(lowerName, "plugin.command.") {
		return ""
	}
	id := strings.TrimSpace(name[len("Plugin.Command."):])
	if command, ok := plughost.PluginCommandByID(id); ok {
		return command.Shortcut
	}
	return ""
}

func assignPluginHotkey(actionName, label string, onComplete func()) {
	hm := keymap.GlobalHotkeysMgr
	if hm == nil || vtui.FrameManager == nil || !isMenuHotkeyActionName(actionName) {
		return
	}
	showPluginHotkeyDialog(hm, actionName, label, onComplete)
}

func isMenuHotkeyActionName(name string) bool {
	return keymap.IsPluginActionName(name) || keymap.IsDriveMenuActionName(name)
}

// bindPluginMenuHotkey stores a letter for the entry. A letter identifies
// exactly one row, so it is taken away from whoever held it, and the entry
// loses whatever it held before: one hot key per plugin, as in Far.
func bindPluginMenuHotkey(hm *keymap.HotkeyManager, actionName string, r rune) bool {
	if hm == nil || r == 0 || !isMenuHotkeyActionName(actionName) {
		return false
	}
	key := string(unicode.ToUpper(r))
	for _, area := range []string{"Shell", "Common"} {
		for boundKey, binding := range hm.Bindings[area] {
			name := strings.SplitN(binding, ":", 2)[0]
			if !isMenuHotkeyActionName(name) {
				continue
			}
			if strings.EqualFold(boundKey, key) || strings.EqualFold(name, actionName) {
				keymap.RestoreDefaultBinding(hm, area, boundKey)
			}
		}
	}
	hm.Bind("Shell", key, actionName)
	hm.Save()
	return true
}

func pluginHotkeyDeleteQuestion(key, label string) string {
	cleanLabel, _, _ := vtui.ParseAmpersandString(label)
	return fmt.Sprintf(i18n.Msg("Plugins.HotkeyRemoveQuestion"), keymap.FormatKeyForUI(key), cleanLabel)
}

// pluginHotkeyEventRune reports the letter or digit a key event stands for, or
// zero when the event carries a modifier or is not a printable character.
func pluginHotkeyEventRune(e *vtinput.InputEvent) rune {
	if e == nil || e.Type != vtinput.KeyEventType {
		return 0
	}
	mods := keymap.NormalizeMods(e.ControlKeyState)
	if mods.Contains(vtinput.LeftCtrlPressed) || mods.Contains(vtinput.LeftAltPressed) {
		return 0
	}
	if r := pluginMenuHotkeyRune(string(e.Char)); r != 0 {
		return r
	}
	vk := e.VirtualKeyCode
	if (vk >= 'A' && vk <= 'Z') || (vk >= '0' && vk <= '9') {
		return rune(vk)
	}
	return 0
}

// PluginHotkeyAssignFrame asks for a single letter or digit, the way Far does
// for its plugin menu: Del drops the current assignment, Esc leaves it alone,
// and a key that could never work as a menu accelerator is simply ignored.
type PluginHotkeyAssignFrame struct {
	*vtui.Window
	hm         *keymap.HotkeyManager
	actionName string
	onComplete func()
	// declaredKey is the hotkey the plugin brings with it, in the hotkey
	// manager's spelling, when nothing is configured and the user has not
	// removed it: it is what the plugin menu shows and what Del removes.
	declaredKey string
}

func NewPluginHotkeyAssignFrame(hm *keymap.HotkeyManager, actionName, label string, onComplete func()) *PluginHotkeyAssignFrame {
	width, height := 48, 9
	f := &PluginHotkeyAssignFrame{
		Window:     vtui.NewCenteredDialog(width, height, i18n.Msg("Plugins.HotkeyTitle")),
		hm:         hm,
		actionName: actionName,
		onComplete: onComplete,
	}

	cleanLabel, _, _ := vtui.ParseAmpersandString(label)
	current := i18n.Msg("Plugins.HotkeyNone")
	if _, key := keymap.ConfiguredHotkeyBinding(hm, actionName); key != "" {
		current = keymap.FormatKeyForUI(key)
	} else if def := declaredHotkeyString(PluginActionDefaultShortcut(actionName)); def != "" && !PluginDefaultKeyOff(def) {
		f.declaredKey = def
		current = keymap.FormatKeyForUI(def)
	}
	lines := []string{
		cleanLabel,
		i18n.Msg("Plugins.HotkeyPrompt"),
		fmt.Sprintf(i18n.Msg("Plugins.HotkeyCurrent"), current),
		i18n.Msg("Plugins.HotkeyHelp"),
	}

	vbox := vtui.NewVBoxLayout(f.X1+2, f.Y1+2, width-4, height-4)
	for i, line := range lines {
		text := vtui.NewText(0, 0, line, vtui.Palette[vtui.ColDialogText])
		f.AddItem(text)
		margins := vtui.Margins{}
		if i > 0 {
			margins.Top = 1
		}
		vbox.Add(text, margins, vtui.AlignCenter)
	}
	vbox.Apply()
	return f
}

func (f *PluginHotkeyAssignFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e.Type == vtinput.FocusEventType {
		return f.Window.ProcessKey(e)
	}
	if !e.KeyDown {
		return false
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE:
		f.finish(false)
		return true
	case vtinput.VK_DELETE, vtinput.VK_BACK:
		changed := false
		if area, key := keymap.ConfiguredHotkeyBinding(f.hm, f.actionName); key != "" {
			changed = keymap.DeletePluginHotkey(f.hm, area, key)
		} else if f.declaredKey != "" {
			// The plugin's own default: it cannot be unbound in the hotkey
			// manager (it is not there), so it is switched off.
			SetPluginDefaultKeyOff(f.declaredKey, true)
			changed = true
		}
		f.finish(changed)
		return true
	}

	if r := pluginHotkeyEventRune(e); r != 0 {
		f.finish(bindPluginMenuHotkey(f.hm, f.actionName, r))
		return true
	}

	// F-keys, chords and bare modifiers are not menu accelerators. The dialog
	// stays open rather than storing a key the menu could never dispatch.
	return true
}

func (f *PluginHotkeyAssignFrame) finish(changed bool) {
	f.Close()
	if changed && f.onComplete != nil {
		f.onComplete()
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (f *PluginHotkeyAssignFrame) ProcessMouse(e *vtinput.InputEvent) bool { return true }
func (f *PluginHotkeyAssignFrame) GetType() vtui.FrameType                 { return vtui.TypeDialog }
func (f *PluginHotkeyAssignFrame) IsModal() bool                           { return true }

func PluginMenuKeyLabels(pf *PanelsFrame) *vtui.KeySet {
	if pf != nil && macro.MacroMgr != nil {
		if base := pf.GetKeyLabels(); base != nil {
			labels := *base
			labels.Normal[3] = "F4"
			labels.Normal[8] = "F9"
			return &labels
		}
	}
	labels := &vtui.KeySet{Normal: vtui.KeyBarLabels{"", "", "", "F4"}}
	labels.Normal[8] = "F9"
	return labels
}

// PluginHotkeyActionsSnapshot includes commands that are currently hidden from
// the F11 menu as well. A user can therefore assign a shortcut once and keep
// it when moving to another drive or when a plugin changes its visibility.
func PluginHotkeyActionsSnapshot() []action.Action {
	commandIDs := plughost.PluginCommandIDs()

	actions := make([]action.Action, 0, len(commandIDs)+len(plughost.PluginMenuItemsSnapshot()))
	for _, id := range commandIDs {
		command, ok := plughost.PluginCommandByID(id)
		if !ok {
			continue
		}
		if action, ok := PluginActionForName(keymap.PluginCommandActionName(command.ID)); ok {
			actions = append(actions, action)
		}
	}
	for index, item := range plughost.PluginMenuItemsSnapshot() {
		name := item.ActionName
		if name == "" {
			name = keymap.LegacyPluginActionName(index)
		}
		if action, ok := PluginActionForName(name); ok {
			actions = append(actions, action)
		}
	}
	return actions
}

// commandsForPluginMenu drops the commands that are not meant for the F11 menu
// (vfs.PluginCommand.NotInPluginMenu).
func commandsForPluginMenu(commands []vfs.PluginCommand) []vfs.PluginCommand {
	out := make([]vfs.PluginCommand, 0, len(commands))
	for _, c := range commands {
		if !c.NotInPluginMenu {
			out = append(out, c)
		}
	}
	return out
}
