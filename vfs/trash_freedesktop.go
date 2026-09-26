//go:build linux || dragonfly || freebsd || netbsd || openbsd || solaris || illumos

package vfs

import (
	"context"
	"os"
	"syscall"
)

var _ TrashVFS = (*OSVFS)(nil)

// MoveToTrash hands off to the shared FreeDesktop.org Trash algorithm
// (vfs/trash_xdg.go). This file supplies that algorithm's platform
// substitution points: real POSIX syscalls, unchanged from before the split
// in every observable way.
func (v *OSVFS) MoveToTrash(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := v.Abs(path)
	if err != nil {
		return err
	}
	return moveToFreedesktopTrash(ctx, abs)
}

func defaultTrashGetuid() int { return os.Getuid() }

func defaultTrashHomeEnv(key string) string { return os.Getenv(key) }

func defaultTrashHomeDir() (string, error) { return os.UserHomeDir() }

// defaultTrashWriteExclusive creates path exclusively, writes data, fsyncs
// and closes it -- unchanged from the pre-split writeExclusiveFile.
func defaultTrashWriteExclusive(path string, data []byte, mode os.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			_ = os.Remove(path)
			err = closeErr
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err = f.Sync(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// trashStatIdentity extracts the device and owning uid a real POSIX stat(2)
// reports, unchanged from the pre-split unixStatIdentity.
func trashStatIdentity(info os.FileInfo) (device uint64, owner uint32, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(stat.Dev), stat.Uid, true
}
