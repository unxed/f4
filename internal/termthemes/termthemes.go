// Package termthemes holds the terminal colour themes offered by the Colors
// group of Settings Center (f4#2071): a theme is a ready set of the 16 ANSI
// colours plus the embedded terminal's text, background, cursor and selection
// colours. The themes live in themes.md, a list of "## Name" headings each
// followed by "* **key**: #rrggbb" lines, and are embedded into the binary.
package termthemes

import (
	"bufio"
	_ "embed" // themes.md
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/config"
)

//go:embed themes.md
var themesMD string

// Theme is one named colour set. Values is keyed by the settings.ini names
// (config.AnsiColorKeys and config.TerminalColorKeys), each #RRGGBB in
// capitals.
type Theme struct {
	Name   string
	Values map[string]string
}

// keyOf maps the key names of themes.md onto settings keys. cursor_text has
// no counterpart in f4 (its caret has one colour only) and is ignored.
var keyOf = func() map[string]string {
	m := map[string]string{
		"primary_foreground":   "TerminalForeground",
		"primary_background":   "TerminalBackground",
		"cursor":               "TerminalCursorColor",
		"selection_text":       "TerminalSelectionText",
		"selection_background": "TerminalSelectionColor",
	}
	names := [8]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}
	for i, n := range names {
		m["normal_"+n] = config.AnsiColorKeys[i]
		m["bright_"+n] = config.AnsiColorKeys[8+i]
	}
	return m
}()

// Parse reads themes in the format of themes.md. A key it does not know
// (cursor_text) is skipped; a malformed colour, a key before any heading or a
// theme short of a colour is an error.
func Parse(r io.Reader) ([]Theme, error) {
	var themes []Theme
	var cur *Theme
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "## "):
			themes = append(themes, Theme{Name: strings.TrimSpace(line[3:]), Values: map[string]string{}})
			cur = &themes[len(themes)-1]
		case strings.HasPrefix(line, "* **"):
			rest := line[len("* **"):]
			name, value, ok := strings.Cut(rest, "**:")
			if !ok {
				return nil, fmt.Errorf("line %d: expected \"* **key**: #rrggbb\"", n)
			}
			if cur == nil {
				return nil, fmt.Errorf("line %d: colour before the first theme heading", n)
			}
			key, known := keyOf[name]
			if !known {
				continue
			}
			rgb, ok := config.ParseAnsiColor(value)
			if !ok {
				return nil, fmt.Errorf("line %d: %q is not a colour", n, strings.TrimSpace(value))
			}
			cur.Values[key] = config.FormatAnsiColor(rgb)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, t := range themes {
		if len(t.Values) != len(keyOf) {
			return nil, fmt.Errorf("theme %q has %d of %d colours", t.Name, len(t.Values), len(keyOf))
		}
	}
	return themes, nil
}

var (
	once sync.Once
	all  []Theme
)

// All returns the embedded themes in the order of themes.md. A defect in the
// embedded file is a build mistake, so it panics at first use and the tests
// catch it.
func All() []Theme {
	once.Do(func() {
		var err error
		if all, err = Parse(strings.NewReader(themesMD)); err != nil {
			panic("termthemes: themes.md: " + err.Error())
		}
	})
	return all
}

// Find returns the theme called name.
func Find(name string) (Theme, bool) {
	for _, t := range All() {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// Match returns the name of the theme whose colours are exactly values (a map
// keyed like Theme.Values; colours compare as RGB, so case and a missing "#"
// do not matter), or "" when none is.
func Match(values map[string]string) string {
	for _, t := range All() {
		if sameColours(t.Values, values) {
			return t.Name
		}
	}
	return ""
}

func sameColours(theme, values map[string]string) bool {
	for key, want := range theme {
		got, ok := config.ParseAnsiColor(values[key])
		w, _ := config.ParseAnsiColor(want)
		if !ok || got != w {
			return false
		}
	}
	return true
}
