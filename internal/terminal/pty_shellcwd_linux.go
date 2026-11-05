//go:build linux

package terminal

import (
	"os"
	"path/filepath"
	"strconv"
)

// ShellCwd reports the current directory of the shell this terminal.PTY runs
// (the target of /proc/<pid>/cwd). The panel uses it to learn about a directory
// change made inside the shell by a function, an alias or a script, which f4
// cannot see in the text of the command line (unxed/f4#2272). ok is false when
// the shell is not running or its directory cannot be read.
func (p *PTY) ShellCwd() (dir string, ok bool) {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return "", false
	}
	dir, err := os.Readlink("/proc/" + strconv.Itoa(p.Cmd.Process.Pid) + "/cwd")
	if err != nil || !filepath.IsAbs(dir) {
		return "", false
	}
	return dir, true
}
