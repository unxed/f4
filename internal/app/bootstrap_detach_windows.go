//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	winescape "github.com/unxed/libwinescape/go"

	"github.com/unxed/f4/internal/gui"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

func checkAndDetach(attached bool) {
	if attached || os.Getenv("F4_DETACHED") == "1" {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "F4_DETACHED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}

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

// redirectDetachedStdout closes the one way a detached copy running under
// Wine still reaches the terminal it was launched from. Call it right after
// vtui.SetupStderrLog, as on Unix. On native Windows it does nothing: the
// copy has no console, and its crash log already takes everything that
// matters.
//
// checkAndDetach hands the copy NUL for stdin, stdout and stderr. Wine turns
// the first two into the copy's Unix descriptors 0 and 1, but leaves Unix
// descriptor 2 as it was in the launching process: spawn_process in
// dlls/ntdll/unix/process.c only ever sets 0 and 1. Wine prints its own
// diagnostics -- fixme and err lines, and the MESSAGE lines WINEDEBUG does
// not silence -- to that descriptor, not to the Windows handle, so they kept
// going to the launcher's terminal. When nobody reads that terminal, as far2l
// does not once the command it ran has returned, its buffer fills and the
// thread that wrote next blocks for good: the window stopped answering after
// a few dozen keystrokes (issue #474). Pointing descriptor 2 at /dev/null
// gives Wine the NUL the copy was asked to have.
func redirectDetachedStdout() {
	detached := os.Getenv("F4_DETACHED") == "1"
	// hostmode.Allowed() is the UseWinescape setting: with it off, f4 uses
	// libwinescape nowhere, this fix included. A copy started under Wine by a
	// user who turned it off keeps Wine's descriptor 2, which is the pre-#474
	// behaviour and their choice to make.
	if !detached || !hostmode.Allowed() || !winescape.Available() {
		return
	}
	var tio winescape.Termios
	stdoutIsTerminal := winescape.Tcgetattr(1, &tio) == nil
	if !detachedWineStderrGoesToNull(detached, stdoutIsTerminal) {
		return
	}
	null, err := winescape.Open("/dev/null", winescape.O_WRONLY|winescape.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	if null != 2 {
		_ = winescape.Dup3(null, 2, 0)
		_ = winescape.Close(null)
	}
}

// detachedWineStderrGoesToNull decides whether this process is the copy
// checkAndDetach started, from the two facts that say so. The flag alone is
// not proof: it travels in the environment, and a process that inherited it
// has a terminal on its standard output (the same trap as issue #1151 on
// Unix), where Wine's messages belong.
func detachedWineStderrGoesToNull(detached, stdoutIsTerminal bool) bool {
	return detached && !stdoutIsTerminal
}

// wineStderrLogPath is the session's own Wine-diagnostics file, set by
// redirectConsoleWineStderr and removed by cleanupWineStderrLog if the
// session never wrote a byte to it. Empty means no redirect happened --
// GUI, detached, non-Wine, or UseWinescape=0 -- and cleanup is a no-op.
var wineStderrLogPath string

// wineStderrLogKeep is how many non-empty session logs redirectConsoleWineStderr
// keeps around; see its comment for why they are kept at all instead of
// deleted like vtui's own empty stderr logs.
const wineStderrLogKeep = 5

// redirectConsoleWineStderr points Wine's own unix descriptor 2 at a session
// log file instead of leaving it on the terminal f4 is about to start
// drawing on. Call it once, right before the process commits to console
// mode (terminal.ManageSessions) -- the one case redirectDetachedStdout
// above does not cover, because there descriptor 2 is not a stray inherited
// handle from a detached copy but the very terminal the console session is
// about to share the screen with (WINE.md §18.4, "read access error").
//
// Unlike the detached case, plain "/dev/null" is wrong here: this is the
// session's own diagnostics, and losing them would make a future
// #474-shaped bug unreachable by the same means #474 itself was found (the
// fixme/err line read by eye off the terminal). So they go to their own
// file instead, crashes/wine_<timestamp>_<pid>.log -- named and pruned like
// vtui's stderr_*.log (SetupStderrLog/CleanupStderrLog) but kept as a
// separate file on purpose: folding Wine's routine fixme/err chatter into
// the same file as a Go panic would make the crash log noisy on every
// single Wine start, which is exactly the regression §16.3 fixed by moving
// f4's own two startup-time debug lines out of stderr entirely. A non-empty
// crash log already means "look here"; a non-empty Wine log should not
// carry the same weight.
//
// f4 deliberately does not set WINEDEBUG for itself or for any child it
// spawns: MESSAGE() -- the source of the #474 "read access error" line --
// calls wine_dbg_printf directly and ignores WINEDEBUG regardless
// (include/wine/debug.h), so the variable would not quiet the one thing
// this exists to catch; and in posix mode a "child" is a host process the
// user asked f4 to run, for whom WINEDEBUG is exactly the kind of
// environment variable a program is entitled to see set exactly as the
// user -- not f4 -- left it. terminal.HostStderrFD carries a fresh
// descriptor onto the same terminal forward so that an inherited host
// command (simple-inline) still gets it, not this file, as its own
// descriptor 2.
//
// Left alone on purpose, matching WINE.md §18.4's decision table: a user
// who already redirected descriptor 2 themselves (`f4 2>somewhere.log`),
// and `--attached` GUI under wineconsole, where descriptor 2 belongs to the
// launching terminal and descriptor 1 does not -- there is nothing on that
// second terminal for f4 to protect.
func redirectConsoleWineStderr() {
	if !hostmode.Allowed() || !winescape.Available() {
		return
	}
	var outSt, errSt winescape.Stat_t
	if winescape.Fstat(1, &outSt) != nil || winescape.Fstat(2, &errSt) != nil {
		return
	}
	if outSt.Dev != errSt.Dev || outSt.Ino != errSt.Ino {
		// Different devices already: a user redirect, or an --attached
		// wineconsole session where descriptor 2 is the launcher's
		// terminal and descriptor 1 is f4's own. Nothing to protect.
		return
	}
	var tio winescape.Termios
	if winescape.Tcgetattr(1, &tio) != nil {
		// Not a terminal at all (piped/redirected stdout): no screen for
		// Wine's messages to interleave with.
		return
	}

	// A fresh descriptor onto the same terminal, captured before descriptor
	// 2 is repointed, so a host command f4 goes on to run still reaches it.
	// /proc/self/fd is Linux-specific; on the other libwinescape host (BSD)
	// this simply fails and the whole redirect is skipped below, leaving
	// today's behaviour (issue #474's fix already covers the detached case
	// on every host).
	target := make([]byte, 256)
	n, err := winescape.Readlink("/proc/self/fd/2", target)
	if err != nil {
		return
	}
	termFD, err := winescape.Open(string(target[:n]), winescape.O_WRONLY|winescape.O_CLOEXEC, 0)
	if err != nil {
		return
	}

	logPath, logFD, err := openWineStderrLog()
	if err != nil {
		_ = winescape.Close(termFD)
		return
	}
	if logFD != 2 {
		if err := winescape.Dup3(logFD, 2, 0); err != nil {
			_ = winescape.Close(logFD)
			_ = winescape.Close(termFD)
			return
		}
		_ = winescape.Close(logFD)
	}
	terminal.HostStderrFD = termFD
	wineStderrLogPath = logPath
	vtui.DebugLog("WINE: descriptor 2 now goes to %s; a host command still gets the terminal (fd %d)", logPath, termFD)
}

// openWineStderrLog creates this session's Wine-diagnostics file under
// vtui.CrashDirFull and prunes old ones down to wineStderrLogKeep-1, making
// room for the one just created. Pruning uses the Windows-side path through
// plain os.*: the crash directory is an ordinary Win32 directory regardless
// of hostmode.Posix (WINE.md §18.2 keeps the profile in the wineprefix in
// both personalities), so there is no need to go through libwinescape for
// anything but the fd this returns.
//
// Empty logs from a still-running instance are a known, accepted gap: an
// empty file is otherwise indistinguishable from stale debris, and this
// runs before the new session's own file exists, so it cannot prune its own
// future output by mistake -- only, in the narrow case of two instances
// starting at once, a sibling's not-yet-written file.
func openWineStderrLog() (path string, fd int, err error) {
	dir := vtui.CrashDirFull
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", -1, err
	}
	entries, _ := os.ReadDir(dir)
	var nonEmpty []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "wine_") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() == 0 {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		nonEmpty = append(nonEmpty, name)
	}
	sort.Strings(nonEmpty) // the timestamp prefix sorts chronologically
	for len(nonEmpty) > wineStderrLogKeep-1 {
		_ = os.Remove(filepath.Join(dir, nonEmpty[0]))
		nonEmpty = nonEmpty[1:]
	}

	path = filepath.Join(dir, fmt.Sprintf("wine_%s_%d.log", time.Now().UTC().Format("20060102-150405"), os.Getpid()))
	unixPath, ok := gui.HostUnixPath(path)
	if !ok {
		return "", -1, fmt.Errorf("resolve host path for %s", path)
	}
	fd, err = winescape.Open(unixPath, winescape.O_CREAT|winescape.O_WRONLY|winescape.O_APPEND|winescape.O_CLOEXEC, 0644)
	if err != nil {
		return "", -1, err
	}
	return path, fd, nil
}

// cleanupWineStderrLog removes this session's Wine-diagnostics file if
// nothing was ever written to it, the same rule vtui.CleanupStderrLog
// applies to its own stderr_*.log. Call it wherever that is called: both
// are cheap, empty-only, best-effort housekeeping over the same directory.
func cleanupWineStderrLog() {
	if wineStderrLogPath == "" {
		return
	}
	if info, err := os.Stat(wineStderrLogPath); err == nil && info.Size() == 0 {
		_ = os.Remove(wineStderrLogPath)
	}
}
