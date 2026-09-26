package keymap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// conditionRegistry holds the dynamic checks a binding may name after a colon
// ("Esc:EscToggle"). It starts empty on purpose: every condition f4 ships asks
// the panels frame what is on screen, which is knowledge this package does not
// have and must not import. The owner of that state registers its own checks.
var conditionRegistry = map[string]func() bool{}

// NativeShortcutOwnedByCurrentContext reports whether the active frame consumes
// key before vtui's fallback can route it to actionName, so a menu must not
// advertise it. Only the frame layer can answer; an unfilled seam advertises
// every native key, which is what a keymap-only build wants.
var NativeShortcutOwnedByCurrentContext = func(actionName, key string) bool { return false }

// LookupAction resolves an action by name. action.Lookup alone cannot see the
// actions a plugin contributed, so the application replaces this with a lookup
// that falls back to the plugin table.
var LookupAction = action.Lookup

// ConditionTrue reports whether a named condition holds. An unknown name is
// true: a binding must not silently stop working because the frame that owns
// its condition is not on screen to register it.
func ConditionTrue(name string) bool {
	condition, ok := conditionRegistry[strings.ToLower(strings.TrimSpace(name))]
	return !ok || condition()
}

// HotkeyManager handles mapping of key combinations to application actions.
type HotkeyManager struct {
	Bindings map[string]map[string]string // Area -> Key -> ActionName
	Defaults map[string]map[string]string // Area -> Key -> ActionName
	IniPath  string

	// generation counts changes to the active bindings (Bind, Unbind, Load,
	// ReplaceBindingsFrom). It lets a caller that derives something from
	// GetKeyForAction/MenuShortcutsForAction — a generated menu's shortcut
	// column, say — cache that work and cheaply notice when a rebind (issue
	// #651, the Options > Keys dialog) invalidates it, without recomputing it
	// on every call just to compare it against what it already had.
	generation uint64
}

// Generation reports how many times this manager's active bindings have
// changed. A nil manager (no HotkeyManager configured yet) never changes, so
// it reports 0.
func (hm *HotkeyManager) Generation() uint64 {
	if hm == nil {
		return 0
	}
	return hm.generation
}

// GetConditions returns the user-friendly names of all registered conditions.
func GetConditions() []string {
	return []string{"None", "SearchFirst", "EmptyCommandLine", "CommandLineNotEmpty", "EscToggle", "TerminalQuiet", "AltPanelVisible", "NoAltScreenApp", "NoTerminalApp"}
}

// RegisterCondition adds a dynamic boolean check accessible by hotkey bindings.
func RegisterCondition(name string, fn func() bool) {
	conditionRegistry[strings.ToLower(name)] = fn
}

var GlobalHotkeysMgr *HotkeyManager

func NewHotkeyManager(iniPath string) *HotkeyManager {
	hm := &HotkeyManager{
		Bindings: make(map[string]map[string]string),
		Defaults: make(map[string]map[string]string),
		IniPath:  iniPath,
	}
	hm.InitDefaults()
	hm.Load()
	return hm
}

func cloneHotkeyBindings(src map[string]map[string]string) map[string]map[string]string {
	if src == nil {
		return nil
	}

	dst := make(map[string]map[string]string, len(src))
	for area, bindings := range src {
		dst[area] = make(map[string]string, len(bindings))
		for key, action := range bindings {
			dst[area][key] = action
		}
	}
	return dst
}

// CloneForEdit returns an isolated manager for a settings dialog. Mutations
// made to the clone do not affect runtime dispatch or the user's INI file
// until ReplaceBindingsFrom followed by Save is explicitly called.
func (hm *HotkeyManager) CloneForEdit() *HotkeyManager {
	if hm == nil {
		return nil
	}
	return &HotkeyManager{
		Bindings: cloneHotkeyBindings(hm.Bindings),
		Defaults: cloneHotkeyBindings(hm.Defaults),
		IniPath:  hm.IniPath,
	}
}

