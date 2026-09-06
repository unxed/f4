package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// GlobalKeyRemap holds the user's input remapping table, read from keymap.ini
// in the profile directory. It stays nil until the profile is located, and an
// absent or empty file leaves every keystroke untouched.
var GlobalKeyRemap *KeyRemap

const (
	// keyRemapWildcard ends both sides of a rule that rewrites a modifier
	// prefix ("CtrlAlt*=Ctrl*") instead of one whole key.
	keyRemapWildcard = "*"
	// keyRemapCommonSection applies to every area, exactly like the Common
	// section of hotkeys.ini.
	keyRemapCommonSection = "Common"
)

// keyRemapPrefixRule rewrites the leading modifiers of a key spelling. from is
// stored lower-cased for matching, to in the canonical spelling ParseFarKey
// expects.
type keyRemapPrefixRule struct {
	from string
	to   string
}

// KeyRemap substitutes one key for another before anything else in f4 looks
// at the event. It exists for terminals f4 does not have to itself: a
// multiplexer (tmux, zellij, screen, dvtm) claims its own chords upstream, so
// keys such as Ctrl+B, Ctrl+A, Ctrl+O or Ctrl+P may never arrive, and small
// keyboards have no F-row to send F1-F12 with at all. Both are the same
// problem seen from the input side, and the hotkey manager cannot solve
// either one on its own: it binds actions, while frameworks, dialogs, menus
// and the frames themselves also read keys directly.
//
// Sections are area names, as in hotkeys.ini; Common applies everywhere. A
// rule maps the spelling shown in the Hotkey Configurator to the spelling f4
// should see instead:
//
//	[Common]
//	CtrlAltO=CtrlO    ; Ctrl+Alt+O now toggles the panels
//	Alt1=F1           ; a keyboard without an F-row
//	CtrlAlt*=Ctrl*    ; every Ctrl chord also answers to Ctrl+Alt
//
// Substitution happens once per keystroke: the result is never fed back
// through the table, so rules cannot chain or loop.
type KeyRemap struct {
	// Exact maps an area to lower-cased source spellings and their canonical
	// replacements.
	Exact map[string]map[string]string
	// Prefix holds the wildcard rules of an area, longest source first so
	// that CtrlAltShift* wins over CtrlAlt*.
	Prefix map[string][]keyRemapPrefixRule

	iniPath string
}

// NewKeyRemap reads keymap.ini. A missing file is not an error: it yields an
// empty table that Apply short-circuits on.
func NewKeyRemap(iniPath string) *KeyRemap {
	kr := &KeyRemap{iniPath: iniPath}
	kr.Load()
	return kr
}

// Load re-reads the table from disk, discarding what was held before.
func (kr *KeyRemap) Load() {
	if kr == nil {
		return
	}
	kr.Exact = make(map[string]map[string]string)
	kr.Prefix = make(map[string][]keyRemapPrefixRule)
	if kr.iniPath == "" {
		return
	}
	ini := LoadIni(kr.iniPath)
	for area, rules := range ini.data {
		for source, target := range rules {
			kr.addRule(area, source, target)
		}
	}
	for area := range kr.Prefix {
		rules := kr.Prefix[area]
		sort.SliceStable(rules, func(i, j int) bool {
			if len(rules[i].from) != len(rules[j].from) {
				return len(rules[i].from) > len(rules[j].from)
			}
			return rules[i].from < rules[j].from
		})
		kr.Prefix[area] = rules
	}
}

// IsEmpty reports whether the table can be skipped entirely. Every keystroke
// passes through this check, so the common case of no keymap.ini must cost
// nothing.
func (kr *KeyRemap) IsEmpty() bool {
	return kr == nil || (len(kr.Exact) == 0 && len(kr.Prefix) == 0)
}

func (kr *KeyRemap) addRule(area, source, target string) {
	area = strings.TrimSpace(area)
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	if area == "" || source == "" {
		return
	}
	// f4's INI reader has no notion of comments, so a commented-out sample
	// line still arrives here with its marker attached. Dropping it keeps the
	// shipped keymap.ini self-documenting instead of registering rules no
	// keystroke can match.
	if strings.HasPrefix(source, ";") || strings.HasPrefix(source, "#") {
		return
	}

	sourceWild := strings.HasSuffix(source, keyRemapWildcard)
	targetWild := strings.HasSuffix(target, keyRemapWildcard)
	if sourceWild != targetWild {
		// One side alone is meaningless: "Ctrl*=F1" would collapse every Ctrl
		// chord onto a single key, and "CtrlB=Ctrl*" names no key at all.
		return
	}

	if sourceWild {
		from := strings.ToLower(strings.TrimSuffix(source, keyRemapWildcard))
		to := canonicalKeySpelling(strings.TrimSuffix(target, keyRemapWildcard))
		if from == "" || strings.EqualFold(from, to) {
			// An empty source prefix would match every key, and a prefix that
			// rewrites to itself is a no-op.
			return
		}
		kr.Prefix[area] = append(kr.Prefix[area], keyRemapPrefixRule{from: from, to: to})
		return
	}

	if target == "" || strings.EqualFold(source, target) {
		return
	}
	if kr.Exact[area] == nil {
		kr.Exact[area] = make(map[string]string)
	}
	kr.Exact[area][strings.ToLower(source)] = canonicalKeySpelling(target)
}

