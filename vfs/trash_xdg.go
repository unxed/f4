//go:build linux || dragonfly || freebsd || netbsd || openbsd || solaris || illumos || windows

// The FreeDesktop.org Trash specification, shared between two callers:
// trash_freedesktop.go (a real POSIX build) and trash_windows.go (Wine's
// posix personality only -- native Windows trash still goes through the
// Win32 Recycle Bin in trash_windows.go, unchanged). Everything below is
// personality-agnostic on purpose: no "syscall" import, no direct
// winescape.* call, only the small substitution-point set declared below --
// the same "what to do" vs "how to talk to the OS" split vfs/hostfs and
// vfs/hostpath already use for the rest of the file layer (WINE.md §13,
// Part E; WINE.md §18.2, "Корзина", is the gap this file closes).
//
// The build tag above is deliberately not "!windows": darwin has its own
// native NSWorkspace trash (trash_darwin.go) and was never part of this
// spec, and the remaining GOOS values f4 builds for (hurd, redox, nuttx,
// wasm, …) have no trash implementation of any kind. This file's tag is
// exactly the union of its two callers' own tags, nothing wider.
package vfs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	stdpath "path"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/vfs/hostfs"
)

const privateTrashDirMode = 0700

// Substitution points (WINE.md §13 pattern). hostfs.Lstat/MkdirAll/Chmod/
// Remove already have the exact same signature on every platform this file
// builds for (a direct os.* forward on a real POSIX build, a
// hostmode.Posix()-branching call on Windows), so they need no per-platform
// default here -- unlike vfs/hostfs and vfs/hostpath's own operations,
// nothing below them changes. renameNoReplace is the existing move
// primitive (rename_noreplace_*.go) and is already personality-safe on its
// own: its Windows build already branches on hostmode.Posix() internally.
//
// getuid, the two host-environment lookups, and "create this file
// exclusively" have no such existing primitive -- that gap is genuinely new
// (WINE.md §18.2 called it out by name) and each gets exactly one default
// per platform file, assigned below, mirroring hostfs/hostpath's own
// pattern. trashStatIdentity (declared once in trash_freedesktop.go with
// *syscall.Stat_t, once in trash_windows.go with *winescape.Stat_t) is the
// one substitution point that is a plain function rather than a var: it
// never calls anything unsafe outside a real Wine process, so a test can
// exercise the real implementation directly with a literal Stat_t value
// instead of needing a double for it.
//
// Every var below defaults to the real, unchanged primitive; nothing except
// a windows-only test (trash_windows_posix_test.go) ever reassigns them,
// and it always restores the original before returning. Reassigning
// trashGetuid/trashHomeEnv/trashHomeDir/trashWriteExclusive (or the four
// hostfs-backed vars) to point at the real winescape/libwinescape calls
// outside a genuine Wine process is not safe -- see hostmode.Posix's own
// doc comment -- which is exactly why a test double, not a live call, is
// what exercises this file's logic on a windows/amd64 test runner that has
// no Wine underneath it.
var (
	trashLstat           = hostfs.Lstat
	trashMkdirAll        = hostfs.MkdirAll
	trashChmod           = hostfs.Chmod
	trashRemove          = hostfs.Remove
	trashRenameNoReplace = renameNoReplace

	trashGetuid         = defaultTrashGetuid
	trashHomeEnv        = defaultTrashHomeEnv
	trashHomeDir        = defaultTrashHomeDir
	trashWriteExclusive = defaultTrashWriteExclusive
)

type freedesktopTrash struct {
	root          string
	files         string
	info          string
	pathBase      string
	absolutePaths bool
}

// moveToFreedesktopTrash implements the FreeDesktop.org Trash specification.
// It never invokes sudo and never falls back to a cross-device copy or
// Remove.
func moveToFreedesktopTrash(ctx context.Context, source string) error {
	source = posixAbsClean(source)
	sourceInfo, err := trashLstat(source)
	if err != nil {
		return err
	}
	sourceDev, _, ok := trashStatIdentity(sourceInfo)
	if !ok {
		return fmt.Errorf("cannot determine source filesystem for Recycle Bin: %s", source)
	}

	var homeTrash freedesktopTrash
	homeRoot, homeErr := freedesktopHomeTrashRoot()
	if homeErr == nil {
		if isPathWithin(homeRoot, source) {
			homeErr = fmt.Errorf("home Recycle Bin is inside the selected item")
		} else {
			homeTrash, homeErr = prepareHomeTrashAt(homeRoot)
		}
	}
	if homeErr == nil {
		if info, statErr := trashLstat(homeTrash.root); statErr == nil {
			if homeDev, _, statOK := trashStatIdentity(info); statOK && homeDev == sourceDev {
				return moveIntoFreedesktopTrash(ctx, source, homeTrash)
			}
		}
	}

	mountRoot, err := filesystemMountRoot(source, sourceDev)
	if err != nil {
		return err
	}
	if source == mountRoot {
		return fmt.Errorf("cannot move a filesystem mount root to Recycle Bin: %s", source)
	}
	volumeTrash, err := prepareVolumeTrash(mountRoot)
	if err != nil {
		if homeErr != nil {
			return fmt.Errorf("home Recycle Bin unavailable (%v); volume Recycle Bin unavailable: %w", homeErr, err)
		}
		return err
	}
	return moveIntoFreedesktopTrash(ctx, source, volumeTrash)
}

