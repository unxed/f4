//go:build !windows

package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUnixSessionIdentityHelpersCoverLiveAndInvalidProcesses(t *testing.T) {
	if !SupportsBackgrounding() {
		t.Fatal("Unix session manager does not advertise backgrounding")
	}
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("the current test process was reported dead")
	}
	for _, pid := range []int{0, -1} {
		if isProcessAlive(pid) {
			t.Fatalf("invalid process %d was reported alive", pid)
		}
	}
	dir := sessionDir()
	if filepath.Base(dir) == "" {
		t.Fatalf("sessionDir() = %q", dir)
	}
}

func TestUnixSessionStartupLogTrimsOnlyTrailingNewlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.log")
	if err := os.WriteFile(path, []byte("first\nsecond\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readStartupLog(path); got != "first\nsecond" {
		t.Fatalf("readStartupLog() = %q, want %q", got, "first\nsecond")
	}
	if got := readStartupLog(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("missing startup log = %q, want empty", got)
	}
	removeStartupLog(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("removeStartupLog left %q (err=%v)", path, err)
	}
}

func TestWaitForDaemonSocketReapsExitedProcess(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "never-created.sock")
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if waitForDaemonSocket(cmd, sockPath) {
		t.Fatal("waitForDaemonSocket returned true for an exited process without a socket")
	}
}
