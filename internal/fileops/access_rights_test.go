package fileops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/vfs"
)

// copyOnce runs one copy through the same code path the dialog uses, with the
// overwrite question already answered, because what is under test here is the
// permissions of the result and not the conflict prompt.
func copyOnce(t *testing.T, srcRoot, dstRoot, name string, rights AccessRightsMode) {
	t.Helper()
	srcVfs := vfs.NewOSVFS(srcRoot)
	dstVfs := vfs.NewOSVFS(dstRoot)
	state := &FileOpState{
		Buffer:       make([]byte, 32*1024),
		OverwriteAll: true,
		AccessRights: rights,
	}
	if err := recursiveCopy(context.Background(), srcVfs, filepath.Join(srcRoot, name), dstVfs, filepath.Join(dstRoot, name), state, 0); err != nil {
		t.Fatalf("copy %q with rights %d: %v", name, rights, err)
	}
}

func fileRights(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return st.Mode().Perm()
}

// TestCopyAccessRightsModes pins the three answers f4 gives to "what
// permissions does the copy get" (#722). Windows has no permission bits worth
// asserting on: Go's Chmod there only toggles the read-only attribute.
func TestCopyAccessRightsModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}

	t.Run("default gives a new copy the source rights", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFileWithRights(t, filepath.Join(src, "f.txt"), 0o700)

		copyOnce(t, src, dst, "f.txt", AccessRightsDefault)

		if got := fileRights(t, filepath.Join(dst, "f.txt")); got != 0o700 {
			t.Errorf("copy has rights %04o, want 0700 from the source", got)
		}
	})

	t.Run("default leaves an overwritten file its own rights", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFileWithRights(t, filepath.Join(src, "f.txt"), 0o700)
		writeFileWithRights(t, filepath.Join(dst, "f.txt"), 0o644)

		copyOnce(t, src, dst, "f.txt", AccessRightsDefault)

		if got := fileRights(t, filepath.Join(dst, "f.txt")); got != 0o644 {
			t.Errorf("overwritten file has rights %04o, want its own 0644", got)
		}
	})

	t.Run("copy forces the source rights onto an overwritten file", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFileWithRights(t, filepath.Join(src, "f.txt"), 0o700)
		writeFileWithRights(t, filepath.Join(dst, "f.txt"), 0o644)

		copyOnce(t, src, dst, "f.txt", AccessRightsCopy)

		if got := fileRights(t, filepath.Join(dst, "f.txt")); got != 0o700 {
			t.Errorf("overwritten file has rights %04o, want 0700 from the source", got)
		}
	})

	t.Run("inherit takes the destination folder rights", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFileWithRights(t, filepath.Join(src, "f.txt"), 0o777)
		if err := os.Chmod(dst, 0o750); err != nil {
			t.Fatal(err)
		}

		copyOnce(t, src, dst, "f.txt", AccessRightsInherit)

		// 0750 without the execute bits a folder needs only to be entered.
		if got := fileRights(t, filepath.Join(dst, "f.txt")); got != 0o640 {
			t.Errorf("copy has rights %04o, want 0640 inherited from the folder", got)
		}
	})

	t.Run("inherit reaches the whole copied tree", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		if err := os.Mkdir(filepath.Join(src, "tree"), 0o777); err != nil {
			t.Fatal(err)
		}
		writeFileWithRights(t, filepath.Join(src, "tree", "f.txt"), 0o777)
		if err := os.Chmod(filepath.Join(src, "tree"), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dst, 0o750); err != nil {
			t.Fatal(err)
		}

		copyOnce(t, src, dst, "tree", AccessRightsInherit)

		if got := fileRights(t, filepath.Join(dst, "tree")); got != 0o750 {
			t.Errorf("copied folder has rights %04o, want 0750 inherited from its parent", got)
		}
		if got := fileRights(t, filepath.Join(dst, "tree", "f.txt")); got != 0o640 {
			t.Errorf("file inside the copied folder has rights %04o, want 0640", got)
		}
	})
}

// TestAccessRightsModeFromConfig covers the two directions the stored number
// travels: a value the dialog wrote, and a value f4 does not know.
func TestAccessRightsModeFromConfig(t *testing.T) {
	for value, want := range map[int]AccessRightsMode{
		0:  AccessRightsDefault,
		1:  AccessRightsCopy,
		2:  AccessRightsInherit,
		7:  AccessRightsDefault,
		-1: AccessRightsDefault,
	} {
		if got := AccessRightsModeFromConfig(value); got != want {
			t.Errorf("AccessRightsModeFromConfig(%d) = %d, want %d", value, got, want)
		}
	}

	old := config.App.CopyAccessRights
	defer func() { config.App.CopyAccessRights = old }()
	config.App.CopyAccessRights = 2
	if got := DefaultFileOpOptions().AccessRights; got != AccessRightsInherit {
		t.Errorf("an operation started without the dialog uses %d, want the configured %d", got, AccessRightsInherit)
	}
}

func writeFileWithRights(t *testing.T, path string, rights os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("payload"), rights); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies the umask, and every expectation here is exact.
	if err := os.Chmod(path, rights); err != nil {
		t.Fatal(err)
	}
}
