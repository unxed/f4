package vfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrDestinationExists    = errors.New("destination already exists")
	ErrNoReplaceUnsupported = errors.New("no-replace rename is unsupported for this object")
)

func destinationExists(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return ErrDestinationExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sameObject(oldPath, newPath string) bool {
	oldInfo, oldErr := os.Stat(oldPath)
	newInfo, newErr := os.Stat(newPath)
	return oldErr == nil && newErr == nil && os.SameFile(oldInfo, newInfo)
}

func renameSameObject(oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}
	// Some case-insensitive filesystems need an intermediate spelling.
	dir := filepath.Dir(oldPath)
	tmp, err := os.CreateTemp(dir, ".f4-visren-case-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return errors.Join(err, os.Remove(tmpPath))
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(oldPath, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, newPath); err != nil {
		_ = os.Rename(tmpPath, oldPath)
		return err
	}
	return nil
}

// renameNoReplacePortable is the fail-safe fallback for Unix kernels without
// renameat2/renamex_np. A hard link reserves file targets atomically. Directory
// targets are reserved with an empty mode-000 directory, which rename may
// replace but an unrelated existing target cannot be mistaken for ours.
func renameNoReplacePortable(oldPath, newPath string) error {
	if sameObject(oldPath, newPath) {
		return renameSameObject(oldPath, newPath)
	}
	if err := destinationExists(newPath); err != nil {
		return err
	}
	info, err := os.Lstat(oldPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.Mkdir(newPath, 0); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrDestinationExists
			}
			return err
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			_ = os.Remove(newPath)
			// Some filesystems (notably eCryptfs) reject renaming a directory
			// over the empty directory used as our reservation. Retry once the
			// reservation is gone, but only if the destination is still absent.
			if errors.Is(err, os.ErrExist) {
				if existsErr := destinationExists(newPath); existsErr != nil {
					return existsErr
				}
				return os.Rename(oldPath, newPath)
			}
			return err
		}
		return nil
	}
	if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err := os.Link(oldPath, newPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrDestinationExists
			}
			return err
		}
		if err := os.Remove(oldPath); err != nil {
			_ = os.Remove(newPath)
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNoReplaceUnsupported, info.Mode())
}
