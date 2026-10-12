package vtvibe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// f4#1842, stage H9 item 4: editing a fragment and searching the tree.

func runTool(t *testing.T, tool Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return tool.Run(context.Background(), raw)
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEditFileReplacesAFragment(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "x := 1\ny := 1\nz := 2\n"})
	edit := EditFileTool(dir)
	if _, err := runTool(t, edit, map[string]any{"path": "a.go", "old_text": ":= 1", "new_text": ":= 3"}); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("an ambiguous fragment was replaced: %v", err)
	}
	if _, err := runTool(t, edit, map[string]any{"path": "a.go", "old_text": "w := 9", "new_text": "w := 0"}); err == nil {
		t.Fatal("a missing fragment did not fail")
	}
	if out, err := runTool(t, edit, map[string]any{"path": "a.go", "old_text": "z := 2", "new_text": "z := 5"}); err != nil || !strings.Contains(out, "1 occurrence") {
		t.Fatalf("%q, %v", out, err)
	}
	if out, err := runTool(t, edit, map[string]any{"path": "a.go", "old_text": ":= 1", "new_text": ":= 3", "replace_all": true}); err != nil || !strings.Contains(out, "2 occurrence") {
		t.Fatalf("%q, %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(data) != "x := 3\ny := 3\nz := 5\n" {
		t.Fatalf("file %q", data)
	}
	if info, _ := os.Stat(filepath.Join(dir, "a.go")); info.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("the file lost its mode: %v", info.Mode())
	}
}

func TestGrepAndFindFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":          "package main\n// TODO: fix\nfunc main() {}\n",
		"sub/util.go":      "package sub\n// todo later\n",
		"sub/notes.md":     "TODO in markdown\n",
		".git/config":      "TODO hidden\n",
		"bin/blob.dat":     "TODO\x00binary",
		"sub/util_test.go": "package sub\n",
	})
	out, err := runTool(t, GrepTool(dir), map[string]any{"pattern": "TODO"})
	if err != nil || !strings.Contains(out, "main.go:2: // TODO: fix") || !strings.Contains(out, "sub/notes.md:1: TODO in markdown") ||
		strings.Contains(out, ".git") || strings.Contains(out, "blob") || strings.Contains(out, "todo later") {
		t.Fatalf("grep: %q, %v", out, err)
	}
	out, _ = runTool(t, GrepTool(dir), map[string]any{"pattern": "(?i)todo", "glob": "*.go", "path": "sub"})
	if out != "util.go:2: // todo later" {
		t.Fatalf("grep in sub for *.go: %q", out)
	}
	if out, _ := runTool(t, GrepTool(dir), map[string]any{"pattern": "nothing-here"}); out != "no matches" {
		t.Fatalf("no matches: %q", out)
	}
	if _, err := runTool(t, GrepTool(dir), map[string]any{"pattern": "("}); err == nil {
		t.Fatal("a bad pattern did not fail")
	}
	out, err = runTool(t, FindFilesTool(dir), map[string]any{"glob": "*.go"})
	if err != nil || out != "main.go\nsub/util.go\nsub/util_test.go" {
		t.Fatalf("find_files: %q, %v", out, err)
	}
	if _, err := runTool(t, FindFilesTool(dir), map[string]any{"glob": "["}); err == nil {
		t.Fatal("a bad glob did not fail")
	}
}

func TestWorkersGetTheNewTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range WorkTools(t.TempDir()) {
		names[tool.Name] = true
	}
	for _, want := range []string{"shell", "read_file", "write_file", "edit_file", "grep", "find_files", "fetch_url"} {
		if !names[want] {
			t.Errorf("no %s tool", want)
		}
	}
}
