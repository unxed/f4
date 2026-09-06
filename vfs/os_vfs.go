package vfs

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"strings"

	"runtime"

	"github.com/unxed/f4/vfs/hostfs"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/f4/vfs/hostpath"
	"github.com/unxed/vtui"
)

type OSVFS struct {
	currentPath string
}

func NewOSVFS(initialPath string) *OSVFS {
	abs, _ := hostpath.Abs(initialPath)
	return &OSVFS{currentPath: abs}
}

func (v *OSVFS) GetPath() string        { return v.currentPath }
func (v *OSVFS) IsAbs(path string) bool { return hostpath.IsAbs(path) }

func (v *OSVFS) IsAtRoot() bool {
	if runtime.GOOS == "windows" && !hostmode.Posix() {
		vol := hostpath.VolumeName(v.currentPath)
		p := hostpath.Clean(v.currentPath)
		// Standardize to backslash for comparison on Windows
		p = strings.ReplaceAll(p, "/", "\\")
		vol = strings.ReplaceAll(vol, "/", "\\")
		return p == vol || p == vol+"." || p == vol+"\\" || p == "\\"
	}
	return v.currentPath == "/"
}

func (v *OSVFS) SetPath(path string) error {
	vtui.DebugLog("VFS: SetPath(%q) called", path)
	target := path
	if !hostpath.IsAbs(path) && hostpath.VolumeName(path) == "" {
		target = hostpath.Join(v.currentPath, path)
	}
	abs, err := hostpath.Abs(target)
	if err != nil {
		return err
	}

	// First try to verify the original path directly. If it exists, is
	// accessible and is a directory (or a symlink to one), we keep the
	// original visual path in the panel without forcing a dereference.
	if st, errStat := hostfs.Stat(prepareOSPath(abs)); errStat == nil && st.IsDir() {
		goto verify
	}

	// If direct access failed (e.g. Permission Denied on the Windows system
	// junction "Documents and Settings" or on a nested per-user profile
	// junction like "Application Data"), force-resolve the reparse points.
	// Resolution runs on the PLAIN path (no \\?\ prefix): the prefix disables
	// Win32's transparent reparse redirection, so "Documents and Settings"
	// is opened directly and yields Access Denied.
	if runtime.GOOS == "windows" {
		for _, candidate := range resolveReparseCandidates(abs) {
			vtui.DebugLog("VFS: SetPath: trying reparse candidate %q -> %q", abs, candidate)
			if st, errStat := hostfs.Stat(prepareOSPath(candidate)); errStat == nil && st.IsDir() {
				abs = candidate
				goto verify
			}
		}
	}

verify:
	st, err := hostfs.Stat(prepareOSPath(abs))
	if err != nil {
		if os.IsPermission(err) && globalSudoClient.IsAvailable() {
			vtui.DebugLog("VFS: SetPath: Permission denied for %q, checking via sudo...", abs)
			item, sudoErr := globalSudoClient.Stat(prepareOSPath(abs))
			if sudoErr == nil {
				if item.IsDir {
					vtui.DebugLog("VFS: Path changed to %q (via sudo Stat)", abs)
					v.currentPath = abs
					return nil
				}
				vtui.DebugLog("VFS: SetPath(%q) FAILED: not a directory (via sudo Stat)", abs)
				return os.ErrInvalid
			}
			return sudoErr
		}
		return err
	}
	if !st.IsDir() {
		vtui.DebugLog("VFS: SetPath(%q) FAILED: not a directory", abs)
		return os.ErrInvalid
	}
	vtui.DebugLog("VFS: Path changed to %q", abs)
	v.currentPath = abs
	return nil
}

