//go:build windows

package sysinfo

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	winescape "github.com/unxed/libwinescape/go"
	"golang.org/x/sys/windows"

	"github.com/unxed/f4/vfs/hostfs"
	"github.com/unxed/f4/vfs/hostmode"
)

// GetDiskFreeSpaceW isn't bound by golang.org/x/sys/windows (only its
// Ex variant is). We need the non-Ex version because it's the one
// that returns sectors-per-cluster / bytes-per-sector, from which the
// cluster (allocation-unit) size is derived. Uses the kernel32
// LazyDLL declared in mem_info_windows.go.
var procGetDiskFreeSpaceW = kernel32.NewProc("GetDiskFreeSpaceW")

// FsInfo returns filesystem info for the drive containing path.
// ok=false if the value can't be determined.
func FS(path string) (FSInfo, bool) {
	if path == "" {
		return FSInfo{}, false
	}
	// WINE.md §18.2, "Список дисков... FS(\"/\")": in posix personality
	// path is a real POSIX path ("/", "/tmp"), not a drive letter --
	// GetDiskFreeSpaceEx below was never going to resolve it correctly,
	// and silently answering for whatever Win32 makes of it (the
	// wineprefix's own current drive) would be worse than the honest
	// "no info" this returned before Part E existed. winescape.Statfs
	// asks the real host filesystem instead.
	if hostmode.Posix() {
		return fsPosix(path)
	}
	info := FSInfo{}

	// Drive root — e.g. "C:\\" — is what most Volume APIs expect.
	root := filepath.VolumeName(path)
	if root != "" && !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	if root == "" {
		root = path
	}
	info.Mount = root

	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return info, false
	}
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeAvail, &totalBytes, &totalFree); err != nil {
		return info, false
	}
	info.Total = totalBytes
	info.Free = freeAvail

	// Cluster (allocation-unit) size — only available via the non-Ex
	// GetDiskFreeSpace. Silent failure keeps ClusterSize at 0 so the
	// consumer can gracefully hide the Physical/Ratio/Cluster rows.
	var sectorsPerCluster, bytesPerSector, freeClusters, totalClusters uint32
	r, _, _ := procGetDiskFreeSpaceW.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&sectorsPerCluster)),
		uintptr(unsafe.Pointer(&bytesPerSector)),
		uintptr(unsafe.Pointer(&freeClusters)),
		uintptr(unsafe.Pointer(&totalClusters)),
	)
	if r != 0 {
		info.ClusterSize = uint64(sectorsPerCluster) * uint64(bytesPerSector)
	}

	// Volume label / serial / max filename length / flags / fs name.
	readVolumeInformation(rootPtr, &info)
	return info, true
}

// readVolumeInformation fills the label, serial, filename limit, flags and
// filesystem name through a handle to the volume's root directory, using
// GetVolumeInformationByHandleW rather than GetVolumeInformationW(root).
//
// Both report the same fields, but under Wine GetVolumeInformationW first
// opens the volume device for reading, and when that is refused -- as it is
// for the drive mapped to "/" -- it prints "wine: Read access denied for
// device ..., FS volume label and serial are not available." straight to the
// process's Unix stderr, past the Windows handles and past WINEDEBUG
// (dlls/kernelbase/volume.c). The panels ask for this on every frame, so the
// line landed in the terminal between frames of the console view, and a
// detached GUI copy whose stderr still pointed at a terminal nobody read
// filled that terminal's buffer until the frame thread blocked in write()
// and the window stopped answering (issue #474). The by-handle call only
// queries the open handle and prints nothing.
//
// Failure leaves the fields empty, as the old call did.
func readVolumeInformation(rootPtr *uint16, info *FSInfo) {
	h, err := windows.CreateFile(rootPtr, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var (
		volumeName         [windows.MAX_PATH + 1]uint16
		fsName             [windows.MAX_PATH + 1]uint16
		serialNumber       uint32
		maxComponentLength uint32
		fileSystemFlags    uint32
	)
	if err := windows.GetVolumeInformationByHandle(
		h,
		&volumeName[0], uint32(len(volumeName)),
		&serialNumber,
		&maxComponentLength,
		&fileSystemFlags,
		&fsName[0], uint32(len(fsName)),
	); err != nil {
		return
	}
	info.Label = windows.UTF16ToString(volumeName[:])
	info.Type = windows.UTF16ToString(fsName[:])
	info.MaxFilename = int(maxComponentLength)
	info.Serial = fmt.Sprintf("%04X-%04X", serialNumber>>16, serialNumber&0xFFFF)
	info.Flags = decodeVolumeFlags(fileSystemFlags)
}

func decodeVolumeFlags(f uint32) string {
	// Names come from WinBase.h FILE_* constants. Kept to the ones
	// far2l's info panel typically surfaces; the raw hex tail keeps
	// bits we didn't decode visible for the curious.
	type flag struct {
		bit uint32
		s   string
	}
	all := []flag{
		{0x00000001, "case_sensitive"},
		{0x00000002, "case_preserved"},
		{0x00000004, "unicode"},
		{0x00000008, "acls"},
		{0x00000010, "compressed_files"},
		{0x00000040, "quotas"},
		{0x00000080, "sparse"},
		{0x00000100, "reparse_points"},
		{0x00008000, "compressed_vol"},
		{0x00010000, "objects_ids"},
		{0x00020000, "encryption"},
		{0x00040000, "streams"},
		{0x02000000, "readonly"},
	}
	var parts []string
	for _, e := range all {
		if f&e.bit != 0 {
			parts = append(parts, e.s)
			f &^= e.bit
		}
	}
	if f != 0 {
		parts = append(parts, fmt.Sprintf("0x%X", f))
	}
	return strings.Join(parts, ",")
}

// fsPosix is FS's posix-personality branch: the same fields fs_linux.go's
// FS fills, reached through libwinescape's Statfs instead of the "linux"
// build's direct syscall package, since this binary is windows/GOOS and
// the standard syscall package speaks Win32 here regardless of
// personality.
func fsPosix(path string) (FSInfo, bool) {
	var st winescape.Statfs_t
	if err := winescape.Statfs(path, &st); err != nil {
		return FSInfo{}, false
	}
	bs := uint64(st.Bsize)
	info := FSInfo{
		Total:       uint64(st.Blocks) * bs,
		Free:        uint64(st.Bavail) * bs,
		MaxFilename: int(st.Namelen),
		ClusterSize: bs,
	}
	enrichFromHostProcMounts(path, &info)
	return info, true
}

// enrichFromHostProcMounts mirrors fs_linux.go's enrichFromProcMounts,
// reading the host's own /proc/mounts (the real Linux host under Wine, not
// anything inside the wineprefix) for the mount point / fs type / flags
// GO's statfs-equivalent struct does not carry on Linux.
func enrichFromHostProcMounts(path string, info *FSInfo) {
	data, err := hostfs.ReadFile("/proc/mounts")
	if err != nil {
		return
	}
	bestLen := -1
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		mp := fields[1]
		if !mountCoversPosix(mp, path) {
			continue
		}
		if len(mp) > bestLen {
			bestLen = len(mp)
			info.Mount = mp
			info.Type = fields[2]
			info.Flags = fields[3]
		}
	}
}

// mountCoversPosix reports whether mount point mp is an ancestor of path.
func mountCoversPosix(mp, path string) bool {
	if mp == path || mp == "/" {
		return true
	}
	return strings.HasPrefix(path, mp+"/")
}
