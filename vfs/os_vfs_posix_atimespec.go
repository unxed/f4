//go:build darwin || freebsd || netbsd

package vfs

import (
	"os"
	"syscall"
	"time"
)

func fillPlatformTimes(item *VFSItem, info os.FileInfo) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		item.KnownMetadata |= MetadataATime | MetadataCTime | MetadataUID | MetadataGID | MetadataNlink
		// macOS and BSDs use Atimespec/Ctimespec instead of Atim/Ctim
		item.UnixMode = uint32(stat.Mode) & 07777
		item.ATime = time.Unix(int64(stat.Atimespec.Sec), int64(stat.Atimespec.Nsec))
		item.CTime = time.Unix(int64(stat.Ctimespec.Sec), int64(stat.Ctimespec.Nsec))
		// Birthtimespec is the real creation time on these three (f4#1404):
		// unlike Ctimespec it is never "inode metadata changed", it is the
		// object's actual birth time. A filesystem that doesn't track birth
		// time (e.g. some virtual/network mounts) reports it as the zero
		// Unix epoch; treat that as "unknown" rather than a real date.
		if stat.Birthtimespec.Sec != 0 || stat.Birthtimespec.Nsec != 0 {
			item.KnownMetadata |= MetadataBTime
			item.BTime = time.Unix(int64(stat.Birthtimespec.Sec), int64(stat.Birthtimespec.Nsec))
		}
		item.Uid = int(stat.Uid)
		item.Gid = int(stat.Gid)
		item.Nlink = uint64(stat.Nlink)
	}
}