func (v *OSVFS) ReadDir(ctx context.Context, path string, onChunk func([]VFSItem)) error {
	dirPath := path
	entries, err := hostfs.ReadDir(prepareOSPath(dirPath))
	if err != nil && os.IsPermission(err) && runtime.GOOS == "windows" {
		// Resolve protected/per-user junctions (e.g. "Documents and
		// Settings", "<user>\Application Data") the same way SetPath does.
		for _, candidate := range resolveReparseCandidates(dirPath) {
			vtui.DebugLog("VFS: ReadDir: trying reparse candidate %q -> %q", dirPath, candidate)
			if e, eErr := hostfs.ReadDir(prepareOSPath(candidate)); eErr == nil {
				vtui.DebugLog("VFS: ReadDir: resolved junction %q -> %q", dirPath, candidate)
				dirPath = candidate
				entries, err = e, nil
				break
			}
		}
	}
	if err != nil {
		if os.IsPermission(err) && globalSudoClient.IsAvailable() {
			vtui.DebugLog("VFS: Permission denied for ReadDir(%q), attempting sudo...", dirPath)
			items, sudoErr := globalSudoClient.ReadDir(prepareOSPath(dirPath))
			if sudoErr == nil {
				vtui.DebugLog("VFS: Sudo ReadDir(%q) SUCCESS, items: %d", dirPath, len(items))
				if len(items) > 0 && onChunk != nil {
					onChunk(items)
				}
				return nil
			}
			vtui.DebugLog("VFS: Sudo ReadDir(%q) FAILED: %v", dirPath, sudoErr)
		} else {
			vtui.DebugLog("VFS: ReadDir(%q) FAILED: %v (Permission: %v, SudoAvailable: %v)", dirPath, err, os.IsPermission(err), globalSudoClient.IsAvailable())
		}
		return err
	}

	// hostfs.ReadDir returns the whole directory at once (both the posix
	// os.ReadDir backend and, from Stage E3, the libwinescape backend do),
	// unlike the old *os.File.ReadDir(1000) this replaces, which streamed
	// incrementally straight from the OS. True incremental disk reading is
	// gone; what's kept is chunked *delivery* to onChunk, so a huge
	// directory still arrives to the UI in batches instead of one giant
	// slice that blocks a single redraw.
	const chunkSize = 1000
	for start := 0; start < len(entries); start += chunkSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := start + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]

		items := make([]VFSItem, 0, len(batch))
		for _, e := range batch {
			info, _ := e.Info()
			var size int64
			var mtime time.Time
			var isExec bool
			if info != nil {
				size = info.Size()
				mtime = info.ModTime()
				isExec = info.Mode().Perm()&0111 != 0
			}
			isDir := e.IsDir()
			isSymlink := e.Type()&os.ModeSymlink != 0
			// Windows NTFS junctions are reparse points but Go doesn't
			// always report them via ModeSymlink — the classification
			// has drifted across releases (ModeSymlink / ModeIrregular).
			// Treat the FILE_ATTRIBUTE_REPARSE_POINT bit as authoritative
			// so the scanner's leaf mode actually stops at things like
			// C:\Users\<user>\AppData\Local\Application Data instead of
			// walking into the self-loop.
			if !isSymlink && info != nil && isReparsePoint(info) {
				isSymlink = true
			}
			// If it's not a direct directory, it might be a symlink or a Windows Junction.
			// If it's not a regular file, ask the OS to resolve the final target.
			if !isDir && !e.Type().IsRegular() {
				if target, err := hostfs.Stat(hostpath.Join(dirPath, e.Name())); err == nil {
					isDir = target.IsDir()
				}
			}

			entryPath := hostpath.Join(dirPath, e.Name())
			item := VFSItem{
				Name:         e.Name(),
				Size:         size,
				SizeKnown:    true,
				IsDir:        isDir,
				IsSymlink:    isSymlink,
				MTime:        mtime,
				IsExecutable: isExec,
				IsHidden:     isHidden(entryPath, e.Name(), info),
			}
			// Cheap variant: on Unix stat.Blocks is already loaded
			// alongside FileInfo, so filling PhysicalSize here is free.
			// On Windows this is a no-op — the scan path pays for
			// GetCompressedFileSize lazily via Stat() when it actually
			// needs the number (see vfs/scanner.go).
			fillPhysicalSizeCheap(&item, info)
			items = append(items, item)
		}

		if len(items) > 0 && onChunk != nil {
			onChunk(items)
		}
	}
	return nil
}

