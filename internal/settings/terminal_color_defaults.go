package settings

import "github.com/unxed/f4/internal/config"

// terminalColorDefaultsCommand is the button of the Colors group of Terminal
// & environment that brings every terminal colour back to its default
// (f4#2071). It works on the open draft, not on the applied settings, so the
// fields show the change at once and Apply or Cancel decide on it; its Run
// does nothing and the center does the work (resetTerminalColors).
const terminalColorDefaultsCommand = "terminal.colorDefaults"

// resetTerminalColors puts the default palette into the 16 ANSI colour
// fields of the draft and empties the five fields of the terminal's own
// colours, which then take the theme's.
func (c *settingsCenter) resetTerminalColors() {
	for _, s := range c.sessions {
		if s.catalog.ID != "core" || s.draft == nil {
			continue
		}
		for i, key := range config.AnsiColorKeys {
			s.draft.Values[key] = config.FormatAnsiColor(config.DefaultAnsiColors[i])
		}
		for _, key := range config.TerminalColorKeys {
			s.draft.Values[key] = ""
		}
		s.draft.Values[terminalThemeID] = terminalThemeDefault
	}
	c.rebuildCategory()
}
