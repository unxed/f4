//go:build linux

package app

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// setProcessName sets the kernel's 16-byte comm field to "f4" — what top,
// htop and a window manager's fallback taskbar label read from
// /proc/<pid>/comm. Without this, a Linux release binary (built with
// goffi_universal to bind to either glibc or musl at runtime, see
// internal/update/selfexec.go) re-execs itself through the dynamic loader
// before main ever runs, so the kernel recorded the loader's own name
// ("ld-linux-x86-64.so.2") as comm instead of the program's (f4 #1390).
// comm is a live kernel field, not fixed at exec time, so setting it here —
// whether or not that re-exec happened — is enough.
func setProcessName() {
	// PR_SET_NAME only renames the calling thread's own /proc/<pid>/task/<tid>/comm.
	// What ps/top/a window manager read by default is /proc/<pid>/comm, an alias
	// for the thread GROUP LEADER's entry — so this only takes effect while still
	// running on the process's first (leader) OS thread, which is where Main
	// calls this, before anything has had a chance to move the goroutine off it.
	// Locking keeps it that way for whatever runs next on this goroutine too.
	runtime.LockOSThread()
	name := append([]byte("f4"), 0)
	_ = unix.Prctl(unix.PR_SET_NAME, uintptr(unsafe.Pointer(&name[0])), 0, 0, 0)
}