func (v *OSVFS) Stat(ctx context.Context, path string) (VFSItem, error) {
	if ctx.Err() != nil {
		return VFSItem{}, ctx.Err()
	}
	preparedPath := prepareOSPath(path)
	linkInfo, err := hostfs.Lstat(preparedPath)
	if err != nil {
		if os.IsPermission(err) && globalSudoClient.IsAvailable() {
			vtui.DebugLog("VFS: Permission denied for Stat(%q), attempting sudo...", path)
			item, sudoErr := globalSudoClient.Stat(prepareOSPath(path))
			if sudoErr == nil {
				vtui.DebugLog("VFS: Sudo Stat(%q) SUCCESS", path)
				return item, nil
			}
			vtui.DebugLog("VFS: Sudo Stat(%q) FAILED: %v", path, sudoErr)
		}
		return VFSItem{}, err
	}
	isSymlink := linkInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(linkInfo)
	info := linkInfo
	if isSymlink {
		// Preserve the historical Stat view of the target while exposing that
		// the selected entry itself is a link/junction. Broken links remain
		// addressable as leaf entries.
		if targetInfo, targetErr := hostfs.Stat(preparedPath); targetErr == nil {
			info = targetInfo
		}
	}

	item := VFSItem{
		Name:         info.Name(),
		Size:         info.Size(),
		SizeKnown:    true,
		IsDir:        info.IsDir(),
		IsSymlink:    isSymlink,
		MTime:        info.ModTime(),
		UnixMode:     uint32(info.Mode().Perm()),
		IsExecutable: info.Mode().Perm()&0111 != 0,
		IsHidden:     isHidden(path, info.Name(), info),
	}

	// Platform specific time extraction
	fillPlatformTimes(&item, info)
	fillPhysicalSize(&item, info, preparedPath)

	return item, nil
}

func (v *OSVFS) Join(elem ...string) string { return hostpath.Join(elem...) }
func (v *OSVFS) Lstat(ctx context.Context, path string) (VFSItem, error) {
	if ctx.Err() != nil {
		return VFSItem{}, ctx.Err()
	}
	absPath, err := v.Abs(path)
	if err != nil {
		return VFSItem{}, err
	}
	preparedPath := prepareOSPath(absPath)
	info, err := hostfs.Lstat(preparedPath)
	if err != nil {
		if os.IsPermission(err) && globalSudoClient.IsAvailable() {
			item, sudoErr := globalSudoClient.Stat(prepareOSPath(path))
			if sudoErr == nil {
				return item, nil
			}
		}
		return VFSItem{}, err
	}
	isSymlink := info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info)
	isDir := info.IsDir()
	if isSymlink {
		if target, err := hostfs.Stat(preparedPath); err == nil {
			isDir = target.IsDir()
		}
	}

	item := VFSItem{
		Name:         info.Name(),
		Size:         info.Size(),
		SizeKnown:    true,
		IsDir:        isDir,
		IsSymlink:    isSymlink,
		MTime:        info.ModTime(),
		UnixMode:     uint32(info.Mode().Perm()),
		IsExecutable: info.Mode().Perm()&0111 != 0,
		IsHidden:     isHidden(path, info.Name(), info),
	}

	fillPlatformTimes(&item, info)
	fillPhysicalSize(&item, info, preparedPath)

	return item, nil
}

func (v *OSVFS) Abs(path string) (string, error) {
	if hostpath.IsAbs(path) {
		return hostpath.Clean(path), nil
	}
	// Correctly resolve relative to the VFS current path, not process CWD
	return hostpath.Join(v.currentPath, path), nil
}

func (v *OSVFS) Base(path string) string { return hostpath.Base(path) }
func (v *OSVFS) Dir(path string) string  { return hostpath.Dir(path) }
func (v *OSVFS) MkDir(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := hostfs.MkdirAll(prepareOSPath(path), 0755)
	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		vtui.DebugLog("VFS: Permission denied for MkDir(%q), attempting sudo...", path)
		return globalSudoClient.MkDir(prepareOSPath(path), 0755)
	}
	return err
}

func (v *OSVFS) Remove(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := hostfs.RemoveAll(prepareOSPath(path))
	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		return globalSudoClient.Remove(prepareOSPath(path))
	}
	return err
}

func (v *OSVFS) Rename(ctx context.Context, old, new string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if overwrite, known := DestinationOverwrite(ctx); known && !overwrite {
		return v.RenameNoReplace(ctx, old, new)
	}
	err := hostfs.Rename(prepareOSPath(old), prepareOSPath(new))
	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		return globalSudoClient.Rename(prepareOSPath(old), prepareOSPath(new))
	}
	return err
}

