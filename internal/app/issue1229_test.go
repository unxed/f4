package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// pumpUntil runs queued UI tasks until cond holds or the deadline passes.
func pumpUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !cond() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func setupRenameConflict(t *testing.T) (*panel.PanelsFrame, *panel.FileSystemPanel, string) {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	dir := t.TempDir()
	for name, data := range map[string]string{"a.txt": "A", "b.txt": "B"} {
		// #nosec G703 -- the path is inside the private test temp directory.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(dir)
	pf.ActiveIdx = 0
	return pf, fsp, dir
}

func topDialog(t *testing.T) *vtui.Window {
	t.Helper()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame is %T, want a dialog", vtui.FrameManager.GetTopFrame())
	}
	return dlg
}

func readFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	// #nosec G304 G703 -- the path is inside the private test temp directory.
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// TestIssue1229RenameOntoExistingFileAsksToOverwrite is the regression test
// for issue #1229: Shift+F6 to a name that is taken only reported "file
// exists", while Shift+F5 (copy) offers to overwrite. Rename now asks first,
// and nothing is replaced until the user agrees.
func TestIssue1229RenameOntoExistingFileAsksToOverwrite(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)

	renameEntry(pf, fsp, "a.txt", "b.txt")
	pumpUntil(t, "the overwrite question", func() bool {
		dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		return ok && dlg.OnResult != nil
	})
	if got, _ := readFile(t, filepath.Join(dir, "b.txt")); got != "B" {
		t.Fatalf("b.txt was replaced before the user agreed: %q", got)
	}

	topDialog(t).OnResult(0) // "Overwrite" is the first button
	pumpUntil(t, "the rename", func() bool {
		// Stat, not a read: on Windows a file that is being renamed cannot be
		// opened, and the sharing violation is not a failure of the test.
		_, err := os.Stat(filepath.Join(dir, "a.txt"))
		return os.IsNotExist(err)
	})
	if got, _ := readFile(t, filepath.Join(dir, "b.txt")); got != "A" {
		t.Errorf("b.txt = %q after overwriting, want the content of a.txt", got)
	}
}

// TestIssue1229RenameOntoExistingFileCanBeCancelled checks the other answer.
func TestIssue1229RenameOntoExistingFileCanBeCancelled(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)

	renameEntry(pf, fsp, "a.txt", "b.txt")
	pumpUntil(t, "the overwrite question", func() bool {
		dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		return ok && dlg.OnResult != nil
	})
	topDialog(t).OnResult(1) // Cancel
	for i := 0; i < 20; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got, ok := readFile(t, filepath.Join(dir, "a.txt")); !ok || got != "A" {
		t.Errorf("a.txt = %q (exists=%v) after cancelling, want it untouched", got, ok)
	}
	if got, _ := readFile(t, filepath.Join(dir, "b.txt")); got != "B" {
		t.Errorf("b.txt = %q after cancelling, want it untouched", got)
	}
}

// A rename whose new name differs from the old one only in letter case is one
// entry on a file system that folds case, but two files on one that does not.
// There, renaming a.txt onto an existing A.txt must ask like any other rename
// onto an existing file, and not replace it silently (#1229).
func TestIssue1229CaseOnlyRenameOntoAnotherFileAsks(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)
	upper := filepath.Join(dir, "A.txt")
	// #nosec G304 G703 -- the path is inside the private test temp directory.
	f, err := os.OpenFile(upper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			t.Skip("this file system folds letter case: a.txt and A.txt are one file")
		}
		t.Fatal(err)
	}
	_, _ = f.WriteString("UPPER")
	_ = f.Close()

	renameEntry(pf, fsp, "a.txt", "A.txt")
	pumpUntil(t, "the overwrite question", func() bool {
		dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		return ok && dlg.OnResult != nil
	})
	if got, _ := readFile(t, upper); got != "UPPER" {
		t.Fatalf("A.txt was replaced before the user agreed: %q", got)
	}
	topDialog(t).OnResult(1) // Cancel
}

// With nothing in the way a case-only rename just happens, on any file system.
func TestIssue1229CaseOnlyRenameWithNothingInTheWayJustRenames(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)

	renameEntry(pf, fsp, "a.txt", "A.txt")
	pumpUntil(t, "the rename", func() bool {
		// #nosec G304 G703 -- the path is inside the private test temp directory.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() == "A.txt" {
				return true
			}
		}
		return false
	})
	if dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok && dlg.OnResult != nil {
		t.Fatal("asked to overwrite although nothing was in the way")
	}
}
