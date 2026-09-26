//go:build linux

package vfs

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/unxed/vtui"
)

// runExternalCommand runs name with args and returns its combined output. A
// package-level var, like mountOnce/unmountOwn in internal/fusefs/cli.go,
// so a test can substitute it and assert on the command f4 would have run
// (f4#415) without ever executing anything.
var runExternalCommand = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- args are our own literals plus a device path from /proc/mounts, not user text.
	return cmd.CombinedOutput()
}

// lookupExecutable resolves name on PATH. A package-level var for the same
// testability reason as runExternalCommand: a test can pretend udisksctl is
// or is not installed without touching the real PATH.
var lookupExecutable = exec.LookPath

// unmountSyscall is the raw unmount(2) UnmountDevice falls back to. A
// package-level var, again for testability: a test can make this branch
// succeed or fail with a chosen errno without ever calling unmount(2) on a
// real (or fake) mount point.
var unmountSyscall = func(path string, flags int) error {
	return syscall.Unmount(path, flags)
}

// UnmountDevice detaches the mount at mountPoint, backed by block device
// device (e.g. "/dev/sdb1"), such as a USB flash drive shown in the disk
// menu's live mount-point rows (f4#415, drives_unix.go).
//
// It tries udisksctl first: on any desktop running udisks2 -- which is the
// mechanism behind GNOME's, KDE's and most other desktops' automount on
// plug-in -- unmounting removable media the user's own session mounted
// needs no root at all, because polkit authorizes it per user. That is
// exactly the common case this action targets ("unmount a flash drive").
// Only when udisksctl is unavailable (no udisksctl on PATH, e.g. a minimal
// or server install) or it refuses (the mount was not udisks-managed --
// an admin's manual `mount`, for instance) does this fall back to a plain
// unmount(2), escalated through the existing sudo dispatcher exactly the
// way OSVFS.Open already retries a permission-denied Open (os_vfs.go): try
// unprivileged first, and ask for root only if that failed on a permission
// error and the caller has not ruled elevation out (vfs.WithoutElevation /
// vfs.ElevationAllowed).
func UnmountDevice(ctx context.Context, device, mountPoint string) error {
	if mountPoint == "" {
		return errors.New("unmount: no mount point given")
	}

	var udisksErr error
	if path, lookErr := lookupExecutable("udisksctl"); lookErr == nil && device != "" {
		out, err := runExternalCommand(path, "unmount", "-b", device)
		if err == nil {
			return nil
		}
		udisksErr = fmt.Errorf("udisksctl unmount -b %s: %w (%s)", device, err, strings.TrimSpace(string(out)))
		vtui.DebugLog("MOUNT: %v; falling back to unmount(2)", udisksErr)
	}

	err := unmountSyscall(mountPoint, 0)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		if udisksErr != nil {
			return fmt.Errorf("%v; unmount(2): %w", udisksErr, err)
		}
		return err
	}
	if !ElevationAllowed(ctx) {
		return err
	}
	client := GetSudoClient()
	if client == nil || !client.IsAvailable() {
		return err
	}
	return client.Unmount(mountPoint)
}
