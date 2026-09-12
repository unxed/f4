package editor

import (
	"errors"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtui"
)

func selectEditorBytes(ev *EditorView, end int) {
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorLine = ev.Li.GetLineAtOffset(end)
	ev.CursorPos = end - ev.Li.GetLineOffset(ev.CursorLine)
}

func TestEditorBase64EncodeAndDecodeSelection(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("hello world")), nil, "test.txt")
	defer ev.Close()

	selectEditorBytes(ev, len("hello world"))
	if err := ev.TransformBase64Selection(true); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got := ev.GetText(); got != "aGVsbG8gd29ybGQ=" {
		t.Fatalf("encoded text = %q", got)
	}

	selectEditorBytes(ev, len("aGVsbG8gd29ybGQ="))
	if err := ev.TransformBase64Selection(false); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := ev.GetText(); got != "hello world" {
		t.Fatalf("decoded text = %q", got)
	}
}

func TestEditorBase64DecodeAcceptsWhitespaceAndRejectsInvalidInput(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("aGVs\n bG8=")), nil, "test.txt")
	defer ev.Close()

	selectEditorBytes(ev, len("aGVs\n bG8="))
	if err := ev.TransformBase64Selection(false); err != nil {
		t.Fatalf("decode wrapped Base64: %v", err)
	}
	if got := ev.GetText(); got != "hello" {
		t.Fatalf("wrapped decode = %q", got)
	}

	ev.SetText("not Base64!")
	selectEditorBytes(ev, len("not Base64!"))
	before := ev.GetText()
	if err := ev.TransformBase64Selection(false); err == nil {
		t.Fatal("invalid Base64 was accepted")
	}
	if got := ev.GetText(); got != before {
		t.Fatalf("invalid decode changed text to %q", got)
	}
}

func TestEditorBase64DecodeSelectionWithTrailingNewline(t *testing.T) {
	const input = "aGVsbG8gd29ybGQ=\n"
	ev := NewEditorView(piecetable.New([]byte(input)), nil, "test.txt")
	defer ev.Close()

	selectEditorBytes(ev, len(input))
	if err := ev.TransformBase64Selection(false); err != nil {
		t.Fatalf("decode trailing-newline selection: %v", err)
	}
	if got := ev.GetText(); got != "hello world" {
		t.Fatalf("trailing-newline decode = %q", got)
	}
}

func TestEditorBase64RejectsRectangularSelection(t *testing.T) {
	ev := NewEditorView(piecetable.New([]byte("hello")), nil, "test.txt")
	defer ev.Close()
	ev.RectSelActive = true

	err := ev.TransformBase64Selection(true)
	if err == nil || errors.Is(err, errBase64NoSelection) {
		t.Fatalf("rectangular selection error = %v", err)
	}
}

func TestEditorSortLinesMenu(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := NewEditorView(piecetable.New([]byte("one\ntwo")), nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)

	ev.ShowPluginsMenu()
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	defer menu.Close()
	if got, want := menu.GetTitle(), "Plugins"; got != want {
		t.Fatalf("menu title = %q, want %q", got, want)
	}
	if len(menu.Items) != 4 || !menu.Items[2].Separator {
		t.Fatalf("menu items = %#v, want two tools, separator, and sort", menu.Items)
	}
}

func TestEditorSortLines(t *testing.T) {
	ev := newDuplicateLineEditor(t, "zebra\napple\nBeta\n")
	ev.CursorLine = 1
	ev.CursorPos = 2

	if err := ev.SortLines(true, true); err != nil {
		t.Fatalf("sort ascending: %v", err)
	}
	if got, want := ev.Pt.String(), "Beta\napple\nzebra\n"; got != want {
		t.Fatalf("ascending sort = %q, want %q", got, want)
	}
	if ev.CursorLine != 1 || ev.CursorPos != 2 {
		t.Fatalf("cursor = line %d pos %d, want line 1 pos 2", ev.CursorLine, ev.CursorPos)
	}

	ev.Undo()
	if got, want := ev.Pt.String(), "zebra\napple\nBeta\n"; got != want {
		t.Fatalf("after undo = %q, want %q", got, want)
	}
}

func TestEditorSortLinesSelectionAndOptions(t *testing.T) {
	ev := newDuplicateLineEditor(t, "outside\nbeta\nAlpha\nALPHA\ncharlie\nafter")
	ev.SelActive = true
	ev.SelAnchorOffset = len("outside\n")
	ev.CursorLine = 4
	ev.CursorPos = len("charlie")

	if err := ev.SortLines(false, false); err != nil {
		t.Fatalf("sort selected lines descending without case: %v", err)
	}
	if got, want := ev.Pt.String(), "outside\ncharlie\nbeta\nAlpha\nALPHA\nafter"; got != want {
		t.Fatalf("selected sort = %q, want %q", got, want)
	}
	if !ev.SelActive || ev.SelAnchorOffset != len("outside\n") {
		t.Fatalf("selection was not preserved: active=%v anchor=%d", ev.SelActive, ev.SelAnchorOffset)
	}
}

func TestEditorSortLinesPreservesCRLF(t *testing.T) {
	ev := newDuplicateLineEditor(t, "b\r\na\r\nlast")
	if err := ev.SortLines(true, true); err != nil {
		t.Fatalf("sort CRLF: %v", err)
	}
	if got, want := ev.Pt.String(), "a\r\nb\r\nlast"; got != want {
		t.Fatalf("CRLF sort = %q, want %q", got, want)
	}
}

func TestEditorSortLinesKeepsUnterminatedFinalLineSeparated(t *testing.T) {
	ev := newDuplicateLineEditor(t, "zebra\napple")

	if err := ev.SortLines(true, true); err != nil {
		t.Fatalf("sort with unterminated final line: %v", err)
	}
	if got, want := ev.Pt.String(), "apple\nzebra"; got != want {
		t.Fatalf("sort with unterminated final line = %q, want %q", got, want)
	}
}

func TestEditorSortLinesMovesUnterminatedFinalLine(t *testing.T) {
	ev := newDuplicateLineEditor(t, "z\nalpha\nlast")
	if err := ev.SortLines(true, true); err != nil {
		t.Fatalf("sort unterminated final line: %v", err)
	}
	if got, want := ev.Pt.String(), "alpha\nlast\nz"; got != want {
		t.Fatalf("sort moved unterminated final line = %q, want %q", got, want)
	}
}
