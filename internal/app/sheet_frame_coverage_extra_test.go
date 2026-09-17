package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/sheet"
)

func TestSheetFrameGeometryAndEditingHelpers(t *testing.T) {
	sf := newSheetFrameForTest(t)
	if sf.visibleRows() < 1 || sf.gridWidth() < 1 || len(sf.visibleColumns()) == 0 {
		t.Fatalf("sheet geometry = rows %d, width %d, columns %#v", sf.visibleRows(), sf.gridWidth(), sf.visibleColumns())
	}

	if got := sf.currentText(); got != "" {
		t.Fatalf("empty currentText() = %q", got)
	}
	sf.beginEdit("draft")
	if got := sf.currentText(); got != "draft" {
		t.Fatalf("editing currentText() = %q", got)
	}
	sf.cancelEdit()
	sf.Document().SetText(2, 3, "stored")
	sf.gotoCell(2, 3)
	if got := sf.currentText(); got != "stored" {
		t.Fatalf("stored currentText() = %q", got)
	}

	sf.SetPath("/tmp/book.f4s.sqlite")
	if !strings.Contains(sf.GetTitle(), "book.f4s.sqlite") {
		t.Fatalf("sheet title = %q", sf.GetTitle())
	}
	if got := sf.Block(); got != (sheet.Rect{Left: 2, Top: 3, Right: 2, Bottom: 3}) {
		t.Fatalf("unmarked block = %+v", got)
	}

	sf.gotoCell(-10, sheet.MaxRows+10)
	if got := sf.Cursor(); got.Col != 0 || got.Row != sheet.MaxRows-1 {
		t.Fatalf("clamped cursor = %+v", got)
	}
	for _, tc := range []struct {
		value, low, high, want int
	}{
		{-1, 0, 10, 0},
		{5, 0, 10, 5},
		{11, 0, 10, 10},
	} {
		if got := clampInt(tc.value, tc.low, tc.high); got != tc.want {
			t.Errorf("clampInt(%d,%d,%d) = %d, want %d", tc.value, tc.low, tc.high, got, tc.want)
		}
	}
}