func freedesktopHomeTrashRoot() (string, error) {
	dataHome := trashHomeEnv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := trashHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = stdpath.Join(home, ".local", "share")
	}
	if !stdpath.IsAbs(dataHome) {
		return "", fmt.Errorf("trash data home is not absolute: %s", dataHome)
	}
	return stdpath.Join(dataHome, "Trash"), nil
}

func prepareHomeTrashAt(root string) (freedesktopTrash, error) {
	if err := ensurePrivateTrashDir(root); err != nil {
		return freedesktopTrash{}, err
	}
	t := freedesktopTrash{root: root, files: stdpath.Join(root, "files"), info: stdpath.Join(root, "info"), absolutePaths: true}
	if err := ensureTrashSubdirs(t); err != nil {
		return freedesktopTrash{}, err
	}
	return t, nil
}

func prepareVolumeTrash(mountRoot string) (freedesktopTrash, error) {
	uid := strconv.Itoa(trashGetuid())
	shared := stdpath.Join(mountRoot, ".Trash")
	root := ""
	if info, err := trashLstat(shared); err == nil {
		// The specification requires falling back to .Trash-$uid when the
		// shared directory is untrusted; an unsafe .Trash must never be
		// followed, but it need not disable a safe private volume trash.
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSticky != 0 {
			root = stdpath.Join(shared, uid)
			if err := ensurePrivateTrashDir(root); err != nil {
				return freedesktopTrash{}, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return freedesktopTrash{}, err
	}
	if root == "" {
		root = stdpath.Join(mountRoot, ".Trash-"+uid)
		if err := ensurePrivateTrashDir(root); err != nil {
			return freedesktopTrash{}, err
		}
	}
	t := freedesktopTrash{
		root:          root,
		files:         stdpath.Join(root, "files"),
		info:          stdpath.Join(root, "info"),
		pathBase:      mountRoot,
		absolutePaths: false,
	}
	if err := ensureTrashSubdirs(t); err != nil {
		return freedesktopTrash{}, err
	}
	return t, nil
}

func ensureTrashSubdirs(t freedesktopTrash) error {
	if err := ensurePrivateTrashDir(t.files); err != nil {
		return err
	}
	return ensurePrivateTrashDir(t.info)
}

func ensurePrivateTrashDir(path string) error {
	if err := trashMkdirAll(path, privateTrashDirMode); err != nil {
		return err
	}
	info, err := trashLstat(path)
	if err != nil {
		return err
	}
	_, owner, ok := trashStatIdentity(info)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe Recycle Bin directory: %s", path)
	}
	uid := trashGetuid()
	if owner != uint32(uid) {
		return fmt.Errorf("Recycle Bin directory is owned by uid %d, not uid %d: %s", owner, uid, path)
	}
	if info.Mode().Perm()&0077 != 0 {
		// Existing desktop trash implementations may have created these
		// directories with 0755/0775. Repair them when they are owned by the
		// current user instead of making F8 deletion unusable forever.
		if err := trashChmod(path, privateTrashDirMode); err != nil {
			return fmt.Errorf("cannot restrict Recycle Bin directory permissions (%#o): %s: %w", info.Mode().Perm(), path, err)
		}
		info, err = trashLstat(path)
		if err != nil {
			return err
		}
		_, owner, ok = trashStatIdentity(info)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || owner != uint32(uid) {
			return fmt.Errorf("Recycle Bin directory changed while repairing permissions: %s", path)
		}
		if info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("Recycle Bin directory permissions are too broad (%#o): %s", info.Mode().Perm(), path)
		}
	}
	return nil
}

