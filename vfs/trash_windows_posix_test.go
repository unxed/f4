//go:build windows

package vfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	stdpath "path"
	"strings"
	"sync"
	"testing"
	"time"

	winescape "github.com/unxed/libwinescape/go"
)

// Regression coverage for f4#1461 (part 1 of 3): Wine's posix personality
// now shares vfs/trash_xdg.go's FreeDesktop.org Trash algorithm with a real
// Linux build (vfs/trash_windows.go's MoveToTrash), instead of the Win32
// Recycle Bin or nothing at all.
//
// These tests exercise that shared algorithm directly -- moveToFreedesktopTrash,
// moveIntoFreedesktopTrash, ensurePrivateTrashDir, prepareVolumeTrash -- the
// same functions trash_freedesktop_test.go exercises on a real POSIX build,
// but driven by an in-memory double instead of a real filesystem.
//
// A double, not a real Wine process, is deliberate: winescape.Getuid(),
// winescape.HostGetenv and every hostfs call in posix mode reach into
// libwinescape's raw syscall trampoline (trampoline_windows_amd64.go),
// which assumes it is running inside an actual Wine process. This test
// binary runs on a plain windows-latest GitHub Actions runner with no Wine
// underneath it (see build.yml's OS matrix); calling any of those for real
// here would not exercise "Wine's posix personality" -- there is no Wine --
// it would just be an unchecked raw syscall against whatever the trampoline
// address happens to hold on a real Windows kernel, which is exactly the
// kind of call vfs/hostmode.Posix's own doc comment says must only happen
// once IsWine()/Available() have already confirmed a real Wine process.
// The double stands in for that host environment instead, the same way a
// unit test doubles any other external dependency.
func TestWinePosixTrash(t *testing.T) {
	t.Run("WritesInfoAndKeepsCollisions", testWinePosixTrashWritesInfoAndKeepsCollisions)
	t.Run("CancellationIsNonDestructive", testWinePosixTrashCancellationIsNonDestructive)
	t.Run("RejectsItsOwnAncestor", testWinePosixTrashRejectsItsOwnAncestor)
	t.Run("RepairsBroadPermissions", testWinePosixTrashRepairsBroadPermissions)
	t.Run("RejectsSymlink", testWinePosixTrashRejectsSymlink)
	t.Run("SelectsVolumeTrashOnDifferentDevice", testWinePosixTrashSelectsVolumeTrashOnDifferentDevice)
	t.Run("SelectsHomeTrashOnSameDevice", testWinePosixTrashSelectsHomeTrashOnSameDevice)
}

