package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTraceFileArg(t *testing.T) {
	for _, tc := range []struct {
		name, flagVal, next, want string
		wantConsumed              bool
	}{
		{name: "after equals", flagVal: "t.out", next: "--version", want: "t.out"},
		{name: "next word", next: "t.out", want: "t.out", wantConsumed: true},
		{name: "next word is a switch", next: "--version"},
		{name: "nothing follows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, consumed := traceFileArg(tc.flagVal, tc.next)
			if got != tc.want || consumed != tc.wantConsumed {
				t.Errorf("traceFileArg(%q, %q) = %q, %v; want %q, %v", tc.flagVal, tc.next, got, consumed, tc.want, tc.wantConsumed)
			}
		})
	}
}

func TestStallWatchdogLimit(t *testing.T) {
	for _, tc := range []struct {
		name, flagVal, next string
		want                time.Duration
		wantConsumed        bool
		wantErr             bool
	}{
		{name: "after equals", flagVal: "500ms", want: 500 * time.Millisecond},
		{name: "bad value after equals was meant as one", flagVal: "nonsense", wantErr: true},
		{name: "next word is a duration", next: "1s", want: time.Second, wantConsumed: true},
		// The whole point of the optional value: a file named on the command
		// line is left for the panels rather than eaten by the switch.
		{name: "next word is a filename", next: "notes.txt", want: defaultStallWatchdogLimit},
		{name: "nothing follows", want: defaultStallWatchdogLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, consumed, err := stallWatchdogLimit(tc.flagVal, tc.next)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want || consumed != tc.wantConsumed {
				t.Errorf("stallWatchdogLimit(%q, %q) = %v, %v; want %v, %v", tc.flagVal, tc.next, got, consumed, tc.want, tc.wantConsumed)
			}
		})
	}
}

func TestArgAfter(t *testing.T) {
	args := []string{"f4", "--trace", "t.out"}
	if got := argAfter(args, 1); got != "t.out" {
		t.Errorf("argAfter = %q", got)
	}
	if got := argAfter(args, 2); got != "" {
		t.Errorf("argAfter past the end = %q, want empty", got)
	}
}

func TestArmDiagnostics_NothingAsked(t *testing.T) {
	stop, notice, err := armDiagnostics("", 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Errorf("a run that asked for nothing was told %q", notice)
	}
	stop()
}

func TestArmDiagnostics_ArmsTheWatchdogAndSaysWhere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crashes")
	stop, notice, err := armDiagnostics("", 300*time.Millisecond, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	logPath := filepath.Join(dir, "stall-watchdog.log")
	if !strings.Contains(notice, logPath) {
		t.Errorf("the notice does not name the log file: %q", notice)
	}
	if !strings.Contains(notice, "armed") {
		t.Errorf("the notice does not say it armed: %q", notice)
	}
	// Which frames are watched changes what an empty log means.
	if !strings.Contains(notice, "editor frames") {
		t.Errorf("the notice does not say what it watches: %q", notice)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("the log file is not there: %v", err)
	}
}

// A watchdog that cannot write its log collects nothing, and the user would
// otherwise wait for a freeze and find an empty folder.
func TestArmDiagnostics_SaysWhenTheWatchdogCouldNotArm(t *testing.T) {
	// A file where the directory should be: MkdirAll cannot make one, on
	// every platform.
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop, notice, err := armDiagnostics("", 300*time.Millisecond, filepath.Join(blocked, "crashes"))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.Contains(notice, "not armed") {
		t.Errorf("a watchdog that could not arm reported %q", notice)
	}
}

func TestArmDiagnostics_StartsAndStopsATrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.out")
	stop, _, err := armDiagnostics(path, 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stop()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the trace file was not written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the trace file is empty")
	}
}

func TestArmDiagnostics_ReportsATraceItCannotWrite(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop, _, err := armDiagnostics(filepath.Join(blocked, "trace.out"), 0, t.TempDir())
	if err == nil {
		stop()
		t.Fatal("a trace that could not be created was reported as started")
	}
}

// The default build has no vtui hook to bind and says so; the tagged build
// replaces this with the half that binds it and reports true.
func TestBindFrameWatch(t *testing.T) {
	if bindFrameWatch() {
		t.Error("the default build claimed to have bound a vtui hook it does not have")
	}
}

