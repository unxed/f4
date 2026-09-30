package keymap

import (
	"testing"

	"github.com/unxed/f4/internal/action"
)

// Chords with two modifiers get their own key bar rows, filled from the
// bindings and nothing else (f4#1704): Alt+Shift+F3 is not the Shift+F3 row.
func TestKeyBarLabelsForArea_CombinedModifierRows(t *testing.T) {
	restore := action.Snapshot()
	defer restore()

	for name, key := range map[string]string{
		"Test.Combined.AltShiftF3":  "AltShiftF3",
		"Test.Combined.CtrlShiftF5": "CtrlShiftF5",
		"Test.Combined.CtrlAltF7":   "CtrlAltF7",
		"Test.Combined.ShiftF3":     "ShiftF3",
	} {
		action.RegisterAction(action.Action{
			Name:        name,
			Area:        "TestKeyBarCombinedArea",
			Label:       name,
			DefaultKeys: []string{key},
			Handler:     func() bool { return true },
		})
	}

	oldHm := GlobalHotkeysMgr
	oldLookup := LookupAction
	t.Cleanup(func() {
		GlobalHotkeysMgr = oldHm
		LookupAction = oldLookup
	})
	LookupAction = action.Lookup
	GlobalHotkeysMgr = NewHotkeyManager("")

	set := KeyBarLabelsForArea("TestKeyBarCombinedArea", nil)
	if got := set.AltShift[2]; got != "Test.Combined.AltShiftF3" {
		t.Errorf("AltShift F3 = %q", got)
	}
	if got := set.CtrlShift[4]; got != "Test.Combined.CtrlShiftF5" {
		t.Errorf("CtrlShift F5 = %q", got)
	}
	if got := set.CtrlAlt[6]; got != "Test.Combined.CtrlAltF7" {
		t.Errorf("CtrlAlt F7 = %q", got)
	}
	if got := set.Shift[2]; got != "Test.Combined.ShiftF3" {
		t.Errorf("Shift F3 = %q, the single-modifier row must be unchanged", got)
	}
	if set.AltShift[3] != "" || set.CtrlAlt[2] != "" {
		t.Error("an unbound combined slot must stay empty")
	}
}
