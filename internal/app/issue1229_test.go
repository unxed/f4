package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// pumpUntil runs queued UI tasks until cond holds or the deadline passes.
func pumpUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
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

func drainRenameTasks(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
			return
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
	paneltest.WaitForLoad(t, fsp)
	t.Cleanup(func() { drainRenameTasks(t) })
	return pf, fsp, dir
}

// waitOverwriteQuestion waits for the rename's own question: a new warning
// dialog on top, not before. Any window with OnResult used to count, and on
// CI (linux/arm64, 10-10-2026) the test once timed out waiting for the
// rename after answering: a dialog posted by an earlier test's leftover task
// had come up first and got the answer meant for the question.
func waitOverwriteQuestion(t *testing.T, before vtui.Frame) *vtui.Window {
	t.Helper()
	var question *vtui.Window
	pumpUntil(t, "the overwrite question", func() bool {
		top := vtui.FrameManager.GetTopFrame()
		dlg, ok := top.(*vtui.Window)
		if !ok || top == before || dlg.OnResult == nil ||
			strings.TrimSpace(dlg.GetTitle()) != strings.TrimSpace(i18n.Msg("Warning.Title")) {
			return false
		}
		question = dlg
		return true
	})
	return question
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

	before := vtui.FrameManager.GetTopFrame()
	renameEntry(pf, fsp, "a.txt", "b.txt")
	question := waitOverwriteQuestion(t, before)
	if got, _ := readFile(t, filepath.Join(dir, "b.txt")); got != "B" {
		t.Fatalf("b.txt was replaced before the user agreed: %q", got)
	}

	question.OnResult(0) // "Overwrite" is the first button
	pumpUntil(t, "the rename", func() bool {
		// Stat, not a read: on Windows a file that is being renamed cannot be
		// opened, and the sharing violation is not a failure of the test.
		_, err := os.Stat(filepath.Join(dir, "a.txt"))
		return os.IsNotExist(err)
	})
	// a.txt being gone does not mean the rename has let go of b.txt yet: on
	// Windows (seen on windows/arm64) opening it right away can still hit
	// the same sharing violation, so wait until it can actually be read.
	pumpUntil(t, "b.txt to become readable after the rename", func() bool {
		// #nosec G304 G703 -- the path is inside the private test temp directory.
		_, err := os.ReadFile(filepath.Join(dir, "b.txt"))
		return err == nil
	})
	if got, _ := readFile(t, filepath.Join(dir, "b.txt")); got != "A" {
		t.Errorf("b.txt = %q after overwriting, want the content of a.txt", got)
	}
}

// TestIssue1229RenameOntoExistingFileCanBeCancelled checks the other answer.
func TestIssue1229RenameOntoExistingFileCanBeCancelled(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)

	before := vtui.FrameManager.GetTopFrame()
	renameEntry(pf, fsp, "a.txt", "b.txt")
	question := waitOverwriteQuestion(t, before)
	question.OnResult(1) // Cancel
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

	before := vtui.FrameManager.GetTopFrame()
	renameEntry(pf, fsp, "a.txt", "A.txt")
	question := waitOverwriteQuestion(t, before)
	if got, _ := readFile(t, upper); got != "UPPER" {
		t.Fatalf("A.txt was replaced before the user agreed: %q", got)
	}
	question.OnResult(1) // Cancel
}

// With nothing in the way a case-only rename just happens, on any file system.
func TestIssue1229CaseOnlyRenameWithNothingInTheWayJustRenames(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)

	before := vtui.FrameManager.GetTopFrame()
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
	// Only the rename's own question counts: a new warning on top. Any window
	// with OnResult used to, and on CI (windows/amd64, 12-10-2026) a dialog
	// from an earlier test's leftover task failed the test after the rename
	// had already happened.
	top := vtui.FrameManager.GetTopFrame()
	if dlg, ok := top.(*vtui.Window); ok && top != before && dlg.OnResult != nil &&
		strings.TrimSpace(dlg.GetTitle()) == strings.TrimSpace(i18n.Msg("Warning.Title")) {
		t.Fatal("asked to overwrite although nothing was in the way")
	}
}

// A directory can be renamed to a free name without prompting for overwrite
// or surfacing the filesystem's no-replace reservation as an error (#2208).
func TestIssue2208RenameDirectoryToFreeName(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "testdir")
	newPath := filepath.Join(dir, "testdir_newname")
	if err := os.Mkdir(oldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- the path is inside the private test temp directory.
	if err := os.WriteFile(filepath.Join(oldPath, "inside"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(dir)
	pf.ActiveIdx = 0
	paneltest.WaitForLoad(t, fsp)
	t.Cleanup(func() { drainRenameTasks(t) })

	before := vtui.FrameManager.GetTopFrame()
	renameEntry(pf, fsp, "testdir", "testdir_newname")
	pumpUntil(t, "the directory rename", func() bool {
		_, oldErr := os.Stat(oldPath)
		_, childErr := os.Stat(filepath.Join(newPath, "inside"))
		return os.IsNotExist(oldErr) && childErr == nil
	})
	if top := vtui.FrameManager.GetTopFrame(); top != before {
		if dlg, ok := top.(*vtui.Window); ok && dlg.OnResult != nil {
			t.Fatal("asked to overwrite a free directory name")
		}
	}
}

// A dialog some earlier task left in the UI queue must not get the answer
// meant for the rename's question (see waitOverwriteQuestion).
func TestIssue1229AnswerGoesToTheRenameQuestion(t *testing.T) {
	pf, fsp, dir := setupRenameConflict(t)
	vtui.FrameManager.PostTask(func() {
		stale := vtui.ShowMessage(" Error ", "left over from another test", []string{"&Ok"})
		stale.OnResult = func(int) {}
	})

	before := vtui.FrameManager.GetTopFrame()
	renameEntry(pf, fsp, "a.txt", "b.txt")
	question := waitOverwriteQuestion(t, before)
	question.OnResult(0)
	pumpUntil(t, "the rename", func() bool {
		_, err := os.Stat(filepath.Join(dir, "a.txt"))
		return os.IsNotExist(err)
	})
}