func TestDiagnosticFlags_Apply(t *testing.T) {
	t.Run("trace takes the word after it", func(t *testing.T) {
		var d diagnosticFlags
		consumed, err := d.apply("--trace", "", "t.out")
		if err != nil || consumed != 1 || d.tracePath != "t.out" {
			t.Errorf("apply = %d, %v; tracePath %q", consumed, err, d.tracePath)
		}
	})
	t.Run("trace leaves a switch alone", func(t *testing.T) {
		var d diagnosticFlags
		consumed, err := d.apply("--trace", "", "--version")
		if err != nil || consumed != 0 || d.tracePath != "" {
			t.Errorf("apply = %d, %v; tracePath %q", consumed, err, d.tracePath)
		}
	})
	t.Run("watchdog takes a duration", func(t *testing.T) {
		var d diagnosticFlags
		consumed, err := d.apply("--stall-watchdog", "", "1s")
		if err != nil || consumed != 1 || d.stallLimit != time.Second {
			t.Errorf("apply = %d, %v; limit %v", consumed, err, d.stallLimit)
		}
	})
	t.Run("watchdog leaves a filename alone", func(t *testing.T) {
		var d diagnosticFlags
		consumed, err := d.apply("--stall-watchdog", "", "notes.txt")
		if err != nil || consumed != 0 || d.stallLimit != defaultStallWatchdogLimit {
			t.Errorf("apply = %d, %v; limit %v", consumed, err, d.stallLimit)
		}
	})
	t.Run("a value written after = was meant as one", func(t *testing.T) {
		var d diagnosticFlags
		if _, err := d.apply("--stall-watchdog", "nonsense", ""); err == nil {
			t.Error("a bad duration after = was accepted")
		}
	})
	t.Run("another switch is not ours", func(t *testing.T) {
		var d diagnosticFlags
		consumed, err := d.apply("--version", "", "x")
		if err != nil || consumed != 0 || d.wanted() {
			t.Errorf("apply(--version) = %d, %v; wanted %v", consumed, err, d.wanted())
		}
	})
}

func TestDiagnosticFlags_Wanted(t *testing.T) {
	var none diagnosticFlags
	if none.wanted() {
		t.Error("a command line that asked for nothing wants something")
	}
	if !(diagnosticFlags{tracePath: "t.out"}).wanted() {
		t.Error("--trace alone was not wanted")
	}
	if !(diagnosticFlags{stallLimit: time.Second}).wanted() {
		t.Error("--stall-watchdog alone was not wanted")
	}
}

func TestDiagnosticFlags_Arm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crashes")
	d := diagnosticFlags{stallLimit: 300 * time.Millisecond}
	stop := d.arm(dir)
	defer stop()
	if _, err := os.Stat(filepath.Join(dir, "stall-watchdog.log")); err != nil {
		t.Errorf("arm did not start the watchdog: %v", err)
	}
}

// A run started to collect evidence must not carry on quietly collecting
// none, so a path that cannot be written stops it.
func TestDiagnosticFlags_ArmPanicsOnAPathItCannotWrite(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("a trace that could not be created did not stop the run")
		}
	}()
	d := diagnosticFlags{tracePath: filepath.Join(blocked, "trace.out")}
	d.arm(t.TempDir())()
}

// The UI of a terminal session is drawn by the daemon, so a profile of the
// process the user ran is empty (#884): the switches have to reach the daemon.
func TestServerDiagnosticArgsReachTheDaemonWithItsOwnFiles(t *testing.T) {
	if got := serverDiagnosticArgs("", diagnosticFlags{}); len(got) != 0 {
		t.Fatalf("nothing was asked for, yet the daemon gets %q", got)
	}
	got := serverDiagnosticArgs("/tmp/f4.prof", diagnosticFlags{tracePath: "/tmp/f4.trace", stallLimit: 300 * time.Millisecond})
	want := []string{"--cpuprofile", "/tmp/f4.prof.server", "--trace", "/tmp/f4.trace.server", "--stall-watchdog", "300ms"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// What the daemon is given has to be read back by the same parser.
	limit, _, err := stallWatchdogLimit("", got[5])
	if err != nil || limit != 300*time.Millisecond {
		t.Fatalf("the stall limit given to the daemon does not read back: %v, %v", limit, err)
	}
}
