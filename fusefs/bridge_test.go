package fusefs

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

// fakeVFS is an in-memory tree with the two properties that matter here:
// it counts how often it is asked something, and it can pretend to have no
// random access, which is the case the spooling path exists for.
type fakeVFS struct {
	files    map[string]string
	dirs     map[string]bool
	random   bool
	readDirs int
	stats    int
	opens    int
	closed   bool
	statErr  bool
}

func newFakeVFS(random bool) *fakeVFS {
	return &fakeVFS{
		files: map[string]string{
			"/root/a.txt":       "hello",
			"/root/b.bin":       strings.Repeat("x", 700*1024),
			"/root/sub/c.txt":   "nested",
			"/root/sub/d/e.txt": "deep",
		},
		dirs: map[string]bool{
			"/root":       true,
			"/root/sub":   true,
			"/root/sub/d": true,
		},
		random: random,
	}
}

func (f *fakeVFS) IsAtRoot() bool          { return false }
func (f *fakeVFS) GetPath() string         { return "/root" }
func (f *fakeVFS) IsAbs(p string) bool     { return path.IsAbs(p) }
func (f *fakeVFS) SetPath(p string) error  { return nil }
func (f *fakeVFS) Join(e ...string) string { return path.Join(e...) }
func (f *fakeVFS) Abs(p string) (string, error) {
	return p, nil
}
func (f *fakeVFS) Base(p string) string { return path.Base(p) }
func (f *fakeVFS) Dir(p string) string  { return path.Dir(p) }

func (f *fakeVFS) ReadDir(ctx context.Context, dir string, onChunk func([]vfs.VFSItem)) error {
	f.readDirs++
	if !f.dirs[dir] {
		return os.ErrNotExist
	}
	onChunk([]vfs.VFSItem{{Name: "."}, {Name: ".."}})
	var chunk []vfs.VFSItem
	for p := range f.files {
		if path.Dir(p) == dir {
			chunk = append(chunk, vfs.VFSItem{Name: path.Base(p), Size: int64(len(f.files[p]))})
		}
	}
	for p := range f.dirs {
		if p != dir && path.Dir(p) == dir {
			chunk = append(chunk, vfs.VFSItem{Name: path.Base(p), IsDir: true})
		}
	}
	onChunk(chunk)
	return nil
}

func (f *fakeVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	f.stats++
	if f.statErr {
		return vfs.VFSItem{}, errors.New("stat is not supported here")
	}
	if f.dirs[p] {
		return vfs.VFSItem{Name: path.Base(p), IsDir: true}, nil
	}
	if data, ok := f.files[p]; ok {
		return vfs.VFSItem{Name: path.Base(p), Size: int64(len(data)), MTime: time.Unix(1000, 0)}, nil
	}
	return vfs.VFSItem{}, os.ErrNotExist
}

func (f *fakeVFS) MkDir(ctx context.Context, p string) error           { return os.ErrPermission }
func (f *fakeVFS) Remove(ctx context.Context, p string) error          { return os.ErrPermission }
func (f *fakeVFS) Rename(ctx context.Context, oldp, newp string) error { return os.ErrPermission }
func (f *fakeVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: f.random}
}
func (f *fakeVFS) Search(ctx context.Context, p, pat string) (chan int64, error) {
	return nil, nil
}

func (f *fakeVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	f.opens++
	data, ok := f.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &fakeReader{data: []byte(data)}, nil
}

func (f *fakeVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	return nil, os.ErrPermission
}
func (f *fakeVFS) SetAttributes(ctx context.Context, p string, item vfs.VFSItem) error {
	return os.ErrPermission
}
func (f *fakeVFS) ParentVFS() vfs.VFS { return nil }
func (f *fakeVFS) Clone() vfs.VFS     { return f }
func (f *fakeVFS) Close() error       { f.closed = true; return nil }

type fakeReader struct {
	data []byte
	pos  int
	shut bool
}

func (r *fakeReader) Size() int64 { return int64(len(r.data)) }
func (r *fakeReader) Close() error {
	r.shut = true
	return nil
}

func (r *fakeReader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	return n, nil
}