func testWinePosixTrashWritesInfoAndKeepsCollisions(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)

	trash := freedesktopTrash{
		root:          "/home/user/.local/share/Trash",
		files:         "/home/user/.local/share/Trash/files",
		info:          "/home/user/.local/share/Trash/info",
		absolutePaths: true,
	}
	fake.seedDir(trash.root, 1, 1000, privateTrashDirMode)
	if err := ensureTrashSubdirs(trash); err != nil {
		t.Fatal(err)
	}

	srcA := "/home/user/a/same name.txt"
	srcB := "/home/user/b/same name.txt"
	for _, path := range []string{srcA, srcB} {
		fake.seedFile(path, 1, 1000, 0600, []byte(path))
		if err := moveIntoFreedesktopTrash(context.Background(), path, trash); err != nil {
			t.Fatal(err)
		}
		if fake.exists(path) {
			t.Fatalf("source still exists after trash move: %s", path)
		}
	}

	for _, name := range []string{"same name.txt", "same name.txt.1"} {
		if !fake.exists(stdpath.Join(trash.files, name)) {
			t.Errorf("missing trashed payload %q", name)
		}
		body, err := fake.readFile(stdpath.Join(trash.info, name+".trashinfo"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.HasPrefix(text, "[Trash Info]\nPath=/") || strings.Contains(text, "%2F") || !strings.Contains(text, "%20") || !strings.Contains(text, "\nDeletionDate=") {
			t.Errorf("invalid .trashinfo for %q:\n%s", name, text)
		}
	}
}

func testWinePosixTrashCancellationIsNonDestructive(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)

	trash := freedesktopTrash{
		root:          "/home/user/.local/share/Trash",
		files:         "/home/user/.local/share/Trash/files",
		info:          "/home/user/.local/share/Trash/info",
		absolutePaths: true,
	}
	fake.seedDir(trash.root, 1, 1000, privateTrashDirMode)
	if err := ensureTrashSubdirs(trash); err != nil {
		t.Fatal(err)
	}
	source := "/home/user/keep.txt"
	fake.seedFile(source, 1, 1000, 0600, []byte("keep"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := moveIntoFreedesktopTrash(ctx, source, trash); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !fake.exists(source) {
		t.Fatal("canceled trash move changed source")
	}
	if left := fake.listUnder(trash.info); len(left) != 0 {
		t.Fatalf("canceled trash move left metadata: %v", left)
	}
}

func testWinePosixTrashRejectsItsOwnAncestor(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)

	source := "/home/user/data"
	fake.seedDir(source, 1, 1000, 0755)
	trash := freedesktopTrash{
		root:          "/home/user/data/Trash",
		files:         "/home/user/data/Trash/files",
		info:          "/home/user/data/Trash/info",
		absolutePaths: true,
	}
	fake.seedDir(trash.root, 1, 1000, privateTrashDirMode)
	if err := ensureTrashSubdirs(trash); err != nil {
		t.Fatal(err)
	}
	if err := moveIntoFreedesktopTrash(context.Background(), source, trash); err == nil {
		t.Fatal("trash ancestor was moved into itself")
	}
	if !fake.exists(source) {
		t.Fatal("rejecting trash ancestor changed source")
	}
}

func testWinePosixTrashRepairsBroadPermissions(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)

	path := "/home/user/.local/share/Trash"
	fake.seedDir(path, 1, 1000, 0775)
	if err := ensurePrivateTrashDir(path); err != nil {
		t.Fatal(err)
	}
	info, err := fake.lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != privateTrashDirMode {
		t.Fatalf("repaired trash directory mode = %#o, want %#o", got, privateTrashDirMode)
	}
}

func testWinePosixTrashRejectsSymlink(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)

	fake.seedDir("/home/user/target", 1, 1000, privateTrashDirMode)
	fake.seedSymlink("/home/user/Trash", 1, 1000, privateTrashDirMode)
	if err := ensurePrivateTrashDir("/home/user/Trash"); err == nil {
		t.Fatal("symlink trash directory was accepted")
	}
}

// testWinePosixTrashSelectsVolumeTrashOnDifferentDevice is the one genuinely
// new thing this PR adds versus the pre-split code: a home trash and a
// source item that live on different real POSIX devices (the fake's dev
// numbers stand in for winescape.Stat_t.Dev/*syscall.Stat_t.Dev, exactly
// what trashStatIdentity extracts in production) must fall back to a volume
// trash under the source's own mount root, not silently use the home trash.
func testWinePosixTrashSelectsVolumeTrashOnDifferentDevice(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)
	trashHomeEnv = func(key string) string {
		if key == "XDG_DATA_HOME" {
			return "/home/user/.local/share"
		}
		return ""
	}
	trashHomeDir = func() (string, error) { return "/home/user", nil }

	fake.seedDir("/home/user/.local/share/Trash", 1, 1000, privateTrashDirMode)
	fake.seedDir("/mnt", 1, 1000, 0755)
	fake.seedDir("/mnt/data", 2, 1000, 0755)
	fake.seedFile("/mnt/data/gone.txt", 2, 1000, 0600, []byte("bye"))

	if err := moveToFreedesktopTrash(context.Background(), "/mnt/data/gone.txt"); err != nil {
		t.Fatal(err)
	}
	if fake.exists("/mnt/data/gone.txt") {
		t.Fatal("source still exists after MoveToTrash")
	}
	if !fake.exists("/mnt/data/.Trash-1000/files/gone.txt") {
		t.Fatal("expected the item in a volume trash under the source's own mount root")
	}
	if fake.exists("/home/user/.local/share/Trash/files/gone.txt") {
		t.Fatal("item ended up in the home trash despite living on a different device")
	}
}

