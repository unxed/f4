package editor

import (
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// The wrap glyph sits on the background the text is drawn on when the theme
// gives it the text's own background, and keeps its own otherwise (f4#1232).
func TestWrapMarkAttrFollowsTheTextBackgroundOnlyWhenThemeSaysSo(t *testing.T) {
	savedMark, savedText := vtui.Palette[theme.ColEditorWrapMark], vtui.Palette[theme.ColEditorText]
	t.Cleanup(func() {
		vtui.Palette[theme.ColEditorWrapMark], vtui.Palette[theme.ColEditorText] = savedMark, savedText
	})
	ev := NewEditorView(piecetable.New([]byte("text")), nil, "t.txt")
	defer ev.Close()

	text := vtui.SetRGBBoth(0, 0x34E2E2, 0x3465A4)
	vtui.Palette[theme.ColEditorText] = text

	// Same background as the text in the theme: the mark follows the text.
	vtui.Palette[theme.ColEditorWrapMark] = vtui.SetRGBBoth(0, 0xFCE94F, 0x3465A4)
	if got := ev.wrapMarkAttr(); got != vtui.Palette[theme.ColEditorWrapMark] {
		t.Errorf("with the text on the theme's own background the mark changed: %#x", got)
	}
	// A theme that sets the mark apart keeps it apart.
	vtui.Palette[theme.ColEditorWrapMark] = vtui.SetRGBBoth(0, 0xFCE94F, 0x772953)
	if got := ev.wrapMarkAttr(); got != vtui.Palette[theme.ColEditorWrapMark] {
		t.Errorf("a mark with a background of its own was repainted: %#x", got)
	}
}
