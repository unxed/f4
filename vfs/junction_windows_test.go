//go:build windows

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// f4#1828: Junction makes a real junction, not a symbolic link.
func TestOSVFSJunctionCreatesAMountPoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junc")
	v := NewOSVFS(root)
	if err := v.Junction(context.Background(), target, link); err != nil {
		t.Fatalf("Junction: %v", err)
	}
	item, err := v.Lstat(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if item.ReparseTag != ReparseTagMountPoint || LinkKindOf(&item) != LinkJunction {
		t.Fatalf("the link has reparse tag %#x, kind %v: want a junction", item.ReparseTag, LinkKindOf(&item))
	}
	if _, err := os.Stat(filepath.Join(link, "f.txt")); err != nil {
		t.Errorf("the junction does not lead to the folder: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil || filepath.Clean(got) != filepath.Clean(target) {
		t.Errorf("Readlink = %q, %v, want %q", got, err, target)
	}
	if err := v.Remove(context.Background(), link); err != nil {
		t.Errorf("a junction is removed as a link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "f.txt")); err != nil {
		t.Errorf("removing the junction touched the folder it pointed to: %v", err)
	}
}

func TestOSVFSJunctionRefusesAFileAndLeavesNothingBehind(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junc")
	if err := NewOSVFS(root).Junction(context.Background(), file, link); err == nil {
		t.Fatal("a junction to a file was made")
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("a failed junction left a folder behind")
	}
}
