//go:build windows

package vfs

import (
	"context"
	"os"
	"syscall"
	"unsafe"

	winescape "github.com/unxed/libwinescape/go"
	"golang.org/x/sys/windows"

	"github.com/unxed/f4/vfs/hostmode"
)

// fileStandardInfo mirrors the Win32 FILE_STANDARD_INFO struct.
// x/sys/windows exposes the GetFileInformationByHandleEx class constant
// (FileStandardInfo) but not the struct, so we declare the layout we
// need. AllocationSize is the on-disk footprint — cluster-granular and
// compression/sparse aware — the Windows analogue of the Unix
// stat.Blocks*512 the other platforms report.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  uint8
	Directory      uint8
	_              [2]byte // pad to the struct's natural 8-byte alignment
}

// fillPhysicalSizeCheap is a no-op on native Windows — there's no way to
// get on-disk allocation size from FileInfo alone; GetCompressedFileSize
// is a separate syscall we don't want to pay in the ReadDir path (an
// extra kernel round-trip per file, and on SMB an extra network
// round-trip too). Consumers that actually need PhysicalSize (the
// QuickView scan) go through fillPhysicalSize / Stat instead.
//
// In posix mode, the answer is already free exactly like on real Unix:
// hostfs's wineFileInfo.Sys() returns *winescape.Stat_t, which already
// carries Blocks/Dev/Ino from the Lstat the ReadDir loop had to do
// anyway (see vfs/hostfs/hostfs_winescape.go). This is the fix for the
// gotcha WINE.md §14.2 flagged: the old code here only ever tried
// *syscall.Stat_t (the standard-library Unix type), which posix mode's
// *winescape.Stat_t is never assignable to -- the assertion silently
// failed and PhysicalSize stayed zero for every file under Wine's posix
// mode. Checking hostmode.Posix() explicitly makes the two cases
// unambiguous instead of relying on a type assertion falling through by
// accident.
func fillPhysicalSizeCheap(item *VFSItem, info os.FileInfo) {
	if info == nil {
		return
	}
	if hostmode.Posix() {
		if stat, ok := info.Sys().(*winescape.Stat_t); ok {
			item.PhysicalSize = stat.Blocks * 512
			item.KnownMetadata |= MetadataPhysicalSize
			item.Device = stat.Dev
			item.Inode = stat.Ino
		}
	}
}

// fillPhysicalSize asks NTFS for the on-disk allocation of path via
// GetFileInformationByHandleEx(FileStandardInfo).AllocationSize. This
// matches far2l/Far and the Unix stat.Blocks*512 path: NTFS-compressed
// files report their compressed allocation, sparse regions are
// excluded, MFT-resident tiny files report 0, and plain files report
// their cluster-aligned size. (GetCompressedFileSize, used before,
// returns the packed data size WITHOUT cluster rounding, so it
// under-reported the real footprint by the per-file cluster slack —
// ~24 MB on a 116 GB tree.) Non-NTFS or unopenable paths fall back to
// info.Size().
//
// The same FILE_STANDARD_INFO query also carries NumberOfLinks, so this
// fills VFSItem.Nlink too (f4#1400's "LN" panel column) at no extra
// syscall cost — fillPlatformTimes can't report it honestly on native
// Windows because Win32FileAttributeData (what the cheap ReadDir path
// uses) has no link count at all.
func fillPhysicalSize(item *VFSItem, info os.FileInfo, path string) {
	if path == "" || info == nil {
		return
	}
	// Directories occupy $INDEX clusters we don't attribute to the
	// tree's on-disk size — Far's "Реальный размер" counts file
	// allocation only (dirs = 0), and this keeps Physical symmetric
	// with the logical total, which also drops directory sizes on
	// Windows. Leaving PhysicalSize at 0 for dirs avoids charging the
	// scanned root's own index cluster to the result.
	if info.IsDir() {
		return
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		item.PhysicalSize = info.Size()
		return
	}
	h, err := windows.CreateFile(
		ptr,
		0, // querying metadata needs no access rights
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		item.PhysicalSize = info.Size()
		return
	}
	defer windows.CloseHandle(h)

	var fsi fileStandardInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileStandardInfo,
		(*byte)(unsafe.Pointer(&fsi)), uint32(unsafe.Sizeof(fsi))); err != nil {
		item.PhysicalSize = info.Size()
		return
	}
	item.PhysicalSize = fsi.AllocationSize
	item.KnownMetadata |= MetadataPhysicalSize
	item.Nlink = uint64(fsi.NumberOfLinks)
	item.KnownMetadata |= MetadataNlink
}

// SupportsPhysicalSize is true on Windows — see the Unix version
// for the rationale of keeping the answer per-platform.
func (v *OSVFS) SupportsPhysicalSize() bool { return true }

// FileIdentity resolves a path's NTFS file identity via
// GetFileInformationByHandle: the volume serial number plus the 64-bit
// file index uniquely identify a file record, so every hard link to
// one file reports the same (device, inode). This is what lets the
// scanner's DedupInodes pass count hard-linked files once on Windows,
// matching the Unix Stat_t path — Go's ReadDir (FindNextFile) can't
// supply a file index, so we open the entry here. FILE_FLAG_BACKUP_
// SEMANTICS lets us open directories too; FILE_FLAG_OPEN_REPARSE_POINT
// makes us identify a reparse point itself rather than its target. Any
// failure (vanished path, access denied, or a volume like FAT/some SMB
// shares that returns a zero index) yields ok=false and the scanner
// simply skips dedup for that entry.
func (v *OSVFS) FileIdentity(ctx context.Context, path string) (device, inode uint64, ok bool) {
	if ctx.Err() != nil {
		return 0, 0, false
	}
	ptr, err := windows.UTF16PtrFromString(prepareOSPath(path))
	if err != nil {
		return 0, 0, false
	}
	h, err := windows.CreateFile(
		ptr,
		0, // querying metadata needs no access rights
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, 0, false
	}
	defer windows.CloseHandle(h)

	var bhfi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bhfi); err != nil {
		return 0, 0, false
	}
	inode = (uint64(bhfi.FileIndexHigh) << 32) | uint64(bhfi.FileIndexLow)
	// A zero index means the volume doesn't expose persistent file IDs
	// (FAT, some network shares). Treat it as "no identity" so we never
	// merge unrelated entries under (serial, 0).
	if inode == 0 {
		return 0, 0, false
	}
	return uint64(bhfi.VolumeSerialNumber), inode, true
}

// isReparsePoint reports whether the entry described by info is any
// kind of NTFS reparse point — symlinks, junctions (mount points),
// OneDrive/Dropbox placeholders, etc. Go's Mode()&ModeSymlink covers
// the plain symlink case, but its handling of junctions has flipped
// between releases (ModeSymlink vs ModeIrregular); relying on it
// alone was letting the scanner walk INTO junctions like
// C:\Users\<user>\AppData\Local\Application Data which points back
// at its own parent — cue millions of ghost files and hundreds of
// gigabytes of "physical" size that the disk doesn't have.
// FILE_ATTRIBUTE_REPARSE_POINT is the authoritative NTFS bit.
func isReparsePoint(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	// No such concept in posix mode -- only real symlinks exist there,
	// already covered by Mode()&ModeSymlink at the caller. Matches the
	// Unix stub below verbatim.
	if hostmode.Posix() {
		return false
	}
	if a, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return a.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}
