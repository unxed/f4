//go:build !windows

package sysinfo

import "path/filepath"

// finalPath is where path really lives, with links expanded.
func finalPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
