package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// f4#107: montoner0 found that a farcolors.ini exported before f4 started
// writing the [style] marker (or one that just predates a slot or two added
// since) made every color style picked in Settings look identical: the file
// still defines almost every color, and without the marker isCompleteColorIni
// used to require *literal* 100% coverage of the current, ever-growing
// ColorSlots list to recognize it as a full scheme. Missing even one recently
// added slot flipped it back to "partial override", which then got layered
// on top of *every* built-in style, masking whichever one was actually
// selected.

// markerFg/markerBg are colors no built-in style uses, so a wrongful overlay
// is trivially detectable.
const (
	missingSectionMarkerFg = "#111111"
	missingSectionMarkerBg = "#222222"
)

// nearCompleteFarcolorsBody returns a [farcolors] body that defines every
// slot except a few "heldOut" ones (standing in for slots f4 grew after the
// file was written), optionally preceded by an explicit [style] section.
func nearCompleteFarcolorsBody(t *testing.T, withStyleSection bool, heldOut map[string]bool) string {
	t.Helper()
	var sb strings.Builder
	if withStyleSection {
		sb.WriteString("[style]\nName = Custom\n")
	}
	sb.WriteString("[farcolors]\n")
	wrote := 0
	for _, slot := range ColorSlots {
		if slot.Index == vtui.ColDialogIndicatorBackground || slot.Index == ColDialogSettingsBackground {
			continue
		}
		if heldOut[slot.Canonical] {
			continue
		}
		fmt.Fprintf(&sb, "%s = foreground:%s | background:%s\n", slot.Canonical, missingSectionMarkerFg, missingSectionMarkerBg)
		wrote++
	}
	if wrote == 0 {
		t.Fatal("nearCompleteFarcolorsBody wrote no slots")
	}
	return sb.String()
}

func withUserOverridesFile(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "farcolors.ini"), []byte(body), 0600); err != nil {
		t.Fatalf("write farcolors.ini: %v", err)
	}
	old := UserColorOverridesPath
	UserColorOverridesPath = func() string { return filepath.Join(dir, "farcolors.ini") }
	t.Cleanup(func() { UserColorOverridesPath = old })

	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	t.Cleanup(func() { config.App = oldCfg })
}

// A farcolors.ini without a [style] section, but that otherwise defines
// nearly every color slot, must NOT mask the style selected in Settings: it
// is a full scheme that predates some slots f4 has since added, not a
// deliberate handful of tweaks. Regression test for f4#107.
func TestApplyColorStyle_NearCompleteOverridesWithoutStyleSectionDoNotMaskSelectedTheme(t *testing.T) {
	heldOut := map[string]bool{}
	held := 0
	for _, slot := range ColorSlots {
		if slot.Index == vtui.ColDialogIndicatorBackground || slot.Index == ColDialogSettingsBackground {
			continue
		}
		heldOut[slot.Canonical] = true
		held++
		if held >= 3 {
			break
		}
	}

	withUserOverridesFile(t, nearCompleteFarcolorsBody(t, false, heldOut))

	if err := ApplyColorStyle("Modern"); err != nil {
		t.Fatalf("ApplyColorStyle(Modern): %v", err)
	}

	fg, bg := GetColorRGBBoth(vtui.Palette[ColPanelText])
	if fg == 0x111111 && bg == 0x222222 {
		t.Fatalf("Panel.Text = #%06x on #%06x, want Modern's own colors: a near-complete "+
			"farcolors.ini without a [style] section masked the selected theme (f4#107)", fg, bg)
	}
	if fg != 0xAAAAAA || bg != 0x232323 {
		t.Errorf("Panel.Text = #%06x on #%06x, want Modern's #aaaaaa on #232323", fg, bg)
	}
}

// The same near-complete content, but explicitly marked [style] Name=Custom,
// must keep behaving as an exported scheme: available only as the Custom
// style, never forced onto a differently-named one either. This is the
// legitimate "force these colors" path and must not regress while fixing
// f4#107.
func TestApplyColorStyle_NearCompleteOverridesWithStyleSectionStayCustomOnly(t *testing.T) {
	withUserOverridesFile(t, nearCompleteFarcolorsBody(t, true, nil))

	if err := ApplyColorStyle("Modern"); err != nil {
		t.Fatalf("ApplyColorStyle(Modern): %v", err)
	}
	if fg, bg := GetColorRGBBoth(vtui.Palette[ColPanelText]); fg == 0x111111 && bg == 0x222222 {
		t.Fatalf("Panel.Text = #%06x on #%06x: an explicit [style] Custom scheme leaked into Modern", fg, bg)
	}

	if err := ApplyColorStyle(CustomColorStyleName); err != nil {
		t.Fatalf("ApplyColorStyle(Custom): %v", err)
	}
	if fg, bg := GetColorRGBBoth(vtui.Palette[ColPanelText]); fg != 0x111111 || bg != 0x222222 {
		t.Errorf("Panel.Text = #%06x on #%06x, want the exported marker colors #111111 on #222222", fg, bg)
	}
}

// A genuinely partial farcolors.ini (a couple of tweaks, no [style] section)
// must keep overlaying on top of every style, exactly as before f4#107 was
// fixed — this is the legitimate, deliberately-partial use case and must not
// be broken by tightening the "is this a full scheme" detection.
func TestApplyColorStyle_SmallOverridesWithoutStyleSectionStillApplyEverywhere(t *testing.T) {
	withUserOverridesFile(t, "[farcolors]\nPanel.Text = foreground:#123456 | background:#654321\n")

	for _, style := range []string{"Modern", "Classic"} {
		if err := ApplyColorStyle(style); err != nil {
			t.Fatalf("ApplyColorStyle(%q): %v", style, err)
		}
		fg, bg := GetColorRGBBoth(vtui.Palette[ColPanelText])
		if fg != 0x123456 || bg != 0x654321 {
			t.Errorf("after switching to %s, Panel.Text = #%06x on #%06x, want the override #123456 on #654321",
				style, fg, bg)
		}
	}
}
