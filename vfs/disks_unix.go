//go:build !windows

package vfs

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/unxed/f4/vfs/hostfs"
)

func parseSysfsBlockSize(data []byte) (int64, bool) {
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || sectors <= 0 || sectors > int64(1<<63-1)/512 {
		return 0, false
	}
	return sectors * 512, true
}

func resolveDevicePath(name string) string {
	if !strings.HasPrefix(name, "/dev/") {
		return "/dev/" + name
	}
	return name
}

func getPlatformBlockDevices(ctx context.Context) []VFSItem {
	var items []VFSItem

	// Linux /sys/class/block enumeration (world-readable)
	entries, err := os.ReadDir("/sys/class/block")
	if err == nil {
		for _, e := range entries {
			if ctx.Err() != nil {
				break
			}
			name := e.Name()

			// Read sector size from sysfs
			sizeData, err := os.ReadFile("/sys/class/block/" + name + "/size")
			if err != nil {
				continue
			}
			size, ok := parseSysfsBlockSize(sizeData)
			if !ok {
				continue
			}

			displayName := name
			if dmName, err := os.ReadFile("/sys/class/block/" + name + "/dm/name"); err == nil {
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
		if len(items) > 0 {
			return items
		}
	}

	// Fallback for macOS / BSDs: scan /dev
	devEntries, err := os.ReadDir("/dev")
	if err == nil {
		for _, e := range devEntries {
			if ctx.Err() != nil {
				break
			}
			info, err := e.Info()
			// Block devices only: a character device here is a terminal or
			// serial port, not a disk — and opening one to probe its size
			// can block forever (macOS tty.* waits for carrier, and every
			// Mac ships /dev/tty.Bluetooth-Incoming-Port).
			if err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0 {
				size, sizeErr := getDeviceSize("/dev/"+e.Name(), nil)
				if sizeErr != nil {
					continue
				}
				items = append(items, VFSItem{
					Name:      e.Name(),
					Size:      size,
					SizeKnown: true,
					MTime:     info.ModTime(),
				})
			}
		}
	}
	return items
}

// getDeviceSize's f parameter is hostfs.File (not *os.File) purely so its
// signature stays identical to disks_windows.go's counterpart -- both are
// called from the platform-neutral disks_vfs.go, whose Open/PatchInPlace
// now hand it whatever hostfs.OpenFile/hostfs.Open returned (f4#1461, task
// 3). On this GOOS hostfs is a direct forward to os.*, so the value here is
// always a genuine *os.File in practice, same as before the type changed.
func getDeviceSize(devPath string, f hostfs.File) (int64, error) {
	if f != nil {
		if size, found, err := probeSeekSize(f); err != nil {
			return 0, err
		} else if found {
			return size, nil
		}
	}
	// Try sysfs lookup if devPath is /dev/xxx
	sysName := strings.TrimPrefix(devPath, "/dev/")
	sysName = strings.TrimPrefix(sysName, "mapper/")
	if sizeData, err := os.ReadFile("/sys/class/block/" + sysName + "/size"); err == nil {
		if size, ok := parseSysfsBlockSize(sizeData); ok {
			return size, nil
		}
	}
	// Direct open seek fallback
	if f == nil {
		// O_NONBLOCK: never wait for a device to become ready just to
		// measure it; a probe must not hang on carrier or DTR.
		if localF, err := os.OpenFile(devPath, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			defer func() {
				_ = localF.Close() // The probe handle is read-only.
			}()
			if pos, err := localF.Seek(0, io.SeekEnd); err == nil && pos > 0 {
				return pos, nil
			}
		}
	}
	return 0, nil
}
