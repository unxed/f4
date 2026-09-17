package editor

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
)

func TestEditorStatusPositionAndModes(t *testing.T) {
	if line, total, col, width := (&EditorView{CursorLine: -2, CursorPos: -3}).editorStatusPosition(); line != 1 || total != 1 || col != 1 || width != 1 {
		t.Fatalf("nil-index position = %d/%d col %d width %d", line, total, col, width)
	}

	ev := NewEditorView(piecetable.New([]byte("one\n世界\nlast")), nil, "status.txt")
	t.Cleanup(func() { ev.Close() })
	ev.CursorLine = 1
	ev.CursorPos = 2
	if line, total, col, width := ev.editorStatusPosition(); line != 2 || total != 3 || col < 1 || width != 1 {
		t.Fatalf("normal position = %d/%d col %d width %d", line, total, col, width)
	}
	if got := ev.editorStatusPositionText(); !strings.Contains(got, "Ln 2/3") || !strings.Contains(got, "Col") {
		t.Fatalf("position text = %q", got)
	}

	ev.Modified = true
	if got := ev.EditorStatusText(); !strings.Contains(got, "*") || !strings.Contains(got, "Ln") {
		t.Fatalf("regular status = %q", got)
	}

	ev.colorerIndexing = true
	ev.colorerTotal = 0
	if got := ev.EditorStatusText(); !strings.Contains(got, "Colorer 0%") {
		t.Fatalf("zero colorer status = %q", got)
	}
	ev.colorerTotal = 4
	ev.colorerProgress = 9
	if got := ev.EditorStatusText(); !strings.Contains(got, "Colorer 100%") {
		t.Fatalf("clamped colorer status = %q", got)
	}
	ev.colorerIndexing = false

	ev.IndexStatus = IndexStatus{Phase: IndexScanning, Scanned: 1, Total: 4}
	if got := ev.EditorStatusText(); !strings.Contains(got, "25%") {
		t.Fatalf("indexing status = %q", got)
	}

	ev.IndexStatus = IndexStatus{}
	ev.DecodeMode = true
	ev.CursorPos = 1
	if got := ev.EditorStatusText(); !strings.Contains(got, "0x") {
		t.Fatalf("decode status = %q", got)
	}
	ev.DecodeMode = false
	ev.HexMode = true
	if got := ev.EditorStatusText(); !strings.Contains(got, "Hex") || !strings.Contains(got, "0x") {
		t.Fatalf("hex status = %q", got)
	}
}

func TestEditorStatusPositionKeepsLazyCursorConsistent(t *testing.T) {
	ev := &EditorView{CursorLine: 4, CursorPos: 8, Li: piecetable.NewLineIndex()}
	if line, total, col, width := ev.editorStatusPosition(); line != 5 || total != 5 || col != 9 || width != 1 {
		t.Fatalf("out-of-index position = %d/%d col %d width %d", line, total, col, width)
	}
}
