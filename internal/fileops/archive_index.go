//go:build !dragonfly && !netbsd && !solaris && !illumos

package fileops

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/tarindexcache"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/tar"
	"github.com/unxed/vtui"
)

func isTarArchive(path string) bool {
	name := strings.ToLower(path)
	return strings.HasSuffix(name, ".tar") ||
		strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") ||
		strings.HasSuffix(name, ".tar.bz2") || strings.HasSuffix(name, ".tbz2") ||
		strings.HasSuffix(name, ".tar.xz") || strings.HasSuffix(name, ".txz") ||
		strings.HasSuffix(name, ".tar.zst") || strings.HasSuffix(name, ".tar.zstd")
}

func handleArchiveIndexOp(srcVfs vfs.VFS, oldPath string, dstVfs vfs.VFS, newPath string, isMove bool) {
	if !isTarArchive(oldPath) {
		return
	}
	// Standard tar indexes are local sidecars. Treating a cloud:// URI as an
	// OS path creates a bogus "cloud:" tree on Windows and can never migrate a
	// useful index to or from a remote object.
	if _, ok := srcVfs.(*vfs.OSVFS); !ok {
		return
	}
	if _, ok := dstVfs.(*vfs.OSVFS); !ok {
		return
	}

	absOld, oldErr := srcVfs.Abs(oldPath)
	absNew, newErr := dstVfs.Abs(newPath)
	if oldErr != nil || newErr != nil {
		return
	}
	// The index f4 keeps in its own cache goes with a moved archive: what it
	// holds is the archive's content, which a move does not change (#1187).
	if isMove {
		tarindexcache.Move(absOld, absNew)
	}

	oldIdx, oldErr := tar.GetStandardIndexPath(absOld)
	newIdx, newErr := tar.GetStandardIndexPath(absNew)
	if oldErr != nil || newErr != nil {
		return
	}

	if _, err := os.Stat(oldIdx); err == nil {
		if isMove {
			vtui.DebugLog("FILEOP: Moving archive index: %s -> %s", oldIdx, newIdx)
			os.Rename(oldIdx, newIdx)
		} else {
			vtui.DebugLog("FILEOP: Copying archive index: %s -> %s", oldIdx, newIdx)
			s, err := os.Open(oldIdx)
			if err != nil {
				return
			}
			defer s.Close()

			os.MkdirAll(filepath.Dir(newIdx), 0755)
			d, err := os.Create(newIdx)
			if err != nil {
				return
			}
			defer d.Close()

			io.Copy(d, s)
		}
	}
}

func handleArchiveIndexDelete(ctx context.Context, v vfs.VFS, p string) {
	removeArchiveIndexes(collectArchiveIndexes(ctx, v, p))
}

// collectArchiveIndexes snapshots companion indexes before their source is
// deleted or moved to trash. Removal happens only after the filesystem
// mutation succeeds; deleting an index first would corrupt state on failure.
func collectArchiveIndexes(ctx context.Context, v vfs.VFS, p string) []string {
	if ctx.Err() != nil {
		return nil
	}
	// Standard tar indexes are local sidecar/cache files. A remote VFS cannot
	// own one, and recursively walking it here would defeat the O(selected
	// roots) trash path used for cloud directories.
	if _, local := v.(*vfs.OSVFS); !local {
		return nil
	}
	st, err := v.Stat(ctx, p)
	if err != nil {
		return nil
	}

	if st.IsDir && !st.IsSymlink {
		var indexes []string
		v.ReadDir(ctx, p, func(items []vfs.VFSItem) {
			for _, itm := range items {
				if ctx.Err() != nil || itm.Name == ".." {
					continue
				}
				indexes = append(indexes, collectArchiveIndexes(ctx, v, v.Join(p, itm.Name))...)
			}
		})
		return indexes
	} else if isTarArchive(p) {
		abs, _ := v.Abs(p)
		// The indexes f4 keeps in its cache would otherwise stay for good.
		indexes := tarindexcache.Files(abs)
		idx, _ := tar.GetStandardIndexPath(abs)
		if _, err := os.Stat(idx); err == nil {
			indexes = append(indexes, idx)
		}
		return indexes
	}
	return nil
}

func removeArchiveIndexes(indexes []string) {
	for _, idx := range indexes {
		vtui.DebugLog("FILEOP: Deleting archive index after successful source deletion: %s", idx)
		if err := os.Remove(idx); err != nil && !os.IsNotExist(err) {
			vtui.DebugLog("FILEOP: Cannot delete archive index %s: %v", idx, err)
		}
	}
}
