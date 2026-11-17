package theme

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

func TestTerminalThemeColorSlotsParse(t *testing.T) {
	oldPalette := append([]uint64(nil), vtui.Palette...)
	oldThemePalette := vtui.ThemePalette
	t.Cleanup(func() {
		vtui.Palette = oldPalette
		vtui.ThemePalette = oldThemePalette
	})
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	var source strings.Builder
	source.WriteString("[farcolors]\n")
	want := make(map[string]uint32, len(terminalThemeTestColors))
	for i, key := range terminalThemeTestColors {
		color := uint32(0x102030 + i*0x010101)
		fmt.Fprintf(&source, "%s = foreground:#%06X | background:#000000\n", key, color)
		want[key] = color
	}

	ApplyColorIni(ini.Parse(strings.NewReader(source.String())))
	for _, slot := range ColorSlots {
		color, ok := want[slot.Canonical]
		if !ok {
			continue
		}
		if got := vtui.GetRGBFore(vtui.Palette[slot.Index]); got != color {
			t.Errorf("%s foreground = #%06X, want #%06X", slot.Canonical, got, color)
		}
		delete(want, slot.Canonical)
	}
	for key := range want {
		t.Errorf("terminal theme key %s is not registered as a color slot", key)
	}
}

var terminalThemeTestColors = []string{
	"Terminal.Text", "Terminal.Text.Selected",
	"Terminal.Ansi.Black", "Terminal.Ansi.Red", "Terminal.Ansi.Green", "Terminal.Ansi.Yellow",
	"Terminal.Ansi.Blue", "Terminal.Ansi.Magenta", "Terminal.Ansi.Cyan", "Terminal.Ansi.White",
	"Terminal.Ansi.BrightBlack", "Terminal.Ansi.BrightRed", "Terminal.Ansi.BrightGreen", "Terminal.Ansi.BrightYellow",
	"Terminal.Ansi.BrightBlue", "Terminal.Ansi.BrightMagenta", "Terminal.Ansi.BrightCyan", "Terminal.Ansi.BrightWhite",
}
