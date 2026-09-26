//go:build darwin || freebsd || netbsd

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOSVFS_BirthTimeIsPopulated locks in f4#1404's Created-time support on
// macOS/FreeBSD/NetBSD: syscall.Stat_t's Birthtimespec is the real creation
// time on these three (unlike Ctimespec, which is "inode metadata changed"),
// so fillPlatformTimes must surface it as VFSItem.BTime with MetadataBTime
// set, not leave it to be inferred (or missed) the way a zero-value field
// would be.
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
		t.Fatal("MetadataBTime not set for a freshly created file on a platform with Birthtimespec")
	}
	if item.BTime.Before(before) || item.BTime.After(after) {
		t.Errorf("BTime = %v, want within [%v, %v]", item.BTime, before, after)
	}
}