// ReplaceBindingsFrom commits an isolated settings-dialog draft to the
// runtime manager. The caller controls when persistence happens by calling
// Save separately.
func (hm *HotkeyManager) ReplaceBindingsFrom(src *HotkeyManager) {
	if hm == nil || src == nil {
		return
	}
	hm.Bindings = cloneHotkeyBindings(src.Bindings)
	hm.generation++
}

// GetActiveBindings returns a map of Area -> Key -> ActionName containing all active bindings.
func (hm *HotkeyManager) GetActiveBindings() map[string]map[string]string {
	res := make(map[string]map[string]string)
	for area, binds := range hm.Defaults {
		res[area] = make(map[string]string)
		for k, v := range binds {
			res[area][k] = v
		}
	}
	for area, binds := range hm.Bindings {
		if res[area] == nil {
			res[area] = make(map[string]string)
		}
		for k, v := range binds {
			if v == "None" || v == "" {
				delete(res[area], k)
			} else {
				res[area][k] = v
			}
		}
	}
	return res
}

// GetKeyForAction searches for a key combination bound to the given action in an area.
func (hm *HotkeyManager) GetKeyForAction(area, actionName string) string {
	find := func(binds map[string]string) string {
		// Preserve the declared primary shortcut when an action has aliases:
		// Add/Subtract/Multiply and F3 remain the menu hints, not laptop aliases.
		if a, ok := LookupAction(actionName); ok {
			for _, spec := range a.DefaultKeys {
				key, _, _ := strings.Cut(spec, ":")
				name, _, _ := strings.Cut(binds[key], ":")
				if strings.EqualFold(name, actionName) {
					return key
				}
			}
		}
		var keys []string
		for key, binding := range binds {
			parts := strings.SplitN(binding, ":", 2)
			if strings.EqualFold(parts[0], actionName) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return keys[0]
		}
		return ""
	}
	if binds, ok := hm.Bindings[area]; ok {
		if key := find(binds); key != "" {
			return key
		}
	}
	if area != "Common" {
		if binds, ok := hm.Bindings["Common"]; ok {
			if key := find(binds); key != "" {
				return key
			}
		}
	}
	return ""
}

var keyTokenDisplayNames = map[string]string{
	"VK_BA": ";",
	"VK_BB": "=",
	"VK_BC": ",",
	"VK_BD": "-",
	"VK_BE": ".",
	"VK_BF": "/",
	"VK_C0": "`",
	"VK_DB": "[",
	"VK_DC": "\\",
	"VK_DD": "]",
	"VK_DE": "'",
	"VK_E2": "\\",
}

func formatKeyTokenForUI(key string) string {
	if name, ok := keyTokenDisplayNames[strings.ToUpper(key)]; ok {
		return name
	}
	if strings.HasPrefix(strings.ToUpper(key), "VK_") {
		if len(key) == len("VK_")+1 {
			last := key[len(key)-1]
			if (last >= 'A' && last <= 'Z') || (last >= 'a' && last <= 'z') || (last >= '0' && last <= '9') {
				return strings.ToUpper(string(last))
			}
		}
		value, err := strconv.ParseUint(key[3:], 16, 8)
		if err == nil {
			switch {
			case value >= 'A' && value <= 'Z':
				return string(rune(value))
			case value >= '0' && value <= '9':
				return string(rune(value))
			}
		}
	}
	return key
}

// FormatKeyForUI converts a raw key string (like CtrlShiftF5) into a pretty UI string (Ctrl+Shift+F5).
func FormatKeyForUI(key string) string {
	if key == "" {
		return ""
	}
	var parts []string
	if strings.HasPrefix(key, "Ctrl") {
		parts = append(parts, "Ctrl")
		key = key[4:]
	}
	if strings.HasPrefix(key, "Alt") {
		parts = append(parts, "Alt")
		key = key[3:]
	}
	if strings.HasPrefix(key, "Shift") {
		parts = append(parts, "Shift")
		key = key[5:]
	}
	if key != "" {
		parts = append(parts, formatKeyTokenForUI(key))
	}
	return strings.Join(parts, "+")
}

