package piecetable

import "testing"

func TestLineIndex_Build(t *testing.T) {
	// Текст:
	// Line 1 (6 bytes: L,i,n,e,1,\n)
	// Line 2 (6 bytes: L,i,n,e,2,\n)
	// Line 3 (5 bytes: L,i,n,e,3)
	pt := New([]byte("Line 1\nLine 2\nLine 3"))
	li := NewLineIndex()
	li.Rebuild(pt)

	if li.LineCount() != 3 {
		t.Errorf("Expected 3 lines, got %d", li.LineCount())
	}

	// Проверка смещений
	if li.GetLineOffset(0) != 0 {
		t.Errorf("Line 0 offset: expected 0, got %d", li.GetLineOffset(0))
	}
	if li.GetLineOffset(1) != 7 { // "Line 1\n" -> 7 bytes
		t.Errorf("Line 1 offset: expected 7, got %d", li.GetLineOffset(1))
	}
	if li.GetLineOffset(2) != 14 { // "Line 1\nLine 2\n" -> 14 bytes
		t.Errorf("Line 2 offset: expected 14, got %d", li.GetLineOffset(2))
	}
}

func TestLineIndex_GetLineAtOffset(t *testing.T) {
	pt := New([]byte("AAA\nBBB\nCCC"))
	li := NewLineIndex()
	li.Rebuild(pt)
	// Offsets: [0, 4, 8]

	tests := []struct {
		offset int
		want   int
	}{
		{0, 0}, {1, 0}, {3, 0},
		{4, 1}, {5, 1}, {7, 1},
		{8, 2}, {10, 2},
	}

	for _, tt := range tests {
		got := li.GetLineAtOffset(tt.offset)
		if got != tt.want {
			t.Errorf("At offset %d: expected line %d, got %d", tt.offset, tt.want, got)
		}
	}
}

func TestLineIndex_Empty(t *testing.T) {
	pt := New([]byte(""))
	li := NewLineIndex()
	li.Rebuild(pt)

	if li.LineCount() != 1 {
		t.Errorf("Empty file should have 1 line, got %d", li.LineCount())
	}
	if li.GetLineOffset(0) != 0 {
		t.Error("Line 0 offset should be 0 even for empty file")
	}
}