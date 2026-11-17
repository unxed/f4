package theme

import (
	"github.com/unxed/f4/internal/ini"
	"strings"
	"testing"
)

// TestBuiltInThemesCoverPanelGroup guards against the class of bug
// reported in #376: a new palette entry was added to ColorSlots but
// modern.ini (or another dark-first theme) forgot to override it, so
// the built-in classic default (bright cyan/yellow on blue) leaked
// through and painted a single field in classic colours over an
// otherwise-dark
//
// For every non-classic built-in theme, every entry in the Panel group
// must be defined by the theme — either under its canonical name or
// via one of its aliases. Classic is exempt because it deliberately
// inherits from f4's built-in palette (see the header comment in
// styles/classic.ini) and only overrides where the built-in defaults
// don't already match the classic look.
func TestBuiltInThemesCoverPanelGroup(t *testing.T) {
	styles := AvailableColorStyles()
	for _, style := range styles {
		if style.custom || strings.EqualFold(style.Name, "Classic") {
			continue
		}
		var missing []string
		for _, slot := range ColorSlots {
			if slot.Group != "Panel" {
				continue
			}
			if !slotDefinedInIni(style.ini, slot) {
				missing = append(missing, slot.Canonical)
			}
		}
		if len(missing) > 0 {
			t.Errorf("theme %q is missing Panel-group overrides for: %s\n"+
				"    each such entry falls back to the classic built-in palette "+
				"(bright cyan/yellow on blue) and shows up as a stray classic-\n"+
				"    coloured field over an otherwise-themed panel — that is the "+
				"exact bug #376 reported for Panel.Info.Total / Panel.Info.Selected.",
				style.Name, strings.Join(missing, ", "))
		}
	}
}

// Every non-classic built-in style is a complete farcolors.ini that can be
// exported and edited without silently falling back to the classic palette.
// Classic deliberately remains a sparse inheritance style.
func TestBuiltInThemesCoverAllColorSlots(t *testing.T) {
	for _, style := range AvailableColorStyles() {
		if style.custom || strings.EqualFold(style.Name, "Classic") {
			continue
		}
		for _, slot := range ColorSlots {
			// Terminal colors were added after the built-in themes were
			// authored. They are optional in legacy themes and use the
			// application defaults until a theme explicitly defines them.
			if isTerminalThemeSlot(slot) {
				continue
			}
			if !slotDefinedInIni(style.ini, slot) {
				t.Errorf("theme %q is missing %s in [farcolors]", style.Name, slot.Canonical)
			}
		}
	}
}

func isTerminalThemeSlot(slot ColorSlot) bool {
	return strings.HasPrefix(slot.Canonical, "Terminal.Text") || strings.HasPrefix(slot.Canonical, "Terminal.Ansi.")
}

// slotDefinedInIni reports whether the given colour slot has any entry
// in the theme's [farcolors] section, checking the canonical key first
// and then each alias.
func slotDefinedInIni(ini *ini.File, slot ColorSlot) bool {
	const sentinel = "\x00missing\x00"
	if ini.GetString("farcolors", slot.Canonical, sentinel) != sentinel {
		return true
	}
	for _, alias := range slot.Aliases {
		if ini.GetString("farcolors", alias, sentinel) != sentinel {
			return true
		}
	}
	return false
}
