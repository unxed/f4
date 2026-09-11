package ttyx

import (
	"errors"
	"testing"
)

func TestOpenRequiresDisplay(t *testing.T) {
	_, err := open(func(string) string { return "  " }, nil)
	if !errors.Is(err, ErrNoDisplay) {
		t.Fatalf("open without DISPLAY = %v, want %v", err, ErrNoDisplay)
	}
}
