//go:build windows

package vfs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	winescape "github.com/unxed/libwinescape/go"
	"golang.org/x/sys/windows"
)

func fillPlatformTimes(item *VFSItem, info os.FileInfo) {
	if stat, ok := info.Sys().(*winescape.Stat_t); ok {
		item.KnownMetadata |= MetadataATime | MetadataCTime | MetadataUID | MetadataGID | MetadataPermissions | MetadataNlink
		item.UnixMode = stat.Mode & 07777
		item.Uid, item.Gid = int(stat.Uid), int(stat.Gid)
		item.Nlink = stat.Nlink
		item.ATime = time.Unix(stat.Atim.Sec, stat.Atim.Nsec)
		item.CTime = time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	}
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		item.KnownMetadata &^= MetadataPermissions
		item.KnownMetadata |= MetadataATime | MetadataCTime | MetadataWinAttrs
		item.ATime = time.Unix(0, stat.LastAccessTime.Nanoseconds())
		item.CTime = time.Unix(0, stat.CreationTime.Nanoseconds())
		item.WinAttrs = stat.FileAttributes
		// Win32FileAttributeData carries no link count. Most files have
		// exactly one; report that rather than leaving MetadataNlink unset
		// and the LN column blank for every ordinary file (f4#1400).
		item.KnownMetadata |= MetadataNlink
		item.Nlink = 1
	}
}

// resolveWindowsJunction attempts to resolve a Windows junction/symlink reparse point
// target when os.Readlink fails due to permission restrictions.
// Uses CreateFile with FILE_READ_ATTRIBUTES (minimal access) + FILE_FLAG_OPEN_REPARSE_POINT
// and DeviceIoControl with FSCTL_GET_REPARSE_POINT to read the target.
func resolveWindowsJunction(path string) (string, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}

	h, err := windows.CreateFile(ptr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE]byte
	var ret uint32
	err = windows.DeviceIoControl(
		h,
		windows.FSCTL_GET_REPARSE_POINT,
		nil, 0,
		&buf[0], uint32(len(buf)),
		&ret, nil)
	if err != nil {
		return "", err
	}

	tag := *(*uint32)(unsafe.Pointer(&buf[0]))
	switch tag {
	case windows.IO_REPARSE_TAG_MOUNT_POINT:
		substOff := *(*uint16)(unsafe.Pointer(&buf[8]))
		substLen := *(*uint16)(unsafe.Pointer(&buf[10]))
		if int(substOff)+int(substLen) > len(buf) {
			return "", os.ErrInvalid
		}
		raw := buf[16+substOff : 16+substOff+substLen]
		u16 := make([]uint16, len(raw)/2)
		for i := range u16 {
			u16[i] = *(*uint16)(unsafe.Pointer(&raw[i*2]))
		}
		target := string(utf16.Decode(u16))
		target = strings.TrimPrefix(target, `\??\`)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return target, nil

	case windows.IO_REPARSE_TAG_SYMLINK:
		substOff := *(*uint16)(unsafe.Pointer(&buf[8]))
		substLen := *(*uint16)(unsafe.Pointer(&buf[10]))
		if int(substOff)+int(substLen) > len(buf) {
			return "", os.ErrInvalid
		}
		raw := buf[16+substOff : 16+substOff+substLen]
		u16 := make([]uint16, len(raw)/2)
		for i := range u16 {
			u16[i] = *(*uint16)(unsafe.Pointer(&raw[i*2]))
		}
		target := string(utf16.Decode(u16))
		target = strings.TrimPrefix(target, `\??\`)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return target, nil
	}

	return "", os.ErrInvalid
}
