package settings

import (
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/termthemes"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtui"
)

// The theme list above the colour fields of Terminal & environment → Colors
// (f4#2071). "Default" is f4's own palette, then come the themes of
// termthemes, and "Custom" is what the fields show when they match none of
// them: it is never picked by hand, any edit of a colour field selects it.
// The list is not stored: the colour fields are, and the list shows whichever
// set they spell out, so a saved theme comes back by name.
const (
	terminalThemeID      = "TerminalTheme"
	terminalThemeDefault = "default"
	terminalThemeCustom  = "custom"
)

// terminalThemeChoices is the content of the list.
func terminalThemeChoices() []f4settings.Choice {
	choices := []f4settings.Choice{{Value: terminalThemeDefault, Label: f4settings.Text{English: "Default", Literal: true}}}
	for _, t := range termthemes.All() {
		choices = append(choices, f4settings.Choice{Value: t.Name, Label: f4settings.Text{English: t.Name, Literal: true}})
	}
	return append(choices, f4settings.Choice{Value: terminalThemeCustom, Label: f4settings.Text{English: "Custom", Literal: true}})
}

// terminalThemeOf names the colour set that get reads for the 21 colour
// keys: "default" when every field is empty or f4's own colour, the name of a
// theme with exactly these colours, else "custom".
func terminalThemeOf(get func(key string) string) string {
	isDefault := true
	values := make(map[string]string, 21)
	for i, key := range config.AnsiColorKeys {
		v := get(key)
		values[key] = v
		if rgb, ok := config.ParseAnsiColor(v); strings.TrimSpace(v) != "" && (!ok || rgb != config.DefaultAnsiColors[i]) {
			isDefault = false
		}
	}
	for _, key := range config.TerminalColorKeys {
		v := get(key)
		values[key] = v
		if strings.TrimSpace(v) != "" {
			isDefault = false
		}
	}
	if isDefault {
		return terminalThemeDefault
	}
	if name := termthemes.Match(values); name != "" {
		return name
	}
	return terminalThemeCustom
}

func isTerminalColorField(id string) bool {
	return config.AnsiColorIndex(id) >= 0 || config.TerminalColorKey(id)
}

// applyTerminalTheme fills the colour fields of the draft with the theme
// the list now shows. "Custom" keeps whatever the fields hold.
func (c *settingsCenter) applyTerminalTheme(name string) {
	if name == terminalThemeCustom {
		return
	}
	if name == terminalThemeDefault {
		c.resetTerminalColors()
		return
	}
	theme, ok := termthemes.Find(name)
	if !ok {
		return
	}
	for _, s := range c.sessions {
		if s.catalog.ID != "core" || s.draft == nil {
			continue
		}
		for key, value := range theme.Values {
			s.draft.Values[key] = value
		}
		s.draft.Values[terminalThemeID] = name
	}
	c.rebuildCategory()
}

// syncTerminalTheme runs after a colour field was edited: the list moves to
// the theme the fields now spell out, or to Custom.
func (c *settingsCenter) syncTerminalTheme() {
	for _, s := range c.sessions {
		if s.catalog.ID != "core" || s.draft == nil {
			continue
		}
		name := terminalThemeOf(func(key string) string { return s.draft.Values[key] })
		if s.draft.Values[terminalThemeID] == name {
			continue
		}
		s.draft.Values[terminalThemeID] = name
		for _, r := range c.page.rows {
			combo, ok := r.control.(*vtui.ComboBox)
			if !ok || r.field.ID != terminalThemeID {
				continue
			}
			for i, ch := range r.field.Choices {
				if ch.Value == name {
					combo.Menu.SetSelectPos(i)
					combo.Edit.SetText(ch.Label.English)
				}
			}
		}
	}
}
