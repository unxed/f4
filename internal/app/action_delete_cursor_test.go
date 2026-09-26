package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4 #1430: after a delete attempt, the cursor must stay on a file that
// failed to delete instead of always jumping to whatever GetSuccessorName
// picked before the operation ran (that name is only right when every
// requested file was actually removed).
func TestDeleteRefreshCallback_StaysOnSurvivingFile(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmp := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(tmp)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// "a.txt" deleted fine; "b.txt" is still here, as if removing it failed.
	if err := os.Remove(filepath.Join(tmp, "a.txt")); err != nil {
		t.Fatal(err)
	}
	fsp.PendingSelection = "c.txt" // what GetSuccessorName picked before the op ran

	deleteRefreshCallback(pf, fsp, fsp.Vfs, tmp, []string{"a.txt", "b.txt"})()

	if fsp.PendingSelection != "b.txt" {
		t.Errorf("PendingSelection = %q, want %q (the file that failed to delete)", fsp.PendingSelection, "b.txt")
	}
}

// When every requested name is actually gone, the pre-computed successor
// (whatever the caller already put in PendingSelection) must survive
// untouched.
func TestDeleteRefreshCallback_KeepsSuccessorWhenAllDeleted(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "c.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(tmp)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	fsp.PendingSelection = "c.txt"

	// Neither "a.txt" nor "b.txt" exists: both deletes succeeded.
	deleteRefreshCallback(pf, fsp, fsp.Vfs, tmp, []string{"a.txt", "b.txt"})()

	if fsp.PendingSelection != "c.txt" {
		t.Errorf("PendingSelection = %q, want the untouched successor %q", fsp.PendingSelection, "c.txt")
	}
}
