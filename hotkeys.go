package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/vtui"
)

// HotkeyManager handles mapping of key combinations to application actions.
type HotkeyManager struct {
	Bindings map[string]map[string]string // Area -> Key -> ActionName
	Defaults map[string]map[string]string // Area -> Key -> ActionName
	iniPath  string
}

var conditionRegistry = map[string]func() bool{
	"emptycommandline": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			return pf.cmdLine.IsEmpty()
		}
		return false
	},
	"commandlinenotempty": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			return !pf.cmdLine.IsEmpty()
		}
		return false
	},
	"esctoggle": func() bool {
		if !AppConfig.EscTogglePanels {
			return false
		}
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			if !pf.cmdLine.IsEmpty() {
				return false
			}
			if pf.showPanels {
				return true
			}
			return !pf.termView.UseAltScreen && !pf.isPtyBusy()
		}
		return false
	},
	// noaltscreenapp gates keys that must reach an interactive AltScreen
	// application (mc, htop) instead of triggering f4's own actions.
	"noaltscreenapp": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			return pf.showPanels || !pf.termView.UseAltScreen
		}
		return false
	},
	// noterminalapp is the stricter sibling of noaltscreenapp: it also
	// stands down for a plain child process that is merely busy (a shell
	// command, a REPL). With the panels hidden such a process owns the
	// keyboard and the command line is not even drawn, so actions that
	// type into it must not fire. With the panels shown nothing is in the
	// way, which keeps the Shell binding of such a key unconditional.
	"noterminalapp": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			return pf.showPanels || (!pf.termView.UseAltScreen && !pf.isPtyBusy())
		}
		return false
	},
	// terminalquiet reports a hidden-panels terminal with no AltScreen app
	// and no busy PTY, so F3/F4 may open the terminal log instead of
	// being forwarded to the running application.
	"terminalquiet": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			return !pf.termView.UseAltScreen && !pf.isPtyBusy()
		}
		return false
	},
	// altpanelvisible reports that an info or quick-view panel is shown,
	// gating the plain-letter toggles that belong to those panels.
	"altpanelvisible": func() bool {
		if pf := findPanelsFrameAnyScreen(); pf != nil {
			for _, a := range pf.altPanels {
				if a != nil && (a.Kind() == "info" || a.Kind() == "quick_view") {
					return true
				}
			}
		}
		return false
	},
}

// GetConditions returns the user-friendly names of all registered conditions.
func GetConditions() []string {
	return []string{"None", "EmptyCommandLine", "CommandLineNotEmpty", "EscToggle", "TerminalQuiet", "AltPanelVisible", "NoAltScreenApp", "NoTerminalApp"}
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
		iniPath:  iniPath,
	}
	hm.initDefaults()
	hm.Load()
	return hm
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
	if binds, ok := hm.Bindings[area]; ok {
		for key, binding := range binds {
			parts := strings.SplitN(binding, ":", 2)
			if strings.EqualFold(parts[0], actionName) {
				return key
			}
		}
	}
	if area != "Common" {
		if binds, ok := hm.Bindings["Common"]; ok {
			for key, binding := range binds {
				parts := strings.SplitN(binding, ":", 2)
				if strings.EqualFold(parts[0], actionName) {
					return key
				}
			}
		}
	}
	return ""
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
		parts = append(parts, key)
	}
	return strings.Join(parts, "+")
}

// initDefaults builds the default bindings from the action registry.
// The registry is the single source of truth: every action carrying
// DefaultKeys gets them bound in its Area (plus any DefaultAreas).
// A key entry may carry a ":Condition" suffix (e.g. "Esc:EscToggle").
func (hm *HotkeyManager) initDefaults() {
	hm.Defaults = make(map[string]map[string]string)
	for _, a := range GetOrderedActions() {
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
	hm.Bindings = make(map[string]map[string]string)

	// Copy defaults
	for area, binds := range hm.Defaults {
		hm.Bindings[area] = make(map[string]string)
		for k, v := range binds {
			hm.Bindings[area][k] = v
		}
	}

	if hm.iniPath == "" {
		return
	}

	ini := LoadIni(hm.iniPath)
	for area, binds := range ini.data {
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
}

// Save writes only overridden or new bindings to the INI file.
func (hm *HotkeyManager) Save() {
	if hm.iniPath == "" {
		return
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
			sb.WriteString(fmt.Sprintf("[%s]\n", area))
			for key, action := range diffs {
				sb.WriteString(fmt.Sprintf("%s=%s\n", key, action))
			}
			sb.WriteString("\n")
		}
	}

	os.MkdirAll(filepath.Dir(hm.iniPath), 0755)
	os.WriteFile(hm.iniPath, []byte(sb.String()), 0644)
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

	if binds, ok := hm.Bindings[area]; ok {
		if binding, ok := binds[key]; ok {
			if action := evalBinding(binding); action != "" {
				return action
			}
		}
	}
	if area != "Common" {
		if binds, ok := hm.Bindings["Common"]; ok {
			if binding, ok := binds[key]; ok {
				if action := evalBinding(binding); action != "" {
					return action
				}
			}
		}
	}
	return ""
}

// Bind assigns an action to a key in a specific area.
func (hm *HotkeyManager) Bind(area, key, action string) {
	if hm.Bindings[area] == nil {
		hm.Bindings[area] = make(map[string]string)
	}
	hm.Bindings[area][key] = action
}

// Unbind removes a hotkey binding.
func (hm *HotkeyManager) Unbind(area, key string) {
	if binds, ok := hm.Bindings[area]; ok {
		delete(binds, key)
	}
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
				if act, ok := GetAction(actName); ok {
					return plainLabel(act.DisplayLabel())
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
