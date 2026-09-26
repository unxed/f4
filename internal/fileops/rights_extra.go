package fileops

import (
	"context"

	"github.com/unxed/f4/vfs"
)

// rightsAfterExternalCopy puts the chosen permissions on a file that a
// server-side or server-to-server copy wrote. Neither streams through f4, so
// neither passed through destinationRights, and scp -p even forces the
// source's permissions onto an overwritten file, where "Default" promises
// that it keeps its own: existingRights is what it had.
func rightsAfterExternalCopy(ctx context.Context, state *FileOpState, dstVfs vfs.VFS, destPath string, srcRights uint32, existed bool, existingRights uint32) {
	mode := destinationRights(ctx, state, dstVfs, destPath, srcRights, false, existed)
	if mode == 0 {
		mode = existingRights
	}
	if mode == 0 {
		return
	}
	_ = dstVfs.SetAttributes(ctx, destPath, vfs.VFSItem{UnixMode: mode, Uid: -1, Gid: -1})
}

// inheritMovedTree gives a renamed object the rights of the folder it now sits
// in, under "Inherit". A rename keeps the object's own permissions, so the
// copy path's destinationRights never sees it; Far resets the security of a
// renamed object for the same choice (far/copy.cpp, ResetSecurity after
// move_file).
//
// On Windows the reset of the top object is all there is to do: its children
// inherit from it. With permission bits the whole tree has to be walked, the
// way a copy under "Inherit" gives every copied object its folder's rights.
// A link is left alone, because chmod would follow it out of the moved tree.
func inheritMovedTree(ctx context.Context, state *FileOpState, dstVfs vfs.VFS, path string, depth int) {
	if state == nil || state.AccessRights != AccessRightsInherit || depth > 1000 || ctx.Err() != nil {
		return
	}
	if depth == 0 {
		applyPlatformRights(ctx, state, nil, "", dstVfs, path)
	}
	// vfs.WindowsPersonality, not a raw GOOS check (WINE.md §18.2, "права"):
	// under Wine's posix personality the destination is a real POSIX
	// filesystem with real Unix permission bits, read and written through
	// libwinescape/hostfs, so the tree walk below is exactly as meaningful
	// there as it is on the Linux build -- only native Windows leaves the
	// top-level reset as "all there is to do".
	if vfs.WindowsPersonality() && IsLocalOSVFS(dstVfs) {
		return
	}
	item, err := vfs.Lstat(ctx, dstVfs, path)
	if err != nil || item.IsSymlink {
		return
	}
	if mode := inheritedRights(ctx, state, dstVfs, path, item.IsDir); mode != 0 {
		_ = dstVfs.SetAttributes(ctx, path, vfs.VFSItem{UnixMode: mode, Uid: -1, Gid: -1})
	}
	if !item.IsDir {
		return
	}
	var children []vfs.VFSItem
	if err := dstVfs.ReadDir(ctx, path, func(chunk []vfs.VFSItem) {
		children = append(children, chunk...)
	}); err != nil {
		state.note("RIGHTS   %s: %v", path, err)
		return
	}
	for _, child := range children {
		if child.Name == "." || child.Name == ".." {
			continue
		}
		inheritMovedTree(ctx, state, dstVfs, dstVfs.Join(path, child.Name), depth+1)
	}
}
