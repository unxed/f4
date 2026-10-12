//go:build windows

package vfs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// createMountPoint makes link a directory junction to target: it creates the
// empty directory and sets the mount point reparse data on it. Unlike a
// symbolic link it needs no privilege. A directory that was made and could not
// be turned into a junction is removed again (f4#1828).
func createMountPoint(target, link string) error {
	data, err := mountPointReparseBuffer(target)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		return errors.New("a junction can only point to an existing folder")
	}
	if err := os.Mkdir(link, 0o700); err != nil { // #nosec G703 -- the junction folder the user asked for
		return err
	}
	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		_ = os.Remove(link) // #nosec G703 -- the folder made just above
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		_ = os.Remove(link) // #nosec G703 -- the folder made just above
		return err
	}
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
	closeErr := windows.CloseHandle(handle)
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(link) // #nosec G703 -- the folder made just above
	}
	return err
}
