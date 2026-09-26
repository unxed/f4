//go:build windows

package vfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	winescape "github.com/unxed/libwinescape/go"
	"github.com/zzl/go-win32api/v2/win32"

	"github.com/unxed/f4/vfs/hostfs"
	"github.com/unxed/f4/vfs/hostmode"
)

var clsidFileOperation = syscall.GUID{
	Data1: 0x3AD05575,
	Data2: 0x8857,
	Data3: 0x4850,
	Data4: [8]byte{0x92, 0x77, 0x11, 0xB8, 0x5B, 0xDB, 0x8E, 0x09},
}

var _ TrashVFS = (*OSVFS)(nil)

// MoveToTrash uses IFileOperation with FOFX_RECYCLEONDELETE on native
// Windows. Unlike the older FOF_ALLOWUNDO flag, RECYCLEONDELETE requires
// recycling and fails instead of silently deleting an item permanently when
// no recycle bin is available.
//
// Under Wine's posix personality there is no Win32 Recycle Bin to ask: the
// real filesystem behind this VFS is the host's POSIX one, reached through
// libwinescape/hostfs (WINE.md §18.2, "Корзина"). Trash there behaves
// exactly as it does on a native Linux build -- the FreeDesktop.org Trash
// spec, shared via moveToFreedesktopTrash (vfs/trash_xdg.go) -- instead of
// being unavailable or going through the Recycle Bin API.
func (v *OSVFS) MoveToTrash(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := v.Abs(path)
	if err != nil {
		return err
	}
	if hostmode.Posix() {
		return moveToFreedesktopTrash(ctx, abs)
	}
	abs = filepath.Clean(stripExtendedPrefix(abs))
	if _, err := os.Lstat(abs); err != nil {
		return err
	}

	// COM apartment initialization and every interface call must remain on the
	// same native thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr := win32.CoInitializeEx(nil, win32.COINIT_APARTMENTTHREADED)
	if win32.FAILED(hr) {
		return windowsTrashHRESULT("initialize COM apartment", hr)
	}
	defer win32.CoUninitialize()

	var operation *win32.IFileOperation
	hr = win32.CoCreateInstance(
		&clsidFileOperation,
		nil,
		win32.CLSCTX_INPROC_SERVER,
		&win32.IID_IFileOperation,
		unsafe.Pointer(&operation),
	)
	if win32.FAILED(hr) {
		return windowsTrashHRESULT("create IFileOperation", hr)
	}
	defer operation.Release()

	flags := uint32(win32.FOF_SILENT | win32.FOF_NOCONFIRMATION | win32.FOF_NOCONFIRMMKDIR | win32.FOF_NOERRORUI |
		win32.FOFX_RECYCLEONDELETE | win32.FOFX_EARLYFAILURE | win32.FOFX_ADDUNDORECORD)
	if hr = operation.SetOperationFlags(flags); win32.FAILED(hr) {
		return windowsTrashHRESULT("set recycle operation flags", hr)
	}

	pathPtr, err := syscall.UTF16PtrFromString(abs)
	if err != nil {
		return err
	}
	var item *win32.IShellItem
	hr = win32.SHCreateItemFromParsingName(win32.PWSTR(pathPtr), nil, &win32.IID_IShellItem, unsafe.Pointer(&item))
	if win32.FAILED(hr) {
		return windowsTrashHRESULT("create shell item", hr)
	}
	defer item.Release()

	if err := ctx.Err(); err != nil {
		return err
	}
	if hr = operation.DeleteItem(item, nil); win32.FAILED(hr) {
		return windowsTrashHRESULT("queue recycle operation", hr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	performHR := operation.PerformOperations()
	var aborted win32.BOOL
	abortedHR := operation.GetAnyOperationsAborted(&aborted)
	if !win32.FAILED(performHR) && !win32.FAILED(abortedHR) && aborted == 0 {
		// Once the native operation has definitely completed, a concurrent
		// context cancellation must not turn success into a retryable error.
		return nil
	}

	var resultErrs []error
	if win32.FAILED(performHR) {
		resultErrs = append(resultErrs, windowsTrashHRESULT("perform recycle operation", performHR))
	}
	if win32.FAILED(abortedHR) {
		resultErrs = append(resultErrs, windowsTrashHRESULT("query recycle operation result", abortedHR))
	} else if aborted != 0 {
		resultErrs = append(resultErrs, fmt.Errorf("Windows recycle operation was aborted"))
	}
	resultErr := errors.Join(resultErrs...)

	// Microsoft requires querying GetAnyOperationsAborted even when
	// PerformOperations fails. If the source is still present, retrying is
	// safe; if it vanished, we cannot prove whether it reached the Recycle Bin
	// or was removed by another actor, so expose an explicit unknown state.
	if _, statErr := os.Lstat(abs); statErr == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return resultErr
	}
	return &UnknownOperationStateError{Operation: "Windows recycle operation", Err: resultErr}
}

func windowsTrashHRESULT(operation string, hr win32.HRESULT) error {
	return fmt.Errorf("%s: %s", operation, win32.HRESULT_ToString(hr))
}

// The four functions below are vfs/trash_xdg.go's platform substitution
// points for Wine's posix personality. They are only ever reached from
// moveToFreedesktopTrash, which this file only calls once hostmode.Posix()
// is already true, so none of them needs its own Posix() branch -- unlike
// vfs/hostfs, which is called from both personalities and branches inside
// itself.

// defaultTrashGetuid answers the host's real POSIX uid via libwinescape,
// exactly as os.Getuid() does on a real POSIX build (os.Getuid() itself
// always reports -1 on native Windows, posix or not, since Go's windows
// package does not implement it).
func defaultTrashGetuid() int { return winescape.Getuid() }

// defaultTrashHomeEnv reads an environment variable from the host's own
// environment, bypassing Wine's Win32 environment block -- the same bypass
// vfs/hostmode.HomeDir documents for HOME specifically, generalized to
// XDG_DATA_HOME here (Wine's get_initial_environment strips HOME from the
// Win32 side, and XDG_DATA_HOME simply never crosses over in the first
// place: it is not one of the handful of variables Wine mirrors at all).
func defaultTrashHomeEnv(key string) string { return winescape.HostGetenv(key) }

// defaultTrashHomeDir reuses hostmode.HomeDir -- the same lookup
// terminal/child_env.go and the other os.UserHomeDir call sites listed in
// WINE.md §18.2 already use for $HOME under posix personality -- rather
// than duplicating the HostGetenv("HOME") call here.
func defaultTrashHomeDir() (string, error) {
	home, ok := hostmode.HomeDir()
	if !ok {
		return "", fmt.Errorf("host $HOME is not set")
	}
	return home, nil
}

// defaultTrashWriteExclusive mirrors its real-POSIX twin
// (defaultTrashWriteExclusive in trash_freedesktop.go) through hostfs
// instead of package os.
//
// One real difference: libwinescape v0.2.1 does not expose fsync(2) yet
// (see the File interface's doc comment in vfs/hostfs/hostfs_posix.go), so
// the Sync type assertion below never succeeds in posix-under-Wine mode
// today. A crash between Close and the next read can in principle still
// lose the .trashinfo write that a real fsync would have made durable on a
// native Linux build. Every other platform this trash algorithm runs on
// keeps its real fsync unchanged (the assertion succeeds there); this is a
// known, narrower durability guarantee specific to the Wine posix path, not
// a silently dropped one.
func defaultTrashWriteExclusive(path string, data []byte, mode os.FileMode) (err error) {
	f, err := hostfs.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			_ = hostfs.Remove(path)
			err = closeErr
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = hostfs.Remove(path)
		return err
	}
	if syncer, ok := f.(interface{ Sync() error }); ok {
		if err = syncer.Sync(); err != nil {
			_ = hostfs.Remove(path)
			return err
		}
	}
	return nil
}

// trashStatIdentity extracts the device and owning uid a real POSIX stat(2)
// on the host reports through libwinescape -- the Windows-personality twin
// of trash_freedesktop.go's own trashStatIdentity, over *winescape.Stat_t
// instead of *syscall.Stat_t (Windows' package syscall has no Stat_t at
// all). winescape.Stat_t's Dev/Uid fields come straight from a real Linux
// stat(2) on the host, so this carries exactly the same semantics as the
// real-POSIX build's version -- not an approximation of it.
func trashStatIdentity(info os.FileInfo) (device uint64, owner uint32, ok bool) {
	stat, ok := info.Sys().(*winescape.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return stat.Dev, stat.Uid, true
}
