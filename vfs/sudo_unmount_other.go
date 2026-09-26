//go:build !windows && !linux && !darwin && !freebsd && !dragonfly && !openbsd && !netbsd

package vfs

import "errors"

// rawUnmount has no syscall.Unmount to call here (solaris, illumos, and
// whatever else the "!windows" build tag on sudo_dispatcher_unix.go lets
// through but sudo_unmount_bsd.go's more specific list does not): the
// standard library's syscall package does not define one for them, and
// f4's mount-manager (f4#415) does not target any of them yet regardless.
func rawUnmount(path string) error {
	return errors.New("unmount is not supported on this platform")
}