// NativeShortcutsForAction returns framework-owned shortcuts that still reach
// action in area. Native keys are intentionally absent from Defaults, because
// vtui must offer them to the focused frame before running its fallback. An
// explicit user binding on the same key can nevertheless override or silence
// that fallback, so do not advertise a native shortcut that is currently
// claimed by another action (or by None).
func NativeShortcutsForAction(area string, action action.Action) []string {
	seen := make(map[string]bool)
	var shortcuts []string
	for _, spec := range action.NativeKeys {
		key, condition, _ := strings.Cut(spec, ":")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !nativeShortcutConditionTrue(area, condition) {
			continue
		}
		if NativeShortcutOwnedByCurrentContext(action.Name, key) {
			continue
		}
		if GlobalHotkeysMgr != nil {
			bound := GlobalHotkeysMgr.GetAction(area, key)
			if bound != "" && !strings.EqualFold(bound, action.Name) {
				continue
			}
		}
		formatted := FormatKeyForUI(key)
		if formatted == "" || seen[formatted] {
			continue
		}
		seen[formatted] = true
		shortcuts = append(shortcuts, formatted)
	}
	sort.Strings(shortcuts)
	return shortcuts
}

// MenuShortcutsForAction combines configurable and framework-owned shortcuts
// for a menu item. Native shortcuts deliberately do not live in
// HotkeyManager.Defaults, because the focused frame must get first chance to
// consume them; menu presentation still needs to advertise them.
func MenuShortcutsForAction(area, actionName string) string {
	var groups [][]string
	if GlobalHotkeysMgr != nil {
		if key := GlobalHotkeysMgr.GetKeyForAction(area, actionName); key != "" {
			groups = append(groups, []string{FormatKeyForUI(key)})
		}
	}
	if action, ok := LookupAction(actionName); ok {
		groups = append(groups, NativeShortcutsForAction(area, action))
	}
	return strings.Join(MergeShortcuts(groups...), ", ")
}

func nativeShortcutConditionTrue(area, condition string) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	switch strings.ToLower(condition) {
	case "frameworknoterminalapp":
		return !strings.EqualFold(area, "Terminal") || ConditionTrue("NoTerminalApp")
	case "terminalctrlnworkspace":
		return !strings.EqualFold(area, "Terminal") || config.App.TerminalCtrlNWorkspace
	default:
		return ConditionTrue(condition)
	}
}

// initDefaults builds the default bindings from the action registry.
// The registry is the single source of truth: every action carrying
// DefaultKeys gets them bound in its Area (plus any DefaultAreas).
// A key entry may carry a ":Condition" suffix (e.g. "Esc:EscToggle").
func (hm *HotkeyManager) InitDefaults() {
	hm.Defaults = make(map[string]map[string]string)
	for _, a := range action.All() {
		if len(a.DefaultKeys) == 0 {
			continue
		}
		areas := append([]string{a.Area}, a.DefaultAreas...)
		for _, area := range areas {
			if area == "" {
				continue
			}
			for _, keySpec := range a.DefaultKeys {
				key, cond, _ := strings.Cut(keySpec, ":")
				if key == "" {
					continue
				}
				binding := a.Name
				if cond != "" {
					binding += ":" + cond
				}
				if hm.Defaults[area] == nil {
					hm.Defaults[area] = make(map[string]string)
				}
				hm.Defaults[area][key] = binding
			}
		}
	}
}