func testWinePosixTrashSelectsHomeTrashOnSameDevice(t *testing.T) {
	fake := newWinePosixFakeFS()
	installWinePosixFakeFS(t, fake)
	trashHomeEnv = func(string) string { return "" }
	trashHomeDir = func() (string, error) { return "/home/user", nil }

	fake.seedDir("/home/user/.local/share/Trash", 1, 1000, privateTrashDirMode)
	fake.seedFile("/home/user/docs/gone.txt", 1, 1000, 0600, []byte("bye"))

	if err := moveToFreedesktopTrash(context.Background(), "/home/user/docs/gone.txt"); err != nil {
		t.Fatal(err)
	}
	if fake.exists("/home/user/docs/gone.txt") {
		t.Fatal("source still exists after MoveToTrash")
	}
	if !fake.exists("/home/user/.local/share/Trash/files/gone.txt") {
		t.Fatal("expected the item in the home trash")
	}
}

// --- the double itself -----------------------------------------------------

type winePosixFakeEntry struct {
	mode os.FileMode // ModeDir/ModeSymlink bits plus Perm() bits, POSIX semantics
	dev  uint64
	uid  uint32
	data []byte
}

// winePosixFakeFS is a minimal in-memory stand-in for the host POSIX
// filesystem libwinescape/hostfs would otherwise reach. It tracks real
// POSIX permission/ownership/device semantics itself rather than asking a
// real OS for them, because a genuinely native (non-Wine) Windows
// filesystem cannot represent those semantics at all -- see the package
// doc comment above for why that rules out testing this through a real
// directory on the test runner's disk.
type winePosixFakeFS struct {
	mu      sync.Mutex
	entries map[string]*winePosixFakeEntry
}

func newWinePosixFakeFS() *winePosixFakeFS {
	return &winePosixFakeFS{
		entries: map[string]*winePosixFakeEntry{
			"/": {mode: os.ModeDir | 0755, dev: 1, uid: 1000},
		},
	}
}

func (f *winePosixFakeFS) seedDir(path string, dev uint64, uid uint32, perm os.FileMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[stdpath.Clean(path)] = &winePosixFakeEntry{mode: os.ModeDir | perm.Perm(), dev: dev, uid: uid}
}

func (f *winePosixFakeFS) seedSymlink(path string, dev uint64, uid uint32, perm os.FileMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[stdpath.Clean(path)] = &winePosixFakeEntry{mode: os.ModeSymlink | perm.Perm(), dev: dev, uid: uid}
}

func (f *winePosixFakeFS) seedFile(path string, dev uint64, uid uint32, perm os.FileMode, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	buf := append([]byte(nil), data...)
	f.entries[stdpath.Clean(path)] = &winePosixFakeEntry{mode: perm.Perm(), dev: dev, uid: uid, data: buf}
}

func (f *winePosixFakeFS) exists(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.entries[stdpath.Clean(path)]
	return ok
}

func (f *winePosixFakeFS) readFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[stdpath.Clean(path)]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: path, Err: os.ErrNotExist}
	}
	return append([]byte(nil), e.data...), nil
}

func (f *winePosixFakeFS) listUnder(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := stdpath.Clean(prefix) + "/"
	var out []string
	for k := range f.entries {
		if strings.HasPrefix(k, p) {
			out = append(out, k)
		}
	}
	return out
}

// lstat matches vfs/trash_xdg.go's trashLstat substitution point.
func (f *winePosixFakeFS) lstat(path string) (os.FileInfo, error) {
	f.mu.Lock()
	e, ok := f.entries[stdpath.Clean(path)]
	f.mu.Unlock()
	if !ok {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
	}
	return &winePosixFakeInfo{name: stdpath.Base(path), entry: e}, nil
}

// mkdirAll matches trashMkdirAll. Like os.MkdirAll, it leaves an existing
// directory (or, so a subsequent Lstat-based check can still catch it, an
// existing symlink) untouched rather than erroring or resetting its mode.
func (f *winePosixFakeFS) mkdirAll(path string, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mkdirAllLocked(stdpath.Clean(path), perm)
}

func (f *winePosixFakeFS) mkdirAllLocked(path string, perm os.FileMode) error {
	if e, ok := f.entries[path]; ok {
		if !e.mode.IsDir() && e.mode&os.ModeSymlink == 0 {
			return &fs.PathError{Op: "mkdir", Path: path, Err: errors.New("not a directory")}
		}
		return nil
	}
	dev, uid := uint64(1), uint32(1000)
	parent := stdpath.Dir(path)
	if parent != path {
		if err := f.mkdirAllLocked(parent, perm); err != nil {
			return err
		}
		dev, uid = f.entries[parent].dev, f.entries[parent].uid
	}
	f.entries[path] = &winePosixFakeEntry{mode: os.ModeDir | perm.Perm(), dev: dev, uid: uid}
	return nil
}

