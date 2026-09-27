//go:build windows

package proclist

import (
	"errors"

	"golang.org/x/sys/windows"
)

// errDetailUnsupportedWindows explains why the environment and open-files
// sections of f4#312 part 4's F3 view are empty on Windows: both need
// reading another process' PEB (environment) or enumerating its handles
// (open files), and both live behind NtQuerySystemInformation and other
// undocumented NT APIs -- the same class this plugin already refuses to use
// for WMI performance counters and the handle viewer (plugin.go's own
// package doc) and for Ctrl+F8 suspend/resume (actions_windows.go).
var errDetailUnsupportedWindows = errors.New("proclist: not available on Windows without an undocumented API")

// collectProcDetails on Windows fills only the command line section's first
// (and only) line: the executable's own full path, from
// QueryFullProcessImageName -- the one documented, no-elevation-required API
// among these three that this plugin can still use. It is not the true
// command line (arguments are not included -- Windows has no documented,
// unprivileged way to read another process' argv either), so this is
// genuinely all that is "cheap" here, matching the ticket's own "not
// necessarily on par with Linux" scope.
func collectProcDetails(pid int) procDetails {
	return procDetails{
		cmdline:   readWindowsExecutablePath(pid),
		environ:   procDetailsSection{err: errDetailUnsupportedWindows},
		openFiles: procDetailsSection{err: errDetailUnsupportedWindows},
	}
}

func readWindowsExecutablePath(pid int) procDetailsSection {
	// #nosec G115 -- pid is a real OS process id, the same conversion
	// collector_windows.go's own readWindowsProcess already makes for
	// OpenProcess.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return procDetailsSection{err: err}
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	// windows.MAX_PATH (260) is the historical limit; a few multiples of it
	// leaves headroom for a long-path-aware target without needing to retry
	// on ERROR_INSUFFICIENT_BUFFER.
	buf := make([]uint16, windows.MAX_PATH*4)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return procDetailsSection{err: err}
	}
	return procDetailsSection{lines: []string{windows.UTF16ToString(buf[:size])}}
}
