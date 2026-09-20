package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/trace"
	"time"

	"github.com/unxed/f4/internal/stallwatch"
)

// defaultStallWatchdogLimit is what --stall-watchdog watches for when it is
// given no duration of its own. Long enough that no ordinary frame reaches it,
// short enough that a freeze a user would notice does.
const defaultStallWatchdogLimit = 250 * time.Millisecond

// traceFileArg is the path --trace was given: the value after its "=", or the
// next word when it is not itself a switch. It reports whether that next word
// was taken, so the caller can step over it.
func traceFileArg(flagVal, next string) (path string, consumedNext bool) {
	if flagVal != "" {
		return flagVal, false
	}
	if next == "" || next[0] == '-' {
		return "", false
	}
	return next, true
}

// stallWatchdogLimit is the duration --stall-watchdog was given.
//
// The duration is optional, so the next word is taken only when it is one:
// "f4 --stall-watchdog notes.txt" opens notes.txt with the watchdog on its
// default rather than failing on a filename that is not a duration. A value
// written after "=" was meant as the duration, so a bad one is an error
// rather than something to step over.
func stallWatchdogLimit(flagVal, next string) (limit time.Duration, consumedNext bool, err error) {
	if flagVal != "" {
		d, err := time.ParseDuration(flagVal)
		if err != nil {
			return 0, false, err
		}
		return d, false, nil
	}
	if next != "" {
		if d, err := time.ParseDuration(next); err == nil {
			return d, true, nil
		}
	}
	return defaultStallWatchdogLimit, false, nil
}

// armDiagnostics starts whatever the command line asked for: an execution
// trace, the stall watchdog, or neither. It returns the function that closes
// them down, and the one line the user is told on the way past — the profile
// directory depends on whether this executable found a portable profile
// beside it, and a watchdog nobody can find the output of is no use.
//
// It is kept out of Main so that what it decides can be tested; Main itself
// wants a terminal.
func armDiagnostics(tracePath string, stallLimit time.Duration, crashesDir string) (stop func(), notice string, err error) {
	stop = func() {}
	if tracePath != "" {
		// #nosec G703 -- tracePath is the path the user typed after
		// --trace; writing where they asked is the whole feature.
		f, createErr := os.Create(tracePath)
		if createErr != nil {
			return stop, "", createErr
		}
		if startErr := trace.Start(f); startErr != nil {
			_ = f.Close()
			return stop, "", startErr
		}
		stop = func() {
			trace.Stop()
			_ = f.Close()
		}
	}
	if stallLimit > 0 {
		logPath := stallwatch.Start(crashesDir, stallLimit)
		if logPath == "" {
			// Saying so is the point: a watchdog that could not open its log
			// collects nothing, and the user would otherwise wait for a
			// freeze and find an empty folder.
			notice = fmt.Sprintf("stall watchdog could not write to %s; it is not armed", filepath.Clean(crashesDir))
			return stop, notice, nil
		}
		// Which frames are watched is worth saying: the ordinary build marks
		// the editor's own, and only a build with the vtui hook sees a freeze
		// anywhere else in the loop. A log with no entries means different
		// things in the two cases.
		scope := "editor frames"
		if bindFrameWatch() {
			scope = "the whole UI loop"
		}
		notice = fmt.Sprintf("stall watchdog armed at %v over %s; writing to %s", stallLimit, scope, logPath)
	}
	return stop, notice, nil
}

// argAfter is the word following index i on a command line, or "" when there
// is none. It keeps the switch cases from repeating the bounds check.
func argAfter(args []string, i int) string {
	if i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// diagnosticFlags is what a command line asked of the diagnostics, gathered as
// the switches are read.
//
// It is a type so that both halves -- reading the switches and acting on them
// -- sit outside Main. No test runs Main: it wants a terminal, so every line
// inside it is a line nothing checks.
type diagnosticFlags struct {
	tracePath  string
	stallLimit time.Duration
}

// apply reads one diagnostic switch and reports how many of the words after it
// were taken. The switches are read in a loop over the command line and only
// that loop knows where it is, so the count goes back rather than the index
// coming in.
func (d *diagnosticFlags) apply(name, flagVal, next string) (consumed int, err error) {
	switch name {
	case "--trace":
		path, tookNext := traceFileArg(flagVal, next)
		d.tracePath = path
		if tookNext {
			return 1, nil
		}
	case "--stall-watchdog":
		limit, tookNext, err := stallWatchdogLimit(flagVal, next)
		if err != nil {
			return 0, err
		}
		d.stallLimit = limit
		if tookNext {
			return 1, nil
		}
	}
	return 0, nil
}

// serverDiagnosticArgs are the switches that go on to the daemon a terminal
// session starts. The daemon writes next to the file that was asked for, with
// ".server" added: this process writes the file itself, and two processes on one
// profile would overwrite each other.
func serverDiagnosticArgs(cpuprofile string, d diagnosticFlags) []string {
	var args []string
	if cpuprofile != "" {
		args = append(args, "--cpuprofile", cpuprofile+".server")
	}
	if d.tracePath != "" {
		args = append(args, "--trace", d.tracePath+".server")
	}
	if d.stallLimit > 0 {
		args = append(args, "--stall-watchdog", d.stallLimit.String())
	}
	return args
}

// wanted reports whether the command line asked for any of this at all.
func (d diagnosticFlags) wanted() bool {
	return d.tracePath != "" || d.stallLimit > 0
}

// arm starts what was asked for and returns the function that closes it down,
// having told the user what it did. A path that cannot be written is fatal
// here: the run was started to collect evidence, and quietly collecting none
// is worse than not starting at all.
func (d diagnosticFlags) arm(crashesDir string) func() {
	stop, notice, err := armDiagnostics(d.tracePath, d.stallLimit, crashesDir)
	if err != nil {
		panic(err)
	}
	if notice != "" {
		// Said on the way past, before the UI takes the screen.
		fmt.Println(notice)
	}
	return stop
}
