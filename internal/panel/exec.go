package panel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// shellCommandFlag is the flag that makes the platform's shell run a single
// command string: the same one exec.Command is given a few lines above, kept
// in one place so the two spawn paths cannot drift apart.
func shellCommandFlag() string {
	if terminal.WindowsShellSyntax() {
		return "/c"
	}
	return "-c"
}

// FitConsoleWindow brings the host console's window down to the cursor after
// output has been written to it (terminal.ScrollHostConsoleToCursor). It is a
// variable so a test can see when it is called relative to what has been
// printed.
var FitConsoleWindow = terminal.ScrollHostConsoleToCursor

// RunSimpleInlineCommand executes a command directly in the host console
// without a terminal.PTY: it suspends vtui, runs the command with inherited
// stdio, and gives the screen back to f4 as soon as the command exits. The
// output stays in the host console, where Ctrl+O shows it.
func (pf *PanelsFrame) RunSimpleInlineCommand(dir, command string) {
	shell := terminal.GetSystemShell()

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

	var runErr error
	if terminal.WindowsShellSyntax() {
		// Start the cmd child the way cmd.exe starts a program -- inheriting
		// the console itself, with no explicit standard handles -- rather than
		// the way os/exec does. On ReactOS the explicit handles arrive invalid
		// in the child; see terminal/console_spawn_windows.go and WINE.md
		// §17.3e. The branch is deliberately limited to the Windows shell
		// personality: POSIX Wine mode must use the host process transport.
		cmd := exec.Command(shell, "/c", command)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if dir != "" {
			cmd.Dir = dir
		}
		runErr = terminal.RunOnHostConsole(dir, shell, shellCommandFlag(), command)
		if errors.Is(runErr, terminal.ErrConsoleSpawnUnavailable) {
			runErr = cmd.Run()
		}
	} else {
		// This includes a Windows binary under Wine with UseWinescape on.
		// The host shell is selected by $SHELL and is started through
		// libwinescape, because os/exec cannot execute a host ELF path from
		// the Windows process.
		runErr = terminal.RunLocalCommandInline(dir, command)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "\r\nf4: host shell: %v\r\n", runErr)
		}
	}
	vtui.DebugLog("EXEC: shell=%q command=%q err=%v", shell, command, runErr)

	// The child may have printed past the bottom row of the console window.
	// Windows scrolls the window to follow the cursor as that happens;
	// ReactOS 0.4.16 does not, so the output ends up in buffer rows below
	// the visible window and the screen keeps showing what was there before
	// -- measured on the live system, see WINE.md and issue #513. Do it for
	// the console before anything reads it: captureHostConsoleBuffer below
	// snapshots the rectangle at srWindow.Top, so a stale window means a
	// stale snapshot on the next Ctrl+O round-trip too.
	FitConsoleWindow()

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

	// Launched from the panels: go straight back to them, the way Far and
	// far2l do, and the way f4 itself does wherever it has a PTY
	// (CONSOLE_MODES.md §4.8). There used to be a "Press any key to return
	// to f4..." pause here (#897); the output it held on screen is not lost
	// without it -- it stays in the host console, and Ctrl+O shows it.
	//
	// That Ctrl+O view paints the far-style overlay (command line and
	// keybar) over the bottom rows of the console window, which is where
	// the child's last lines are whenever its output reached the bottom of
	// the window. The prompt used to push them up out of those rows as a
	// side effect. Do it on purpose now, the same way the console-view
	// branch above does, so the end of the output is not the part the
	// overlay hides. The newlines can move the cursor below the window
	// again, and ReactOS does not follow it there (WINE.md §17.6), so fit
	// the window once more before the snapshot below reads it.
	if n := pf.OverlayLines(); n > 0 {
		os.Stdout.WriteString(strings.Repeat("\r\n", n))
		FitConsoleWindow()
	}

	captureHostConsoleBuffer(pf.LastW, pf.LastH)

	vtui.Resume()
	// The command ran in this very console and may have left its input mode
	// changed: with ENABLE_MOUSE_INPUT off, a right click on XP opened the
	// console's own Edit menu instead of reaching f4 (Zeroes1, f4#897, item
	// 4). Ask for the mouse again, as the host-console path does on its way
	// back (console.go), before the redraw.
	restoreHostInputModes()
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
