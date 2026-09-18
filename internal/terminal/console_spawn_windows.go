//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/unxed/vtui"
	"golang.org/x/sys/windows"
)

// Handing a child explicit standard handles is what makes its output vanish
// on ReactOS (f4 issue #513).
//
// Go's os/exec always sets STARTF_USESTDHANDLES and fills StdInput/StdOutput/
// StdError from the *exec.Cmd (syscall/exec_windows.go:342), then restricts
// inheritance to exactly those handles with PROC_THREAD_ATTRIBUTE_HANDLE_LIST.
// On ReactOS 0.4.16 that translation goes wrong once the process owns more
// than one console screen buffer -- which f4 always does, because
// vtui.NewWin32ConsoleRenderer makes one for the panels. Measured from inside
// the child (a probe run from f4's own command line, WINE.md §17.3e): the
// child's STD_OUTPUT_HANDLE arrives invalid, GetFileType and GetConsoleMode
// on it fail with ERROR_INVALID_HANDLE, and every write returns the same --
// so cmd.exe exits 0 having printed nothing anywhere. Its standard input is
// broken the same way, which is why "dir & pause" never took a keystroke.
//
// cmd.exe itself does not do that: it starts programs with CreateProcessW,
// bInheritHandles=TRUE and no STARTF_USESTDHANDLES at all, and lets the
// console host attach the child to the console. That path works on ReactOS
// every time (the same probe launched from cmd.exe gets a working handle, and
// the standalone repro in WINE.md §17.3e survives every iteration on it).
// So that is what this does.
//
// It is only usable when the child really should get f4's console as it is:
// if f4's own stdio has been redirected, the handles carry that redirection
// and dropping them would send the child's output to the console instead of
// the file. hostConsoleSpawnUsable checks exactly that.

// ErrConsoleSpawnUnavailable means the caller should fall back to os/exec:
// this is not a console session, or the escape hatch is set. It is not a
// failure of the command and must never be reported to the user as one.
var ErrConsoleSpawnUnavailable = errors.New("terminal: host console spawn not applicable")

// LegacyChildStdioEnvVar, set to anything non-empty, forces the old os/exec
// path with explicit standard handles. It is the escape hatch for a host
// where letting the console host wire the child up turns out to be worse
// than the bug this avoids.
const LegacyChildStdioEnvVar = "F4_LEGACY_CHILD_STDIO"

// isConsoleHandle reports whether h is a real console handle.
func isConsoleHandle(h windows.Handle) bool {
	if h == 0 || h == windows.InvalidHandle {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(h, &mode) == nil
}

// hostConsoleSpawnUsable reports whether a child may be started without
// explicit standard handles: both f4's own input and output must still be the
// console, or the child would lose a redirection the user asked for.
func hostConsoleSpawnUsable() bool {
	if os.Getenv(LegacyChildStdioEnvVar) != "" {
		return false
	}
	return isConsoleHandle(windows.Handle(os.Stdin.Fd())) &&
		isConsoleHandle(windows.Handle(os.Stdout.Fd()))
}

// RunOnHostConsole runs the shell command in dir on f4's own console, started
// the way cmd.exe starts a program: no STARTF_USESTDHANDLES, inheritance on,
// console wiring left to the console host. It blocks until the child exits.
func RunOnHostConsole(dir, shell, flag, command string) error {
	if !hostConsoleSpawnUsable() {
		return ErrConsoleSpawnUnavailable
	}
	cmdLine := BuildShellCmdLine(shell, flag, command)
	argv, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return ErrConsoleSpawnUnavailable
	}
	var dirp *uint16
	if dir != "" {
		if dirp, err = syscall.UTF16PtrFromString(dir); err != nil {
			return ErrConsoleSpawnUnavailable
		}
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	// si.Flags stays zero on purpose: no STARTF_USESTDHANDLES, no explicit
	// handles. That is the whole fix; see the comment at the top of the file.
	var pi syscall.ProcessInformation

	if err := syscall.CreateProcess(nil, argv, nil, nil, true, 0, nil, dirp, &si, &pi); err != nil {
		vtui.DebugLog("EXEC513: CreateProcessW(%q) failed: %v; falling back to os/exec", cmdLine, err)
		return ErrConsoleSpawnUnavailable
	}
	defer syscall.CloseHandle(pi.Thread)
	defer syscall.CloseHandle(pi.Process)

	if _, err := syscall.WaitForSingleObject(pi.Process, syscall.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(windows.Handle(pi.Process), &code); err != nil {
		// The child is gone and its status is unreadable; treat that as a
		// clean run rather than inventing a failure the user never saw.
		return nil
	}
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}
