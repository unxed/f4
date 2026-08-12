//go:build !windows && !plan9 && !js

package fusefs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// detachAttr puts the daemon child in its own session, so that a Ctrl+C in
// the shell that started it, or the shell itself going away, does not take
// the mount down with it.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// unmountHelpers is tried in order. fusermount3 comes first because that is
// what fuse3 installs and what go-fuse itself prefers; plain umount is last
// and generally needs privileges, but it is the only option on the systems
// that have no helper at all.
func unmountHelpers() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"umount"}, {"diskutil", "unmount"}}
	default:
		return [][]string{{"fusermount3", "-u"}, {"fusermount", "-u"}, {"umount"}}
	}
}

// systemUnmount ends a mount owned by some other process.
//
// The in-process Unmount() cannot help here: it knows about the mounts this
// f4 started, and the whole point of the registry is that most mounts were
// started by a different one.
func systemUnmount(mountPoint string) error {
	var attempts []string
	for _, helper := range unmountHelpers() {
		path, err := exec.LookPath(helper[0])
		if err != nil {
			continue
		}
		args := append(append([]string{}, helper[1:]...), mountPoint)
		var errBuf bytes.Buffer
		cmd := exec.Command(path, args...)
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			return nil
		}
		detail := strings.TrimSpace(errBuf.String())
		if detail == "" {
			detail = "failed"
		}
		// A busy mount is a question for the user, not something to
		// work around by trying the next helper with more force.
		if strings.Contains(strings.ToLower(detail), "busy") {
			return fmt.Errorf("%s: %s", helper[0], detail)
		}
		attempts = append(attempts, fmt.Sprintf("%s: %s", helper[0], detail))
	}
	if len(attempts) == 0 {
		return fmt.Errorf("no unmount helper found (install fuse3 for fusermount3)")
	}
	return fmt.Errorf("%s", strings.Join(attempts, "; "))
}

// processExists probes with signal 0. os.FindProcess always succeeds on unix,
// so the signal is the only real test; EPERM means the process is there and
// belongs to somebody else.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
