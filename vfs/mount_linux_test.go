//go:build linux

package vfs

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
)

// stubExternalCommand replaces runExternalCommand for the duration of the
// test and records every invocation.
func stubExternalCommand(t *testing.T, fn func(name string, args ...string) ([]byte, error)) *[][]string {
	t.Helper()
	old := runExternalCommand
	t.Cleanup(func() { runExternalCommand = old })
	var calls [][]string
	runExternalCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return fn(name, args...)
	}
	return &calls
}

func stubLookupExecutable(t *testing.T, path string, err error) {
	t.Helper()
	old := lookupExecutable
	t.Cleanup(func() { lookupExecutable = old })
	lookupExecutable = func(name string) (string, error) { return path, err }
}

func stubUnmountSyscall(t *testing.T, err error) *int {
	t.Helper()
	old := unmountSyscall
	t.Cleanup(func() { unmountSyscall = old })
	calls := 0
	unmountSyscall = func(path string, flags int) error {
		calls++
		return err
	}
	return &calls
}

// TestUnmountDeviceRequiresMountPoint makes sure UnmountDevice refuses to
// unmount "nothing in particular" instead of forwarding an empty path to
// udisksctl or unmount(2).
func TestUnmountDeviceRequiresMountPoint(t *testing.T) {
	calls := stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "/usr/bin/udisksctl", nil)
	if err := UnmountDevice(context.Background(), "/dev/sdb1", ""); err == nil {
		t.Fatal("UnmountDevice(\"\") = nil error, want one")
	}
	if len(*calls) != 0 {
		t.Fatalf("UnmountDevice ran %v external commands for an empty mount point", *calls)
	}
}

// TestUnmountDevicePrefersUdisksctl covers the common desktop case the
// disk-menu action targets: udisksctl available and willing, which needs
// no root and must run before the raw unmount(2) fallback is even
// considered.
func TestUnmountDevicePrefersUdisksctl(t *testing.T) {
	calls := stubExternalCommand(t, func(string, ...string) ([]byte, error) { return []byte("Unmounted /dev/sdb1.\n"), nil })
	stubLookupExecutable(t, "/usr/bin/udisksctl", nil)
	unmountCalls := stubUnmountSyscall(t, nil)

	if err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK"); err != nil {
		t.Fatalf("UnmountDevice() = %v, want nil", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("external commands run = %v, want exactly one", *calls)
	}
	got := (*calls)[0]
	want := []string{"/usr/bin/udisksctl", "unmount", "-b", "/dev/sdb1"}
	if len(got) != len(want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command = %v, want %v", got, want)
		}
	}
	if *unmountCalls != 0 {
		t.Fatalf("unmount(2) was called %d times, want 0: udisksctl already succeeded", *unmountCalls)
	}
}

// TestUnmountDeviceFallsBackWhenUdisksctlMissing covers a minimal/server
// install with no udisksctl on PATH: UnmountDevice must still try the raw
// unmount(2) instead of giving up.
func TestUnmountDeviceFallsBackWhenUdisksctlMissing(t *testing.T) {
	calls := stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "", errors.New("executable file not found in $PATH"))
	unmountCalls := stubUnmountSyscall(t, nil)

	if err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK"); err != nil {
		t.Fatalf("UnmountDevice() = %v, want nil", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("external commands run = %v, want none: udisksctl is not on PATH", *calls)
	}
	if *unmountCalls != 1 {
		t.Fatalf("unmount(2) was called %d times, want 1", *unmountCalls)
	}
}

// TestUnmountDeviceFallsBackWhenUdisksctlRefuses covers a mount point
// udisksctl does not manage (e.g. an admin's manual `mount`): udisksctl is
// on PATH and runs, but its failure must not be the final answer.
func TestUnmountDeviceFallsBackWhenUdisksctlRefuses(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) {
		return []byte("Error unmounting /dev/sdb1: not authorized\n"), errors.New("exit status 1")
	})
	stubLookupExecutable(t, "/usr/bin/udisksctl", nil)
	unmountCalls := stubUnmountSyscall(t, nil)

	if err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK"); err != nil {
		t.Fatalf("UnmountDevice() = %v, want nil", err)
	}
	if *unmountCalls != 1 {
		t.Fatalf("unmount(2) was called %d times, want 1", *unmountCalls)
	}
}

