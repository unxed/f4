package sysinfo

import (
	"sync"

	"github.com/unxed/f4/vfs"
)

// The drive registry is what the drive menu, the command palette and every
// plugin that adds a file system agree on. It needs nothing but a slice and a
// lock, which is why it can sit below everything that reads it.

type DriveEntry struct {
	Name    string
	Factory func() vfs.VFS

	// InfoPath, when set, is the real filesystem path the drive menu should
	// use to look up free space and filesystem type (sysinfo.FS), instead of
	// trying to derive one from Name the way it does for the built-in
	// "/ Root" and "~ Home" rows. Live mount-point entries (f4#415) set this
	// to their mount point.
	InfoPath string

	// UnmountDevice, when set, is the block device (e.g. "/dev/sdb1")
	// backing this entry's mount point, and marks the row as one the drive
	// menu's Del key can unmount (f4#415).
	UnmountDevice string
}

var (
	driveRegistryMu sync.RWMutex
	DriveRegistry   []DriveEntry
)

// RegisterDrive adds a drive, or replaces the factory of one already
// registered under that name. Replacing in place is deliberate: a plugin
// reloaded at runtime must not appear twice in the menu.
func RegisterDrive(name string, factory func() vfs.VFS) {
	driveRegistryMu.Lock()
	defer driveRegistryMu.Unlock()
	for i, d := range DriveRegistry {
		if d.Name == name {
			DriveRegistry[i].Factory = factory
			return
		}
	}
	DriveRegistry = append(DriveRegistry, DriveEntry{Name: name, Factory: factory})
}

func DriveRegistrySnapshot() []DriveEntry {
	driveRegistryMu.RLock()
	defer driveRegistryMu.RUnlock()
	return append([]DriveEntry(nil), DriveRegistry...)
}

// Drives returns the registry as a slice, ordered as it was registered.
func Drives() []DriveEntry { return DriveRegistrySnapshot() }

// SetDrives replaces the registry wholesale. It exists for tests: the mutex is
// this package's own, so a caller that swaps the slice from outside cannot take
// it, and a swap without it races every reader.
func SetDrives(entries []DriveEntry) {
	driveRegistryMu.Lock()
	defer driveRegistryMu.Unlock()
	DriveRegistry = append([]DriveEntry(nil), entries...)
}

// SnapshotDrives copies the registry and returns a function that puts it back.
func SnapshotDrives() (restore func()) {
	previous := DriveRegistrySnapshot()
	return func() { SetDrives(previous) }
}
