package panel

import (
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/vtui"
)

// togglePanelsVisibility switches this workspace between its panels and its
// console view -- what Ctrl+O does. It lives here, and not inline in the
// Panel.Toggle handler, so that a freshly forked terminal workspace can reach
// its console through exactly the same, already-tested path a keypress takes,
// including the per-shell-mode work the switch involves (issue #128).
func (pf *PanelsFrame) TogglePanelsVisibility() {
	if pf.PanelsLocked() {
		return
	}
	pf.ShowPanels = !pf.ShowPanels
	if pf.ShowPanels && !pf.ShowLeftPanel && !pf.ShowRightPanel {
		pf.ShowLeftPanel = true
		pf.ShowRightPanel = true
	}
	// terminal.ShellModeSimpleInline manages its own geometry refresh below,
	// timed to when the real terminal screen is actually the one f4
	// is about to draw on (see the two branches). Calling the full,
	// layout-and-repaint-triggering ResizeConsole() here, before that
	// screen switch happens, let vtui's own panel/keybar repaint land
	// on whichever screen (primary or alt) happened to still be
	// active at that exact moment: sometimes the host console the
	// user just switched to, leaving a stray copy of the keybar that
	// a later Ctrl+O toggle would reveal stacked on top of the next
	// one. Every other shell mode keeps the previous unconditional
	// call.
	if pf.ShellMode == terminal.ShellModeSimpleInline {
		pf.lastShowPanels = pf.ShowPanels
	} else if pf.MenuBar != nil && pf.LastW > 0 && pf.LastH > 0 {
		pf.ResizeConsole(pf.LastW, pf.LastH)
		pf.lastShowPanels = pf.ShowPanels
	}
	switch pf.ShellMode {
	case terminal.ShellModeHost:
		if pf.ShowPanels {
			pf.LeaveHostConsole()
		} else {
			pf.EnterHostConsole()
		}
	case terminal.ShellModeSimpleInline:
		if !pf.ShowPanels {
			vtui.SetAltScreen(false)
			pf.SetBusy(true)
			pf.syncAutoCompleteSuppression()
			if w, h, err := vtui.GetTerminalSize(); err == nil && w > 0 && h > 0 {
				pf.LastW, pf.LastH = w, h
			}
			terminal.ClearConsoleViewBackground(pf.LastW, pf.LastH)
			if pf.consoleStyle() == terminal.ConsoleViewFar {
				pf.DrawConsoleOverlay()
			}
		} else {
			pf.clearConsoleOverlay()
			vtui.SetAltScreen(true)
			pf.SetBusy(false)
			pf.syncAutoCompleteSuppression()
			if pf.MenuBar != nil && pf.LastW > 0 && pf.LastH > 0 {
				pf.ResizeConsole(pf.LastW, pf.LastH)
			}
			vtui.FrameManager.HardRefresh()
		}
	case terminal.ShellModeSimpleCaptured:
		// Captured mode has no separate console view to switch to;
		// output already went to a dialog, so panels stay visible.
		pf.ShowPanels = true
		toast.Show(i18n.Msg("Terminal.NotAvailableInEnv"), 3*time.Second)
	default:
		vtui.FrameManager.HardRefresh()
	}
	if pf.SearchFirstMode() {
		// The console edit owns the caret while panels are hidden. Preserve
		// the saved navigation target so showing panels restores its focus.
		pf.CmdLine.SetFocus(pf.IsFocused() && (pf.CommandLineFocused || !pf.ShowPanels))
	}
	if pf.ShowPanels {
		pf.RefreshAll()
	}
}

// PanelsLocked reports a workspace that is nothing but a terminal (opened by
// Ctrl+Shift+O): its panels never come back, so Ctrl+O, Esc, Del and the
// file-manager keys reach the shell instead of switching to them (issue #128).
func (pf *PanelsFrame) PanelsLocked() bool {
	return pf.TerminalOnly && !pf.ShowPanels
}

// forkTerminalWorkspace opens the console in a workspace of its own instead of
// hiding the panels of the current one. It is the Ctrl+N, Ctrl+O sequence
// users were told to type by hand (issue #128) performed as a single step: the
// panels stay untouched where they are, the new workspace shows nothing but
// the terminal, and Ctrl+Tab flips between the two.
func (pf *PanelsFrame) forkTerminalWorkspace() bool {
	if vtui.FrameManager == nil {
		return false
	}
	if pf.ShellMode == terminal.ShellModeSimpleCaptured {
		// This environment has no console view to switch to: command output
		// goes to a dialog and Ctrl+O says so while staying on the panels.
		// Forking first would leave the user with a second, identical copy of
		// the panels and the very same message.
		toast.Show(i18n.Msg("Terminal.NotAvailableInEnv"), 3*time.Second)
		return true
	}
	clone := pf.forkPanelsClone()
	vtui.FrameManager.AddScreen(clone)
	// AddScreen activates the workspace it creates, so the switch below runs
	// against the frame the user is now looking at, exactly as Ctrl+O would.
	if clone.ShowPanels {
		clone.TogglePanelsVisibility()
	}
	// Only now: the switch above is the one place the panels may still go away.
	clone.TerminalOnly = true
	return true
}

// ActionWorkspaceNewTerminal serves the Workspace.NewTerminal action. Like
// Workspace.New it works from any workspace, including full-screen editor,
// viewer and queue screens that keep no panels of their own: the terminal is
// forked from the panels those screens were opened from.
func ActionWorkspaceNewTerminal() bool {
	panels := FindPanelsFrameAnyScreen()
	return panels != nil && panels.forkTerminalWorkspace()
}