// Load reads bindings from the INI file, overlaying them onto the defaults.
func (hm *HotkeyManager) Load() {
	hm.generation++
	hm.Bindings = make(map[string]map[string]string)

	// Copy defaults
	for area, binds := range hm.Defaults {
		hm.Bindings[area] = make(map[string]string)
		for k, v := range binds {
			hm.Bindings[area][k] = v
		}
	}

	if hm.IniPath == "" {
		return
	}

	ini := ini.Load(hm.IniPath)
	for area, binds := range ini.Sections() {
		if hm.Bindings[area] == nil {
			hm.Bindings[area] = make(map[string]string)
		}
		for key, action := range binds {
			if action == "" {
				delete(hm.Bindings[area], key)
			} else if strings.EqualFold(action, "none") {
				hm.Bindings[area][key] = "None"
			} else {
				hm.Bindings[area][key] = action
			}
		}
	}

	hm.dropReservedPluginBindings()
}

// Save writes only overridden or new bindings to the INI file.
func (hm *HotkeyManager) Save() { _ = hm.SaveError() }
func (hm *HotkeyManager) SaveError() error {
	if hm.IniPath == "" {
		return fmt.Errorf("hotkey settings path is unavailable")
	}

	var sb strings.Builder
	for area, binds := range hm.Bindings {
		diffs := make(map[string]string)

		// Find overrides and additions
		for key, action := range binds {
			if defAction, ok := hm.Defaults[area][key]; !ok || defAction != action {
				diffs[key] = action
			}
		}

		// Find removals
		if defArea, ok := hm.Defaults[area]; ok {
			for key := range defArea {
				if _, exists := binds[key]; !exists {
					diffs[key] = "None"
				}
			}
		}

		if len(diffs) > 0 {
			fmt.Fprintf(&sb, "[%s]\n", area)
			for key, action := range diffs {
				fmt.Fprintf(&sb, "%s=%s\n", key, action)
			}
			sb.WriteString("\n")
		}
	}

	if err := os.MkdirAll(filepath.Dir(hm.IniPath), 0755); err != nil {
		return err
	}
	return config.WriteUserFileAtomically(hm.IniPath, []byte(sb.String()), 0644)
}

// delKeyAlias returns the other spelling of a Del key string, or "" when the
// key is not a Del key. "ShiftDel" <-> "ShiftNumDel", "Del" <-> "NumDel".
//
// EventToFarString derives the Num prefix from the EnhancedKey flag, but no
// input backend f4 supports reports that flag consistently for Delete: the
// GUI hosts (ebiten, gogpu, x11, wayland) build events with plain Shift/Ctrl/
// Alt state and never set it, and CSI 3~ carries no such flag either, so the
// navigation Del arrives named "NumDel" and every "…Del" binding silently
// misses. far2l has the same two names and binds them to one handler
// (editor.cpp: KEY_SHIFTDEL/KEY_SHIFTNUMDEL/KEY_SHIFTDECIMAL); resolving the
// alias here keeps a binding working whichever name the backend produced.
func delKeyAlias(key string) string {
	if strings.HasSuffix(key, "NumDel") {
		return strings.TrimSuffix(key, "NumDel") + "Del"
	}
	if strings.HasSuffix(key, "Del") {
		return strings.TrimSuffix(key, "Del") + "NumDel"
	}
	return ""
}

