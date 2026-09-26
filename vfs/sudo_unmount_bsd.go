//go:build linux || darwin || freebsd || dragonfly || openbsd || netbsd

package vfs

import "syscall"

// rawUnmount is what the sudo dispatcher's CmdUnmount case (sudo_msg.go,
// sudo_dispatcher_unix.go) actually calls as root. It is split out from the
// dispatcher itself because syscall.Unmount is not defined for every
// GOOS the dispatcher's own "!windows" build tag lets through --
// sudo_unmount_other.go covers the rest. UnmountDevice (mount_linux.go)
// only ever asks for this elevated path on Linux today; the other GOOS
// values here compile it purely so the dispatcher, a single binary shared
// across every non-Windows platform build, keeps building everywhere.
func rawUnmount(path string) error {
	return syscall.Unmount(path, 0)
}
