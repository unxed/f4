package settings

import (
	"context"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/termthemes"
	"github.com/unxed/vtui"
)

func themeCenter(t *testing.T) (*settingsCenter, *settingsSession) {
	t.Helper()
	core, _ := (coreSettingsProvider{}).Begin(context.Background())
	t.Cleanup(func() { core.Close() })
	ops, _ := (settingsOperationsProvider{}).Begin(context.Background())
	t.Cleanup(func() { ops.Close() })
	s := &settingsSession{catalog: (coreSettingsProvider{}).Catalog(), draft: core}
	c := newSettingsCenter([]*settingsSession{s, {catalog: (settingsOperationsProvider{}).Catalog(), draft: ops}})
	c.selectCategory("terminal")
	return c, s
}

func themeCombo(t *testing.T, c *settingsCenter) *vtui.ComboBox {
	t.Helper()
	for _, r := range c.page.rows {
		if r.field.ID == terminalThemeID {
			combo, ok := r.control.(*vtui.ComboBox)
			if !ok {
				t.Fatalf("the theme control is %T, want a drop-down list", r.control)
			}
			return combo
		}
	}
	t.Fatal("no theme list on the Terminal & environment page")
	return nil
}

func colourEdit(t *testing.T, c *settingsCenter, id string) *settingsEdit {
	t.Helper()
	for _, r := range c.page.rows {
		if r.field.ID == id {
			return r.control.(*settingsEdit)
		}
	}
	t.Fatalf("no field %s", id)
	return nil
}

func pick(t *testing.T, combo *vtui.ComboBox, label string) {
	t.Helper()
	for i, ch := range terminalThemeChoices() {
		if ch.Label.English == label {
			combo.Menu.OnAction(i)
			return
		}
	}
	t.Fatalf("no theme %q in the list", label)
}

func TestThemeListFillsTheFieldsAndFallsBackToCustom(t *testing.T) {
	c, s := themeCenter(t)
	if got := themeCombo(t, c).Edit.GetText(); got != "Default" {
		t.Fatalf("a fresh page shows theme %q, want Default", got)
	}

	pick(t, themeCombo(t, c), "Dracula")
	dracula, _ := termthemes.Find("Dracula")
	for key, want := range dracula.Values {
		if got := s.draft.Values[key]; got != want {
			t.Errorf("after picking Dracula %s = %q, want %q", key, got, want)
		}
	}
	if got := colourEdit(t, c, "AnsiColorRed").GetText(); got != dracula.Values["AnsiColorRed"] {
		t.Errorf("the red field shows %q, want the Dracula one", got)
	}
	if got := themeCombo(t, c).Edit.GetText(); got != "Dracula" {
		t.Errorf("the list shows %q after picking Dracula", got)
	}

	colourEdit(t, c, "AnsiColorRed").OnTextChange("#010203")
	if got := themeCombo(t, c).Edit.GetText(); got != "Custom" {
		t.Errorf("after hand-editing a colour the list shows %q, want Custom", got)
	}
	if got := s.draft.Values[terminalThemeID]; got != terminalThemeCustom {
		t.Errorf("draft theme = %q, want custom", got)
	}

	colourEdit(t, c, "AnsiColorRed").OnTextChange(dracula.Values["AnsiColorRed"])
	if got := themeCombo(t, c).Edit.GetText(); got != "Dracula" {
		t.Errorf("typing the theme's colour back leaves the list at %q, want Dracula", got)
	}

	pick(t, themeCombo(t, c), "Default")
	for i, key := range config.AnsiColorKeys {
		if got := s.draft.Values[key]; got != config.FormatAnsiColor(config.DefaultAnsiColors[i]) {
			t.Errorf("Default left %s = %q", key, got)
		}
	}
	if got := s.draft.Values["TerminalBackground"]; got != "" {
		t.Errorf("Default left TerminalBackground = %q, want empty", got)
	}
	if config.App.AnsiColorRed == dracula.Values["AnsiColorRed"] {
		t.Error("picking a theme changed the applied settings; only Apply should")
	}
}

func TestThemeOfReadsTheStoredColours(t *testing.T) {
	cfg := config.F4Config{}
	if got := coreSettingValue(cfg, terminalThemeID); got != terminalThemeDefault {
		t.Errorf("an empty config is theme %q, want default", got)
	}
	d, _ := termthemes.Find("Github")
	for key, v := range d.Values {
		if err := setCoreSetting(&cfg, key, v); err != nil {
			t.Fatal(err)
		}
	}
	if got := coreSettingValue(cfg, terminalThemeID); got != "Github" {
		t.Errorf("stored Github colours read back as %q", got)
	}
	if err := setCoreSetting(&cfg, "AnsiColorRed", "#010203"); err != nil {
		t.Fatal(err)
	}
	if got := coreSettingValue(cfg, terminalThemeID); got != terminalThemeCustom {
		t.Errorf("an edited set reads as %q, want custom", got)
	}
	if err := setCoreSetting(&cfg, terminalThemeID, "Dracula"); err != nil || cfg.AnsiColorRed != "#010203" {
		t.Errorf("the theme id must not write colours itself: err=%v red=%q", err, cfg.AnsiColorRed)
	}
}
