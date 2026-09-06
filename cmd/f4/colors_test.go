package main

import (
	"fmt"
	"github.com/unxed/vtui"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestColors_SetDefaultF4Palette(t *testing.T) {
	// Initialize default palettes
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	if len(vtui.Palette) < LastF4PaletteColor {
		t.Fatalf("Expected palette size of at least %d, got %d", LastF4PaletteColor, len(vtui.Palette))
	}

	// Verify theme palette defaults
	if vtui.ThemePalette[0] != 0x2E3436 {
		t.Errorf("ThemePalette[0] mismatch: expected %06X, got %06X", 0x2E3436, vtui.ThemePalette[0])
	}
	if vtui.ThemePalette[7] != 0xD3D7CF {
		t.Errorf("ThemePalette[7] mismatch: expected %06X, got %06X", 0xD3D7CF, vtui.ThemePalette[7])
	}

	// Verify some critical f4 palette defaults
	blue := uint32(0x0000A0)
	expectedPanelText := vtui.SetRGBBoth(0, 0x00FFFF, blue)
	if vtui.Palette[ColPanelText] != expectedPanelText {
		t.Errorf("ColPanelText mismatch: expected %016X, got %016X", expectedPanelText, vtui.Palette[ColPanelText])
	}

	// Verify newly revived f4 palette defaults
	if vtui.Palette[ColPanelHighlightText] != vtui.Palette[ColPanelText] {
		t.Errorf("ColPanelHighlightText default mismatch: expected %016X, got %016X", vtui.Palette[ColPanelText], vtui.Palette[ColPanelHighlightText])
	}
	if vtui.Palette[ColPanelTotalInfo] != vtui.Palette[ColPanelText] {
		t.Errorf("ColPanelTotalInfo default mismatch: expected %016X, got %016X", vtui.Palette[ColPanelText], vtui.Palette[ColPanelTotalInfo])
	}
	if vtui.Palette[ColPanelSelectedInfo] != vtui.Palette[ColPanelSelectedText] {
		t.Errorf("ColPanelSelectedInfo default mismatch: expected %016X, got %016X", vtui.Palette[ColPanelSelectedText], vtui.Palette[ColPanelSelectedInfo])
	}
	if fg, bg := GetColorRGBBoth(vtui.Palette[ColPanelSelectedTitle]); fg != 0x00FFFF || bg != 0x3030C0 {
		t.Errorf("ColPanelSelectedTitle default mismatch: got %06X on %06X", fg, bg)
	}
}

func TestColors_InitColors_FromIni(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := AppConfig
	AppConfig.EnforceColorCorrection = false
	defer func() { AppConfig = oldCfg }()

	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "farcolors.ini")

	// Pre-populate with custom colors
	iniContent := `
[farcolors]
Panel.Text = foreground:#FF0000 | background:#0000FF
CommandLine.Prompt = foreground:#00FF00
CommandLine.UserScreen = foreground:#D0D0D0 | background:#010203
Editor.Text = foreground:#A0A0A0 | background:#232323
`
	if err := os.WriteFile(iniPath, []byte(iniContent), 0600); err != nil {
		t.Fatalf("Failed to write mock INI: %v", err)
	}

	ini := LoadIni(iniPath)
	InitColors(ini)

	// Check that ColPanelText got updated to custom Red on Blue
	panelTextAttr := vtui.Palette[ColPanelText]
	if vtui.GetRGBFore(panelTextAttr) != 0xFF0000 {
		t.Errorf("Expected foreground Red #FF0000, got %06X", vtui.GetRGBFore(panelTextAttr))
	}
	if vtui.GetRGBBack(panelTextAttr) != 0x0000FF {
		t.Errorf("Expected background Blue #0000FF, got %06X", vtui.GetRGBBack(panelTextAttr))
	}

	// Check that ColCommandLinePrompt got updated to custom Green
	promptAttr := vtui.Palette[ColCommandLinePrompt]
	if vtui.GetRGBFore(promptAttr) != 0x00FF00 {
		t.Errorf("Expected foreground Green #00FF00, got %06X", vtui.GetRGBFore(promptAttr))
	}
	if vtui.ThemePalette[0] != 0x010203 {
		t.Errorf("Expected terminal background #010203, got %06X", vtui.ThemePalette[0])
	}
	if got := vtui.GetRGBBack(vtui.Palette[ColEditorText]); got != 0x232323 {
		t.Errorf("Expected editor background #232323, got %06X", got)
	}
}

