package panel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// navigateElevatedDirectoryAsync's plumbing — the background resolve, the
// RunOnUI hop, and the CommitPath + ReadDirectory it triggers — must behave
// exactly like the synchronous Enter-key path from the user's point of view
// (f4#1411). This does not need a real elevation route: it calls the method
// directly, the same way ProcessKey does once NeedsElevation (covered by
// vfs.TestOSVFSNeedsElevation) has already said yes.
func TestNavigateElevatedDirectoryAsyncAppliesResult(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(root))
	waitForLoad(t, fp)

	osfs, ok := fp.Vfs.(*vfs.OSVFS)
	if !ok {
		t.Fatalf("fp.Vfs is %T, want *vfs.OSVFS", fp.Vfs)
	}
	newPath := fp.Vfs.Join(root, "sub")

	fp.navigateElevatedDirectoryAsync(osfs, newPath, root, "sub")

	deadline := time.After(2 * time.Second)
	for fp.Vfs.GetPath() != newPath {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatalf("timeout waiting for async navigation; path is %q, want %q", fp.Vfs.GetPath(), newPath)
		}
	}
	waitForLoad(t, fp)

	if fp.PendingSelection != ".." {
		t.Errorf("PendingSelection = %q, want %q", fp.PendingSelection, "..")
	}
}

// If the panel navigates away while the background resolve is still in
// flight, applying its (now stale) result must be a no-op rather than
// clobbering wherever the panel actually is.
func TestNavigateElevatedDirectoryAsyncIgnoresStaleResult(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	root := t.TempDir()
	for _, name := range []string{"sub", "other"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(root))
	waitForLoad(t, fp)

	osfs, ok := fp.Vfs.(*vfs.OSVFS)
	if !ok {
		t.Fatalf("fp.Vfs is %T, want *vfs.OSVFS", fp.Vfs)
	}
	stalePath := fp.Vfs.Join(root, "sub")
	fp.navigateElevatedDirectoryAsync(osfs, stalePath, root, "sub")

	// Move the panel elsewhere before the async result above is applied,
	// mirroring what ProcessKey does on the synchronous path.
	otherPath := fp.Vfs.Join(root, "other")
	if err := fp.SetKnownDirectoryPath(otherPath); err != nil {
		t.Fatalf("SetKnownDirectoryPath(%q): %v", otherPath, err)
	}
	fp.ReadDirectory()
	waitForLoad(t, fp)

	// Drain whatever the stale async navigation posts; it must not move the
	// panel back to stalePath.
	drainDeadline := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-drainDeadline:
			break drain
		}
	}

	if fp.Vfs.GetPath() != otherPath {
		t.Fatalf("panel path = %q, want it to have stayed at %q", fp.Vfs.GetPath(), otherPath)
	}
}
