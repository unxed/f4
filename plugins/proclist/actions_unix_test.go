//go:build linux || darwin

package proclist

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNearestNiceIndexSnapsToTheClosestRung(t *testing.T) {
	cases := []struct {
		nice int
		want int
	}{
		// Exact rungs: unambiguous, diff 0.
		{19, 0}, {10, 1}, {0, 2}, {-5, 3}, {-10, 4}, {-20, 5},
		// Outside the ladder entirely: clamps to the nearest end rung.
		{40, 0}, {-40, 5},
	}
	for _, c := range cases {
		if got := nearestNiceIndex(c.nice); got != c.want {
			t.Errorf("nearestNiceIndex(%d) = %d, want %d", c.nice, got, c.want)
		}
	}
}

func TestAbsInt(t *testing.T) {
	if absInt(-5) != 5 || absInt(5) != 5 || absInt(0) != 0 {
		t.Fatalf("absInt gave a wrong result for -5, 5 or 0")
	}
}

// TestChangePriorityLowersOwnNiceValue exercises changePriority's real
// syscalls (Getpriority/Setpriority), not just the pure ladder math above.
// It only tests the "lower priority" direction (up=false, a higher nice
// value): POSIX lets any process raise its own nice value without
// privilege, unlike lowering it, which needs CAP_SYS_NICE/RLIMIT_NICE this
// test's CI user may not have -- asserting that direction here would be
// testing the environment, not this code.
func TestChangePriorityLowersOwnNiceValue(t *testing.T) {
	pid := os.Getpid()
	before, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		t.Fatalf("Getpriority: %v", err)
	}
	t.Cleanup(func() { _ = unix.Setpriority(unix.PRIO_PROCESS, pid, 20-before) })

	level, err := changePriority(pid, false)
	if err != nil {
		t.Fatalf("changePriority(lower): %v", err)
	}
	if level < 0 || level >= len(niceLadder) {
		t.Fatalf("changePriority returned an out-of-range level %d", level)
	}

	after, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		t.Fatalf("Getpriority after: %v", err)
	}
	// getpriority(2)'s raw return is 20-nice (see changePriority's own
	// comment): a higher true nice value -- lower priority, what "lower"
	// asked for -- reads back as a *smaller* raw number.
	if after >= before {
		t.Fatalf("nice value did not increase: before=%d after=%d (raw getpriority, 20-nice)", before, after)
	}
}

// startSleepHelper starts a short-lived, otherwise idle child process for
// killProcess/suspendProcess/resumeProcess to act on -- something this test
// can safely terminate without touching the test binary's own process.
func startSleepHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start the \"sleep\" helper process: %v", err)
	}
	return cmd
}

func TestKillProcessTerminatesTheChild(t *testing.T) {
	cmd := startSleepHelper(t)
	if err := killProcess(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatalf("killProcess: %v", err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatal("cmd.Wait() succeeded; the child should have been killed")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("cmd.Wait() error = %v (%T), want *exec.ExitError", err, err)
	}
}

func TestSuspendThenResumeChildProcess(t *testing.T) {
	cmd := startSleepHelper(t)
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	if err := suspendProcess(pid); err != nil {
		t.Fatalf("suspendProcess: %v", err)
	}
	// unix.Kill with signal 0 sends nothing and only probes whether the pid
	// still exists and is signalable -- SIGSTOP does not make a process
	// disappear, so this must still succeed.
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatalf("process vanished after suspendProcess: %v", err)
	}

	if err := resumeProcess(pid); err != nil {
		t.Fatalf("resumeProcess: %v", err)
	}
	if err := unix.Kill(pid, 0); err != nil {
		t.Fatalf("process vanished after resumeProcess: %v", err)
	}
}

func TestKillProcessOnANonexistentPidFails(t *testing.T) {
	// A pid this unlikely to be in use: killProcess must report the error,
	// not swallow it.
	if err := killProcess(1 << 30); err == nil {
		t.Fatal("killProcess on a made-up pid unexpectedly succeeded")
	}
}
