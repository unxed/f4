package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/i18n"
)

// f4#410 (montoner0): Insert in the column list offers the variants of a
// type with its modifiers -- S and also SC, SE, SF, ST -- each one a token
// the Column types string accepts, named with what the modifier does.
func TestModeColumnVariantsAreValidAndNamed(t *testing.T) {
	sawModifiers := 0
	for _, base := range panelColumnTypeChoices {
		baseColumns, err := TextToViewSettings(base, "0")
		if err != nil || len(baseColumns) != 1 {
			t.Fatalf("%s: %v", base, err)
		}
		variants := modeColumnVariants(base)
		if variants[0] != base {
			t.Errorf("%s: first variant %q, want the plain type", base, variants[0])
		}
		for _, token := range variants[1:] {
			sawModifiers++
			columns, err := TextToViewSettings(token, "0")
			if err != nil || len(columns) != 1 {
				t.Errorf("%s does not parse: %v", token, err)
				continue
			}
			if columns[0].Type != baseColumns[0].Type || columns[0].Flags == 0 {
				t.Errorf("%s parses as type %v flags %v, want %s with a modifier", token, columns[0].Type, columns[0].Flags, base)
			}
			label := modeColumnVariantLabel(base, token)
			if !strings.Contains(label, " — ") || strings.Contains(label, "Panel.Modes.Modifier.") || strings.Contains(label, "?") {
				t.Errorf("label of %s = %q, want the title and what the modifier does", token, label)
			}
		}
	}
	if sawModifiers == 0 {
		t.Fatal("no type offers a modifier")
	}
	if got := strings.Join(modeColumnVariants("S"), ","); got != "S,SC,SE,SF,ST" {
		t.Errorf("size variants = %s", got)
	}
	if label := modeColumnVariantLabel("S", "SC"); !strings.Contains(label, i18n.Msg("Panel.Modes.Modifier.SizeC")) {
		t.Errorf("SC label = %q", label)
	}
}
