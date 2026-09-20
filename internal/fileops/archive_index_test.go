//go:build !dragonfly && !netbsd

package fileops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/tarindexcache"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/tar"
	"github.com/unxed/vtui"
)

type remoteArchiveIndexVFS struct {
	vfs.VFS
	absCalls int
}

func (v *remoteArchiveIndexVFS) Abs(path string) (string, error) {
	v.absCalls++
	return path, nil
}

func TestArchiveIndex_RemoteURIIsNeverTreatedAsLocalSidecar(t *testing.T) {
	remote := &remoteArchiveIndexVFS{}
	local := vfs.NewOSVFS(t.TempDir())
	handleArchiveIndexOp(
		remote,
		"cloud://yandex/11111111-1111-1111-1111-111111111111/archive.tar",
		local,
		"archive.tar",
		false,
	)
	if remote.absCalls != 0 {
		t.Fatalf("remote archive index path reached OS path handling %d times", remote.absCalls)
	}
}

func TestArchiveIndex_AutoOp(t *testing.T) {
	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()
	srcVfs := vfs.NewOSVFS(tmpSrc)
	dstVfs := vfs.NewOSVFS(tmpDst)

	archiveName := "test.tar.gz"
	absOld, _ := srcVfs.Abs(archiveName)
	oldIdx, _ := tar.GetStandardIndexPath(absOld)

	// Ensure cache directory exists and create dummy index
	if err := os.MkdirAll(filepath.Dir(oldIdx), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldIdx, []byte("fake index"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(oldIdx); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove source archive index: %v", err)
		}
	})

	t.Run("Copy Index", func(t *testing.T) {
		newArchiveName := "copied.tar.gz"
		absNew, _ := dstVfs.Abs(newArchiveName)
		newIdx, _ := tar.GetStandardIndexPath(absNew)
		t.Cleanup(func() {
			if err := os.Remove(newIdx); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove copied archive index: %v", err)
			}
		})

		handleArchiveIndexOp(srcVfs, archiveName, dstVfs, newArchiveName, false)

		if _, err := os.Stat(newIdx); os.IsNotExist(err) {
			t.Error("Archive index was not copied")
		}
		if _, err := os.Stat(oldIdx); os.IsNotExist(err) {
			t.Error("Original index was removed during copy")
		}
	})

	t.Run("Move Index", func(t *testing.T) {
		newArchiveName := "moved.tar.gz"
		absNew, _ := dstVfs.Abs(newArchiveName)
		newIdx, _ := tar.GetStandardIndexPath(absNew)
		t.Cleanup(func() {
			if err := os.Remove(newIdx); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove moved archive index: %v", err)
			}
		})

		// Restore old index if previous test cleaned it
		if err := os.WriteFile(oldIdx, []byte("fake index"), 0600); err != nil {
			t.Fatal(err)
		}

		handleArchiveIndexOp(srcVfs, archiveName, dstVfs, newArchiveName, true)

		if _, err := os.Stat(newIdx); os.IsNotExist(err) {
			t.Error("Archive index was not moved")
		}
		if _, err := os.Stat(oldIdx); err == nil {
			t.Error("Original index still exists after move")
		}
	})
}