// RenameNoReplace renames an OS object without ever replacing an unrelated
// destination. It is intentionally an OSVFS capability rather than part of
// the general VFS contract: virtual filesystems may have different atomicity
// guarantees. VisRen uses it to preserve Win32 MoveFile semantics on Unix.
func (v *OSVFS) RenameNoReplace(ctx context.Context, old, new string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return renameNoReplace(prepareOSPath(old), prepareOSPath(new))
}
func (v *OSVFS) SetAttributes(ctx context.Context, path string, item VFSItem) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Try native first
	var errMode error
	if item.UnixMode != 0 {
		errMode = hostfs.Chmod(prepareOSPath(path), os.FileMode(item.UnixMode))
	}

	var errOwn error
	if runtime.GOOS != "windows" {
		if item.Uid != -1 && item.Gid != -1 {
			if item.IsSymlink {
				errOwn = hostfs.Lchown(prepareOSPath(path), item.Uid, item.Gid)
			} else {
				errOwn = hostfs.Chown(prepareOSPath(path), item.Uid, item.Gid)
			}
		}
	}

	var errTime error
	if !item.ATime.IsZero() || !item.MTime.IsZero() {
		atime := item.ATime
		mtime := item.MTime
		if atime.IsZero() {
			atime = mtime
		}
		if mtime.IsZero() {
			mtime = atime
		}
		errTime = hostfs.Chtimes(prepareOSPath(path), atime, mtime)
	}

	errPlat := applyPlatformAttributes(prepareOSPath(path), item)

	// If any operation failed due to permissions, try sudo
	if (os.IsPermission(errMode) || os.IsPermission(errOwn) || os.IsPermission(errTime) || os.IsPermission(errPlat)) && globalSudoClient.IsAvailable() {
		vtui.DebugLog("VFS: SetAttributes permission denied, trying sudo for %q", path)
		return globalSudoClient.SetAttributes(prepareOSPath(path), item)
	}

	if errMode != nil {
		return errMode
	}
	if errOwn != nil {
		return errOwn
	}
	if errTime != nil {
		return errTime
	}
	return errPlat
}

func (v *OSVFS) PatchInPlace(ctx context.Context, path string, pieces []PatchPiece) (returnErr error) {
	// Before the first byte goes out: a patch this cannot express has to be
	// refused while the file is still intact.
	if err := ValidateInPlacePieces(pieces); err != nil {
		return err
	}

	f, err := hostfs.OpenFile(prepareOSPath(path), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, f.Close())
	}()

	var newOffset int64 = 0
	for _, p := range pieces {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.Data != nil {
			if _, err := f.WriteAt(p.Data, newOffset); err != nil {
				return err
			}
		}
		newOffset += p.Length
	}
	// In-place patches can replace the file with fewer bytes than it had
	// before. WriteAt overwrites the prefix but does not remove the old tail,
	// so truncate after the last piece to make the on-disk result match the
	// logical piece stream.
	return f.Truncate(newOffset)
}
func (v *OSVFS) GetCapabilities() VFSCapabilities {
	return VFSCapabilities{
		HasServerSideCopy:        true,
		HasServerSideMove:        true,
		HasRandomAccess:          true,
		HasSearch:                false,
		HasUnixPermissions:       runtime.GOOS != "windows",
		HasAtomicNoReplaceRename: true,
		HasWrite:                 true,
	}
}

func (v *OSVFS) Search(ctx context.Context, path string, pattern string) (chan int64, error) {
	// OSVFS uses local streaming search implemented in actions.go
	return nil, nil
}

type osFileWrapper struct {
	hostfs.File
	// mu guards size alone. RefreshSize is called from a viewer following a
	// growing log while its background fetches ask for the size on another
	// goroutine, so the field is no longer written once and read forever.
	mu   sync.Mutex
	size int64
}

func (f *osFileWrapper) Size() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.size
}

// RefreshSize re-measures the open file. It reports the length the handle now
// has, which is how the viewer notices that a log it is displaying has grown.
func (f *osFileWrapper) RefreshSize(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return f.Size(), err
	}
	if f.File == nil {
		return f.Size(), os.ErrInvalid
	}
	info, err := f.Stat()
	if err != nil {
		return f.Size(), err
	}
	if info.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0 {
		// A device's length was probed by seeking to its end when it was
		// opened; Stat reports zero for one, and taking that would tell the
		// viewer the disk it is showing is empty.
		return f.Size(), nil
	}
	f.mu.Lock()
	f.size = info.Size()
	size := f.size
	f.mu.Unlock()
	return size, nil
}
func (f *osFileWrapper) Read(ctx context.Context, p []byte) (n int, err error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return f.File.Read(p)
}

func (f *osFileWrapper) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return f.File.ReadAt(p, off)
}
func (f *osFileWrapper) Fd() uintptr {
	if f.File != nil {
		return f.File.Fd()
	}
	return 0
}

