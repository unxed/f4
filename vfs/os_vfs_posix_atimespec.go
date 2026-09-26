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
		item.Uid = int(stat.Uid)
		item.Gid = int(stat.Gid)
		item.Nlink = uint64(stat.Nlink)
	}
}
