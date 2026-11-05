package editor

import (
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtui"
)

func TestEditorView_PastePreservesUnicodeAcrossCodepages(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	source := NewEditorView(piecetable.New([]byte("Привет")), nil, "")
	source.Codepage = 11111 // ANSI
	source.CursorPos = len([]byte("Привет"))
	source.SelActive = true
	source.SelAnchorOffset = 0
	source.CopySelection()
	defer source.Close()

	target := NewEditorView(piecetable.New(nil), nil, "")
	target.Codepage = 22222 // OEM
	target.PasteText(vtui.GetClipboard())
	defer target.Close()

	if got := target.GetText(); got != "Привет" {
		t.Fatalf("pasted text = %q, want %q", got, "Привет")
	}
}

func TestEditorView_CodepageSwitchCycleUsesOriginalBytes(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	const want = "Привет, мир — UTF-8"
	ev := NewEditorView(piecetable.New([]byte(want)), nil, "")
	defer ev.Close()
	ev.Codepage = 65001

	// Each switch must reinterpret the original stream. The old
	// implementation re-encoded the already reinterpreted text and could not
	// get back to the original after UTF-8 -> ANSI -> OEM -> UTF-8.
	ev.ReloadWithCodepage(11111)
	ev.ReloadWithCodepage(22222)
	ev.ReloadWithCodepage(65001)

	if got := ev.GetText(); got != want {
		t.Fatalf("cycled text = %q, want %q", got, want)
	}
}
