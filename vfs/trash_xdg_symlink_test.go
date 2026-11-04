//go:build linux || dragonfly || freebsd || netbsd || openbsd || solaris || illumos

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// unxed/f4#2237: the volume of an item is found by walking up its path, so the
// path must be the real one: ~/LINKS/000 -> /media/x/000 used to make the
// symlink itself the "mount root" and .Trash-$uid went into the linked
// directory, where no desktop looks for it.

func TestResolveTrashSourceFollowsDirectorySymlinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "media", "temp", "000")
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(root, "home", "LINKS")
	if err := os.MkdirAll(links, 0700); err != nil {
		t.Fatal(err)
	}
	absLink := filepath.Join(links, "abs")
	relLink := filepath.Join(links, "rel")
	chain := filepath.Join(links, "chain")
	if err := os.Symlink(realDir, absLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../media/temp/000", relLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("abs", chain); err != nil {
		t.Fatal(err)
	}
	itemLink := filepath.Join(realDir, "itself")
	if err := os.Symlink("nowhere", itemLink); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(realDir, "file")
	for _, via := range []string{absLink, relLink, chain, filepath.Join(links, "..", "LINKS", "abs", ".")} {
		got, err := resolveTrashSource(filepath.Join(via, "file"))
		if err != nil {
			t.Fatalf("resolveTrashSource via %s: %v", via, err)
		}
		if got != want {
			t.Errorf("resolveTrashSource via %s = %q, want %q", via, got, want)
		}
	}
	// The item itself stays as it is: a symlink is trashed, not its target.
	if got, err := resolveTrashSource(filepath.Join(absLink, "itself")); err != nil || got != itemLink {
		t.Errorf("resolveTrashSource(link to item) = %q, %v; want %q", got, err, itemLink)
	}
	if got, err := resolveTrashSource("/"); err != nil || got != "/" {
		t.Errorf("resolveTrashSource(/) = %q, %v", got, err)
	}
}

func TestResolveTrashSourceRejectsLoopAndMissingParent(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTrashSource(filepath.Join(loop, "file")); err == nil {
		t.Error("a symlink loop in the directory part was accepted")
	}
	if _, err := resolveTrashSource(filepath.Join(root, "missing", "file")); !os.IsNotExist(err) {
		t.Errorf("missing parent: error = %v, want a not-exist error", err)
	}
}

func TestMoveToFreedesktopTrashThroughDirectorySymlinkUsesRealPath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "doomed.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := moveToFreedesktopTrash(context.Background(), filepath.Join(link, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(realDir, "doomed.txt")); !os.IsNotExist(err) {
		t.Fatalf("item still exists after trashing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(link, ".Trash-"+itoaUID())); !os.IsNotExist(err) {
		t.Fatalf("a trash directory appeared inside the symlinked directory: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "data", "Trash", "info", "doomed.txt.trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Path="+realDir+"/doomed.txt\n") {
		t.Errorf(".trashinfo does not carry the real path:\n%s", body)
	}
}

func itoaUID() string { return strconv.Itoa(trashGetuid()) }
