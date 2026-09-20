package tarindexcache

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func withCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return root, nil }
	t.Cleanup(func() { userCacheDir = old })
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func put(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(Dir(), name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Base(f)
	}
	sort.Strings(out)
	return out
}

func TestFilesAreThoseOfTheArchiveOnly(t *testing.T) {
	withCache(t)
	archive := filepath.Join(t.TempDir(), "data.tar")
	prefix := Prefix(archive)
	put(t, prefix+"-aaaa.index.sqlite")
	put(t, prefix+"-aaaa.index.sqlite-wal")
	put(t, prefix+".index.sqlite") // the library's own older name
	// Not this archive's: another folder, and a longer name that starts alike.
	put(t, Prefix(filepath.Join(t.TempDir(), "data.tar"))+"-bbbb.index.sqlite")
	put(t, prefix+"x-cccc.index.sqlite")

	got := names(Files(archive))
	want := []string{prefix + "-aaaa.index.sqlite", prefix + "-aaaa.index.sqlite-wal", prefix + ".index.sqlite"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Files = %v, want %v", got, want)
		}
	}
}

func TestMoveKeepsTheIndexUnderTheNewName(t *testing.T) {
	withCache(t)
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "old.tar"), filepath.Join(dir, "sub", "new.tar")
	put(t, Prefix(oldPath)+"-ffff.index.sqlite")
	put(t, Prefix(oldPath)+"-ffff.index.sqlite-journal")

	Move(oldPath, newPath)

	if left := Files(oldPath); len(left) != 0 {
		t.Fatalf("the old name still has %v", names(left))
	}
	got := names(Files(newPath))
	want := []string{Prefix(newPath) + "-ffff.index.sqlite", Prefix(newPath) + "-ffff.index.sqlite-journal"}
	sort.Strings(want)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("under the new name: %v, want %v", got, want)
	}

	// A move onto itself changes nothing.
	Move(newPath, newPath)
	if len(Files(newPath)) != 2 {
		t.Fatal("moving onto the same name lost files")
	}
}

func TestFilesWithoutACacheFolder(t *testing.T) {
	root := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return filepath.Join(root, "missing"), nil }
	defer func() { userCacheDir = old }()
	if got := Files("/x/a.tar"); got != nil {
		t.Fatalf("Files = %v with no cache folder", got)
	}
	Move("/x/a.tar", "/x/b.tar") // must not panic
}
