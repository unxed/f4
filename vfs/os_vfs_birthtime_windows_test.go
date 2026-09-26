//go:build windows

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOSVFS_BirthTimeIsPopulated locks in f4#1404's Created-time support on
// native Windows: Win32FileAttributeData's CreationTime is the object's real
// creation time, and fillPlatformTimes must surface it as VFSItem.BTime with
// MetadataBTime set, distinct from the Wine-posix-mode winescape.Stat_t
// branch (see os_vfs_windows.go), whose Ctim has no creation-time meaning at
// all and must never set MetadataBTime.
func TestOSVFS_BirthTimeIsPopulated(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "created.txt")
	before := time.Now().Add(-time.Minute)
	if err := os.WriteFile(path, []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(time.Minute)

	v := NewOSVFS(tmp)
	item, err := v.Stat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if !item.HasMetadata(MetadataBTime) {
		t.Fatal("MetadataBTime not set for a freshly created file on native Windows")
	}
	if item.BTime.Before(before) || item.BTime.After(after) {
		t.Errorf("BTime = %v, want within [%v, %v]", item.BTime, before, after)
	}
}