func (v *OSVFS) Open(ctx context.Context, path string) (ReadAtCloser, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	fi, err := hostfs.Stat(prepareOSPath(path))
	if err == nil && (fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0) {
		return nil, os.ErrInvalid
	}
	f, err := hostfs.OpenFile(prepareOSPath(path), os.O_RDWR, 0)
	if err != nil {
		f, err = hostfs.Open(prepareOSPath(path))
	}
	if err != nil {
		if os.IsPermission(err) && globalSudoClient.IsAvailable() {
			vtui.DebugLog("VFS: Permission denied for Open(%q), attempting sudo...", path)
			sudoF, sudoErr := globalSudoClient.Open(prepareOSPath(path), os.O_RDONLY, 0)
			if sudoErr == nil {
				info, _ := sudoF.Stat()
				size := info.Size()
				if info.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0 {
					if probedSize, found, err := probeSeekSize(sudoF); err != nil {
						_ = sudoF.Close() // The read handle cannot be returned at the wrong offset.
						return nil, err
					} else if found {
						size = probedSize
					}
				}
				vtui.DebugLog("VFS: Sudo Open(%q) SUCCESS, size: %d", path, size)
				return &osFileWrapper{File: sudoF, size: size}, nil
			}
			vtui.DebugLog("VFS: Sudo Open(%q) FAILED: %v", path, sudoErr)
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close() // No writes occurred before the failed metadata read.
		return nil, err
	}
	size := info.Size()
	if info.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0 {
		if probedSize, found, err := probeSeekSize(f); err != nil {
			_ = f.Close() // The handle cannot be returned at the wrong offset.
			return nil, err
		} else if found {
			size = probedSize
		}
	}
	return &osFileWrapper{File: f, size: size}, nil
}

func (v *OSVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	prepared := prepareOSPath(path)
	fi, err := hostfs.Stat(prepared)
	if err == nil && (fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0) {
		return nil, os.ErrInvalid
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if err == nil && (fi.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0) {
		flags = os.O_WRONLY // Do not truncate devices
	}
	createMode := os.FileMode(0o666)
	if overwrite, known := DestinationOverwrite(ctx); known && !overwrite {
		// O_EXCL makes the editor's unique sibling creation collision-safe and
		// closes the Stat/Create race. Do not include O_TRUNC in this mode: an
		// existing path must remain byte-for-byte untouched.
		flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
		// Newly staged/copied data must not be exposed through a permissive
		// umask window before the caller restores its final metadata.
		createMode = 0o600
	}
	f, err := hostfs.OpenFile(prepared, flags, createMode)
	if err != nil && os.IsPermission(err) && globalSudoClient.IsAvailable() {
		vtui.DebugLog("VFS: Permission denied for Create(%q), attempting sudo...", path)
		return globalSudoClient.Open(prepared, flags, uint32(createMode))
	}
	if err != nil {
		// Converting a nil *os.File directly to io.WriteCloser creates a
		// non-nil interface. Return a literal nil so callers cannot accidentally
		// use a writer after O_EXCL or another open failure.
		return nil, err
	}
	return f, nil
}

func (v *OSVFS) ParentVFS() VFS {
	return nil // OSVFS is the root
}
func (v *OSVFS) Clone() VFS {
	return NewOSVFS(v.currentPath)
}
func (v *OSVFS) Close() error { return nil }

// wellKnownJunction checks if the given path on Windows is a well-known junction
// and returns its known target. This is a last-resort fallback when all other
// reparse point resolution methods fail or are blocked by permissions.
func wellKnownJunction(path string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	parent := hostpath.Dir(path)
	name := strings.ToLower(hostpath.Base(path))
	parentBase := strings.ToLower(hostpath.Base(parent))

	// Documents and Settings at drive root -> Users
	if len(path) >= 3 && path[1] == ':' && path[2] == '\\' && name == "documents and settings" {
		return hostpath.Join(parent, "Users"), true
	}

	// C:\Users\All Users -> C:\ProgramData
	if name == "all users" {
		return hostpath.Join(hostpath.Dir(parent), "ProgramData"), true
	}

	// C:\Users\Default User -> C:\Users\Default
	if name == "default user" && parentBase == "users" {
		return hostpath.Join(parent, "Default"), true
	}

	return "", false
}

// resolveReparseCandidates returns candidate paths to retry a failed
// operation on when the original Windows path could not be opened directly
// because of a reparse point (junction or symlink) in its components. This
// covers the compatibility shim "C:\Documents and Settings" (junction to
// "C:\Users") and the per-user profile junctions such as "Application Data"
// (-> "AppData\Roaming"), "Local Settings", "My Documents", etc.
//
// The first candidate is a full EvalSymlinks resolution of the PLAIN path:
// the "\\?\" long-path prefix disables Win32's transparent reparse
// redirection, so resolution must run on the unprefixed path. The remaining
// candidates handle cases where EvalSymlinks itself is blocked (protected
// reparse points): the hard-coded well-known junctions, Readlink on the
// final component, and a raw DeviceIoControl reparse read.
func resolveReparseCandidates(abs string) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	var out []string
	if resolved, errEval := hostpath.EvalSymlinks(abs); errEval == nil {
		resolved = stripExtendedPrefix(resolved)
		// Prevent resolving mapped drives (e.g. T:\) into UNC paths (\\server\share).
		origVol := hostpath.VolumeName(abs)
		resVol := hostpath.VolumeName(resolved)
		if len(origVol) == 2 && origVol[1] == ':' && len(resVol) > 2 && strings.HasPrefix(resVol, `\\`) {
			resolved = origVol + strings.TrimPrefix(resolved, resVol)
		}
		out = append(out, resolved)
	}
	if link, ok := wellKnownJunction(abs); ok {
		out = append(out, link)
	}
	if link, errRead := hostfs.Readlink(abs); errRead == nil {
		if hostpath.IsAbs(link) {
			out = append(out, link)
		} else {
			out = append(out, hostpath.Join(hostpath.Dir(abs), link))
		}
	}
	if link, errJunc := resolveWindowsJunction(abs); errJunc == nil {
		if hostpath.IsAbs(link) {
			out = append(out, link)
		} else {
			out = append(out, hostpath.Join(hostpath.Dir(abs), link))
		}
	}
	return out
}

// prepareOSPath adds the \\?\ prefix on Windows to prevent the Win32 API
// from automatically stripping trailing dots and spaces from file names.
func prepareOSPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if hostmode.Posix() {
		// The \\?\ long-path prefix is a Win32 convention with no meaning
		// to libwinescape's raw syscalls, which expect the plain POSIX
		// string as-is. Prepending it here would silently corrupt every
		// path in posix mode (open("\\?\/home/user/foo") is nonsense to a
		// real open(2) -- backslashes are ordinary filename bytes on
		// Linux, not separators).
		return p
	}
	abs, err := hostpath.Abs(p)
	if err != nil {
		return p
	}
	if strings.HasPrefix(abs, `\\?\`) {
		return abs
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:]
	}
	return `\\?\` + abs
}

// stripExtendedPrefix removes the \\?\ prefix from paths returned by OS functions
// (like EvalSymlinks) so they display nicely in the UI.
func stripExtendedPrefix(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if strings.HasPrefix(p, `\\?\UNC\`) {
		return `\\` + p[8:]
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p[4:]
	}
	return p
}

// Readlink and Symlink make OSVFS a SymlinkVFS. The local file system is the
// one backend where a symbolic link is exactly what the word means.

func (v *OSVFS) Readlink(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	abs, err := v.Abs(path)
	if err != nil {
		return "", err
	}
	return hostfs.Readlink(prepareOSPath(abs))
}

func (v *OSVFS) Symlink(ctx context.Context, target, linkPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := v.Abs(linkPath)
	if err != nil {
		return err
	}
	return hostfs.Symlink(target, prepareOSPath(abs))
}

// OpenWriteAt makes OSVFS a RandomWriteVFS. A local file is the case where
// staging a second copy on the same disk buys nothing at all.
func (v *OSVFS) OpenWriteAt(ctx context.Context, path string) (WriterAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := v.Abs(path)
	if err != nil {
		return nil, err
	}
	return hostfs.OpenFile(prepareOSPath(abs), os.O_RDWR|os.O_CREATE, 0o644)
}

func (v *OSVFS) Hardlink(ctx context.Context, target, linkPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absTarget, err := v.Abs(target)
	if err != nil {
		return err
	}
	absLink, err := v.Abs(linkPath)
	if err != nil {
		return err
	}
	return hostfs.Link(prepareOSPath(absTarget), prepareOSPath(absLink))
}

func (v *OSVFS) Junction(ctx context.Context, target, linkPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absTarget, err := v.Abs(target)
	if err != nil {
		return err
	}
	absLink, err := v.Abs(linkPath)
	if err != nil {
		return err
	}
	return hostfs.Symlink(absTarget, prepareOSPath(absLink))
}
