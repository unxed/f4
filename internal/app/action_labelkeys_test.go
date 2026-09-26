package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
)

// TestActionLabelKeysResolve guards f4#1218 ("навести порядок в строковых
// константах/переводах"): an Action.LabelKey (or DescKey) that names an i18n
// key absent from every language file is a silent bug, not a build error --
// Action.DisplayLabel()/DisplayDescription() quietly fall back to the English
// Label/Description field, in every locale, forever, and nothing catches it.
// That is exactly how "Editor.HexMode" (LabelKey "Action.Editor.HexMode") and
// "Editor.GoTo" (LabelKey "KeyBar.EditorAltF8", the Alt+F8 key bar caption)
// ended up permanently English: their LabelKey pointed at a key that was
// never added to lang/en.lng, so i18n.Msg() returned the "{key}" missing-key
// placeholder and DisplayLabel()'s own placeholder guard fell back to Label.
//
// en.lng is the base table every other language overlays (see
// i18n.defaultLangData), so a key present in en.lng resolves under any
// locale; this only fails for a key missing everywhere, which is the actual
// bug class here -- it does not duplicate lang_ru_complete_test.go's
// "translated into Russian" check.
func TestActionLabelKeysResolve(t *testing.T) {
	for _, a := range action.All() {
		if a.LabelKey != "" {
			if s := i18n.Msg(a.LabelKey); strings.HasPrefix(s, "{") {
				t.Errorf("action %q: LabelKey %q resolves to %q (missing from every lang file); DisplayLabel() silently falls back to English %q in every locale",
					a.Name, a.LabelKey, s, a.Label)
			}
		}
		if a.DescKey != "" {
			if s := i18n.Msg(a.DescKey); strings.HasPrefix(s, "{") {
				t.Errorf("action %q: DescKey %q resolves to %q (missing from every lang file); DisplayDescription() silently falls back to English %q in every locale",
					a.Name, a.DescKey, s, a.Description)
			}
		}
	}
}
