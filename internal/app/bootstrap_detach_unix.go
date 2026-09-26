//go:build !windows

package app

import (
	"os"
	"syscall"

	"github.com/unxed/f4/internal/update"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func checkAndDetach(attached bool) {
	if attached || os.Getenv("F4_DETACHED") == "1" {
		return
	}

	exe, err := update.Executable()
	if err != nil {
		return
	}

	cmd := update.SelfCommand(exe, os.Args[1:]...)
	cmd.Env = append(cmd.Env, "F4_DETACHED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// The copy is on its own: stdin is closed off, and its stdout and
	// stderr start at /dev/null so it never holds the terminal it was
	// launched from. Once it has its crash log it points both at that
	// (see redirectDetachedStdout), so that whatever a library prints on
	// its way out is recorded rather than dropped.
	null, _ := os.Open(os.DevNull)
	if null != nil {
		cmd.Stdin = null
		cmd.Stdout = null
		cmd.Stderr = null
	}

	if err := cmd.Start(); err == nil {
		os.Exit(0)
	}
}

// redirectDetachedStdout sends the standard output of a detached copy to
// wherever its standard error already goes -- the crash log, once
// vtui.SetupStderrLog has pointed fd 2 at it. Call it right after that.
//
// A detached GUI process has no terminal, so its stdout is /dev/null, and
// a library that reports its exit reason there -- neurlang/wayland prints
// the display loop's error with fmt.Println -- leaves an empty crash log
// and a window that simply vanishes. Only the detached copy is touched:
// in the terminal backends stdout is the screen and must stay so.
func redirectDetachedStdout() {
	if !detachedStdoutGoesToCrashLog(os.Getenv("F4_DETACHED") == "1", term.IsTerminal(int(os.Stdout.Fd()))) {
		return
	}
	_ = unix.Dup2(2, 1)
}

// detachedStdoutGoesToCrashLog decides that question from the two facts it
// depends on, so it can be checked without touching this process's own
// descriptors.
//
// The flag alone is not enough. It travels in the environment, and a process
// that inherited it from a detached ancestor is not itself detached -- a
// terminal it can print on is the proof. Redirecting there sends the output
// to a file nobody is looking at: `f4 --version` at the command line of a
// GUI f4 printed its version into the outer session's crash log and left the
// terminal blank (issue #1151). The terminal f4 exports no longer hands the
// flag on, and this keeps the flag from being believed on its own whatever
// other path it arrives by.
func detachedStdoutGoesToCrashLog(detached, stdoutIsTerminal bool) bool {
	return detached && !stdoutIsTerminal
}

// redirectConsoleWineStderr and cleanupWineStderrLog exist only to fix a
// Wine-specific descriptor mixup (WINE.md §18.4): on every other GOOS, Go's
// own os.Stderr already is the terminal or whatever the shell redirected it
// to, and there is no second, Wine-owned copy of descriptor 2 to repoint.
func redirectConsoleWineStderr() {}
func cleanupWineStderrLog()      {}
