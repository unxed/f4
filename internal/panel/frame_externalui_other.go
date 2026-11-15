//go:build !windows

package panel

import "os/exec"

func runAssociatedFileOS(path, dir string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run()
}
