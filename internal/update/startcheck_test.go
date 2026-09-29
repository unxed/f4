package update

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// asF4 makes this test binary stand in for an installed f4 when it is
// started by checkStarts: "ok" answers --version, "fail" exits with an
// error, "hang" never answers.
const asF4 = "F4_UPDATE_TEST_AS_F4"

func TestMain(m *testing.M) {
	switch os.Getenv(asF4) {
	case "ok":
		fmt.Println("v9.9.9 [2030-01-01 00:00]")
		os.Exit(0)
	case "fail":
		fmt.Println("symbol lookup error: undefined symbol: pthread_attr_getstacksize")
		os.Exit(127)
	case "hang":
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

func TestCheckStartsRunsTheInstalledBuild(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv(asF4, "ok")
	if err := checkStarts(self); err != nil {
		t.Errorf("a build that answers --version was refused: %v", err)
	}

	t.Setenv(asF4, "fail")
	err = checkStarts(self)
	if err == nil || !strings.Contains(err.Error(), "undefined symbol") {
		t.Errorf("a build that fails at start passed, or its output was lost: %v", err)
	}

	oldTimeout := StartCheckTimeout
	StartCheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { StartCheckTimeout = oldTimeout })
	t.Setenv(asF4, "hang")
	if err := checkStarts(self); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("a build that never answers passed: %v", err)
	}

	if err := checkStarts(self + ".missing"); err == nil {
		t.Error("a missing build passed")
	}
}

// The CLI keeps the build that works: an update whose new build does not
// start is rolled back, and not recorded as installed.
func TestRunCLIPutsThePreviousBuildBackWhenTheNewOneDoesNotStart(t *testing.T) {
	exe := cliUpdateFixture(t, targz(t, map[string]string{"f4": "new f4"}))
	CheckInstalled = func() error { return errors.New("the new build does not start: exit status 127") }

	saves := 0
	if got := RunCLI("", Settings{Channel: ChannelNightly}, Build{}, func(Settings) { saves++ }); got != 1 {
		t.Fatalf("RunCLI() = %d, want 1", got)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old f4" {
		t.Errorf("binary = %q, want the previous build back", got)
	}
	if saves != 0 {
		t.Errorf("settings saved %d times, want none", saves)
	}
}
