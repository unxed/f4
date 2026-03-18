package piecetable

import (
	"testing"
)

func TestPieceTable_Basic(t *testing.T) {
	pt := New([]byte("Hello"))

	if pt.String() != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", pt.String())
	}
	if pt.Size() != 5 {
		t.Errorf("Expected size 5, got %d", pt.Size())
	}
}

func TestPieceTable_Insert(t *testing.T) {
	pt := New([]byte("Hello"))

	// Insert at end (Append)
	pt.Insert(5, []byte(" World"))
	if pt.String() != "Hello World" {
		t.Errorf("Insert end failed: %s", pt.String())
	}

	// Addition optimization: adding character at end, pieces should merge
	pt.Insert(11, []byte("!"))
	if pt.String() != "Hello World!" {
		t.Errorf("Insert optimization failed: %s", pt.String())
	}
	// We should have exactly 2 pieces: [Hello] and [ World!]
	if len(pt.pieces) != 2 {
		t.Errorf("Optimization failed, expected 2 pieces, got %d", len(pt.pieces))
	}

	// Insert at start
	pt.Insert(0, []byte("Say "))
	if pt.String() != "Say Hello World!" {
		t.Errorf("Insert start failed: %s", pt.String())
	}

	// Insert in middle (splitting original buffer)
	pt.Insert(6, []byte("o "))
	if pt.String() != "Say Heo llo World!" {
		t.Errorf("Insert middle failed: %s", pt.String())
	}
}

func TestPieceTable_Delete(t *testing.T) {
	pt := New([]byte("Hello World!"))

	// Deleting from middle of one piece
	pt.Delete(5, 6) // Remove " World"
	if pt.String() != "Hello!" {
		t.Errorf("Delete middle failed: %s", pt.String())
	}
	// After middle deletion, 1 piece should become 2
	if len(pt.pieces) != 2 {
		t.Errorf("Expected 2 pieces after middle delete, got %d", len(pt.pieces))
	}

	// Deleting on boundary (capturing end of left and start of right piece)
	pt.Insert(5, []byte(" World")) // Restored: "Hello World!" -> pieces: ["Hello"], [" World"], ["!"]

	pt.Delete(4, 3) // Remove "o W" -> Should leave "Hellorld!"
	if pt.String() != "Hellorld!" {
		t.Errorf("Delete across boundary failed: %s", pt.String())
	}

	// Deleting all text
	pt.Delete(0, pt.Size())
	if pt.String() != "" {
		t.Errorf("Delete all failed: '%s'", pt.String())
	}
	if pt.Size() != 0 {
		t.Errorf("Expected size 0, got %d", pt.Size())
	}
}

func TestPieceTable_Complex(t *testing.T) {
	pt := New([]byte("The quick brown fox jumps over the lazy dog"))

	pt.Delete(16, 4) // "The quick brown jumps over the lazy dog"
	pt.Insert(16, []byte("cat ")) // "The quick brown cat jumps over the lazy dog"
	pt.Delete(0, 4) // "quick brown cat jumps over the lazy dog"
	pt.Insert(pt.Size(), []byte(".")) // "quick brown cat jumps over the lazy dog."

	expected := "quick brown cat jumps over the lazy dog."
	if pt.String() != expected {
		t.Errorf("Complex test failed:\nExpected: %s\nGot:      %s", expected, pt.String())
	}
}

func TestPieceTable_GetRange(t *testing.T) {
	pt := New([]byte("0123456789"))
	pt.Insert(5, []byte("abc")) // "01234abc56789"

	// 1. Range from original buffer
	if string(pt.GetRange(1, 3)) != "123" {
		t.Error("GetRange failed on original buffer")
	}

	// 2. Range from add buffer
	if string(pt.GetRange(6, 1)) != "b" {
		t.Error("GetRange failed on add buffer")
	}

	// 3. Range spanning multiple pieces
	if string(pt.GetRange(4, 4)) != "4abc" {
		t.Error("GetRange failed on spanning pieces")
	}

	// 4. Edge cases
	if string(pt.GetRange(0, pt.Size())) != "01234abc56789" {
		t.Error("GetRange failed on full range")
	}
	if pt.GetRange(-1, 5) != nil || pt.GetRange(0, 100) != nil {
		t.Error("GetRange should return nil for invalid ranges")
	}
}
