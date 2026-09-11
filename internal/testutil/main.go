package testutil

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

var configDir string

// taskPumpExitTimeout is how long Main lets stopped task pumps finish
// returning before it calls them leaked. The wait ends as soon as the last one
// is gone, so a clean run spends none of it; the margin is for a loaded -race
// runner, and only a real leak waits it out.
const taskPumpExitTimeout = 5 * time.Second

// ConfigDir is the temporary profile directory Main installed for this test
// binary, or "" when one could not be created. A package whose configuration
// lookup does not read the environment points its own seam at this.
func ConfigDir() string { return configDir }

// Main runs a package's tests inside the isolation every package in this
// repository needs, and returns the exit code its TestMain should pass to
// os.Exit.
//
// Main owns what is the same everywhere: a silent screen so no test can draw
// on the developer's terminal, a clipboard that stays in vtui's process-local
// buffer instead of reaching pbcopy or the CI runner's X server, a profile
// directory of its own, and — after the run — the check that no frame manager
// or task pump outlived it.
//
// before installs the calling package's own seams and runs once the isolation
// is in place, so it may read ConfigDir. after runs its teardown; returning an
// error from it fails the run. Either may be nil.
func Main(m *testing.M, before func(), after func() error) int {
	baseFrameManager := vtui.FrameManager
	baseFrameManager.Init(vtui.NewSilentScreenBuf())

	// The machine's clipboard is global, slow to reach (pbcopy/xclip) and
	// shared with whatever else the CI runner is doing; tests keep clipboard
	// traffic in vtui's process-local buffer instead, and skip the OSC 52
	// stdout fallback that used to spray base64 into the test logs. A test
	// that genuinely targets the OS clipboard switches the knob back off for
	// its own scope.
	vtui.SkipOSClipboard(true)
	vtui.DisableTerminalClipboard()

	tmpDir, tmpErr := os.MkdirTemp("", "f4-test-config-*")
	if tmpErr == nil {
		configDir = tmpDir
		// XDG_CONFIG_HOME/APPDATA cover Linux and Windows; os.UserConfigDir
		// ignores both on darwin, so a package seam pointed at ConfigDir is
		// what actually isolates the suite from the developer's real profile
		// there.
		for _, name := range []string{"XDG_CONFIG_HOME", "APPDATA"} {
			if setErr := os.Setenv(name, tmpDir); setErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "set %s: %v\n", name, setErr)
				_ = os.RemoveAll(tmpDir) // Process exit makes cleanup failure uninteresting.
				return 1
			}
		}
	}

	if before != nil {
		before()
	}

	result := m.Run()

	if after != nil {
		if err := after(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "test teardown: %v\n", err)
			result = 1
		}
	}

	globalFrameManager := vtui.FrameManager
	if globalFrameManager != nil {
		CloseFrameManagerFrames(globalFrameManager)
		globalFrameManager.Shutdown()
	}
	if baseFrameManager != globalFrameManager {
		_, _ = fmt.Fprintln(os.Stderr, "vtui.FrameManager was not restored to the TestMain manager")
		CloseFrameManagerFrames(baseFrameManager)
		baseFrameManager.Shutdown()
		result = 1
	}

	taskPumps, goroutineProfile, profileErr := WaitForTaskPumpExit(0, taskPumpExitTimeout)
	if profileErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "capture goroutine profile after vtui shutdown: %v\n", profileErr)
		result = 1
	} else if taskPumps > 0 {
		_, _ = fmt.Fprintf(os.Stderr,
			"vtui task-pump goroutine leak: %d startTaskPump goroutine(s) remain %v after test teardown; want 0\n%s",
			taskPumps, taskPumpExitTimeout, goroutineProfile)
		result = 1
	}

	if tmpErr == nil {
		if removeErr := os.RemoveAll(tmpDir); removeErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "remove test config directory: %v\n", removeErr)
			result = 1
		}
	}
	return result
}
