package main

import (
	"testing"

	"github.com/unxed/vtui"
)

func init() {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
}

func TestTerminalView_SaveRestoreCursor(t *testing.T) {
	tv := NewTerminalView(80, 24)

	// Set a specific cursor position
	tv.SetCursor(42, 12)

	// Save it
	tv.SaveCursor()

	// Move cursor somewhere else
	tv.SetCursor(0, 0)
	if tv.CursorX != 0 || tv.CursorY != 0 {
		t.Fatal("Failed to move cursor")
	}

	// Restore and verify
	tv.RestoreCursor()
	if tv.CursorX != 42 || tv.CursorY != 12 {
		t.Errorf("Expected restored cursor at (42, 12), got (%d, %d)", tv.CursorX, tv.CursorY)
	}
}

func TestTerminalView_ScrollingRegion(t *testing.T) {
	tv := NewTerminalView(80, 10)
	// Set scrolling region: lines 2 to 5 (1-based: 3;6)
	tv.ScrollTop = 2
	tv.ScrollBottom = 5

	// Filling line 5
	tv.SetCursor(0, 5)
	tv.PutChar('X', 0)

	// Call scroll in region (newline on the last line of region)
	tv.SetCursor(0, 5)
	tv.PutChar('\n', 0)

	// Check: line 5 should be empty, and 'X' should move to line 4
	if tv.Lines[4][0].Char != 'X' {
		t.Errorf("Scroll region failed: 'X' should be at line 4, got %c at line 5", rune(tv.Lines[5][0].Char))
	}
	if tv.Lines[5][0].Char != ' ' {
		t.Error("Scroll region failed: line 5 should be cleared")
	}
}
func TestTerminalView_AutoWrap(t *testing.T) {
	width := 10
	tv := NewTerminalView(width, 5)
	tv.SetCursor(0, 0)

	// Write 10 characters (fill line)
	for i := 0; i < 10; i++ {
		tv.PutChar('X', 0)
	}

	if tv.CursorX != 10 { // On the edge
		t.Errorf("CursorX should be 10, got %d", tv.CursorX)
	}

	// Write 11th character. Auto-wrap should occur.
	tv.PutChar('Y', 0)

	if tv.CursorY != 1 {
		t.Errorf("Auto-wrap failed: CursorY should be 1, got %d", tv.CursorY)
	}
	if tv.CursorX != 1 {
		t.Errorf("Auto-wrap failed: CursorX should be 1, got %d", tv.CursorX)
	}
	if tv.Lines[1][0].Char != 'Y' {
		t.Errorf("Auto-wrap failed: 'Y' should be at (0, 1), got %c", rune(tv.Lines[1][0].Char))
	}
}
