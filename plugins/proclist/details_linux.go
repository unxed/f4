//go:build linux

package proclist

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
)

// collectProcDetails reads the three parts of Linux's f4#312 part 4 detail
// view straight out of /proc, the same source collect() (collector_linux.go)
// already reads for the table itself. Each of the three is read
// independently: a process whose /proc/[pid]/environ this user cannot read
// (root-owned, or another user's) still gets its cmdline and open files
// listed, and vice versa -- "degrade one section, not the whole view",
// exactly like collect() already does for a field it cannot read on one
// process among many.
func collectProcDetails(pid int) procDetails {
	return procDetails{
		cmdline:   readProcNulList(pid, "cmdline"),
		environ:   readProcEnviron(pid),
		openFiles: readProcOpenFiles(pid),
	}
}

func readProcNulList(pid int, name string) procDetailsSection {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s", pid, name))
	if err != nil {
		return procDetailsSection{err: err}
	}
	return procDetailsSection{lines: splitProcNulList(data)}
}

func readProcEnviron(pid int) procDetailsSection {
	section := readProcNulList(pid, "environ")
	sort.Strings(section.lines)
	return section
}

// splitProcNulList turns a /proc/[pid]/{cmdline,environ} NUL-separated
// buffer into one string per entry. Both files end their last entry with a
// NUL too, which would otherwise produce one spurious empty trailing
// element.
func splitProcNulList(data []byte) []string {
	data = bytes.TrimSuffix(data, []byte{0})
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	lines := make([]string, len(parts))
	for i, part := range parts {
		lines[i] = string(part)
	}
	return lines
}

// readProcOpenFiles lists /proc/[pid]/fd the same way `ls -l` does: each
// entry's own name is the descriptor number, and its symlink target is what
// it points at (a real file, "socket:[12345]", "pipe:[12345]", an anonymous
// inode, ...). A descriptor that closes between the directory read and the
// symlink read -- the same kind of race collect() already tolerates for
// /proc/[pid] itself -- is skipped rather than reported as an error for one
// fd among many.
func readProcOpenFiles(pid int) procDetailsSection {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return procDetailsSection{err: err}
	}

	type fdEntry struct {
		num  int
		line string
	}
	fds := make([]fdEntry, 0, len(entries))
	for _, entry := range entries {
		num, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(dir + "/" + entry.Name())
		if err != nil {
			continue
		}
		fds = append(fds, fdEntry{num: num, line: fmt.Sprintf("%s -> %s", entry.Name(), target)})
	}
	sort.Slice(fds, func(i, j int) bool { return fds[i].num < fds[j].num })

	lines := make([]string, len(fds))
	for i, f := range fds {
		lines[i] = f.line
	}
	return procDetailsSection{lines: lines}
}
