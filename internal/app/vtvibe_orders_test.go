package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/vtvibe"
)

func TestAIOrdersTextListsOpenFirst(t *testing.T) {
	text := aiOrdersText([]vtvibe.Order{{ID: 1, Text: "done one", Done: true}, {ID: 2, Text: "still open\nsecond line"}})
	lines := strings.Split(text, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "#2 ") || !strings.HasPrefix(lines[1], "#1 ") || strings.Contains(lines[0], "\n") {
		t.Fatalf("lines = %q", lines)
	}
}
