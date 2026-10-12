//go:build windows

package sysinfo

import (
	"strings"

	"golang.org/x/sys/windows"
)

// finalPath is where path really lives, with symlinks AND junctions
// expanded. filepath.EvalSymlinks stopped following mount points
// (junctions) in Go 1.23, so a folder reached through a junction still
// showed the free space of the disk holding the junction (f4#1835, after
// the first fix). The handle of the folder knows its final path whatever
// kind of reparse point led there.
func finalPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// Access 0 and backup semantics open folders too, without reading them.
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }() // read-only handle
	buf := make([]uint16, windows.MAX_PATH)
	for {
		// Flags 0: FILE_NAME_NORMALIZED | VOLUME_NAME_DOS.
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return trimVerbatim(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n+1)
	}
}

// trimVerbatim turns \\?\C:\dir into C:\dir and \\?\UNC\srv\share into
// \\srv\share, the forms the rest of f4 uses.
func trimVerbatim(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}
