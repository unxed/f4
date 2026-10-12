//go:build !windows

package vfs

import "errors"

// createMountPoint exists so OSVFS.Junction compiles everywhere; only Windows
// has junctions, and Junction does not call it elsewhere.
func createMountPoint(target, link string) error {
	return errors.New("directory junctions exist only on Windows")
}
