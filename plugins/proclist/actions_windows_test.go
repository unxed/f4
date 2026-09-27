//go:build windows

package proclist

import (
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPriorityIndexMapsEachKnownClass(t *testing.T) {
	for want, class := range priorityLadder {
		if got := priorityIndex(class); got != want {
			t.Errorf("priorityIndex(%#x) = %d, want %d", class, got, want)
		}
	}
}

func TestPriorityIndexFallsBackToNormalForAnUnknownClass(t *testing.T) {
	// PROCESS_MODE_BACKGROUND_BEGIN (0x00100000) sets a bit GetPriorityClass
	// can report back that priorityLadder does not itself name.
	if got, want := priorityIndex(0x00100000), 2; got != want {
		t.Fatalf("priorityIndex(unknown) = %d, want %d (Normal)", got, want)
	}
}

// startSleepHelper starts a short-lived, otherwise idle child process for
// killProcess/changePriority to act on -- something this test can safely
// terminate without touching the test binary's own process. timeout.exe
// ships with every supported Windows version, including GitHub's runner
// image.
func startSleepHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("timeout.exe", "/T", "30", "/NOBREAK")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start the \"timeout.exe\" helper process: %v", err)
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

func TestChangePriorityLowersThenRaisesTheChild(t *testing.T) {
	cmd := startSleepHelper(t)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	pid := cmd.Process.Pid

	loweredLevel, err := changePriority(pid, false)
	if err != nil {
		t.Fatalf("changePriority(lower): %v", err)
	}

	raisedLevel, err := changePriority(pid, true)
	if err != nil {
		t.Fatalf("changePriority(raise): %v", err)
	}
	if raisedLevel != loweredLevel+1 {
		t.Fatalf("changePriority(raise) level = %d, want %d (one rung above %d)", raisedLevel, loweredLevel+1, loweredLevel)
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess: %v", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	class, err := windows.GetPriorityClass(handle)
	if err != nil {
		t.Fatalf("GetPriorityClass: %v", err)
	}
	if class != priorityLadder[raisedLevel] {
		t.Fatalf("GetPriorityClass = %#x, want %#x (ladder rung %d)", class, priorityLadder[raisedLevel], raisedLevel)
	}
}

func TestSuspendResumeReportUnsupported(t *testing.T) {
	if suspendResumeSupported {
		t.Fatal("suspendResumeSupported must be false on Windows (no supported API; f4#312 part 3)")
	}
	if err := suspendProcess(0); err == nil {
		t.Fatal("suspendProcess unexpectedly succeeded")
	}
	if err := resumeProcess(0); err == nil {
		t.Fatal("resumeProcess unexpectedly succeeded")
	}
}
