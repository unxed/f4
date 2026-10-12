//go:build windows

package unpack

import (
	"time"

	"golang.org/x/sys/windows"
)

// setCreationTime writes the creation time of path. os.Chtimes cannot reach
// it, and Windows otherwise keeps the creation time of an earlier file of the
// same name for a while after it is replaced.
func setCreationTime(path string, t time.Time) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(ptr, windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ft := windows.NsecToFiletime(t.UnixNano())
	return windows.SetFileTime(h, &ft, nil, nil)
}
