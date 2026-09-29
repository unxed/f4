package update

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// StartCheckTimeout bounds the installed build's --version run. It is wide:
// the first start of a freshly written binary can wait on an antivirus scan,
// and a check that gives up too early rolls back a good update.
var StartCheckTimeout = 2 * time.Minute

// CheckInstalled runs the build Install has just put in place with
// --version, and fails unless it exits cleanly within StartCheckTimeout.
// Callers put the previous build back when it fails.
//
// An update that installs a build which does not start here -- the wrong
// architecture, a loader or libc it cannot use (#1381), an archive that is
// not f4 -- leaves the user without an f4 that could fetch the fix. Run
// before the old process gives way, the check keeps the one that works.
//
// A variable so that tests, whose archives hold no real program, can stand
// in for it.
var CheckInstalled = checkInstalled

func checkInstalled() error {
	exe, err := targetExecutable()
	if err != nil {
		return err
	}
	return checkStarts(exe)
}

func checkStarts(exe string) error {
	cmd := SelfCommand(exe, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	hideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("the new build does not start: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(StartCheckTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			if printed := strings.TrimSpace(out.String()); printed != "" {
				return fmt.Errorf("the new build does not start: %w\n%s", err, printed)
			}
			return fmt.Errorf("the new build does not start: %w", err)
		}
		return nil
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("the new build did not finish `f4 --version` within %s", StartCheckTimeout)
	}
}