func (r *fakeReader) Read(ctx context.Context, p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func newTestBridge(t *testing.T, random bool) (*bridge, *fakeVFS) {
	t.Helper()
	fake := newFakeVFS(random)
	b := newBridge(fake, "/root", Options{})
	t.Cleanup(b.close)
	return b, fake
}

func TestReadDirDropsDotEntries(t *testing.T) {
	b, _ := newTestBridge(t, true)
	items, err := b.readDir(context.Background(), "/root")
	if err != nil {
		t.Fatalf("readDir: %v", err)
	}
	names := map[string]bool{}
	for _, item := range items {
		names[item.Name] = true
	}
	if names["."] || names[".."] {
		t.Fatalf("dot entries must not reach the kernel: %v", names)
	}
	for _, want := range []string{"a.txt", "b.bin", "sub"} {
		if !names[want] {
			t.Fatalf("missing %q in %v", want, names)
		}
	}
}

func TestLookupUsesCachedListing(t *testing.T) {
	b, fake := newTestBridge(t, true)
	if _, err := b.readDir(context.Background(), "/root"); err != nil {
		t.Fatalf("readDir: %v", err)
	}
	statsBefore := fake.stats
	item, err := b.lookup(context.Background(), "/root", "a.txt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if item.Size != 5 {
		t.Fatalf("size = %d, want 5", item.Size)
	}
	if fake.stats != statsBefore {
		t.Fatalf("lookup asked the backend %d extra times, the listing was fresh", fake.stats-statsBefore)
	}
	if _, err := b.lookup(context.Background(), "/root", "nope.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing name error = %v, want ErrNotExist", err)
	}
}

func TestLookupFallsBackToListingWhenStatFails(t *testing.T) {
	b, fake := newTestBridge(t, true)
	fake.statErr = true
	item, err := b.lookup(context.Background(), "/root", "sub")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !item.IsDir {
		t.Fatalf("sub should be a directory")
	}
}

