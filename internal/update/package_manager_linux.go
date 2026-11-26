//go:build linux

package update

import (
	"os/exec"
	"strings"
)

// packageManagerOwner reports the package that owns exe, when pacman is
// available and owns that exact path. An unowned executable remains eligible
// for the built-in updater.
var packageManagerOwner = pacmanPackageOwner

func pacmanPackageOwner(exe string) (manager, packageName string) {
	pacman, err := exec.LookPath("pacman")
	if err != nil {
		return "", ""
	}

	output, err := exec.Command(pacman, "-Qqo", exe).Output()
	if err != nil {
		return "", ""
	}
	if packageName := strings.TrimSpace(string(output)); packageName != "" {
		return "pacman", packageName
	}
	return "", ""
}
