//go:build windows || plan9 || js

package fusefs

import (
	"errors"
	"os"
	"syscall"
)

// detachAttr has nothing to detach from on platforms where nothing mounts.
func detachAttr() *syscall.SysProcAttr { return nil }

// systemUnmount exists so that cli.go compiles everywhere. It is never
// reached: runMount refuses early when Supported() is false, and a registry
// with no mounts in it has nothing to unmount.
func systemUnmount(mountPoint string) error {
	return errors.New("unmounting is not supported on this platform")
}

// processExists relies on FindProcess, which really does look the process up
// on the platforms this file covers.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}
