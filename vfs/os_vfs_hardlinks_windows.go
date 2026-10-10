//go:build windows

package vfs

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/f4/vfs/hostpath"
)

// Every name of a file with hard links, as far3 lists them in its attributes
// dialog (f4#1861): FindFirstFileNameW / FindNextFileNameW walk the names
// NTFS keeps for the file.

var (
	procFindFirstFileNameW = kernel32.NewProc("FindFirstFileNameW")
	procFindNextFileNameW  = kernel32.NewProc("FindNextFileNameW")
)

// maxHardLinkNames bounds the names read for one file; NTFS allows 1024.
const maxHardLinkNames = 1024

// HardLinkNames returns the full path of every name of the file at path,
// path itself included. Directories, and posix mode, have none to give.
func (v *OSVFS) HardLinkNames(ctx context.Context, path string) ([]string, error) {
	if hostmode.Posix() {
		return nil, errors.ErrUnsupported
	}
	abs, err := v.Abs(path)
	if err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(prepareOSPath(abs))
	if err != nil {
		return nil, err
	}
	// The names come back relative to the volume root: "\dir\file.txt".
	volume := hostpath.VolumeName(abs)
	const bufLen = windows.MAX_LONG_PATH
	buf := make([]uint16, bufLen)
	size := uint32(bufLen)
	h, _, callErr := procFindFirstFileNameW.Call(uintptr(unsafe.Pointer(ptr)), 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, callErr
	}
	defer func() { _ = windows.FindClose(windows.Handle(h)) }()
	var names []string
	for len(names) < maxHardLinkNames && ctx.Err() == nil {
		names = append(names, volume+windows.UTF16ToString(buf))
		size = bufLen
		ok, _, callErr := procFindNextFileNameW.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
		if ok == 0 {
			if errors.Is(callErr, windows.ERROR_HANDLE_EOF) {
				break
			}
			return names, callErr
		}
	}
	return names, ctx.Err()
}
