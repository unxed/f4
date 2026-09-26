package theme

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// hasErrorFor reports whether any error message mentions both name and
// substr, so tests can pin down which slot a validation failure is about.
func hasErrorFor(errs []error, name, substr string) bool {
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, name) && strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

// TestValidateColorScheme_FlagsDefaultHighlightRegression is the concrete
// regression f4#363 asks for: hotkey/highlight text rendered as a light
// yellow on a light gray dialog background, as happened historically in
// far2l's default dark scheme. vtui's own built-in defaults set exactly
// this pair (Dialog.Text.Highlight: yellow on light gray), so applying them
// with contrast correction off must be caught by ValidateColorScheme.
func TestValidateColorScheme_FlagsDefaultHighlightRegression(t *testing.T) {
	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	errs := ValidateColorScheme()
	if !hasErrorFor(errs, "Dialog.Text.Highlight", "insufficient contrast") {
		t.Errorf("expected Dialog.Text.Highlight (light-yellow-on-light-gray) to be flagged for insufficient contrast, got: %v", errs)
	}
}

// TestValidateColorScheme_GoodPairsAreNotFlagged spot-checks that well
// behaved, comfortably-contrasted slots in the same default palette are not
// flagged: the validator must discriminate, not flag everything.
func TestValidateColorScheme_GoodPairsAreNotFlagged(t *testing.T) {
	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	errs := ValidateColorScheme()
	for _, name := range []string{"Dialog.Text", "Panel.Text"} {
		for _, e := range errs {
			if strings.Contains(e.Error(), "["+name+"]") {
				t.Errorf("did not expect %s to be flagged, got: %v", name, e)
			}
		}
	}
}

// TestColorSlotPairs_ExcludesNonMeaningfulSlots checks the adapter's
// exclusion rules directly: the caret, the optional "inherit" backgrounds,
// frame/box lines and Colorer's syntax slots must not appear, and a palette
// index shared by several canonical names must be validated only once.
func TestColorSlotPairs_ExcludesNonMeaningfulSlots(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	pairs := colorSlotPairs()

	byName := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		byName[p.Name] = true
	}

	for _, excluded := range []string{
		"Terminal.Cursor",
		"Dialog.Indicator.Background",
		"Dialog.Settings.Background",
		"Dialog.Box",
		"Panel.Box",
		"Editor.Syntax.Comment",
	} {
		if byName[excluded] {
			t.Errorf("expected %q to be excluded from colorSlotPairs, but it was present", excluded)
		}
	}

	// WarnDialog.Edit, WarnDialog.Edit.Unchanged and WarnDialog.Edit.Selected
	// all map onto the same index; only the first should survive.
	seen := 0
	for _, p := range pairs {
		if strings.HasPrefix(p.Name, "WarnDialog.Edit") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("expected exactly one WarnDialog.Edit* pair (index is shared), got %d: %+v", seen, pairs)
	}
}

// TestAdjacentSurfacePairs_ComparesDialogAndPanelBoxes checks the
// dialog/panel adjacency wiring itself, independent of whether the default
// palette's own boxes happen to clash.
func TestAdjacentSurfacePairs_ComparesDialogAndPanelBoxes(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	_, wantDialogBG := GetColorRGBBoth(vtui.Palette[vtui.ColDialogBox])
	_, wantPanelBG := GetColorRGBBoth(vtui.Palette[ColPanelBox])

	pairs := adjacentSurfacePairs()
	if len(pairs) != 1 {
		t.Fatalf("expected exactly one adjacency pair, got %d: %+v", len(pairs), pairs)
	}
	got := pairs[0]
	if got.FG != wantDialogBG || got.BG != wantPanelBG {
		t.Errorf("adjacency pair = %+v, want FG=#%06x (Dialog.Box) BG=#%06x (Panel.Box)", got, wantDialogBG, wantPanelBG)
	}
}

// TestValidateColorSchemeWithRules_ContrastDisabledForAdjacency makes sure
// the adjacency pair is never judged by the text-contrast rule, even when a
// custom rule set is passed in: two backgrounds can legitimately be close
// in luminance without being a readability problem.
func TestValidateColorSchemeWithRules_ContrastDisabledForAdjacency(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	rules := vtui.DefaultColorRules
	rules.MinContrastRatio = 100 // impossibly strict, would fail almost any slot pair
	errs := ValidateColorSchemeWithRules(rules)

	if hasErrorFor(errs, "adjacent surfaces", "insufficient contrast") {
		t.Errorf("adjacency pair must not be judged by MinContrastRatio, got: %v", errs)
	}
}
