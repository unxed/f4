//go:build !linux

package update

// packageManagerOwner is Linux-only: on other platforms f4's built-in updater
// is not competing with pacman.
var packageManagerOwner = func(string) (manager, packageName string) {
	return "", ""
}