func TestArchiveIndex_AutoDelete(t *testing.T) {
	tmp := t.TempDir()
	v := vfs.NewOSVFS(tmp)

	t.Run("Delete single file index", func(t *testing.T) {
		name := "to_delete.tgz"
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("arc"), 0600); err != nil {
			t.Fatal(err)
		}
		abs, _ := v.Abs(name)
		idx, _ := tar.GetStandardIndexPath(abs)
		if err := os.MkdirAll(filepath.Dir(idx), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(idx, []byte("idx"), 0600); err != nil {
			t.Fatal(err)
		}

		handleArchiveIndexDelete(context.Background(), v, v.Join(v.GetPath(), name))

		if _, err := os.Stat(idx); err == nil {
			t.Error("Archive index was not deleted")
		}
	})

	t.Run("Recursive delete index", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(tmp, "subdir"), 0700); err != nil {
			t.Fatal(err)
		}
		name := "subdir/nested.tar.zst"
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("arc"), 0600); err != nil {
			t.Fatal(err)
		}
		abs, _ := v.Abs(name)
		idx, _ := tar.GetStandardIndexPath(abs)
		if err := os.WriteFile(idx, []byte("idx"), 0600); err != nil {
			t.Fatal(err)
		}

		handleArchiveIndexDelete(context.Background(), v, v.Join(v.GetPath(), "subdir"))

		if _, err := os.Stat(idx); err == nil {
			t.Error("Nested archive index was not deleted during recursive folder cleanup")
		}
	})
}

func TestExecuteFileOp_ArchiveIndexMigration(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()
	srcVfs := vfs.NewOSVFS(tmpSrc)
	dstVfs := vfs.NewOSVFS(tmpDst)

	name := "migrate.tar.gz"
	if err := os.WriteFile(filepath.Join(tmpSrc, name), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	absOld, _ := srcVfs.Abs(name)
	oldIdx, _ := tar.GetStandardIndexPath(absOld)
	if err := os.MkdirAll(filepath.Dir(oldIdx), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldIdx, []byte("index-data"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(oldIdx); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove source archive index: %v", err)
		}
	})

	done := make(chan struct{})
	ExecuteFileOp(srcVfs, dstVfs, []string{name}, tmpDst, true, 2, func() { close(done) })

	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case <-done:
			break loop
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	absNew, _ := dstVfs.Abs(name)
	newIdx, _ := tar.GetStandardIndexPath(absNew)
	t.Cleanup(func() {
		if err := os.Remove(newIdx); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove migrated archive index: %v", err)
		}
	})

	if _, err := os.Stat(newIdx); os.IsNotExist(err) {
		t.Error("Archive index did not migrate during ExecuteFileOp (Move)")
	}
	if _, err := os.Stat(oldIdx); err == nil {
		t.Error("Old index remained after ExecuteFileOp (Move)")
	}
}

// The index f4 keeps in its own cache follows an archive that is moved and is
// removed with one that is deleted, instead of being left behind (#1187).
func TestArchiveIndex_MoveAndDeleteTakeTheCachedIndexAlong(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)

	root := t.TempDir()
	v := vfs.NewOSVFS(root)
	oldPath, newPath := filepath.Join(root, "a.tar"), filepath.Join(root, "b.tar")
	if err := os.WriteFile(oldPath, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tarindexcache.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tarindexcache.Dir(), tarindexcache.Prefix(oldPath)+"-ffff.index.sqlite"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	handleArchiveIndexOp(v, oldPath, v, newPath, true)
	if len(tarindexcache.Files(oldPath)) != 0 || len(tarindexcache.Files(newPath)) != 1 {
		t.Fatalf("after the move: old %v, new %v", tarindexcache.Files(oldPath), tarindexcache.Files(newPath))
	}

	indexes := collectArchiveIndexes(context.Background(), v, newPath)
	if len(indexes) != 1 {
		t.Fatalf("collected %v, want the cached index of the archive", indexes)
	}
	removeArchiveIndexes(indexes)
	if left := tarindexcache.Files(newPath); len(left) != 0 {
		t.Fatalf("the cached index stayed after the delete: %v", left)
	}

	// A copy keeps the original's index and builds its own on demand.
	if err := os.WriteFile(filepath.Join(tarindexcache.Dir(), tarindexcache.Prefix(newPath)+"-ffff.index.sqlite"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	handleArchiveIndexOp(v, newPath, v, filepath.Join(root, "c.tar"), false)
	if len(tarindexcache.Files(newPath)) != 1 {
		t.Fatal("copying an archive took the original's index")
	}
}
