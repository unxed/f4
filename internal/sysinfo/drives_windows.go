//go:build windows

package sysinfo

import (
	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
	"golang.org/x/sys/windows"
)

func GetPlatformDrives() []DriveEntry {
	if hostmode.Posix() {
		// No drive letters in posix mode -- the whole point of WINE.md
		// Part E is that under Wine this looks like the Linux build, not
		// like Windows-with-extra-steps. Mirrors drives_unix.go exactly.
		//
		// hostmode.HomeDir, not os.Getenv("HOME"): Wine's Win32 environment
		// never carries HOME at all (dlls/ntdll/unix/env.c strips it as one
		// of four Unix-only variables), so the plain os.Getenv read always
		// came back empty here and this entry never appeared.
		home, _ := hostmode.HomeDir()
		drives := []DriveEntry{
			{Name: "/ Root", Factory: func() vfs.VFS { return vfs.NewOSVFS("/") }},
		}
		if home != "" {
			drives = append(drives, DriveEntry{Name: "~ Home", Factory: func() vfs.VFS { return vfs.NewOSVFS(home) }})
		}
		// WINE.md §18.2, "Список дисков... Провайдера «Physical Disks» в
		// этом режиме нет": drives_unix.go always lists this entry; posix
		// personality can list it too now that getPlatformBlockDevices has
		// a /sys/class/block branch (vfs/disks_windows.go). No "Windows
		// Registry" entry follows, unlike the non-posix branch below --
		// posix personality has no registry to browse.
		drives = append(drives, DriveEntry{
			Name:    "Physical Disks (/dev)",
			Factory: func() vfs.VFS { return vfs.NewDisksVFS() },
		})
		return drives
	}
	var drives []DriveEntry
	bitmask, err := windows.GetLogicalDrives()
	if err != nil {
		return drives
	}
	for i := 0; i < 26; i++ {
		if bitmask&(1<<uint(i)) != 0 {
			letter := string(rune('A' + i))
			path := letter + ":\\"
			drives = append(drives, DriveEntry{
				Name:    letter + ": Local",
				Factory: func() vfs.VFS { return vfs.NewOSVFS(path) },
			})
		}
	}
	drives = append(drives, DriveEntry{
		Name:    "Physical Disks",
		Factory: func() vfs.VFS { return vfs.NewDisksVFS() },
	})
	drives = append(drives, DriveEntry{
		Name:    "Windows Registry",
		Factory: func() vfs.VFS { return vfs.NewRegistryVFS() },
	})
	return drives
}
