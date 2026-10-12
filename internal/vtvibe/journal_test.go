package vtvibe

import (
	"os"
	"path/filepath"
	"testing"
)

// f4#1842, stage H9 item 6: a worker's file changes can be undone.

func TestJournalUndoesWritesAndEdits(t *testing.T) {
	dir := writeTree(t, map[string]string{"keep.txt": "original\n", "edit.txt": "a b c\n"})
	var j Journal
	tools := WithJournal(WorkTools(dir), dir, &j)
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	steps := []struct {
		tool string
		args map[string]any
	}{
		{"write_file", map[string]any{"path": "keep.txt", "content": "changed\n"}},
		{"write_file", map[string]any{"path": "keep.txt", "content": "changed twice\n"}},
		{"edit_file", map[string]any{"path": "edit.txt", "old_text": "b", "new_text": "B"}},
		{"write_file", map[string]any{"path": "new/made.txt", "content": "new\n"}},
		{"read_file", map[string]any{"path": "keep.txt"}},
	}
	for _, s := range steps {
		if _, err := runTool(t, byName[s.tool], s.args); err != nil {
			t.Fatalf("%s: %v", s.tool, err)
		}
	}
	if files := j.Files(); len(files) != 3 {
		t.Fatalf("journaled %q", files)
	}
	done, err := j.Undo()
	if err != nil || len(done) != 3 {
		t.Fatalf("undo: %q, %v", done, err)
	}
	for name, want := range map[string]string{"keep.txt": "original\n", "edit.txt": "a b c\n"} {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "made.txt")); !os.IsNotExist(err) {
		t.Errorf("a file the run created was not removed: %v", err)
	}
	if len(j.Files()) != 0 {
		t.Error("the journal kept files after undo")
	}
}
