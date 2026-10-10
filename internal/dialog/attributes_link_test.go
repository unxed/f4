package dialog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#1828: a symbolic link is not pointed at nothing without asking.

func TestLinkTargetExistsRelativeToTheLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(dir)
	link := filepath.Join(dir, "link")
	if !linkTargetExists(t.Context(), v, link, "real.txt") || !linkTargetExists(t.Context(), v, link, filepath.Join(dir, "real.txt")) {
		t.Fatal("an existing target was taken for missing")
	}
	if linkTargetExists(t.Context(), v, link, "missing.txt") {
		t.Fatal("a missing target was taken for existing")
	}
}

func TestConfirmDanglingLinkAsksOnlyForAMissingTarget(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(dir)
	link := filepath.Join(dir, "link")

	ran := 0
	confirmDanglingLink(v, link, "real.txt", false, func() { ran++ })
	confirmDanglingLink(v, link, "missing.txt", true, func() { ran++ }) // a junction: its creation decides
	if ran != 2 {
		t.Fatalf("proceeded %d times, want 2 without asking", ran)
	}

	before := vtui.FrameManager.GetTopFrame()
	confirmDanglingLink(v, link, "missing.txt", false, func() { ran++ })
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok || vtui.FrameManager.GetTopFrame() == before || ran != 2 {
		t.Fatalf("a missing target did not ask first (ran %d)", ran)
	}
	dlg.OnResult(1) // Cancel
	if ran != 2 {
		t.Fatal("Cancel saved the link")
	}
	confirmDanglingLink(v, link, "missing.txt", false, func() { ran++ })
	vtui.FrameManager.GetTopFrame().(*vtui.Window).OnResult(0) // Save
	if ran != 3 {
		t.Fatal("Save did not save the link")
	}
}

// f4#1861: Ctrl+A on a file with several hard links lists their names.

type hardLinkListingVFS struct {
	*vfs.OSVFS
	names []string
}

func (v hardLinkListingVFS) HardLinkNames(context.Context, string) ([]string, error) {
	return v.names, nil
}

func TestAttributesListHardLinkNames(t *testing.T) {
	v := hardLinkListingVFS{OSVFS: vfs.NewOSVFS(t.TempDir()), names: []string{`E:\a.txt`, `E:\b.txt`, `E:\c.txt`, `E:\d.txt`, `E:\e.txt`, `E:\f.txt`}}
	names := attributesHardLinkNames(v, `E:\a.txt`, vfs.VFSItem{Name: "a.txt"}, false)
	if len(names) != 6 {
		t.Fatalf("names %v", names)
	}
	if attributesHardLinkNames(v, `E:\a.txt`, vfs.VFSItem{Name: "a.txt"}, true) != nil ||
		attributesHardLinkNames(v, `E:\dir`, vfs.VFSItem{Name: "dir", IsDir: true}, false) != nil {
		t.Fatal("names were listed for a selection or a folder")
	}
	single := hardLinkListingVFS{OSVFS: v.OSVFS, names: []string{`E:\a.txt`}}
	if attributesHardLinkNames(single, `E:\a.txt`, vfs.VFSItem{Name: "a.txt"}, false) != nil {
		t.Fatal("a file with one name got a list")
	}
	if attributesHardLinkNames(v.OSVFS, `E:\a.txt`, vfs.VFSItem{Name: "a.txt"}, false) != nil {
		t.Fatal("a file system without the names gave some")
	}

	height := func(fs vfs.VFS) int {
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
		ShowAttributesWindows(nil, fs, `E:\a.txt`, vfs.VFSItem{Name: "a.txt"})
		win, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if !ok {
			t.Fatal("no attributes dialog")
		}
		return win.Y2 - win.Y1
	}
	// Six names: the count, four names and "… and 2 more".
	if got, base := height(v), height(v.OSVFS); got != base+2+maxShownLinkNames+1 {
		t.Fatalf("dialog height %d, without links %d", got, base)
	}
}