func TestColors_HelpBoxOverrideReachesHelpViewFrame(t *testing.T) {
	oldPalette := append([]uint64(nil), vtui.Palette...)
	oldTheme := vtui.ThemePalette
	oldCfg := AppConfig
	t.Cleanup(func() {
		vtui.Palette = oldPalette
		vtui.ThemePalette = oldTheme
		AppConfig = oldCfg
	})

	AppConfig.EnforceColorCorrection = false
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	InitColors(ParseIni(strings.NewReader(`[farcolors]
Help.Box = foreground:#102030 | background:#405060
`)))

	engine := vtui.NewHelpEngine(&memoryHelpVFS{files: map[string]string{}})
	engine.AddTopic(&vtui.HelpTopic{Name: "Test", Lines: []string{"text"}})
	view := vtui.NewHelpView(engine, "Test")
	view.SetPosition(0, 0, 30, 5)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(32, 7)
	view.Show(scr)

	if got, want := scr.GetCell(0, 0).Attributes, vtui.Palette[vtui.ColHelpBox]; got != want {
		t.Fatalf("Help.Box frame attribute = %#x, want %#x", got, want)
	}
}

// Help draws its scrollbar inside its own window, so it must not follow the
// shared Scrollbar key that is tuned for lists sitting on the dialog
// background (issue #261).
func TestColors_HelpScrollbarOverrideReachesHelpViewScrollbar(t *testing.T) {
	oldPalette := append([]uint64(nil), vtui.Palette...)
	oldTheme := vtui.ThemePalette
	oldCfg := AppConfig
	t.Cleanup(func() {
		vtui.Palette = oldPalette
		vtui.ThemePalette = oldTheme
		AppConfig = oldCfg
	})

	AppConfig.EnforceColorCorrection = false
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	InitColors(ParseIni(strings.NewReader(`[farcolors]
Scrollbar = foreground:#C0C0C0 | background:#0000A0
Help.Scrollbar = foreground:#102030 | background:#405060
`)))

	engine := vtui.NewHelpEngine(&memoryHelpVFS{files: map[string]string{}})
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "help line"
	}
	engine.AddTopic(&vtui.HelpTopic{Name: "Long", Lines: lines})
	view := vtui.NewHelpView(engine, "Long")
	view.SetPosition(0, 0, 30, 8)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(32, 10)
	view.Show(scr)

	// The scrollbar runs down the right padding column of the help window.
	cell := scr.GetCell(28, 1)
	if cell.Char != vtui.ScrollUpArrow {
		t.Fatalf("no scrollbar drawn at the right padding column: got %q", rune(cell.Char))
	}
	if got, want := cell.Attributes, vtui.Palette[vtui.ColHelpScrollbar]; got != want {
		t.Fatalf("help scrollbar attribute = %#x, want %#x", got, want)
	}
	if cell.Attributes == vtui.Palette[vtui.ColScrollBar] {
		t.Fatal("help scrollbar still follows the shared Scrollbar color")
	}
}

// Table.Box was renamed to Table.Separator; existing farcolors.ini files must
// keep working.
func TestColors_TableBoxAliasStillMapsToTableSeparator(t *testing.T) {
	sepIdx, ok := colorMap["Table.Separator"]
	if !ok {
		t.Fatal("Table.Separator is not mapped")
	}
	aliasIdx, ok := colorMap["Table.Box"]
	if !ok {
		t.Fatal("the legacy Table.Box key no longer resolves")
	}
	if sepIdx != aliasIdx {
		t.Fatalf("Table.Box maps to slot %d, want %d", aliasIdx, sepIdx)
	}
}

