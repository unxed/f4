package termthemes

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
)

func TestEmbeddedThemesAreComplete(t *testing.T) {
	themes := All()
	if len(themes) < 6 {
		t.Fatalf("got %d themes, want the six of themes.md", len(themes))
	}
	seen := map[string]bool{}
	for _, th := range themes {
		if seen[th.Name] {
			t.Errorf("theme %q twice", th.Name)
		}
		seen[th.Name] = true
		for _, key := range append(append([]string{}, config.AnsiColorKeys[:]...), config.TerminalColorKeys[:]...) {
			if _, ok := config.ParseAnsiColor(th.Values[key]); !ok {
				t.Errorf("theme %q: %s = %q is not a colour", th.Name, key, th.Values[key])
			}
		}
	}
	for _, name := range []string{"Dracula", "Github", "Miasma", "Cobalt 2", "Tango Dark", "Retro Green"} {
		if !seen[name] {
			t.Errorf("theme %q is missing", name)
		}
	}
}

func TestDraculaMapping(t *testing.T) {
	d, ok := Find("Dracula")
	if !ok {
		t.Fatal("no Dracula")
	}
	want := map[string]string{
		"TerminalForeground":     "#F8F8F2",
		"TerminalBackground":     "#282A36",
		"TerminalCursorColor":    "#F8F8F2",
		"TerminalSelectionText":  "#F8F8F2",
		"TerminalSelectionColor": "#44475A",
		"AnsiColorBlack":         "#21222C",
		"AnsiColorWhite":         "#F8F8F2",
		"AnsiColorBrightBlack":   "#6272A4",
		"AnsiColorBrightWhite":   "#FFFFFF",
	}
	for k, v := range want {
		if d.Values[k] != v {
			t.Errorf("%s = %q, want %q", k, d.Values[k], v)
		}
	}
	if _, ok := Find("Nope"); ok {
		t.Error("Find invented a theme")
	}
}

func TestMatchFindsAThemeByItsColours(t *testing.T) {
	d, _ := Find("Dracula")
	values := map[string]string{}
	for k, v := range d.Values {
		values[k] = strings.ToLower(v)
	}
	if got := Match(values); got != "Dracula" {
		t.Fatalf("Match = %q, want Dracula", got)
	}
	values["AnsiColorRed"] = "#010203"
	if got := Match(values); got != "" {
		t.Fatalf("an edited theme matched %q", got)
	}
	if got := Match(map[string]string{}); got != "" {
		t.Fatalf("an empty set matched %q", got)
	}
}

func TestParseIgnoresCursorTextAndRejectsBadInput(t *testing.T) {
	full := "## T\n"
	for key := range keyOf {
		full += "* **" + key + "**: #112233\n"
	}
	themes, err := Parse(strings.NewReader(full + "* **cursor_text**: #000000\n"))
	if err != nil || len(themes) != 1 || len(themes[0].Values) != 21 {
		t.Fatalf("Parse = %v, %v", themes, err)
	}
	for name, src := range map[string]string{
		"colour before heading": "* **cursor**: #000000\n",
		"bad colour":            "## T\n* **cursor**: red\n",
		"missing colon":         "## T\n* **cursor** #000000\n",
		"short theme":           "## T\n* **cursor**: #000000\n",
	} {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
