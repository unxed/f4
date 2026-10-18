package netfox

import (
	"errors"
	"testing"

	"github.com/unxed/f4/vfs"
)

type startPathTestVFS struct {
	vfs.VFS
	path       string
	setPathErr error
}

func (v *startPathTestVFS) SetPath(p string) error {
	if v.setPathErr != nil {
		return v.setPathErr
	}
	v.path = p
	return nil
}

func TestEnterStartPath(t *testing.T) {
	t.Run("empty keeps server default", func(t *testing.T) {
		fs := &startPathTestVFS{path: "/home/user"}
		if got := enterStartPath(fs, ""); got != fs || fs.path != "/home/user" {
			t.Fatalf("enterStartPath() = %v, path = %q", got, fs.path)
		}
	})

	t.Run("enters configured folder", func(t *testing.T) {
		fs := &startPathTestVFS{path: "/home/user"}
		if got := enterStartPath(fs, "/shared/work"); got != fs || fs.path != "/shared/work" {
			t.Fatalf("enterStartPath() = %v, path = %q", got, fs.path)
		}
	})

	t.Run("invalid folder leaves server default", func(t *testing.T) {
		fs := &startPathTestVFS{path: "/home/user", setPathErr: errors.New("missing")}
		if got := enterStartPath(fs, "/missing"); got != fs || fs.path != "/home/user" {
			t.Fatalf("enterStartPath() = %v, path = %q", got, fs.path)
		}
	})
}
