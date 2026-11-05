package panel

import (
	"strings"
)

// defaultCommandWrappers are the commands that take another command as their
// argument, so a file name inserted right after one is still in command
// position (`sudo ./script.sh`). A later step makes the list a setting.
var defaultCommandWrappers = []string{"sudo", "doas", "time", "nohup", "env", "exec", "strace", "xargs"}

// inCommandPosition reports whether a word typed right after `before` would be
// the command to run: at the start of the line, after a command separator
// (`;`, `&`, `|`, `(`), or after a wrapper such as `sudo`. A word glued to the
// text before it (no blank, no separator) is not in command position.
func inCommandPosition(before string, wrappers []string) bool {
	if before == "" {
		return true
	}
	last := before[len(before)-1]
	if strings.IndexByte(";&|(", last) >= 0 {
		return true
	}
	if last != ' ' && last != '\t' {
		return false
	}
	trimmed := strings.TrimRight(before, " \t")
	if trimmed == "" {
		return true
	}
	if strings.IndexByte(";&|(", trimmed[len(trimmed)-1]) >= 0 {
		return true
	}
	start := strings.LastIndexAny(trimmed, " \t") + 1
	word := trimmed[start:]
	for _, w := range wrappers {
		if word == w {
			return inCommandPosition(trimmed[:start], wrappers)
		}
	}
	return false
}

// withDotSlash gives a file name inserted into the command line by Ctrl+Enter
// the `./` it needs on a POSIX shell (f4#2235, the "auto" rule): an executable
// file in command position, because a bare word is looked up in PATH and not
// in the current folder; and a name that starts with `-`, anywhere, because
// otherwise the command takes it for an option. Every other name stays as it
// is. A name that already holds a slash (the parent folder's full path for
// "..") is a path and needs nothing.
func withDotSlash(name, before string, isExec, isDir bool, wrappers []string) string {
	if name == "" || strings.Contains(name, "/") {
		return name
	}
	if strings.HasPrefix(name, "-") ||
		(isExec && !isDir && inCommandPosition(before, wrappers)) {
		return "./" + name
	}
	return name
}
