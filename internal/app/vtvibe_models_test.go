package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/vtvibe"
)

func TestAIModelLinesPutFreeModelsFirst(t *testing.T) {
	lines := aiModelLines([]vtvibe.ModelInfo{{ID: "paid-a"}, {ID: "x:free", Free: true}, {ID: "paid-b"}}, 2)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "x:free") || lines[1] != "paid-a" {
		t.Fatalf("lines = %q", lines)
	}
}