func TestDirCacheExpires(t *testing.T) {
	fake := newFakeVFS(true)
	b := newBridge(fake, "/root", Options{DirCacheTTL: time.Millisecond})
	t.Cleanup(b.close)

	ctx := context.Background()
	if _, err := b.readDir(ctx, "/root"); err != nil {
		t.Fatalf("readDir: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := b.readDir(ctx, "/root"); err != nil {
		t.Fatalf("readDir: %v", err)
	}
	if fake.readDirs != 2 {
		t.Fatalf("backend was asked %d times, want 2 after expiry", fake.readDirs)
	}
}

func TestStatRootSurvivesUnstattableBackend(t *testing.T) {
	b, fake := newTestBridge(t, true)
	fake.statErr = true
	item, err := b.stat(context.Background(), "/root")
	if err != nil {
		t.Fatalf("root stat: %v", err)
	}
	if !item.IsDir {
		t.Fatalf("mount root must be reported as a directory")
	}
	if _, err := b.stat(context.Background(), "/root/a.txt"); err == nil {
		t.Fatalf("a failing stat below the root must stay an error")
	}
}

func TestReadRandomAccess(t *testing.T) {
	b, _ := newTestBridge(t, true)
	h, err := b.open(context.Background(), "/root/a.txt", 5)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer h.release()

	buf := make([]byte, 3)
	n, err := h.readAt(context.Background(), buf, 2)
	if err != nil {
		t.Fatalf("readAt: %v", err)
	}
	if got := string(buf[:n]); got != "llo" {
		t.Fatalf("read %q, want \"llo\"", got)
	}
	if n, err := h.readAt(context.Background(), buf, 99); err != nil || n != 0 {
		t.Fatalf("read past the end = (%d, %v), want (0, nil)", n, err)
	}
}

func TestSequentialBackendIsSpooled(t *testing.T) {
	b, _ := newTestBridge(t, false)
	h, err := b.open(context.Background(), "/root/b.bin", 700*1024)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer h.release()

	if h.tmp == nil {
		t.Fatalf("a backend without random access must be spooled")
	}
	if h.r != nil {
		t.Fatalf("the source must be released once it has been spooled")
	}
	if h.size != 700*1024 {
		t.Fatalf("size = %d, want %d", h.size, 700*1024)
	}

	buf := make([]byte, 4)
	if _, err := h.readAt(context.Background(), buf, 700*1024-4); err != nil {
		t.Fatalf("tail read: %v", err)
	}
	if string(buf) != "xxxx" {
		t.Fatalf("tail = %q", buf)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	b, _ := newTestBridge(t, true)
	h, err := b.open(context.Background(), "/root/a.txt", 5)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	h.release()
	h.release()
	if _, err := h.readAt(context.Background(), make([]byte, 1), 0); !errors.Is(err, errClosed) {
		t.Fatalf("read after release = %v, want errClosed", err)
	}
}

func TestCloseReleasesTheBackend(t *testing.T) {
	fake := newFakeVFS(true)
	b := newBridge(fake, "/root", Options{})
	b.close()
	b.close()
	if !fake.closed {
		t.Fatalf("closing a mount must close the VFS it owns")
	}
	if _, err := b.readDir(context.Background(), "/root"); !errors.Is(err, errClosed) {
		t.Fatalf("readDir after close = %v, want errClosed", err)
	}
}

func TestInodeOfIsStableAndReserved(t *testing.T) {
	if inodeOf("/root/a") != inodeOf("/root/a") {
		t.Fatalf("inode numbers must be stable for a path")
	}
	if inodeOf("/root/a") == inodeOf("/root/b") {
		t.Fatalf("different paths should not collide this easily")
	}
	for _, p := range []string{"", "/", "/root", "x"} {
		switch inodeOf(p) {
		case 0, 1, ^uint64(0):
			t.Fatalf("inodeOf(%q) returned a reserved number", p)
		}
	}
}

func TestUnixMode(t *testing.T) {
	if got := unixMode(vfs.VFSItem{UnixMode: 0o640}); got != 0o640 {
		t.Fatalf("real permissions must win, got %o", got)
	}
	if got := unixMode(vfs.VFSItem{IsDir: true}); got != 0o555 {
		t.Fatalf("directory default = %o", got)
	}
	if got := unixMode(vfs.VFSItem{}); got != 0o444 {
		t.Fatalf("file default = %o", got)
	}
	if got := unixMode(vfs.VFSItem{IsExecutable: true}); got != 0o555 {
		t.Fatalf("executable default = %o", got)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"sftp://user@host/srv/backups": "host-backups",
		"sftp://user@host":             "host",
		"ftp://host/":                  "host",
		"/home/u/photos.zip":           "photos.zip",
		"":                             "",
		"/":                            "",
		strings.Repeat("a", 200):       strings.Repeat("a", 64),
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Fatalf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnsureMountPointRefusesNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if created, err := ensureMountPoint(dir); err != nil || created {
		t.Fatalf("empty existing directory: created=%v err=%v", created, err)
	}

	if err := os.WriteFile(path.Join(dir, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ensureMountPoint(dir); err == nil {
		t.Fatalf("mounting over existing files must be refused")
	}

	fresh := path.Join(dir, "sub", "point")
	created, err := ensureMountPoint(fresh)
	if err != nil || !created {
		t.Fatalf("new directory: created=%v err=%v", created, err)
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"a.txt":      "a.txt",
		"sub/a.txt":  "a.txt",
		`sub\a.txt`:  "a.txt",
		"dir/":       "dir",
		"/root/sub/": "sub",
	}
	for in, want := range cases {
		if got := displayName(in); got != want {
			t.Fatalf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnsupportedPlatformsStillAnswer(t *testing.T) {
	if Supported() {
		return
	}
	if _, err := MountVFS(context.Background(), newFakeVFS(true), Options{MountPoint: t.TempDir()}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("MountVFS on an unsupported platform = %v, want ErrUnsupported", err)
	}
	if got := List(); len(got) != 0 {
		t.Fatalf("no mounts can exist here, got %d", len(got))
	}
}
func TestMountAndUnmount(t *testing.T) {
	if !Supported() {
		// Use a valid source path so it passes os.Stat and reaches MountVFS
		_, err := MountSource(t.TempDir(), Options{MountPoint: t.TempDir()})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("expected ErrUnsupported, got %v", err)
		}
		return
	}

	tmpDir := t.TempDir()
	mountPoint := filepath.Join(t.TempDir(), "mnt")

	m, err := MountSource(tmpDir, Options{MountPoint: mountPoint, ReadOnly: true})
	if err != nil {
		// FUSE mounts can fail in environments without /dev/fuse access (like CI)
		t.Skipf("Mount failed (missing FUSE privileges?): %v", err)
	}
	defer m.Unmount()

	if m.MountPoint != mountPoint {
		t.Errorf("expected mount point %s, got %s", mountPoint, m.MountPoint)
	}

	found := Find(mountPoint)
	if found == nil || found.ID != m.ID {
		t.Errorf("Find failed to locate the mount")
	}

	if err := Unmount(mountPoint); err != nil {
		t.Fatalf("Unmount failed: %v", err)
	}

	if m.Active() {
		t.Errorf("expected mount to be inactive after Unmount")
	}
}
