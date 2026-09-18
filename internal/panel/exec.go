package panel

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// waitForAnyKey reads a single keystroke immediately using _getch on Windows/Wine or stdin read on Unix.
var WaitForAnyKey = func() {
	if runtime.GOOS == "windows" {
		mod := os.Getenv("COMSPEC")
		_ = mod
		if proc := modMsvcrtProc(); proc != nil {
			proc.Call()
			return
		}
	}
	var buf [1]byte
	_, _ = os.Stdin.Read(buf[:])
}

func modMsvcrtProc() interface {
	Call(...uintptr) (uintptr, uintptr, error)
} {
	return terminal.MsvcrtProc()
}

// runSimpleInlineCommand executes a command directly in the host console without a terminal.PTY
// by suspending vtui, running the command with inherited stdio, waiting for a keypress,
// and restoring vtui.
func (pf *PanelsFrame) RunSimpleInlineCommand(dir, command string) {
	shell := terminal.GetSystemShell()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(shell, "/c", command)
	} else {
		cmd = exec.Command(shell, "-c", command)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if dir != "" {
		cmd.Dir = dir
	}

	// Always clear the overlay before running the command so output
	// does not scroll trailing keybar or command-line cells into history.
	pf.clearConsoleOverlay()

	// clearConsoleOverlay() just restored the cursor to overlaySavedCursor
	// -- the console's real cursor position from before f4 ever drew an
	// overlay over it, e.g. wherever an earlier shell prompt happened to
	// end. That column is almost never 0. The row it's on was just blanked
	// by clearConsoleOverlay() (terminal.WinClearConsoleOverlay), so a bare "\r"
	// (no newline, no scroll, nothing consumed) is enough to put the
	// child's own first output character at the start of that already-
	// blank row instead of wherever a previous, unrelated line of text
	// used to end.
	os.Stdout.WriteString("\r")

	inConsoleView := !pf.ShowPanels && pf.ShellMode == terminal.ShellModeSimpleInline &&
		pf.consoleStyle() == terminal.ConsoleViewFar

	vtui.Suspend()
	_ = cmd.Run()

	// The child may have printed past the bottom row of the console window.
	// Windows scrolls the window to follow the cursor as that happens;
	// ReactOS 0.4.16 does not, so the output ends up in buffer rows below
	// the visible window and the screen keeps showing what was there before
	// -- measured on the live system, see WINE.md and issue #513. Do it for
	// the console before anything reads it: captureHostConsoleBuffer below
	// snapshots the rectangle at srWindow.Top, so a stale window means a
	// stale snapshot on the next Ctrl+O round-trip too.
	terminal.ScrollHostConsoleToCursor()

	if inConsoleView {
		// The child just wrote its own output starting wherever the cursor
		// happened to be: clearConsoleOverlay() above parks it at
		// overlaySavedCursor, the console's real cursor position from
		// *before* f4 ever drew an overlay there -- not necessarily column
		// 0 of a fresh line. A real interactive shell only looks safe to
		// redraw at a fixed bottom row because it always prints a fresh
		// "\r\n" before the next prompt, so command output never lands on
		// the same row the prompt is about to reclaim. Nothing here was
		// doing that: the child's entire output (all of it, for a
		// single-line command like "echo 123") could end up sitting on
		// exactly the rows drawConsoleOverlay() is about to overwrite
		// below, and get silently erased regardless of platform -- this
		// isn't a Wine timing issue, it's the same outcome a real Windows
		// console would produce with this same sequence.
		//
		// Force those rows clear first: n newlines guarantee at least n
		// scroll events by the time the cursor (wherever it started) is
		// done, which is exactly enough to push anything that was sitting
		// in the overlay's n reserved rows up and out of them.
		if n := pf.OverlayLines(); n > 0 {
			os.Stdout.WriteString(strings.Repeat("\r\n", n))
		}

		// Snapshot the console now, while the command's output is still the
		// visible content of hStdOut. Without this, terminal.ClearConsoleViewBackground()
		// finds no saved buffer on the next Ctrl+O round-trip and blanks the
		// whole window instead of restoring it (the exact bug this comment
		// used to sit next to, minus the missing capture).
		captureHostConsoleBuffer(pf.LastW, pf.LastH)

		// Re-enable input without the AltScreen round trip Resume() would do
		// (host buffer -> f4's own buffer -> host buffer again, all inside a
		// few milliseconds): the host buffer is already the active one, set
		// by Suspend() before cmd.Run() and never touched since. Under Wine
		// that rapid double SetConsoleActiveScreenBuffer is exactly the kind
		// of call vtui's own WINE.md documents as unreliable -- see the
		// "single-line command output vanishes after Ctrl+O" report this
		// call replaced Resume()+SetAltScreen(false) for.
		vtui.ResumeWithoutAltScreen()
		pf.SetBusy(true)
		pf.DrawConsoleOverlay()
		return
	}

	fmt.Print("\r\nPress any key to return to f4...")
	WaitForAnyKey()

	captureHostConsoleBuffer(pf.LastW, pf.LastH)

	vtui.Resume()
	if vtui.FrameManager != nil {
		vtui.FrameManager.HardRefresh()
	}
	pf.RefreshAll()
}

func captureHostConsoleBuffer(w, h int) {
	terminal.CaptureHostConsoleBuffer(w, h)
}

// runSimpleCapturedCommand executes a command via terminal.LocalCommandRunner and displays
// the streaming output in a scrollable f4 window.
func (pf *PanelsFrame) RunSimpleCapturedCommand(dir, command string) {
	runner := terminal.NewLocalCommandRunner()
	showRemoteCommandOutput(pf, runner, dir, command)
}