// TestUnmountDeviceReportsBothFailuresWhenNeitherIsAPermissionError covers a
// genuinely busy mount: udisksctl refuses, the raw unmount(2) refuses too
// with something other than EPERM/EACCES (EBUSY), so retrying through sudo
// would not help and UnmountDevice must say so instead of silently trying
// anyway.
func TestUnmountDeviceReportsBothFailuresWhenNeitherIsAPermissionError(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) {
		return []byte("target is busy"), errors.New("exit status 1")
	})
	stubLookupExecutable(t, "/usr/bin/udisksctl", nil)
	stubUnmountSyscall(t, syscall.EBUSY)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = answeringDispatcher(t, "") // must never be reached

	err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK")
	if !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("UnmountDevice() = %v, want it to wrap syscall.EBUSY", err)
	}
	if !strings.Contains(err.Error(), "udisksctl") {
		t.Fatalf("UnmountDevice() = %v, want it to also mention the udisksctl failure", err)
	}
}

// TestUnmountDeviceWithoutElevationDoesNotEscalate exercises the
// vfs.WithoutElevation idiom shared with OSVFS.Open (os_vfs.go): a refused
// unmount(2) must be returned as is, never retried through sudo, when the
// caller ruled elevation out.
func TestUnmountDeviceWithoutElevationDoesNotEscalate(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "", errors.New("not found"))
	stubUnmountSyscall(t, syscall.EACCES)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = answeringDispatcher(t, "") // would succeed if ever asked

	err := UnmountDevice(WithoutElevation(context.Background()), "/dev/sdb1", "/media/user/USB_STICK")
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("UnmountDevice() = %v, want syscall.EACCES", err)
	}
}

// TestUnmountDeviceEscalatesThroughSudoDispatcher exercises the other half
// of the same idiom: a permission-denied unmount(2), with elevation
// allowed and the dispatcher available, is retried through it -- and its
// success is what UnmountDevice reports.
func TestUnmountDeviceEscalatesThroughSudoDispatcher(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "", errors.New("not found"))
	stubUnmountSyscall(t, syscall.EPERM)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = answeringDispatcher(t, "")

	if err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK"); err != nil {
		t.Fatalf("UnmountDevice() = %v, want nil (dispatcher answered success)", err)
	}
}

// TestUnmountDeviceReportsDispatcherFailure makes sure a rejection from the
// elevated dispatcher surfaces to the caller instead of being swallowed.
func TestUnmountDeviceReportsDispatcherFailure(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "", errors.New("not found"))
	stubUnmountSyscall(t, syscall.EPERM)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = answeringDispatcher(t, "target is busy")

	err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK")
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("UnmountDevice() = %v, want an error mentioning the dispatcher's refusal", err)
	}
}

// TestUnmountDeviceWithoutSudoClientReturnsLocalError covers the ordinary
// desktop case where udisksctl exists but simply is not what mounted this
// point, and there is no elevated dispatcher running at all (the common
// case: f4 only starts one on demand). The local unmount(2) error must come
// back rather than a nil-pointer panic on globalSudoClient.
func TestUnmountDeviceWithoutSudoClientReturnsLocalError(t *testing.T) {
	stubExternalCommand(t, func(string, ...string) ([]byte, error) { return nil, nil })
	stubLookupExecutable(t, "", errors.New("not found"))
	stubUnmountSyscall(t, syscall.EACCES)

	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = nil

	err := UnmountDevice(context.Background(), "/dev/sdb1", "/media/user/USB_STICK")
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("UnmountDevice() = %v, want syscall.EACCES", err)
	}
}
