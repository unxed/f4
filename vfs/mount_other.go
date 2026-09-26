//go:build !linux

package vfs

import (
	"context"
	"errors"
)

// UnmountDevice is a stub outside Linux: the mount-manager first slice
// (f4#415) is Linux-only by design, matching drives_unix.go/mounts_linux.go
// in internal/sysinfo, which never populate DriveEntry.UnmountDevice on any
// other platform. This symbol still needs to exist everywhere, though --
// internal/panel/frame.go calls it unconditionally from code that builds
// for every OS -- so it is never actually reached elsewhere, but must
// compile.
func UnmountDevice(ctx context.Context, device, mountPoint string) error {
	return errors.New("unmount is not supported on this platform")
}
