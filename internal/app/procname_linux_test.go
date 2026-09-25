//go:build linux

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f4 #1390: a Linux release binary re-execs itself through the dynamic
// loader before main runs (goffi_universal, see procname_linux.go's doc
// comment), which left the kernel's comm field as the loader's own name.
// setProcessName must set it back to "f4" regardless. The check runs in a
// subprocess via TestMain's setProcessNameTestOutEnv short-circuit rather
// than in-process, because PR_SET_NAME only affects the calling thread and
// only the process's leader thread's comm is what ps/top/a window manager
// read as the process name (see setProcessName's doc comment); TestMain's
// check runs before m.Run() touches the scheduler, the same guarantee
// Main() itself relies on.
func TestSetProcessNameSetsComm(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "comm")
	// #nosec G204 G702 -- os.Args[0] is the current test binary, no arguments.
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), setProcessNameTestOutEnv+"="+outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper process: %v\n%s", err, out)
	}
	comm, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read helper result: %v", err)
	}
	if got := strings.TrimSpace(string(comm)); got != "f4" {
		t.Errorf("comm = %q, want %q", got, "f4")
	}
}
