package multiarc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
)

type fakeBackend struct {
	entries        []entry
	listErr        error
	extractOneFunc func(ctx context.Context, localPath, destDir, member string) error
}

func (fakeBackend) id() string { return "fake" }

func (b fakeBackend) list(context.Context, string) ([]entry, error) {
	return b.entries, b.listErr
}

func (fakeBackend) extractAll(context.Context, string, string) error { return nil }

func (b fakeBackend) extractOne(ctx context.Context, localPath, destDir, member string) error {
	if b.extractOneFunc != nil {
		return b.extractOneFunc(ctx, localPath, destDir, member)
	}
	return nil
}

func newTestVFS(t *testing.T, b backend) *MultiArcVFS {
	t.Helper()
	return NewMultiArcVFS(nil, "/archives/test.tar.gz", "test.tar.gz", b, "fake")
}

func TestMultiArcVFSReadDirBuildsTree(t *testing.T) {
	b := fakeBackend{entries: []entry{
		{Path: "top.txt"},
		{Path: "dir/nested.txt"},   // "dir" has no explicit entry of its own
		{Path: "dir/sub/leaf.txt"}, // neither does "dir/sub"
	}}
	v := newTestVFS(t, b)

	var rootItems []vfs.VFSItem
	if err := v.ReadDir(context.Background(), "", func(items []vfs.VFSItem) {
		rootItems = append(rootItems, items...)
	}); err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	isDirByName := map[string]bool{}
	seen := map[string]bool{}
	for _, it := range rootItems {
		seen[it.Name] = true
		isDirByName[it.Name] = it.IsDir
	}
	if !seen["top.txt"] || !seen["dir"] {
		t.Fatalf("root listing = %#v", rootItems)
	}
	if isDirByName["top.txt"] {
		t.Error("top.txt should not be a directory")
	}
	if !isDirByName["dir"] {
		t.Error("dir should be synthesized as a directory (it has children but no explicit entry)")
	}

	var subItems []vfs.VFSItem
	if err := v.ReadDir(context.Background(), "/dir/sub", func(items []vfs.VFSItem) {
		subItems = append(subItems, items...)
	}); err != nil {
		t.Fatalf("ReadDir /dir/sub: %v", err)
	}
	if len(subItems) != 1 || subItems[0].Name != "leaf.txt" || subItems[0].IsDir {
		t.Fatalf("dir/sub listing = %#v", subItems)
	}
}

func TestMultiArcVFSReadDirNoSuchDirectory(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{{Path: "top.txt"}}})
	if err := v.ReadDir(context.Background(), "/nope", func([]vfs.VFSItem) {}); err == nil {
		t.Fatal("expected an error for a directory that does not exist in the archive")
	}
}

func TestMultiArcVFSStat(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{
		{Path: "top.txt", Size: 5, SizeKnown: true},
		{Path: "dir", IsDir: true},
	}})
	item, err := v.Stat(context.Background(), "/top.txt")
	if err != nil {
		t.Fatalf("Stat top.txt: %v", err)
	}
	if item.IsDir || item.Size != 5 || !item.SizeKnown {
		t.Fatalf("top.txt item = %#v", item)
	}
	dirItem, err := v.Stat(context.Background(), "/dir")
	if err != nil || !dirItem.IsDir {
		t.Fatalf("Stat dir = %#v, err=%v", dirItem, err)
	}
	if _, err := v.Stat(context.Background(), "/missing"); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}

func TestMultiArcVFSSetPath(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{{Path: "dir/nested.txt"}}})
	if err := v.SetPath("/dir"); err != nil {
		t.Fatalf("SetPath /dir: %v", err)
	}
	if v.GetPath() != "/dir" {
		t.Fatalf("GetPath = %q, want /dir", v.GetPath())
	}
	if err := v.SetPath("/nope"); err == nil {
		t.Fatal("expected an error setting path to a nonexistent directory")
	}
}

func TestMultiArcVFSMutationsAreReadOnly(t *testing.T) {
	v := newTestVFS(t, fakeBackend{})
	ctx := context.Background()
	if err := v.MkDir(ctx, "/x"); err == nil {
		t.Error("MkDir should fail")
	}
	if err := v.Remove(ctx, "/x"); err == nil {
		t.Error("Remove should fail")
	}
	if err := v.Rename(ctx, "/x", "/y"); err == nil {
		t.Error("Rename should fail")
	}
	if _, err := v.Create(ctx, "/x"); err == nil {
		t.Error("Create should fail")
	}
}

func TestMultiArcVFSOpenExtractsAndReads(t *testing.T) {
	b := fakeBackend{
		entries: []entry{{Path: "dir/file.txt", Size: 7, SizeKnown: true}},
		extractOneFunc: func(ctx context.Context, localPath, destDir, member string) error {
			if member != "dir/file.txt" {
				t.Fatalf("extractOne member = %q", member)
			}
			full := filepath.Join(destDir, filepath.FromSlash(member))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			return os.WriteFile(full, []byte("content"), 0o600)
		},
	}
	v := newTestVFS(t, b)
	t.Cleanup(closeSharedMultiArcTempDirs)

	f, err := v.Open(context.Background(), "/dir/file.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = f.Close() }()
	if f.Size() != 7 {
		t.Errorf("Size() = %d, want 7", f.Size())
	}
	buf := make([]byte, 7)
	n, err := f.ReadAt(context.Background(), buf, 0)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf[:n]) != "content" {
		t.Fatalf("content = %q", buf[:n])
	}
}

func TestMultiArcVFSOpenRejectsDirectory(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{{Path: "dir", IsDir: true}}})
	if _, err := v.Open(context.Background(), "/dir"); err == nil {
		t.Fatal("expected an error opening a directory")
	}
}

func TestMultiArcVFSCloneSharesState(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{{Path: "top.txt"}}})
	if err := v.ReadDir(context.Background(), "", func([]vfs.VFSItem) {}); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	clone := v.Clone().(*MultiArcVFS)
	if clone.state != v.state {
		t.Error("Clone should share the listing state, not re-list")
	}
	if _, err := clone.Stat(context.Background(), "/top.txt"); err != nil {
		t.Fatalf("Stat on clone: %v", err)
	}
}

func TestMultiArcVFSPanelTitle(t *testing.T) {
	v := newTestVFS(t, fakeBackend{entries: []entry{{Path: "dir/file.txt"}}})
	if got := v.PanelTitle(""); got != "MultiArc:fake:test.tar.gz" {
		t.Errorf("root PanelTitle = %q", got)
	}
	if got := v.PanelTitle("/dir"); got != "MultiArc:fake:test.tar.gz/dir" {
		t.Errorf("nested PanelTitle = %q", got)
	}
}