// keyRemapModifiers lists the modifier prefixes a key spelling may carry, in
// the order EventToFarString writes them.
var keyRemapModifiers = []string{"RCtrl", "Ctrl", "Alt", "Shift"}

// canonicalKeySpelling rewrites a hand-written rule into the casing
// ParseFarKey and EventToFarString use, so that "ctrlaltf5" and "CtrlAltF5"
// mean the same thing in keymap.ini.
func canonicalKeySpelling(key string) string {
	rest := strings.TrimSpace(key)
	var sb strings.Builder
	for rest != "" {
		matched := false
		for _, modifier := range keyRemapModifiers {
			if len(rest) > len(modifier) && strings.EqualFold(rest[:len(modifier)], modifier) {
				sb.WriteString(modifier)
				rest = rest[len(modifier):]
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	sb.WriteString(canonicalKeyToken(rest))
	return sb.String()
}

func canonicalKeyToken(key string) string {
	if len(key) > 3 && strings.EqualFold(key[:3], "VK_") {
		return "VK_" + strings.ToUpper(key[3:])
	}
	for _, name := range farKeyNames {
		if strings.EqualFold(key, name) {
			return name
		}
	}
	if len(key) >= 2 && (key[0] == 'F' || key[0] == 'f') {
		if n, err := strconv.Atoi(key[1:]); err == nil && n >= 1 && n <= 24 {
			return "F" + strconv.Itoa(n)
		}
	}
	if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
		return strings.ToUpper(key)
	}
	return key
}

// Resolve returns the spelling that replaces key in area, or "" when no rule
// applies. Exact rules win over wildcard ones, and an area wins over Common,
// mirroring how HotkeyManager resolves a binding.
func (kr *KeyRemap) Resolve(area, key string) string {
	if kr.IsEmpty() || key == "" {
		return ""
	}

	// Far treats both Ctrl keys as one, and so does the hotkey dispatcher
	// unless something is bound on the RCtrl spelling specifically. A rule
	// written for Ctrl therefore also answers Right Ctrl.
	candidates := []string{key}
	if strings.HasPrefix(key, "RCtrl") {
		candidates = append(candidates, "Ctrl"+strings.TrimPrefix(key, "RCtrl"))
	}

	areas := []string{area}
	if !strings.EqualFold(area, keyRemapCommonSection) {
		areas = append(areas, keyRemapCommonSection)
	}

	for _, candidateArea := range areas {
		for _, candidate := range candidates {
			if target, ok := kr.Exact[candidateArea][strings.ToLower(candidate)]; ok {
				return target
			}
		}
	}
	for _, candidateArea := range areas {
		for _, candidate := range candidates {
			if target := kr.prefixTarget(candidateArea, candidate); target != "" {
				return target
			}
		}
	}
	return ""
}

func (kr *KeyRemap) prefixTarget(area, key string) string {
	lower := strings.ToLower(key)
	for _, rule := range kr.Prefix[area] {
		// The rule must leave a key behind, not just its modifiers.
		if len(lower) > len(rule.from) && strings.HasPrefix(lower, rule.from) {
			return rule.to + key[len(rule.from):]
		}
	}
	return ""
}

// keyRemapMods are the event flags a substitution owns. Everything else in
// ControlKeyState (lock states in particular) describes the keyboard, not the
// chord, and is carried over untouched.
const keyRemapMods = vtinput.ShiftPressed | vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed |
	vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.EnhancedKey

// Apply rewrites e in place when a rule matches, and reports whether it did.
// Both key down and key up are rewritten, so a frame that pairs them sees one
// consistent key.
func (kr *KeyRemap) Apply(area string, e *vtinput.InputEvent) bool {
	if kr.IsEmpty() || e == nil || e.Type != vtinput.KeyEventType {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU:
		// A modifier pressed on its own is not a chord. Rewriting it would
		// desynchronize the key bar and the terminal forwarder, both of which
		// track these keys separately.
		return false
	}

	source := EventToHotkeyString(e)
	target := kr.Resolve(area, source)
	if target == "" || strings.EqualFold(target, source) {
		return false
	}
	mapped := ParseFarKey(target)
	if mapped == nil || (mapped.VirtualKeyCode == 0 && mapped.Char == 0) {
		return false
	}

	e.VirtualKeyCode = mapped.VirtualKeyCode
	// The scan code described the physical key that was actually pressed;
	// after a substitution it belongs to no key at all. Injected macro events
	// carry none either.
	e.VirtualScanCode = 0
	e.Char = mapped.Char
	e.UnshiftedChar = mapped.Char
	e.ControlKeyState = (e.ControlKeyState &^ keyRemapMods) | (mapped.ControlKeyState & keyRemapMods)
	vtui.DebugLog("KEYREMAP: %s -> %s in area %s", source, target, area)
	return true
}

// defaultKeymapIni ships as pure documentation: every rule is commented out,
// so loading it changes nothing until the user removes a semicolon.
const defaultKeymapIni = `; Key remapping for f4.
;
; A rule substitutes one key for another before f4 looks at the event:
;
;     <key f4 should see instead>
;     ^
;     PressedKey=TargetKey
;
; Spell both sides the way the Hotkey Configurator (Options -> Key
; bindings) shows them: Ctrl, Alt, Shift and RCtrl prefixes in that
; order, then the key itself (A, 5, F7, Enter, Ins, PgDn, VK_DC ...).
; Case does not matter. Section names are the areas of hotkeys.ini;
; Common applies to all of them.
;
; Substitution happens once per keystroke, so rules never chain.
; It is also skipped while a full-screen or busy program (vim, htop,
; mc) owns the terminal, because those keys belong to that program.
;
; --- Terminal multiplexers -------------------------------------------
;
; tmux, zellij, screen and dvtm claim their own chords before f4 ever
; sees them: Ctrl+B (tmux), Ctrl+A (screen), and Ctrl+P, Ctrl+T,
; Ctrl+N, Ctrl+O, Ctrl+G, Ctrl+Q (zellij). Give the affected f4
; commands a second key here.
;
;[Common]
;CtrlAltO=CtrlO      ; console view, when zellij eats Ctrl+O
;CtrlAltB=CtrlB      ; toggle the key bar, when tmux eats Ctrl+B
;CtrlAltP=CtrlP      ; command palette, when zellij eats Ctrl+P
;
; A trailing * on both sides rewrites the modifiers of every key at
; once, which is the one-line way to move f4 off a prefix the
; multiplexer wants. Longer prefixes are matched first.
;
;[Common]
;CtrlAlt*=Ctrl*      ; every Ctrl chord also answers to Ctrl+Alt
;
; --- Keyboards without an F-row --------------------------------------
;
;[Common]
;Alt1=F1
;Alt2=F2
;Alt3=F3
;Alt4=F4
;Alt5=F5
;Alt6=F6
;Alt7=F7
;Alt8=F8
;Alt9=F9
;Alt0=F10
;AltShift1=ShiftF1   ; the Shift/Alt/Ctrl F-key rows work the same way
`

// createDefaultKeymapIni writes the commented sample file on first start.
// A failure is not worth reporting: the feature is optional and an absent
// file simply means no remapping.
func createDefaultKeymapIni(path string) {
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(defaultKeymapIni), 0644)
}

// keyRemapSuspended reports whether a foreign application currently owns the
// keyboard. With the panels hidden and an AltScreen program or a busy child
// running, every key is forwarded to it verbatim; substituting there would
// send vim or htop a chord the user never pressed. This is the same handover
// the noaltscreenapp and noterminalapp hotkey conditions respect.
func keyRemapSuspended() bool {
	if vtui.FrameManager == nil {
		return false
	}
	pf, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame)
	if !ok || pf.showPanels {
		return false
	}
	if pf.shellMode == ShellModeSimpleInline {
		// No PTY in this mode, so no foreign program can be holding the
		// keyboard; the console view on screen is f4's own overlay.
		return false
	}
	return (pf.termView != nil && pf.termView.UseAltScreen) || pf.isPtyBusy()
}

// applyKeyRemap performs the substitution for a live keystroke and keeps the
// key bar honest about it. It is the only entry point the input filter uses.
func applyKeyRemap(area string, e *vtinput.InputEvent) bool {
	kr := GlobalKeyRemap
	if kr.IsEmpty() || keyRemapSuspended() {
		return false
	}
	if !kr.Apply(area, e) {
		return false
	}
	syncKeyBarModifiers(e)
	return true
}

// syncKeyBarModifiers re-derives the key bar's modifier row from a rewritten
// event. vtui sets that row from the raw event just before calling the input
// filter, so without this the bar would still advertise the Alt labels while
// an "Alt1=F1" rule runs the plain F1 command.
func syncKeyBarModifiers(e *vtinput.InputEvent) {
	if vtui.FrameManager == nil || vtui.FrameManager.KeyBar == nil {
		return
	}
	vtui.FrameManager.KeyBar.SetModifiers(
		e.ControlKeyState&vtinput.ShiftPressed != 0,
		e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0,
		e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0,
	)
}
