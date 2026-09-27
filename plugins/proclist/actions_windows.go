//go:build windows

package proclist

import (
	"errors"

	"golang.org/x/sys/windows"
)

// suspendResumeSupported is false on Windows: the ticket (f4#312 part 3)
// explicitly says not to port suspend/resume here, since Windows has no
// supported, documented API for it -- SuspendProcess/NtSuspendProcess is
// undocumented NT, the same class of API this plugin already refuses to use
// for anything else (see plugin.go's own package doc on WMI/handles/remote
// view). panel.go's ProcessKey never reaches toggleSuspend in this build in
// the first place; suspendProcess/resumeProcess below exist only so this
// file's shape matches actions_unix.go's.
const suspendResumeSupported = false

// priorityLadder is Windows' own six PRIORITY_CLASS constants, in the same
// low-to-high order actions_unix.go's niceLadder uses and
// actions.go's priorityLevelKeys names: 0 Idle .. 5 Realtime.
var priorityLadder = [...]uint32{
	windows.IDLE_PRIORITY_CLASS,
	windows.BELOW_NORMAL_PRIORITY_CLASS,
	windows.NORMAL_PRIORITY_CLASS,
	windows.ABOVE_NORMAL_PRIORITY_CLASS,
	windows.HIGH_PRIORITY_CLASS,
	windows.REALTIME_PRIORITY_CLASS,
}

// killProcess opens pid just long enough to terminate it: FAR3's own F8 is
// TerminateProcess, the same unconditional kill this calls, with exit code 1
// (arbitrary -- nothing reads it back; TerminateProcess requires some value).
func killProcess(pid int) error {
	// #nosec G115 -- pid is a real OS process id, always non-negative and
	// well within uint32 range, the same conversion collector_windows.go's
	// own readWindowsProcess already makes for OpenProcess.
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return windows.TerminateProcess(handle, 1)
}

// changePriority moves pid one rung up (up=true) or down (up=false)
// priorityLadder, reading its current class first so repeated presses move
// predictably. It returns the resulting rung index so the caller (actions.go)
// can name it in a toast. Reaching REALTIME_PRIORITY_CLASS (the top rung)
// without SeIncreaseBasePriorityPrivilege silently downgrades to HIGH on
// real Windows -- SetPriorityClass itself does not report that as an error,
// so this can report success one rung short of what was asked for; that
// matches SetPriorityClass's own documented behavior, not a bug here.
func changePriority(pid int, up bool) (level int, err error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_SET_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	current, err := windows.GetPriorityClass(handle)
	if err != nil {
		return 0, err
	}
	idx := priorityIndex(current)
	switch {
	case up && idx < len(priorityLadder)-1:
		idx++
	case !up && idx > 0:
		idx--
	}
	if err := windows.SetPriorityClass(handle, priorityLadder[idx]); err != nil {
		return 0, err
	}
	return idx, nil
}

// priorityIndex maps a priority class GetPriorityClass returned back to its
// rung. A class priorityLadder does not name (PROCESS_MODE_BACKGROUND_BEGIN
// leaves that bit set in what GetPriorityClass reports back, for one) falls
// back to Normal's rung rather than panicking on an unmapped value.
func priorityIndex(class uint32) int {
	for i, c := range priorityLadder {
		if c == class {
			return i
		}
	}
	return 2
}

var errSuspendResumeUnsupported = errors.New("proclist: suspend/resume has no supported API on Windows")

func suspendProcess(int) error { return errSuspendResumeUnsupported }

func resumeProcess(int) error { return errSuspendResumeUnsupported }
