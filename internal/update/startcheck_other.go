//go:build !windows

package update

import "os/exec"

func hideConsoleWindow(*exec.Cmd) {}
