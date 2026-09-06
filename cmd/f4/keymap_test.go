package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/vtinput"
)

func newTestKeyRemap(t *testing.T, content string) *KeyRemap {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keymap.ini")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return NewKeyRemap(path)
}

func TestKeyRemap_MissingFileIsEmpty(t *testing.T) {
	kr := NewKeyRemap(filepath.Join(t.TempDir(), "keymap.ini"))
	if !kr.IsEmpty() {
		t.Fatalf("a missing keymap.ini must produce an empty table")
	}
	if got := kr.Resolve("Shell", "CtrlO"); got != "" {
		t.Fatalf("empty table resolved %q", got)
	}
}

func TestKeyRemap_ExactRulesAndAreaPrecedence(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
CtrlAltO=CtrlO
alt1=f1
[Editor]
CtrlAltO=CtrlS
`)

	if got := kr.Resolve("Shell", "CtrlAltO"); got != "CtrlO" {
		t.Errorf("Common rule in Shell: got %q, want CtrlO", got)
	}
	if got := kr.Resolve("Editor", "CtrlAltO"); got != "CtrlS" {
		t.Errorf("area rule must win over Common: got %q, want CtrlS", got)
	}
	// Case is irrelevant on both sides, but the stored target has to be
	// spelled the way ParseFarKey expects.
	if got := kr.Resolve("Shell", "Alt1"); got != "F1" {
		t.Errorf("lower-case rule: got %q, want F1", got)
	}
	if got := kr.Resolve("Shell", "CtrlP"); got != "" {
		t.Errorf("unmapped key resolved to %q", got)
	}
}

func TestKeyRemap_RCtrlFallsBackToCtrlRule(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
CtrlAltO=CtrlO
`)
	if got := kr.Resolve("Shell", "RCtrlAltO"); got != "CtrlO" {
		t.Errorf("Right Ctrl should match the Ctrl rule: got %q", got)
	}
}

func TestKeyRemap_PrefixRules(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
CtrlAlt*=Ctrl*
CtrlAltShift*=Alt*
`)

	if got := kr.Resolve("Shell", "CtrlAltF5"); got != "CtrlF5" {
		t.Errorf("prefix rewrite: got %q, want CtrlF5", got)
	}
	// The longer source prefix has to be tried first, whatever order the INI
	// file listed the rules in.
	if got := kr.Resolve("Shell", "CtrlAltShiftF5"); got != "AltF5" {
		t.Errorf("longest prefix must win: got %q, want AltF5", got)
	}
	// A prefix rule must leave a key behind, not just its modifiers.
	if got := kr.Resolve("Shell", "CtrlAlt"); got != "" {
		t.Errorf("bare modifier prefix resolved to %q", got)
	}
}

func TestKeyRemap_ExactRuleWinsOverPrefixRule(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
CtrlAlt*=Ctrl*
CtrlAltO=F9
`)
	if got := kr.Resolve("Shell", "CtrlAltO"); got != "F9" {
		t.Errorf("exact rule must win over the wildcard: got %q, want F9", got)
	}
}

func TestKeyRemap_IgnoresUnusableRules(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
;CtrlAltO=CtrlO
#AltP=CtrlP
Ctrl*=F1
CtrlB=Ctrl*
*=CtrlB
CtrlU=CtrlU
CtrlY=
`)
	if !kr.IsEmpty() {
		t.Fatalf("no rule in this file is usable, got %+v / %+v", kr.Exact, kr.Prefix)
	}
}

func TestKeyRemap_ApplyRewritesEventInPlace(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
Alt1=F1
`)
	e := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_1,
		VirtualScanCode: 2,
		Char:            '1',
		ControlKeyState: vtinput.LeftAltPressed | vtinput.NumLockOn,
	}

	if !kr.Apply("Shell", e) {
		t.Fatalf("Alt+1 was not remapped")
	}
	if e.VirtualKeyCode != vtinput.VK_F1 {
		t.Errorf("virtual key: got %d, want %d", e.VirtualKeyCode, vtinput.VK_F1)
	}
	if e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0 {
		t.Errorf("the Alt modifier survived the substitution: %v", e.ControlKeyState)
	}
	if e.ControlKeyState&vtinput.NumLockOn == 0 {
		t.Errorf("lock state describes the keyboard and must be preserved")
	}
	if e.VirtualScanCode != 0 {
		t.Errorf("scan code of the physical key must be cleared, got %d", e.VirtualScanCode)
	}
	if !e.KeyDown || e.Type != vtinput.KeyEventType {
		t.Errorf("event kind must be preserved")
	}
	if got := EventToHotkeyString(e); got != "F1" {
		t.Errorf("rewritten event spells as %q, want F1", got)
	}
}

func TestKeyRemap_ApplyDoesNotChain(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
AltO=CtrlO
CtrlO=F9
`)
	e := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  'O',
		ControlKeyState: vtinput.LeftAltPressed,
	}
	if !kr.Apply("Shell", e) {
		t.Fatalf("Alt+O was not remapped")
	}
	if got := EventToHotkeyString(e); got != "CtrlO" {
		t.Fatalf("substitution must happen once, got %q, want CtrlO", got)
	}
}

func TestKeyRemap_ApplyLeavesBareModifiersAlone(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
Ctrl*=Alt*
`)
	e := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_CONTROL,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	if kr.Apply("Shell", e) {
		t.Fatalf("a modifier pressed on its own is not a chord and must not be rewritten")
	}
}

func TestKeyRemap_ApplyIgnoresNonKeyEvents(t *testing.T) {
	kr := newTestKeyRemap(t, `[Common]
Alt1=F1
`)
	e := &vtinput.InputEvent{Type: vtinput.MouseEventType}
	if kr.Apply("Shell", e) {
		t.Fatalf("mouse events must pass through untouched")
	}
}

func TestKeyRemap_DefaultIniIsInert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keymap.ini")
	createDefaultKeymapIni(path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default keymap.ini was not written: %v", err)
	}
	if kr := NewKeyRemap(path); !kr.IsEmpty() {
		t.Fatalf("the shipped sample file must remap nothing: %+v / %+v", kr.Exact, kr.Prefix)
	}
}
