package findfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestSearchSelectedFoldersScopeAndProgress(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"first", "second", "other"} {
		if err := os.MkdirAll(filepath.Join(root, dir, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{filepath.Join(dir, "hit.txt"), filepath.Join(dir, "nested", "deep.txt")} {
			if err := os.WriteFile(filepath.Join(root, name), []byte("match"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	base := vfs.NewOSVFS(root)
	for _, generic := range []bool{false, true} {
		var provider vfs.VFS = base
		if generic {
			provider = &walkVFS{VFS: base, read: base.ReadDir}
		}
		for _, restrict := range []bool{false, true} {
			options := Options{}
			if restrict {
				options.SelectedFolders = []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
			}
			var hits []vfs.FoundEntry
			var last vfs.FindProgress
			completed := make(map[int64]bool)
			err := run(context.Background(), provider, root, "*.txt", "", options,
				func(hit vfs.FoundEntry) { hits = append(hits, hit) },
				func(p vfs.FindProgress) {
					last = p
					completed[p.CompletedDirs] = true
				})
			want := 7
			if restrict {
				want = 4
			}
			if err != nil || len(hits) != want {
				t.Fatalf("generic=%v restricted=%v: hits=%v error=%v", generic, restrict, hits, err)
			}
			if restrict {
				for _, hit := range hits {
					rel, err := filepath.Rel(root, hit.Path)
					if err != nil || rel == "root.txt" || filepath.Dir(rel) == "other" || filepath.Dir(rel) == filepath.Join("other", "nested") {
						t.Errorf("hit outside selected scope: %s", hit.Path)
					}
				}
				if !last.DirectoryTotalKnown || last.TotalDirs != 2 || last.CompletedDirs != 2 || !completed[1] || last.Found != 4 {
					t.Errorf("generic=%v: progress=%+v completed=%v", generic, last, completed)
				}
			}
		}
	}
}

type walkVFS struct {
	vfs.VFS
	read func(context.Context, string, func([]vfs.VFSItem)) error
}

func (v *walkVFS) ReadDir(ctx context.Context, dir string, chunk func([]vfs.VFSItem)) error {
	return v.read(ctx, dir, chunk)
}

func TestGenericWalkSkippedChildrenAndLinks(t *testing.T) {
	base := vfs.NewOSVFS(t.TempDir())
	root := base.GetPath()
	provider := &walkVFS{VFS: base, read: func(_ context.Context, dir string, chunk func([]vfs.VFSItem)) error {
		if dir != root {
			return errors.New("permission denied")
		}
		chunk([]vfs.VFSItem{{Name: "denied", IsDir: true}, {Name: "skip", IsDir: true}, {Name: "link", IsDir: true, IsSymlink: true}, {Name: "root.txt"}})
		return nil
	}}
	var last vfs.FindProgress
	var hits []vfs.FoundEntry
	err := run(context.Background(), provider, root, "* | skip", "", Options{}, func(h vfs.FoundEntry) { hits = append(hits, h) }, func(p vfs.FindProgress) { last = p })
	if err != nil || len(hits) != 1 || !last.DirectoryTotalKnown || last.TotalDirs != 1 || last.CompletedDirs != 1 {
		t.Fatalf("hits=%v progress=%+v error=%v", hits, last, err)
	}
}

func TestGenericWalkEmptyRootAndRootError(t *testing.T) {
	base := vfs.NewOSVFS(t.TempDir())
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "error"}[fail], func(t *testing.T) {
			provider := &walkVFS{VFS: base, read: func(context.Context, string, func([]vfs.VFSItem)) error {
				if fail {
					return errors.New("root unavailable")
				}
				return nil
			}}
			var last vfs.FindProgress
			err := run(context.Background(), provider, base.GetPath(), "*", "", Options{}, func(vfs.FoundEntry) { t.Fatal("unexpected hit") }, func(p vfs.FindProgress) { last = p })
			if (err != nil) != fail || last.DirectoryTotalKnown == fail || last.TotalDirs != 0 {
				t.Fatalf("progress=%+v error=%v", last, err)
			}
		})
	}
}

func TestUnsupportedStreamFallsBackWithoutDuplicates(t *testing.T) {
	base := vfs.NewOSVFS(t.TempDir())
	walker := &walkVFS{VFS: base, read: func(_ context.Context, _ string, chunk func([]vfs.VFSItem)) error {
		chunk([]vfs.VFSItem{{Name: "one.txt"}})
		return nil
	}}
	provider := &streamVFS{VFS: walker, search: func(context.Context, vfs.FindQuery, func(vfs.FoundEntry)) error {
		return vfs.ErrFindOptionsUnsupported
	}}
	var hits []vfs.FoundEntry
	err := run(context.Background(), provider, base.GetPath(), "*", "", Options{}, func(h vfs.FoundEntry) { hits = append(hits, h) }, func(vfs.FindProgress) {})
	if err != nil || len(hits) != 1 || hits[0].Item.Name != "one.txt" {
		t.Fatalf("hits=%v error=%v", hits, err)
	}
}
