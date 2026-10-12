package vtvibe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// f4#1842, stage H9 item 3: the user's own commands.

func TestCommandsAreFilesInAFolder(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"review.md": "Review $ARGUMENTS for bugs.", "plan.md": "Make a plan.", "notes.txt": "x", "bad name.md": "x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ListCommands(dir)
	if err != nil || len(names) != 2 || names[0] != "plan" || names[1] != "review" {
		t.Fatalf("names %q, %v", names, err)
	}
	text, err := LoadCommand(dir, "review")
	if err != nil || ExpandCommand(text, " main.go ") != "Review main.go for bugs." {
		t.Fatalf("review: %q, %v", text, err)
	}
	text, _ = LoadCommand(dir, "plan")
	if got := ExpandCommand(text, "for the release"); got != "Make a plan.\n\nfor the release" {
		t.Fatalf("plan with arguments: %q", got)
	}
	if got := ExpandCommand(text, ""); got != "Make a plan." {
		t.Fatalf("plan without arguments: %q", got)
	}
	if _, err := LoadCommand(dir, "missing"); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := LoadCommand(dir, "../secret"); err == nil || errors.Is(err, ErrNoCommand) {
		t.Fatalf("a path was taken as a name: %v", err)
	}
	if names, err := ListCommands(filepath.Join(dir, "none")); err != nil || names != nil {
		t.Fatalf("missing folder: %q, %v", names, err)
	}
}
