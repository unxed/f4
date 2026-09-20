package theme

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// Default Dark is the port of the far2l theme of the same name. Far2l Dark was
// an earlier, approximate attempt at the same thing and has been dropped.
func TestDefaultDarkStyle(t *testing.T) {
	if err := ApplyColorStyle("Default Dark"); err != nil {
		t.Fatalf("Failed to apply Default Dark style: %v", err)
	}
}

// Colours the earlier attempt got wrong, spot-checked against the far2l
// Contrast correction is off here so the palette is compared as authored.
func TestDefaultDarkStyle_PanelColors(t *testing.T) {
	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	if err := ApplyColorStyle("Default Dark"); err != nil {
		t.Fatalf("Failed to apply Default Dark style: %v", err)
	}

	cases := []struct {
		name   string
		index  int
		fg, bg uint32
	}{
		// Panel.Text is B_BLACK | F_LIGHTCYAN — the black background Far2l Dark
		// replaced with far2l's stock blue.
		{"Panel.Text", ColPanelText, 0x34E2E2, 0x2E3436},
		{"Panel.Title", ColPanelTitle, 0xD3D7CF, 0x2E3436},
		{"Panel.Title.Selected", ColPanelSelectedTitle, 0xD3D7CF, 0x555753},
		{"Panel.Title.Column", ColPanelColumnTitle, 0x8AE234, 0x2E3436},
		// Background raised one shade above the base panel (was 0x2E3436,
		// same as Panel.Text) so selected files are readable without a
		// cursor on them — see issue #524.
		{"Panel.Text.Selected", ColPanelSelectedText, 0xFCE94F, 0x3F474A},
		{"Panel.Info.Total", ColPanelTotalInfo, 0xEEEEEC, 0x2E3436},
		// Dark text on teal: the pair that used to be flipped to white by the
		// old contrast approximation.
		{"Keybar.Text", vtui.ColKeyBarText, 0x2E3436, 0x06989A},
		{"HMenu.Text", vtui.ColMenuBarItem, 0x2E3436, 0xD3D7CF},
	}
	for _, tc := range cases {
		fg, bg := GetColorRGBBoth(vtui.Palette[tc.index])
		if fg != tc.fg || bg != tc.bg {
			t.Errorf("%s = #%06x on #%06x, want #%06x on #%06x", tc.name, fg, bg, tc.fg, tc.bg)
		}
	}
}

// With correction on, far2l's algorithm leaves these pairs alone. If a future
// change makes the keybar go white again, this is the test that catches it.
func TestDefaultDarkStyle_SurvivesContrastCorrection(t *testing.T) {
	oldCfg := config.App
	config.App.EnforceColorCorrection = true
	defer func() { config.App = oldCfg }()

	if err := ApplyColorStyle("Default Dark"); err != nil {
		t.Fatalf("Failed to apply Default Dark style: %v", err)
	}

	cases := []struct {
		name   string
		index  int
		fg, bg uint32
	}{
		{"Keybar.Text", vtui.ColKeyBarText, 0x2E3436, 0x06989A},
		{"Panel.Text", ColPanelText, 0x34E2E2, 0x2E3436},
		{"Menu.Text.Selected", vtui.ColMenuSelectedText, 0x2E3436, 0x4E9A06},
	}
	for _, tc := range cases {
		fg, bg := GetColorRGBBoth(vtui.Palette[tc.index])
		if fg != tc.fg || bg != tc.bg {
			t.Errorf("%s = #%06x on #%06x after correction, want #%06x on #%06x",
				tc.name, fg, bg, tc.fg, tc.bg)
		}
	}
}

// The viewer's scrollbar column and the addresses of its Hex mode are drawn on
// the viewer's own background: the blue of the far2l file they came from
// showed as a stripe next to the grey text (#1232).
func TestDefaultDarkStyle_ViewerScrollbarAndAddressesShareTheTextBackground(t *testing.T) {
	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	if err := ApplyColorStyle("Default Dark"); err != nil {
		t.Fatalf("Failed to apply Default Dark style: %v", err)
	}
	_, textBg := GetColorRGBBoth(vtui.Palette[ColViewerText])
	for name, index := range map[string]int{"Viewer.Scrollbar": ColViewerScrollbar, "Viewer.Arrows": ColViewerArrows} {
		if _, bg := GetColorRGBBoth(vtui.Palette[index]); bg != textBg {
			t.Errorf("%s is on #%06x, the viewer text is on #%06x", name, bg, textBg)
		}
	}
}
