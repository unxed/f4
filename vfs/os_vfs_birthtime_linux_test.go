//go:build linux || openbsd || dragonfly || solaris || illumos

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOSVFS_BirthTimeIsGracefullyAbsent documents f4#1404's deliberate scope
// line: classic stat(2) on these platforms has no portable birth time at
// all (Linux would need statx(2)+STATX_BTIME, paid per directory entry, for
// a value not every filesystem even populates), so fillPlatformTimes must
// leave VFSItem.BTime unknown here — not populate it with Ctim (inode
// "metadata changed", not creation) mislabeled as a creation time, and not
// leave a zero BTime that HasMetadata would need to distinguish from "really
// created at the Unix epoch".
func TestOSVFS_BirthTimeIsGracefullyAbsent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "created.txt")
	if err := os.WriteFile(path, []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewOSVFS(tmp)
	item, err := v.Stat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if item.HasMetadata(MetadataBTime) {
		t.Errorf("MetadataBTime set on a platform with no portable birth time; BTime = %v", item.BTime)
	}
	if !item.BTime.IsZero() {
		t.Errorf("BTime = %v, want the zero value when not known", item.BTime)
	}
	// Ctim/Ctimespec (inode metadata change time) must still be reported as
	// CTime, unrelated to and never mistaken for BTime.
	if !item.HasMetadata(MetadataCTime) {
		t.Error("MetadataCTime should still be set: ctime is always available on these platforms")
	}
}
