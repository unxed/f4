package theme

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

// A far2l theme file authored before a color slot existed simply has no row
// for it. Such a slot must not keep leaking whatever the palette happened to
// hold before the theme was applied — it should default to sharing its
// parent element's background instead (f4#1232: this is what made an
// imported theme's Viewer.Scrollbar/Viewer.Arrows/Editor.Scrollbar show a
// stray unrelated color instead of blending in).
func TestApplyColorIni_MissingSlotInheritsParentBackground(t *testing.T) {
	saved := append([]uint64(nil), vtui.Palette...)
	t.Cleanup(func() { vtui.Palette = saved })

	// A minimal theme that only ever heard of Viewer.Text and Editor.Text,
	// not the newer Viewer.Scrollbar / Viewer.Arrows / Editor.Scrollbar.
	iniPath := filepath.Join(t.TempDir(), "old-imported-theme.ini")
	body := "[farcolors]\n" +
		"Viewer.Text = foreground:#ffffff | background:#123456\n" +
		"Editor.Text = foreground:#ffffff | background:#654321\n"
	if err := os.WriteFile(iniPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed the palette with an unrelated stock color first, the way a base
	// style is applied before a user theme is layered on top, so a bug that
	// simply leaves the slot untouched would be caught.
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	vtui.Palette[ColViewerScrollbar] = vtui.SetRGBBoth(0, 0xFFFF00, 0x0000A0)
	vtui.Palette[ColViewerArrows] = vtui.SetRGBBoth(0, 0xFFFF00, 0x0000A0)
	vtui.Palette[ColEditorScrollbar] = vtui.SetRGBBoth(0, 0x808080, 0x0000A0)

	InitColors(ini.Load(iniPath))

	_, viewerBg := GetColorRGBBoth(vtui.Palette[ColViewerText])
	_, editorBg := GetColorRGBBoth(vtui.Palette[ColEditorText])

	for name, index := range map[string]int{
		"Viewer.Scrollbar": ColViewerScrollbar,
		"Viewer.Arrows":    ColViewerArrows,
	} {
		if _, bg := GetColorRGBBoth(vtui.Palette[index]); bg != viewerBg {
			t.Errorf("%s = #%06x, want the viewer text background #%06x", name, bg, viewerBg)
		}
	}
	if _, bg := GetColorRGBBoth(vtui.Palette[ColEditorScrollbar]); bg != editorBg {
		t.Errorf("Editor.Scrollbar = #%06x, want the editor text background #%06x", bg, editorBg)
	}
}

// A theme that DOES set the slot must keep working exactly as before: the
// inheritance fallback only fills gaps, it never overrides an explicit value.
func TestApplyColorIni_ExplicitSlotIsNotOverriddenByInheritance(t *testing.T) {
	saved := append([]uint64(nil), vtui.Palette...)
	t.Cleanup(func() { vtui.Palette = saved })

	iniPath := filepath.Join(t.TempDir(), "explicit-theme.ini")
	body := "[farcolors]\n" +
		"Viewer.Text = foreground:#ffffff | background:#123456\n" +
		"Viewer.Scrollbar = foreground:#808080 | background:#abcdef\n"
	if err := os.WriteFile(iniPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	InitColors(ini.Load(iniPath))

	if _, bg := GetColorRGBBoth(vtui.Palette[ColViewerScrollbar]); bg != 0xABCDEF {
		t.Errorf("Viewer.Scrollbar = #%06x, want the theme's own #abcdef, inheritance must not override it", bg)
	}
}