// GetAction returns the action name mapped to the key in the given area.
func (hm *HotkeyManager) GetAction(area, key string) string {
	evalBinding := func(binding string) string {
		if binding == "" {
			return ""
		}
		parts := strings.SplitN(binding, ":", 2)
		action := parts[0]
		if len(parts) == 2 {
			condName := strings.ToLower(strings.TrimSpace(parts[1]))
			if condFn, ok := conditionRegistry[condName]; ok {
				if !condFn() {
					return "" // Condition failed, act as if unbound
				}
			}
		}
		return action
	}

	// Common bindings carry no conditions and follow the user everywhere
	// f4 has the keyboard. A program running in the terminal is the one place
	// f4 does not: there its keys are its own, as in far2l, and only the
	// Terminal area's bindings -- each gated by what they may take from it --
	// apply. Far Manager has Shift+F10 and Alt+F9 of its own (#1376).
	useCommon := area != "Common" &&
		(!strings.EqualFold(area, "Terminal") || ConditionTrue("NoTerminalApp"))

	if binds, ok := hm.Bindings[area]; ok {
		if binding, ok := binds[key]; ok {
			if action := evalBinding(binding); action != "" {
				return action
			}
		}
	}
	if useCommon {
		if binds, ok := hm.Bindings["Common"]; ok {
			if binding, ok := binds[key]; ok {
				if action := evalBinding(binding); action != "" {
					return action
				}
			}
		}
	}

	// Nothing is bound under this exact name. Before giving up, try the other
	// spelling of a Del key (see delKeyAlias): an explicit binding always wins,
	// this only fills in the name the backend did not produce.
	if alias := delKeyAlias(key); alias != "" {
		if binds, ok := hm.Bindings[area]; ok {
			if binding, ok := binds[alias]; ok {
				if action := evalBinding(binding); action != "" {
					return action
				}
			}
		}
		if useCommon {
			if binds, ok := hm.Bindings["Common"]; ok {
				if binding, ok := binds[alias]; ok {
					if action := evalBinding(binding); action != "" {
						return action
					}
				}
			}
		}
	}
	return ""
}

// hasExplicitBinding reports whether key differs from the built-in binding in
// the effective area. Bindings starts as a copy of Defaults, so comparing the
// two maps also lets us distinguish a user's override from a default RCtrl
// shortcut (notably the built-in RCtrlA AI shortcut). A missing key whose
// default exists is an explicit unbind written by the settings dialog.
func (hm *HotkeyManager) hasExplicitBinding(area, key string) bool {
	if hm == nil {
		return false
	}

	layerHasKey := func(layer string) bool {
		if binds, ok := hm.Bindings[layer]; ok {
			if _, exists := binds[key]; exists {
				return true
			}
		}
		if defaults, ok := hm.Defaults[layer]; ok {
			if _, exists := defaults[key]; exists {
				return true
			}
		}
		return false
	}

	checkLayer := func(layer string) bool {
		if !layerHasKey(layer) {
			return false
		}
		current, currentExists := hm.Bindings[layer][key]
		def, defaultExists := hm.Defaults[layer][key]
		return !currentExists || !defaultExists || current != def
	}

	if checkLayer(area) {
		return true
	}
	// An unchanged area-local default wins over Common in GetAction, so do
	// not inspect Common in that case. Only fall through when the area has no
	// binding layer for this key at all.
	if layerHasKey(area) {
		return false
	}
	if area != "Common" {
		return checkLayer("Common")
	}
	return false
}

// Bind assigns an action to a key in a specific area.
func (hm *HotkeyManager) Bind(area, key, action string) {
	if hm.Bindings[area] == nil {
		hm.Bindings[area] = make(map[string]string)
	}
	hm.Bindings[area][key] = action
	hm.generation++
}

// Unbind removes a hotkey binding.
func (hm *HotkeyManager) Unbind(area, key string) {
	if binds, ok := hm.Bindings[area]; ok {
		delete(binds, key)
	}
	hm.generation++
}

