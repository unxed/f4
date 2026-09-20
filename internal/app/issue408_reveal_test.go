package app

import (
	"os"
	"path/filepath"
	"testing"
)

func writeHistoryFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// #nosec G703 -- the path is inside the private test temp directory.
	if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Ctrl+F10 in the viewer and editor history shows the file in the panel: its
// folder is opened and the cursor is put on it (#408).
func TestIssue408RevealHistoryEntryShowsTheFileInThePanel(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)
	pumpUntil(t, "the first folder to load", func() bool { return len(fsp.Entries) > 1 })

	other := t.TempDir()
	for _, name := range []string{"aaa.txt", "zzz.txt"} {
		writeHistoryFile(t, other, name)
	}
	target := writeHistoryFile(t, other, "target.txt")

	if !revealViewerEditorHistoryEntry(pf, viewerEditorHistoryEntry{Path: target, Local: true}) {
		t.Fatal("the entry could not be shown")
	}
	pumpUntil(t, "the folder of the entry with the cursor on the file", func() bool {
		return filepath.Clean(fsp.Vfs.GetPath()) == filepath.Clean(other) && fsp.GetSelectedName() == "target.txt"
	})

	// A file of the folder that is already open: nothing reloads, the cursor
	// still has to move.
	if !revealViewerEditorHistoryEntry(pf, viewerEditorHistoryEntry{Path: filepath.Join(other, "zzz.txt"), Local: true}) {
		t.Fatal("the entry of the current folder could not be shown")
	}
	pumpUntil(t, "the cursor on a file of the open folder", func() bool { return fsp.GetSelectedName() == "zzz.txt" })
	_ = dir
}

// A file that is gone still gets its folder shown.
func TestIssue408RevealHistoryEntryOfAMissingFileShowsItsFolder(t *testing.T) {
	pf, fsp, _ := setupRenameConflict(t)
	other := t.TempDir()
	writeHistoryFile(t, other, "still-here.txt")

	if !revealViewerEditorHistoryEntry(pf, viewerEditorHistoryEntry{Path: filepath.Join(other, "gone.txt"), Local: true}) {
		t.Fatal("the entry of a missing file could not be shown")
	}
	pumpUntil(t, "the folder of the missing file", func() bool {
		return filepath.Clean(fsp.Vfs.GetPath()) == filepath.Clean(other) && len(fsp.Entries) > 1
	})
}
