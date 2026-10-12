//go:build linux

package vfs

import (
	"time"

	"golang.org/x/sys/unix"
)

// ReadBirthTime asks statx(2) for the creation time of path itself (a symlink
// is not followed). Classic stat has none on Linux, and a filesystem may not
// keep one, so the answer is "unknown" more often than elsewhere. It is read on
// demand, for the few objects an attributes dialog shows, and not for every
// directory entry of a listing (f4#1404, f4#1817).
func ReadBirthTime(path string) (time.Time, bool) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stx); err != nil {
		return time.Time{}, false
	}
	if stx.Mask&unix.STATX_BTIME == 0 || (stx.Btime.Sec == 0 && stx.Btime.Nsec == 0) {
		return time.Time{}, false
	}
	return time.Unix(stx.Btime.Sec, int64(stx.Btime.Nsec)), true
}
