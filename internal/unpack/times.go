package unpack

import (
	"os"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// applyArchiveTimes gives an extracted file the times a release is meant to
// carry (f4#1817): the modification time of the archive member, and on Windows
// the same as the creation time, while the access time is the moment of
// extraction. Without it a file is as new as the update that wrote it, and the
// executable shows the day it was installed instead of the day it was built.
//
// It is best effort: a file that is extracted but keeps the time of the write
// is still a working update, so a failure is only logged. A zero mtime (the
// archive has none) leaves the file alone.
func applyArchiveTimes(path string, mtime time.Time) {
	if mtime.IsZero() {
		return
	}
	now := time.Now()
	err := os.Chtimes(path, now, mtime)
	if err != nil && os.IsPermission(err) && vfs.GetSudoClient().IsAvailable() {
		err = vfs.GetSudoClient().SetAttributes(path, vfs.VFSItem{MTime: mtime, ATime: now, Uid: -1, Gid: -1})
	}
	if err != nil {
		vtui.DebugLog("UPDATER: cannot set the times of %q: %v", path, err)
		return
	}
	if err := setCreationTime(path, mtime); err != nil {
		vtui.DebugLog("UPDATER: cannot set the creation time of %q: %v", path, err)
	}
}