func filesystemMountRoot(source string, sourceDevice uint64) (string, error) {
	current := source
	if info, err := trashLstat(current); err != nil {
		return "", err
	} else if !info.IsDir() {
		current = stdpath.Dir(current)
	}
	for {
		parent := stdpath.Dir(current)
		if parent == current {
			return current, nil
		}
		info, err := trashLstat(parent)
		if err != nil {
			return "", err
		}
		device, _, ok := trashStatIdentity(info)
		if !ok {
			return "", fmt.Errorf("cannot determine filesystem mount root for %s", source)
		}
		if device != sourceDevice {
			return current, nil
		}
		current = parent
	}
}

func moveIntoFreedesktopTrash(ctx context.Context, source string, trash freedesktopTrash) error {
	if isPathWithin(source, trash.root) {
		return fmt.Errorf("item is already inside the Recycle Bin: %s", source)
	}
	if isPathWithin(trash.root, source) {
		return fmt.Errorf("cannot move an ancestor of the Recycle Bin into itself: %s", source)
	}
	trashPath := source
	if !trash.absolutePaths {
		var err error
		trashPath, err = posixRelUnder(trash.pathBase, source)
		if err != nil {
			return fmt.Errorf("cannot express Recycle Bin path relative to mount root: %s", source)
		}
	}
	// EscapedPath applies URL percent-encoding while preserving path
	// separators. The Trash specification's examples require Path to remain
	// visibly absolute/relative (for example, /home/user/file or foo/bar), so
	// treating the whole pathname as one URL segment would be incorrect.
	// trashPath is already POSIX-slash-separated by construction (posixAbsClean
	// / posixRelUnder both build it out of package "path"), so there is no
	// backslash-to-slash conversion left to do here, unlike the pre-split code.
	encodedPath := (&url.URL{Path: trashPath}).EscapedPath()
	base := stdpath.Base(source)
	if base == "." || base == "/" || base == "" {
		return fmt.Errorf("invalid Recycle Bin item name: %s", source)
	}

	for suffix := 0; suffix < 100000; suffix++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := base
		if suffix > 0 {
			name += "." + strconv.Itoa(suffix)
		}
		destination := stdpath.Join(trash.files, name)
		infoPath := stdpath.Join(trash.info, name+".trashinfo")
		if pathExists(destination) || pathExists(infoPath) {
			continue
		}

		contents := "[Trash Info]\nPath=" + encodedPath + "\nDeletionDate=" + time.Now().Format("2006-01-02T15:04:05") + "\n"
		if err := trashWriteExclusive(infoPath, []byte(contents), 0600); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			_ = trashRemove(infoPath)
			return err
		}
		if err := trashRenameNoReplace(source, destination); err != nil {
			_ = trashRemove(infoPath)
			if errors.Is(err, ErrDestinationExists) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("cannot allocate a unique name in Recycle Bin for %s", source)
}

func pathExists(path string) bool {
	_, err := trashLstat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func isPathWithin(path, dir string) bool {
	p := stdpath.Clean(path)
	d := stdpath.Clean(dir)
	if p == d || d == "/" {
		return true
	}
	return strings.HasPrefix(p, d+"/")
}

// posixAbsClean anchors a path to POSIX root and cleans it via package
// "path" (always forward-slash, GOOS-independent) rather than
// path/filepath's Abs/Clean, which would apply Windows drive-letter
// semantics to what is, in both callers of this file, already a plain
// POSIX-style absolute path (OSVFS.Abs already resolved it through
// vfs/hostpath before calling in here). On a real POSIX build this is
// byte-identical to the filepath.Abs+Clean the pre-split code used, since
// path/filepath and "path" agree exactly once the separator is "/".
func posixAbsClean(p string) string {
	if !stdpath.IsAbs(p) {
		p = "/" + p
	}
	return stdpath.Clean(p)
}

// posixRelUnder returns target's path relative to base. Unlike
// filepath.Rel, it never invents ".." segments: filesystemMountRoot always
// hands moveIntoFreedesktopTrash a source it already proved lives under the
// volume's own mount root, so a target that isn't under base here is a
// defensive rejection, not an expected case -- exactly the outcome the
// pre-split code's hand-written ".."-prefix check produced against
// filepath.Rel's more general result.
func posixRelUnder(base, target string) (string, error) {
	base = stdpath.Clean(base)
	target = stdpath.Clean(target)
	if base == target {
		return "", fmt.Errorf("%s is the base itself, not an item under it", target)
	}
	prefix := base
	if prefix != "/" {
		prefix += "/"
	}
	if !strings.HasPrefix(target, prefix) {
		return "", fmt.Errorf("%s is not under %s", target, base)
	}
	rel := strings.TrimPrefix(target, prefix)
	if rel == "" {
		return "", fmt.Errorf("%s is not under %s", target, base)
	}
	return rel, nil
}
