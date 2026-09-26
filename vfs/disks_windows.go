//go:build windows

package vfs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/unxed/f4/vfs/hostfs"
	"github.com/unxed/f4/vfs/hostmode"
)

func resolveDevicePath(name string) string {
	// WINE.md §18.2, "Список дисков, Alt+F1": posix personality has no
	// \\.\PhysicalDriveN namespace -- a block device is a /dev entry, the
	// same shape disks_unix.go resolves on the Linux build.
	if hostmode.Posix() {
		if !strings.HasPrefix(name, "/dev/") {
			return "/dev/" + name
		}
		return name
	}
	if !strings.HasPrefix(name, "\\\\.\\") {
		return "\\\\.\\" + name
	}
	return name
}

// parseSysfsBlockSize parses a /sys/class/block/*/size attribute: a count
// of 512-byte sectors, decimal, newline-terminated. Mirrors
// disks_unix.go's function of the same name and job -- duplicated rather
// than shared because the two files never compile together (disjoint
// GOOS build tags), the same reasoning fs_linux.go/fs_darwin.go already
// follow for their own platform-specific helpers.
func parseSysfsBlockSize(data []byte) (int64, bool) {
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || sectors <= 0 || sectors > int64(1<<63-1)/512 {
		return 0, false
	}
	return sectors * 512, true
}

func getPlatformBlockDevices(ctx context.Context) []VFSItem {
	// WINE.md §18.2, "Список дисков... Провайдера «Physical Disks» в этом
	// режиме нет": listing was simply absent in posix mode (an empty
	// result, not a wrong one -- DeviceIoControl below only ever means
	// something against a real \\.\PhysicalDriveN handle, which posix
	// personality has none of). hostfs.ReadDir/ReadFile read the host's
	// own /sys/class/block through libwinescape, the same sysfs
	// enumeration disks_unix.go uses on the Linux build.
	if hostmode.Posix() {
		return getPosixBlockDevices(ctx)
	}
	var items []VFSItem
	for i := 0; i < 64; i++ {
		if ctx.Err() != nil {
			break
		}
		name := fmt.Sprintf("PhysicalDrive%d", i)
		diskPath := "\\\\.\\" + name
		ptr, err := windows.UTF16PtrFromString(diskPath)
		if err != nil {
			continue
		}
		h, err := windows.CreateFile(ptr, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			var length int64
			var returned uint32
			errIoctl := windows.DeviceIoControl(h, 0x7405C, nil, 0, (*byte)(unsafe.Pointer(&length)), 8, &returned, nil)
			windows.CloseHandle(h)
			if errIoctl != nil || length <= 0 {
				length = 0
			}
			items = append(items, VFSItem{KnownMetadata: MetadataExplicit,
				Name:      name,
				Size:      length,
				SizeKnown: true,
				MTime:     time.Now(),
			})
		}
	}
	return items
}

func getPosixBlockDevices(ctx context.Context) []VFSItem {
	var items []VFSItem
	entries, err := hostfs.ReadDir("/sys/class/block")
	if err != nil {
		return items
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		name := e.Name()

		sizeData, err := hostfs.ReadFile("/sys/class/block/" + name + "/size")
		if err != nil {
			continue
		}
		size, ok := parseSysfsBlockSize(sizeData)
		if !ok {
			continue
		}

		displayName := name
		if dmName, err := hostfs.ReadFile("/sys/class/block/" + name + "/dm/name"); err == nil {
			trimmed := strings.TrimSpace(string(dmName))
			if trimmed != "" {
				displayName = "mapper/" + trimmed
			}
		}

		items = append(items, VFSItem{KnownMetadata: MetadataExplicit,
			Name:      displayName,
			Size:      size,
			SizeKnown: true,
			MTime:     time.Now(),
		})
	}
	return items
}

// getDeviceSize answers a listed device's size. In posix personality this
// only ever reads /sys/class/block/*/size; Open/PatchInPlace in disks_vfs.go
// now open the device through hostfs too (f4#1461, task 3), so f arrives
// here as whatever hostfs.OpenFile/hostfs.Open handed back -- a libwinescape
// wineFile under Wine posix mode, a plain *os.File everywhere else -- and
// the io.Seeker probe below (probeSeekSize) works the same either way.
func getDeviceSize(devPath string, f hostfs.File) (int64, error) {
	if f != nil {
		if size, found, err := probeSeekSize(f); err != nil {
			return 0, err
		} else if found {
			return size, nil
		}
	}
	if hostmode.Posix() {
		sysName := strings.TrimPrefix(devPath, "/dev/")
		sysName = strings.TrimPrefix(sysName, "mapper/")
		if sizeData, err := hostfs.ReadFile("/sys/class/block/" + sysName + "/size"); err == nil {
			if size, ok := parseSysfsBlockSize(sizeData); ok {
				return size, nil
			}
		}
		return 0, nil
	}
	ptr, err := windows.UTF16PtrFromString(devPath)
	if err != nil {
		return 0, nil
	}
	h, err := windows.CreateFile(ptr, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, nil
	}
	defer windows.CloseHandle(h)
	var length int64
	var returned uint32
	if err := windows.DeviceIoControl(h, 0x7405C, nil, 0, (*byte)(unsafe.Pointer(&length)), 8, &returned, nil); err == nil {
		return length, nil
	}
	return 0, nil
}