func TestColors_ExportColors_Grouped(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "exported_colors.ini")

	// Export current palette
	err := ExportColors(iniPath)
	if err != nil {
		t.Fatalf("ExportColors failed: %v", err)
	}

	data, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatalf("Failed to read exported file: %v", err)
	}

	content := string(data)

	// Check that section is present
	if !strings.Contains(content, "[farcolors]") {
		t.Error("Exported file missing [farcolors] section")
	}

	// Check that group comments are present
	expectedHeaders := []string{
		"# Panel",
		"# Lists and tables",
		"# Dialog",
		"# Warning message",
		"# Menu",
		"# Horizontal menu",
		"# Key bar",
		"# Command line",
		"# Viewer",
		"# Editor",
		"# Help",
	}

	for _, h := range expectedHeaders {
		if !strings.Contains(content, h) {
			t.Errorf("Exported file missing expected category header %q", h)
		}
	}
	for _, note := range []string{
		"Table.Separator colors the column separators of a table",
		"Scrollbar is the shared fallback for generic lists and menus",
	} {
		if !strings.Contains(content, note) {
			t.Errorf("Exported file missing color usage note %q", note)
		}
	}

	// Check that specific newly-added keys are exported correctly under their groups
	expectedKeys := []string{
		"WarnDialog.Box",
		"WarnDialog.Text",
		"Help.Text",
		"Help.Topic",
	}

	for _, k := range expectedKeys {
		if !strings.Contains(content, k+" = ") {
			t.Errorf("Exported file missing expected key %q", k)
		}
	}
}

func TestColors_ExportColorsPreservesAuthoredExpressions(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	AppConfig.EnforceColorCorrection = true
	defer func() { AppConfig = oldCfg }()

	ini := ParseIni(strings.NewReader(`[farcolors]
Panel.Cursor.Inactive.Selected = foreground:#feff00 | background:#555753
Panel.FastFindNoMatch = foreground:#d65f5f | background:#0000a0
Panel.Scrollbar.Minimal = foreground:#0000ff | background:#0000a0
WarnDialog.Edit = foreground:#ffffff | background:#0000a0
WarnDialog.Edit.Selected = foreground:#ffffff | background:#000000
WarnDialog.Edit.Unchanged = foreground:#a0a0a0 | background:#0000a0
`))
	InitColors(ini)

	path := filepath.Join(t.TempDir(), "exported.ini")
	if err := ExportColors(path); err != nil {
		t.Fatal(err)
	}
	exported := LoadIni(path)
	for key, want := range map[string]string{
		"Panel.Cursor.Inactive.Selected": "foreground:#feff00 | background:#555753",
		"Panel.FastFindNoMatch":          "foreground:#d65f5f | background:#0000a0",
		"Panel.Scrollbar.Minimal":        "foreground:#0000ff | background:#0000a0",
		"WarnDialog.Edit":                "foreground:#ffffff | background:#0000a0",
		"WarnDialog.Edit.Selected":       "foreground:#ffffff | background:#000000",
		"WarnDialog.Edit.Unchanged":      "foreground:#a0a0a0 | background:#0000a0",
	} {
		if got := exported.GetString("farcolors", key, ""); got != want {
			t.Errorf("%s exported as %q, want authored expression %q", key, got, want)
		}
	}
}

func TestColors_ExportColorsUsesCurrentPaletteAfterDirectChange(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	oldCfg := AppConfig
	AppConfig.EnforceColorCorrection = true
	defer func() { AppConfig = oldCfg }()

	ini := ParseIni(strings.NewReader(`[farcolors]
Panel.Text = foreground:#123456 | background:#654321
`))
	InitColors(ini)
	vtui.Palette[ColPanelText] = vtui.SetRGBBoth(0, 0xabcdef, 0x102030)

	path := filepath.Join(t.TempDir(), "exported.ini")
	if err := ExportColors(path); err != nil {
		t.Fatal(err)
	}
	exported := LoadIni(path)
	if got := exported.GetString("farcolors", "Panel.Text", ""); got != "foreground:#abcdef | background:#102030" {
		t.Fatalf("direct palette change exported as %q", got)
	}
}

func TestColors_AliasesAndCanonicalPrecedence(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "farcolors.ini")

	// Pre-populate with both alias and canonical key.
	// Since canonical key takes precedence, the final color for ColCommandLinePrompt (CommandLine.Prefix / CommandLine.Prompt)
	// should be the one defined in the canonical key "CommandLine.Prefix".
	iniContent := `
[farcolors]
CommandLine.Prompt = foreground:#FF0000
CommandLine.Prefix = foreground:#00FF00
`
	if err := os.WriteFile(iniPath, []byte(iniContent), 0600); err != nil {
		t.Fatalf("Failed to write mock INI: %v", err)
	}

	ini := LoadIni(iniPath)
	InitColors(ini)

	promptAttr := vtui.Palette[ColCommandLinePrompt]
	if vtui.GetRGBFore(promptAttr) != 0x00FF00 {
		t.Errorf("Canonical key 'CommandLine.Prefix' should have taken precedence over alias 'CommandLine.Prompt'. Got %06X, want 00FF00", vtui.GetRGBFore(promptAttr))
	}
}

