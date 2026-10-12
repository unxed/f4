package vfs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// f4#1828: changing a link whose target is gone must not fail on the mode and
// times, which can only be set on what a link points to.
func TestSetAttributesOnADanglingLinkDoesNotFailOnModeAndTimes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic link privileges on Windows")
	}
	root := t.TempDir()
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	v := NewOSVFS(root)
	item, err := v.Lstat(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if !item.IsSymlink {
		t.Fatal("test setup: not reported as a link")
	}
	item.UnixMode = 0o644
	item.MTime = time.Now().Add(-time.Hour)
	item.ATime = item.MTime
	item.Uid, item.Gid = -1, -1
	if err := v.SetAttributes(context.Background(), link, item); err != nil {
		t.Fatalf("SetAttributes on a dangling link: %v", err)
	}
}
