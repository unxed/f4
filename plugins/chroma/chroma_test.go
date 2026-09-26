package chroma

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// applyStyle is ApplyColorStyle plus the vtui reset it always runs alongside
// in production (internal/theme.ApplyColorStyle itself calls
// vtui.SetDefaultPalette + theme.SetDefaultF4Palette before layering the
// named style), matching what a real theme switch does.
func applyStyle(t *testing.T, name string) {
	t.Helper()
	if err := theme.ApplyColorStyle(name); err != nil {
		t.Fatalf("ApplyColorStyle(%q): %v", name, err)
	}
}

func TestGetSyntaxAttr_Fallbacks(t *testing.T) {
	applyStyle(t, "Modern")
	base := uint64(0)

	// 1. Exact match (Keyword)
	attr := GetSyntaxAttr(chroma.Keyword, base)
	if want := SyntaxMap()[chroma.Keyword]; vtui.GetRGBFore(attr) != want {
		t.Errorf("Expected keyword color %06X, got %06X", want, vtui.GetRGBFore(attr))
	}

	// 2. Inheritance (KeywordConstant -> Keyword)
	attrSub := GetSyntaxAttr(chroma.KeywordConstant, base)
	if want := SyntaxMap()[chroma.Keyword]; vtui.GetRGBFore(attrSub) != want {
		t.Errorf("Expected inherited keyword color for KeywordConstant, got %06X", vtui.GetRGBFore(attrSub))
	}

	// 3. No match -> return base
	attrNone := GetSyntaxAttr(chroma.Text, base)
	if attrNone != base {
		t.Error("Expected base attribute for unknown token type")
	}
}

func TestChromaHighlighter_HighlightLogic(t *testing.T) {
	applyStyle(t, "Modern")

	provider := &ChromaProvider{}
	// Create highlighter for Go
	h := provider.Create("test.go", "package main")

	line := "func main() {"
	attrs, nextState := h.Highlight(line, nil, 0)

	if len(attrs) != len([]rune(line)) {
		t.Errorf("Attributes length mismatch: expected %d, got %d", len(line), len(attrs))
	}

	// First 4 chars ("func") should be highlighted as Keyword
	kwColor := SyntaxMap()[chroma.Keyword]
	for i := 0; i < 4; i++ {
		if vtui.GetRGBFore(attrs[i]) != kwColor {
			t.Errorf("Char %d ('%c') should be keyword color, got %06X", i, line[i], vtui.GetRGBFore(attrs[i]))
		}
	}

	if nextState != nil {
		t.Log("Chroma highlighter returned a state")
	}
}

// TestSyntaxColors_FollowActiveTheme is the core regression test for
// f4#1470: Chroma's SyntaxMap used to be a hardcoded palette that never
// changed no matter which f4 theme was active. Switching between two
// built-in themes must now change at least one token color, without
// restarting anything — GetSyntaxAttr/SyntaxMap read the live theme palette
// on every call.
func TestSyntaxColors_FollowActiveTheme(t *testing.T) {
	applyStyle(t, "Modern")
	modern := SyntaxMap()

	applyStyle(t, "Radiola")
	radiola := SyntaxMap()

	if len(modern) == 0 || len(radiola) == 0 {
		t.Fatal("SyntaxMap() returned no entries")
	}

	differing := 0
	for tokenType, modernColor := range modern {
		radiolaColor, ok := radiola[tokenType]
		if !ok {
			t.Fatalf("token type %v missing from Radiola's SyntaxMap", tokenType)
		}
		if modernColor != radiolaColor {
			differing++
		}
	}
	if differing == 0 {
		t.Error("expected at least one Chroma token color to differ between the Modern and Radiola themes, got identical palettes")
	}

	// GetSyntaxAttr must agree with SyntaxMap for the still-active theme
	// (Radiola), confirming both paths read the same live palette.
	attr := GetSyntaxAttr(chroma.Keyword, 0)
	if got, want := vtui.GetRGBFore(attr), radiola[chroma.Keyword]; got != want {
		t.Errorf("GetSyntaxAttr(Keyword) = %06X after switching to Radiola, want %06X", got, want)
	}

	// Switching back to Modern must restore Modern's colors immediately.
	applyStyle(t, "Modern")
	attr = GetSyntaxAttr(chroma.Keyword, 0)
	if got, want := vtui.GetRGBFore(attr), modern[chroma.Keyword]; got != want {
		t.Errorf("GetSyntaxAttr(Keyword) = %06X after switching back to Modern, want %06X", got, want)
	}
}

// TestSyntaxColors_ClassicMatchesOldHardcodedPalette guards against a
// visible regression for anyone not switching themes: Classic (the style
// that intentionally keeps f4's original built-in palette, see
// internal/theme/styles/classic.ini) must reproduce exactly the colors the
// old hardcoded SyntaxMap used to return, for every token category it
// covered.
func TestSyntaxColors_ClassicMatchesOldHardcodedPalette(t *testing.T) {
	applyStyle(t, "Classic")

	want := map[chroma.TokenType]uint32{
		chroma.Comment:        0x555753, // Gray
		chroma.Keyword:        0x729FCF, // Light Blue
		chroma.String:         0x8AE234, // Green
		chroma.Number:         0xAD7FA8, // Purple
		chroma.Operator:       0xFFFFFF, // White
		chroma.NameFunction:   0xFCE94F, // Yellow
		chroma.NameVariable:   0xEEEEEC, // Near White
		chroma.GenericHeading: 0x729FCF,
	}

	got := SyntaxMap()
	for tokenType, wantColor := range want {
		gotColor, ok := got[tokenType]
		if !ok {
			t.Errorf("token type %v missing from Classic's SyntaxMap", tokenType)
			continue
		}
		if gotColor != wantColor {
			t.Errorf("Classic theme: token %v = %06X, want %06X (the old hardcoded value)", tokenType, gotColor, wantColor)
		}
	}
}
