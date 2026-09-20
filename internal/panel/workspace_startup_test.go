package panel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// `f4 dir/file` (issue #991): the panel shows the file's folder with the cursor
// on the file, so closing the viewer the start opened lands on it. The right
// panel still takes the other startup path, and the focus the path named first.
func TestApplyStartupDirsFileOpensItsFolderWithCursorOnIt(t *testing.T) {
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "folder")
	other := filepath.Join(root, "other")
	for _, dir := range []string{folder, other} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Enough names before the target that a cursor left at the top could not
	// be on it by accident.
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "target.txt", "z.txt"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(folder, "target.txt")

	pf := &PanelsFrame{ActiveIdx: 1}
	lp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	rp := NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS(root))
	pf.Panels[0] = lp
	pf.Panels[1] = rp
	defer pf.Close()
	waitForLoad(t, lp)
	waitForLoad(t, rp)

	if !IsStartupFile(target) || IsStartupFile(folder) || IsStartupFile(filepath.Join(folder, "missing")) {
		t.Fatalf("IsStartupFile: file %t, folder %t, missing %t; want true, false, false",
			IsStartupFile(target), IsStartupFile(folder), IsStartupFile(filepath.Join(folder, "missing")))
	}

	ApplyStartupDirs(pf, target, other)
	waitForLoad(t, lp)
	waitForLoad(t, rp)

	if got := filepath.Clean(lp.Vfs.GetPath()); got != folder {
		t.Fatalf("left panel path = %q, want the file's folder %q", got, folder)
	}
	if got := lp.GetRawSelectedName(); got != "target.txt" {
		t.Fatalf("left panel cursor on %q, want target.txt", got)
	}
	if got := filepath.Clean(rp.Vfs.GetPath()); got != other {
		t.Fatalf("right panel path = %q, want %q", got, other)
	}
	if pf.ActiveIdx != 0 {
		t.Fatalf("ActiveIdx = %d, want 0: the focus belongs to the path named first", pf.ActiveIdx)
	}

	// A client attaching to a running daemon applies its startup paths to a
	// workspace that may be in that folder already, with the cursor elsewhere.
	lp.SelectName("a.txt")
	ApplyStartupDirs(pf, target, other)
	waitForLoad(t, lp)
	if got := lp.GetRawSelectedName(); got != "target.txt" {
		t.Fatalf("starting again in the folder already shown leaves the cursor on %q, want target.txt", got)
	}
}

// far2l (issue #495): a single folder on the command line replaces its own
// panel and leaves the other as the session restored it, which
// StartupKeepPanel says in place of a path.
func TestApplyStartupDirsKeepLeavesThePanelAlone(t *testing.T) {
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{ActiveIdx: 1}
	lp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	rp := NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS(root))
	pf.Panels[0] = lp
	pf.Panels[1] = rp
	defer pf.Close()
	waitForLoad(t, lp)
	waitForLoad(t, rp)

	ApplyStartupDirs(pf, folder, StartupKeepPanel)
	waitForLoad(t, lp)
	waitForLoad(t, rp)

	if got := filepath.Clean(lp.Vfs.GetPath()); got != folder {
		t.Fatalf("left panel path = %q, want the folder %q", got, folder)
	}
	if got := filepath.Clean(rp.Vfs.GetPath()); got != root {
		t.Fatalf("right panel path = %q, want it kept at %q", got, root)
	}
	if pf.ActiveIdx != 0 {
		t.Fatalf("ActiveIdx = %d, want 0: the focus belongs to the folder named", pf.ActiveIdx)
	}
}
