//go:build linux || darwin

package proclist

import "golang.org/x/sys/unix"

// suspendResumeSupported is true here: SIGSTOP/SIGCONT are POSIX signals f4
// can send to any process it has permission to signal, with no extra API to
// call and no elevated privilege needed beyond what killing the same
// process would already require.
const suspendResumeSupported = true

// niceLadder is *nix's equivalent of Windows' six PRIORITY_CLASS constants
// (actions_windows.go's priorityLadder), so Shift+F1/F2 move the same
// number of rungs on every platform even though *nix expresses priority as
// a signed nice value (-20 highest .. 19 lowest) rather than a named class.
// Index order matches priorityLevelKeys in actions.go: 0 Idle .. 5 Realtime.
// There is no true POSIX realtime scheduling class hiding behind index 5 --
// that would be SCHED_FIFO/SCHED_RR via sched_setscheduler, a materially
// different (and root-only) mechanism from nice -- so "Realtime" here is
// just the most aggressive nice value, the same simplification top and ps
// implicitly make when they show nice alone.
var niceLadder = [...]int{19, 10, 0, -5, -10, -20}

// killProcess sends SIGKILL, not SIGTERM: FAR3's F8 is TerminateProcess, an
// immediate, unconditional kill with no chance for the target to clean up,
// not the graceful default a shell's own "kill" command sends.
func killProcess(pid int) error {
	return unix.Kill(pid, unix.SIGKILL)
}

// changePriority moves pid one rung up (up=true) or down (up=false) niceLadder,
// snapping its current nice value to the nearest rung first so repeated
// presses move predictably even if some other tool left it at an in-between
// value niceLadder does not name. It returns the resulting rung index so
// the caller (actions.go) can name it in a toast.
func changePriority(pid int, up bool) (level int, err error) {
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	// getpriority(2)'s raw syscall return (what x/sys/unix.Getpriority calls,
	// bypassing glibc) is 20-nice, in 0..39: the kernel cannot return a
	// negative "success" value on that path, unlike glibc's own getpriority()
	// wrapper, which already flips the sign back before handing the value to
	// its caller. setpriority's own prio argument is not encoded the same way
	// -- it takes the true nice value directly, confirmed empirically (set 10,
	// read back 10 via ps -o ni) -- so only the read side needs unflipping
	// here.
	nice := 20 - prio
	idx := nearestNiceIndex(nice)
	switch {
	case up && idx < len(niceLadder)-1:
		idx++
	case !up && idx > 0:
		idx--
	}
	if err := unix.Setpriority(unix.PRIO_PROCESS, pid, niceLadder[idx]); err != nil {
		return 0, err
	}
	return idx, nil
}

func nearestNiceIndex(nice int) int {
	best, bestDiff := 0, absInt(nice-niceLadder[0])
	for i, v := range niceLadder {
		if d := absInt(nice - v); d < bestDiff {
			best, bestDiff = i, d
		}
	}
	return best
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func suspendProcess(pid int) error { return unix.Kill(pid, unix.SIGSTOP) }

func resumeProcess(pid int) error { return unix.Kill(pid, unix.SIGCONT) }
