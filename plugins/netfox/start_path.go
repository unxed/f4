package netfox

import (
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// enterStartPath applies a saved connection's optional start folder. A stale
// folder must not make the connection itself unusable: the VFS implementations
// keep their current path when SetPath fails, so leave the user at the server's
// default and make the reason available in the debug log.
func enterStartPath(fs vfs.VFS, startPath string) vfs.VFS {
	if startPath == "" {
		return fs
	}
	if err := fs.SetPath(startPath); err != nil {
		vtui.DebugLog("NetFox: cannot open start folder %q: %v", startPath, err)
	}
	return fs
}
