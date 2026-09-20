package archive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/config"
)

// With the cache switched off the index of the last open is not trusted: it is
// removed before the next open, so the archive is indexed afresh each time.
// With it on, a saved index stays for the next open (#1187).
func TestTarIndexPathFollowsTheCacheOption(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)
	previous := config.App.ArchiveTarIndexCache
	t.Cleanup(func() { config.App.ArchiveTarIndexCache = previous })

	archive := filepath.Join(t.TempDir(), "data.tar")
	if err := os.WriteFile(archive, []byte("not really a tar, only its bytes matter"), 0o600); err != nil {
		t.Fatal(err)
	}
	leave := func() string {
		t.Helper()
		path := tarIndexPath(archive)
		if path == "" {
			t.Fatal("no index path for a tar archive")
		}
		if err := os.WriteFile(path, []byte("index"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	config.App.ArchiveTarIndexCache = true
	path := leave()
	if again := tarIndexPath(archive); again != path {
		t.Fatalf("the path changed between opens: %q, %q", path, again)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("with the cache on the saved index was removed: %v", err)
	}

	config.App.ArchiveTarIndexCache = false
	if off := tarIndexPath(archive); off != path {
		t.Fatalf("the path changed with the cache off: %q, %q", path, off)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("with the cache off the saved index stayed for the next open: %v", err)
	}
}
