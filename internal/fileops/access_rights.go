package fileops

import (
	"context"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/vfs"
)

// AccessRightsMode is the "Access rights" choice of the F5/F6 dialog: what
// happens to the permission bits of a copy. Far Manager offers the same three,
// and the numbers are written to the configuration file, so they are part of
// the stored format and must not be renumbered.
type AccessRightsMode int

const (
	// AccessRightsDefault leaves the decision where the platform leaves it: a
	// destination that had to be created takes the source's permission bits,
	// a destination that was already there keeps its own. That is what cp(1)
	// and Win32 CopyFile do with an existing target.
	AccessRightsDefault AccessRightsMode = iota
	// AccessRightsCopy puts the source's permission bits on the copy, also
	// when the copy landed on an object that already existed.
	AccessRightsCopy
	// AccessRightsInherit ignores the source's permission bits: the copy is
	// given the permissions of the folder it lands in, the way a newly
	// created object would receive them. Files drop the folder's execute
	// bits, which a folder needs only to be entered.
	AccessRightsInherit
)

// AccessRightsModeFromConfig maps a stored number onto a mode. An unknown
// number is the default rather than an error: a configuration file written by
// a newer f4 must not stop a copy from running.
func AccessRightsModeFromConfig(value int) AccessRightsMode {
	switch AccessRightsMode(value) {
	case AccessRightsCopy:
		return AccessRightsCopy
	case AccessRightsInherit:
		return AccessRightsInherit
	default:
		return AccessRightsDefault
	}
}

// FileOpOptions carries the choices made in the copy/move dialog into the
// operation. Its zero value is the behaviour f4 had before the dialog offered
// any of them.
type FileOpOptions struct {
	AccessRights AccessRightsMode
}

// DefaultFileOpOptions is what an operation started without the dialog runs
// with: the configured default, so Shift+F5 and a copy with confirmations
// turned off follow the same setting as F5.
func DefaultFileOpOptions() FileOpOptions {
	return FileOpOptions{AccessRights: AccessRightsModeFromConfig(config.App.CopyAccessRights)}
}

// fallbackRights is the mode used where the source carries none, which is
// every VFS that does not report Unix permissions at all.
func fallbackRights(isDir bool) uint32 {
	if isDir {
		return 0o755
	}
	return 0o644
}

// destinationRights returns the permission bits to put on a copied object.
// Zero means "leave the permissions alone": that is how every VFS in the tree
// reads a zero UnixMode, and it is the only way to say it through
// SetAttributes, which also carries the timestamps that are set regardless.
func destinationRights(ctx context.Context, state *FileOpState, dstVfs vfs.VFS, destPath string, srcMode uint32, isDir, destinationExisted bool) uint32 {
	mode := AccessRightsDefault
	if state != nil {
		mode = state.AccessRights
	}
	switch mode {
	case AccessRightsInherit:
		return inheritedRights(ctx, state, dstVfs, destPath, isDir)
	case AccessRightsCopy:
		if srcMode == 0 {
			return fallbackRights(isDir)
		}
		return srcMode
	default:
		if destinationExisted {
			return 0
		}
		if srcMode == 0 {
			return fallbackRights(isDir)
		}
		return srcMode
	}
}

// inheritedRights derives the permissions of a copy from the folder it lands
// in. A local copy cannot simply be left as the filesystem created it: f4
// creates a new destination file with 0600 so that its contents are never
// visible through a permissive umask while they are still incomplete, and
// restores the final permissions afterwards. This is that restore.
func inheritedRights(ctx context.Context, state *FileOpState, dstVfs vfs.VFS, destPath string, isDir bool) uint32 {
	parentMode := parentRights(ctx, state, dstVfs, dstVfs.Dir(destPath))
	if parentMode == 0 {
		return fallbackRights(isDir)
	}
	if isDir {
		return parentMode & 0o777
	}
	return parentMode & 0o666
}

// parentRights reads the folder's permissions once per folder. One operation
// copies sequentially in a single goroutine, so the cache needs no lock; it
// exists because a remote destination would otherwise be asked for the same
// folder again for every file that goes into it. A folder that cannot be
// stat'ed is cached as zero and answered from the fallback.
func parentRights(ctx context.Context, state *FileOpState, dstVfs vfs.VFS, parent string) uint32 {
	if state != nil {
		if mode, ok := state.parentRights[parent]; ok {
			return mode
		}
	}
	var mode uint32
	if st, err := dstVfs.Stat(ctx, parent); err == nil {
		mode = st.UnixMode
	}
	if state != nil {
		if state.parentRights == nil {
			state.parentRights = make(map[string]uint32)
		}
		state.parentRights[parent] = mode
	}
	return mode
}
