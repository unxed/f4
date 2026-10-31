package app

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
)

// A quick search started with Alt+F goes on after a switch to the Russian
// layout, with or without Alt held (#2186): the Russian layout's O key types
// щ, and "fщ" finds "foo" as "fo". The character used to be dropped without
// a sign.
func TestQuickSearchGoesOnAfterLayoutSwitch(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods vtinput.ControlKeyState
	}{
		{"plain", 0},
		{"Alt", vtinput.LeftAltPressed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := config.App
			t.Cleanup(func() { config.App = old })
			config.App.PanelQuickSearchXLat = true

			pf := seedPanelForRestore(t, []string{"far", "foo"})
			fsp := pf.GetActivePanel()
			pressKey(pf, charKey(vtinput.VK_F, 'f', vtinput.LeftAltPressed))
			if got := fsp.GetSelectedName(); got != "far" {
				t.Fatalf("setup: Alt+F put the cursor on %q, want %q", got, "far")
			}
			pressKey(pf, charKey(vtinput.VK_O, 'щ', tc.mods))
			if !fsp.FastFindMode || fsp.FastFindStr != "fщ" {
				t.Fatalf("after щ the search is open=%v with %q, want it open with what was typed, %q", fsp.FastFindMode, fsp.FastFindStr, "fщ")
			}
			if got := fsp.GetSelectedName(); got != "foo" {
				t.Fatalf("after щ the cursor is on %q, want %q", got, "foo")
			}
		})
	}
}

// With "Quick search in the other layout" off, the strict search refuses the
// character of the other layout.
func TestQuickSearchOtherLayoutOff(t *testing.T) {
	old := config.App
	t.Cleanup(func() { config.App = old })
	config.App.PanelQuickSearchXLat = false
	config.App.PanelStrictAutoFilter = true

	pf := seedPanelForRestore(t, []string{"far", "foo"})
	fsp := pf.GetActivePanel()
	pressKey(pf, charKey(vtinput.VK_F, 'f', vtinput.LeftAltPressed))
	pressKey(pf, charKey(vtinput.VK_O, 'щ', 0))
	if fsp.FastFindStr != "f" {
		t.Fatalf("search text = %q, want %q: the option is off", fsp.FastFindStr, "f")
	}
}