func TestColors_GenerateDocumentation(t *testing.T) {
	var sb strings.Builder
	if _, err := sb.WriteString("# f4 Palette Colors and Aliases\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.WriteString("This file is automatically generated. Do not edit manually.\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.WriteString("| Canonical far2l Name | Palette Index Slot | Accepted Aliases |\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.WriteString("|---|---|---|\n"); err != nil {
		t.Fatal(err)
	}

	// Sort ColorSlots alphabetically by Canonical name
	slots := make([]ColorSlot, len(ColorSlots))
	copy(slots, ColorSlots)
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].Canonical < slots[j].Canonical
	})

	for _, slot := range slots {
		aliasesStr := "None"
		if len(slot.Aliases) > 0 {
			aliasesStr = "`" + strings.Join(slot.Aliases, "`, `") + "`"
		}
		if _, err := fmt.Fprintf(&sb, "| `%s` | `%s` | %s |\n", slot.Canonical, slot.ConstantName, aliasesStr); err != nil {
			t.Fatal(err)
		}
	}

	targetPath := "COLORS.md"
	if os.Getenv("F4_GENERATE_DOCS") != "1" {
		targetPath = filepath.Join(t.TempDir(), "COLORS.md")
	}
	_ = os.WriteFile(targetPath, []byte(sb.String()), 0600)
}

// TestColors_DocumentationMatchesColorSlots keeps the checked-in generated
// table aligned with the runtime mapping. A stale row here can make a working
// farcolors.ini slot look broken to users, especially when two warning slots
// are adjacent in the list.
func TestColors_DocumentationMatchesColorSlots(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "COLORS.md"))
	if err != nil {
		t.Fatalf("read generated color documentation: %v", err)
	}
	content := string(data)
	for _, slot := range ColorSlots {
		row := fmt.Sprintf("| `%s` | `%s` |", slot.Canonical, slot.ConstantName)
		if !strings.Contains(content, row) {
			t.Errorf("generated color documentation is missing or mis-maps %s", slot.Canonical)
		}
	}
}

func TestColors_ContrastCorrection(t *testing.T) {
	// Dark grey on black is well below the ΔE2000 floor far2l enforces, so the
	// foreground has to move. Note the target is ΔE2000, not a WCAG ratio: the
	// corrected pair here still sits around 3.6:1 and far2l considers it done.
	black := uint32(0x000000)
	darkGray := uint32(0x111111)

	corrected := CorrectContrast(darkGray, black)

	if GetLuminance(corrected) <= GetLuminance(darkGray) {
		t.Errorf("expected a lighter foreground on black, got #%06x", corrected)
	}

	dE := deltaE2000(rgbToLAB(toRGBF(corrected)), rgbToLAB(toRGBF(black)))
	if dE < 29.0 {
		t.Errorf("corrected pair has deltaE2000 %.2f, want the ~30 far2l aims for", dE)
	}
}

// A pair far2l leaves alone must come back bit-identical rather than shifted by
// a unit through the L*a*b* round trip.
func TestColors_ContrastCorrectionLeavesGoodPairsAlone(t *testing.T) {
	white, black := uint32(0xFFFFFF), uint32(0x000000)
	if got := CorrectContrast(white, black); got != white {
		t.Errorf("white on black was rewritten to #%06x", got)
	}
}

// far2l keeps yellow on light grey despite a WCAG ratio near 1.2, because the
// perceptual distance is already large enough. This is the case that told us
// the previous WCAG-only approximation was wrong.
func TestColors_ContrastCorrectionKeepsLowWcagPair(t *testing.T) {
	yellow, lightGray := uint32(0xFCE94F), uint32(0xD3D7CF)

	corrected := CorrectContrast(yellow, lightGray)

	for shift := 0; shift < 24; shift += 8 {
		got := int((corrected >> shift) & 0xFF)
		want := int((yellow >> shift) & 0xFF)
		if diff := got - want; diff > 1 || diff < -1 {
			t.Fatalf("yellow on light grey drifted to #%06x, want it left near #%06x", corrected, yellow)
		}
	}
}