// KeyBarLabelsForArea resolves F1-F12 keybar labels for the given area
// through the active hotkey bindings, falling back to the provided
// defaults when a key has no binding. A key explicitly unbound ("None")
// gets an empty label.
func KeyBarLabelsForArea(area string, fallbacks *vtui.KeySet) *vtui.KeySet {
	var fbNormal, fbShift, fbAlt, fbCtrl vtui.KeyBarLabels
	if fallbacks != nil {
		fbNormal, fbShift, fbAlt, fbCtrl = fallbacks.Normal, fallbacks.Shift, fallbacks.Alt, fallbacks.Ctrl
	}
	resolve := func(prefix, keyNum, fb string) string {
		if hm := GlobalHotkeysMgr; hm != nil {
			if actName := hm.GetAction(area, prefix+keyNum); actName != "" {
				if strings.EqualFold(actName, "none") {
					return ""
				}
				if act, ok := LookupAction(actName); ok {
					return action.PlainLabel(act.DisplayLabel())
				}
			}
		}
		return fb
	}

	set := &vtui.KeySet{}
	for i := 0; i < 12; i++ {
		keyNum := fmt.Sprintf("F%d", i+1)
		set.Normal[i] = resolve("", keyNum, fbNormal[i])
		set.Shift[i] = resolve("Shift", keyNum, fbShift[i])
		set.Alt[i] = resolve("Alt", keyNum, fbAlt[i])
		set.Ctrl[i] = resolve("Ctrl", keyNum, fbCtrl[i])
	}
	return set
}

func IsPluginActionName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "plugin.command.") || strings.HasPrefix(name, "plugin.legacy.")
}

// pluginReservedKeys are bare keys the panels and the plugin menu need for
// themselves. The first version of the F4 dialog accepted any key at all, so a
// user could hand Del to a plugin and end up with no working Del anywhere --
// including the plugin menu, which needs Del to take that very binding back.
var pluginReservedKeys = map[string]bool{
	"Del": true, "NumDel": true, "Ins": true, "BS": true, "Tab": true,
	"Enter": true, "NumEnter": true, "Esc": true, "Space": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"Home": true, "End": true, "PgUp": true, "PgDn": true,
}

func isReservedPluginHotkey(key string) bool {
	return pluginReservedKeys[strings.TrimSpace(key)]
}

// RestoreDefaultBinding gives a key back to its built-in action. Bindings
// starts life as a copy of Defaults, so a plain Unbind on a key that has a
// default looks like a deliberate "None" to Save and would kill the built-in
// shortcut for good.
func RestoreDefaultBinding(hm *HotkeyManager, area, key string) {
	if hm == nil {
		return
	}
	if def, ok := hm.Defaults[area][key]; ok {
		hm.Bind(area, key, def)
		return
	}
	hm.Unbind(area, key)
}

// dropReservedPluginBindings repairs configurations written by that first
// dialog. It runs on every load, so a hotkeys.ini carrying "Del=Plugin.*" stops
// shadowing the file deletion command without the user having to edit the file
// by hand.
func (hm *HotkeyManager) dropReservedPluginBindings() {
	if hm == nil {
		return
	}
	for area, binds := range hm.Bindings {
		for key, binding := range binds {
			name := strings.SplitN(binding, ":", 2)[0]
			if IsPluginActionName(name) && isReservedPluginHotkey(key) {
				RestoreDefaultBinding(hm, area, key)
			}
		}
	}
}

