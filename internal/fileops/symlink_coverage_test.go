package fileops

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/unxed/f4/vfs"
)

type symlinkCoverageVFS struct {
	vfs.VFS
	lstatItem   vfs.VFSItem
	lstatErr    error
	readlink    string
	readlinkErr error
	removeErr   error
	symlinkErr  error
	linkedTo    string
	linkedPath  string
}

func (v *symlinkCoverageVFS) Lstat(context.Context, string) (vfs.VFSItem, error) {
	return v.lstatItem, v.lstatErr
}

func (v *symlinkCoverageVFS) Readlink(context.Context, string) (string, error) {
	return v.readlink, v.readlinkErr
}

func (v *symlinkCoverageVFS) Remove(context.Context, string) error { return v.removeErr }

func (v *symlinkCoverageVFS) Symlink(_ context.Context, target, linkPath string) error {
	v.linkedTo, v.linkedPath = target, linkPath
	return v.symlinkErr
}

func newSymlinkCoverageVFS(t *testing.T) *symlinkCoverageVFS {
	t.Helper()
	return &symlinkCoverageVFS{VFS: vfs.NewOSVFS(t.TempDir()), lstatErr: os.ErrNotExist, readlink: "target.txt"}
}

func TestCopySymlinkAsLinkRequiresSourceLinks(t *testing.T) {
	src := &struct{ vfs.VFS }{VFS: vfs.NewOSVFS(t.TempDir())}
	dst := newSymlinkCoverageVFS(t)

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{}, vfs.VFSItem{})
	if handled || err != nil {
		t.Fatalf("copySymlinkAsLink without source links = (%v, %v), want (false, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkRequiresDestinationLinks(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := &struct{ vfs.VFS }{VFS: vfs.NewOSVFS(t.TempDir())}

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{}, vfs.VFSItem{})
	if handled || err != nil {
		t.Fatalf("copySymlinkAsLink without destination links = (%v, %v), want (false, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkRejectsUnknownTarget(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	src.readlink = ""
	dst := newSymlinkCoverageVFS(t)

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{}, vfs.VFSItem{})
	if handled || err != nil {
		t.Fatalf("copySymlinkAsLink with empty target = (%v, %v), want (false, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkPropagatesDestinationLookupFailure(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.lstatErr = errors.New("lookup failed")

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{}, vfs.VFSItem{})
	if !handled || !errors.Is(err, dst.lstatErr) {
		t.Fatalf("lookup failure = (%v, %v), want handled lookup error", handled, err)
	}
}

func TestCopySymlinkAsLinkIgnoresDestinationLookupFailure(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.lstatErr = errors.New("lookup failed")

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{IgnoreWriteErrors: true}, vfs.VFSItem{})
	if !handled || err != nil {
		t.Fatalf("ignored lookup failure = (%v, %v), want (true, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkRejectsDirectoryDestination(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.lstatItem = vfs.VFSItem{IsDir: true}
	dst.lstatErr = nil

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{}, vfs.VFSItem{})
	if !handled || err == nil {
		t.Fatalf("directory destination = (%v, %v), want handled error", handled, err)
	}
}

func TestCopySymlinkAsLinkSkipsWithSkipAll(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.lstatItem = vfs.VFSItem{Name: "existing"}
	dst.lstatErr = nil
	state := &FileOpState{SkipAll: true}

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", state, vfs.VFSItem{})
	if !handled || err != nil || state.SkippedCount != 1 {
		t.Fatalf("SkipAll = (%v, %v), skipped=%d; want (true, nil), skipped=1", handled, err, state.SkippedCount)
	}
}

func TestCopySymlinkAsLinkIgnoresRemoveFailure(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.lstatItem = vfs.VFSItem{Name: "existing"}
	dst.lstatErr = nil
	dst.removeErr = errors.New("remove failed")

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{OverwriteAll: true, IgnoreWriteErrors: true}, vfs.VFSItem{})
	if !handled || err != nil {
		t.Fatalf("ignored remove failure = (%v, %v), want (true, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkIgnoresCreateFailure(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	dst.symlinkErr = errors.New("create failed")

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", &FileOpState{IgnoreWriteErrors: true}, vfs.VFSItem{})
	if !handled || err != nil {
		t.Fatalf("ignored create failure = (%v, %v), want (true, nil)", handled, err)
	}
}

func TestCopySymlinkAsLinkCreatesRelativeTarget(t *testing.T) {
	src := newSymlinkCoverageVFS(t)
	dst := newSymlinkCoverageVFS(t)
	state := &FileOpState{OverwriteAll: true}

	handled, err := copySymlinkAsLink(context.Background(), src, "source", dst, "dest", state, vfs.VFSItem{})
	if !handled || err != nil {
		t.Fatalf("successful link copy = (%v, %v), want (true, nil)", handled, err)
	}
	if dst.linkedTo != "target.txt" || dst.linkedPath != "dest" {
		t.Fatalf("Symlink called with (%q, %q), want (%q, %q)", dst.linkedTo, dst.linkedPath, "target.txt", "dest")
	}
}