// chmod matches trashChmod.
func (f *winePosixFakeFS) chmod(path string, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[stdpath.Clean(path)]
	if !ok {
		return &fs.PathError{Op: "chmod", Path: path, Err: os.ErrNotExist}
	}
	e.mode = (e.mode &^ os.ModePerm) | mode.Perm()
	return nil
}

// remove matches trashRemove.
func (f *winePosixFakeFS) remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := stdpath.Clean(path)
	if _, ok := f.entries[p]; !ok {
		return &fs.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	delete(f.entries, p)
	return nil
}

// writeExclusive matches trashWriteExclusive.
func (f *winePosixFakeFS) writeExclusive(path string, data []byte, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := stdpath.Clean(path)
	if _, ok := f.entries[p]; ok {
		return &fs.PathError{Op: "open", Path: path, Err: os.ErrExist}
	}
	dev, uid := uint64(1), uint32(1000)
	if e, ok := f.entries[stdpath.Dir(p)]; ok {
		dev, uid = e.dev, e.uid
	}
	buf := append([]byte(nil), data...)
	f.entries[p] = &winePosixFakeEntry{mode: mode.Perm(), dev: dev, uid: uid, data: buf}
	return nil
}

// renameNoReplace matches trashRenameNoReplace (the vfs package's own
// renameNoReplace, which is already personality-safe in production -- see
// rename_noreplace_windows.go).
func (f *winePosixFakeFS) renameNoReplace(oldPath, newPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	oldp, newp := stdpath.Clean(oldPath), stdpath.Clean(newPath)
	e, ok := f.entries[oldp]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldPath, Err: os.ErrNotExist}
	}
	if _, exists := f.entries[newp]; exists {
		return ErrDestinationExists
	}
	delete(f.entries, oldp)
	f.entries[newp] = e
	return nil
}

// winePosixFakeInfo implements os.FileInfo. Its Sys() returns a real
// *winescape.Stat_t literal (safe to construct: it is plain data, no live
// syscall) so that trash_windows.go's own, unmodified trashStatIdentity is
// what this test exercises -- not a stand-in for it.
type winePosixFakeInfo struct {
	name  string
	entry *winePosixFakeEntry
}

func (i *winePosixFakeInfo) Name() string       { return i.name }
func (i *winePosixFakeInfo) Size() int64        { return int64(len(i.entry.data)) }
func (i *winePosixFakeInfo) Mode() os.FileMode  { return i.entry.mode }
func (i *winePosixFakeInfo) ModTime() time.Time { return time.Time{} }
func (i *winePosixFakeInfo) IsDir() bool        { return i.entry.mode.IsDir() }
func (i *winePosixFakeInfo) Sys() interface{} {
	return &winescape.Stat_t{Dev: i.entry.dev, Uid: i.entry.uid, Mode: uint32(i.entry.mode.Perm())}
}

// installWinePosixFakeFS points every vfs/trash_xdg.go substitution point at
// fake and restores the real defaults on test cleanup.
func installWinePosixFakeFS(t *testing.T, fake *winePosixFakeFS) {
	t.Helper()
	origLstat, origMkdirAll, origChmod, origRemove, origRename := trashLstat, trashMkdirAll, trashChmod, trashRemove, trashRenameNoReplace
	origGetuid, origHomeEnv, origHomeDir, origWrite := trashGetuid, trashHomeEnv, trashHomeDir, trashWriteExclusive

	trashLstat = fake.lstat
	trashMkdirAll = fake.mkdirAll
	trashChmod = fake.chmod
	trashRemove = fake.remove
	trashRenameNoReplace = fake.renameNoReplace
	trashWriteExclusive = fake.writeExclusive
	trashGetuid = func() int { return 1000 }

	t.Cleanup(func() {
		trashLstat, trashMkdirAll, trashChmod, trashRemove, trashRenameNoReplace = origLstat, origMkdirAll, origChmod, origRemove, origRename
		trashGetuid, trashHomeEnv, trashHomeDir, trashWriteExclusive = origGetuid, origHomeEnv, origHomeDir, origWrite
	})
}