func ConfiguredHotkeyBinding(hm *HotkeyManager, actionName string) (string, string) {
	if hm == nil {
		return "", ""
	}
	for _, area := range []string{"Shell", "Common"} {
		var keys []string
		for key, binding := range hm.Bindings[area] {
			namePart := strings.SplitN(binding, ":", 2)[0]
			if strings.EqualFold(namePart, actionName) {
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

func DeletePluginHotkey(hm *HotkeyManager, area, key string) bool {
	if hm == nil || area == "" || key == "" {
		return false
	}
	RestoreDefaultBinding(hm, area, key)
	hm.Save()
	return true
}

// ConfigurableHotkeyOwnsPanelBookmark lets an explicit configurable binding
// take the place of far2l's built-in Right Ctrl/Ctrl+Alt bookmark shortcuts.
// Unmodified defaults keep their historical bookmark behavior, while a user
// binding on either Ctrl spelling is honored without requiring both spellings.
func ConfigurableHotkeyOwnsPanelBookmark(hm *HotkeyManager, area string, e *vtinput.InputEvent) bool {
	if hm == nil || e == nil || !IsPanelBookmarkHotkey(e) {
		return false
	}
	key := EventToHotkeyString(e)
	if hm.hasExplicitBinding(area, key) {
		return true
	}
	if strings.HasPrefix(key, "RCtrl") {
		return hm.hasExplicitBinding(area, "Ctrl"+strings.TrimPrefix(key, "RCtrl"))
	}
	return false
}

// IsPanelBookmarkHotkey identifies far2l-compatible folder bookmark keys.
// Built-in bookmark combinations reach PanelsFrame before macro and
// configurable hotkey handling, because EventToFarString intentionally
// normalizes left and right Ctrl. Explicit configurable bindings are allowed
// to reclaim the combination before this handoff.
func IsPanelBookmarkHotkey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}

	rctrl := (e.ControlKeyState & vtinput.RightCtrlPressed) != 0
	lctrl := (e.ControlKeyState & vtinput.LeftCtrlPressed) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	isGoto := (rctrl && !shift && !alt) || ((lctrl || rctrl) && alt && !shift)
	isSave := (rctrl && shift && !alt) || ((lctrl || rctrl) && alt && shift)

	if e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 {
		return isGoto || isSave
	}
	return e.VirtualKeyCode == vtinput.VK_OEM_3 && isGoto
}

func MergeShortcuts(groups ...[]string) []string {
	seen := make(map[string]bool)
	var merged []string
	for _, group := range groups {
		for _, shortcut := range group {
			shortcut = strings.TrimSpace(shortcut)
			if shortcut == "" || seen[shortcut] {
				continue
			}
			seen[shortcut] = true
			merged = append(merged, shortcut)
		}
	}
	sort.Strings(merged)
	return merged
}

// Plugin menu entries are actions too, but their lifetime is controlled by a
// plugin registration rather than by the built-in action registry. Keeping a
// separate namespace lets hotkeys.ini refer to them without leaving stale
// action.Action values behind when an RPC plugin disconnects.
func PluginCommandActionName(id string) string { return "Plugin.Command." + id }

func LegacyPluginActionName(index int) string {
	return "Plugin.Legacy." + strconv.Itoa(index)
}

func ConfiguredHotkeyAction(hm *HotkeyManager, area, key string) string {
	if hm == nil {
		return ""
	}
	action := hm.GetAction(area, key)
	if !strings.HasPrefix(key, "RCtrl") {
		return action
	}

	plainKey := "Ctrl" + strings.TrimPrefix(key, "RCtrl")
	rctrlExplicit := hm.hasExplicitBinding(area, key)
	if rctrlExplicit && action != "" && !strings.EqualFold(action, "none") {
		return action
	}
	if hm.hasExplicitBinding(area, plainKey) {
		if plainAction := hm.GetAction(area, plainKey); plainAction != "" {
			return plainAction
		}
	}
	if rctrlExplicit {
		// The RCtrl spelling was explicitly unbound (or its condition is not
		// met): it no longer claims the key, so Right Ctrl acts as plain Ctrl.
		return hm.GetAction(area, plainKey)
	}
	if action != "" {
		return action
	}
	return hm.GetAction(area, plainKey)
}

// LookupCondition returns the check registered under name.
func LookupCondition(name string) (func() bool, bool) {
	fn, ok := conditionRegistry[strings.ToLower(strings.TrimSpace(name))]
	return fn, ok
}

// SetCondition installs a check and returns the one it replaced, so a caller
// that borrows a condition can put the original back. A nil fn removes the
// entry, which is what "there was nothing here before" has to restore to: a
// registered condition that always answers the same is not the same as no
// condition at all, because an unknown name reads as true.
func SetCondition(name string, fn func() bool) (func() bool, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	previous, existed := conditionRegistry[key]
	if fn == nil {
		delete(conditionRegistry, key)
	} else {
		conditionRegistry[key] = fn
	}
	return previous, existed
}
