package terminal

// The modes a command switches on with DECSET belong to the command. The
// shell marks the start and the end of every command (OSC 133;C and 133;D);
// what the shell itself had on at C is put back at D, so that bracketed
// paste, the mouse modes and the win32-input mode of a finished program do not
// stay on for the next one (docs/FAR2L_COMPAT.md, T-07; unxed/f4#2160). The
// kitty flags have the same scoping in HandleOSC133.

type shellInputModes struct {
	bracketedPaste bool
	focusReporting bool
	mouseSGR       bool
	win32Input     bool
	mouseTracking  int
}

// saveShellModes remembers the modes at the start of a command. A second C
// without a D between (a command inside a command) keeps the first snapshot.
func (tv *TerminalView) saveShellModes() {
	if tv.shellModesPending {
		return
	}
	tv.shellModes = shellInputModes{
		bracketedPaste: tv.BracketedPasteMode,
		focusReporting: tv.FocusReportingMode,
		mouseSGR:       tv.MouseSGRMode,
		win32Input:     tv.Win32InputMode,
		mouseTracking:  tv.MouseTrackingMode,
	}
	tv.shellModesPending = true
}

// restoreShellModes puts the shell's modes back at the end of a command; a D
// without a C (the prompt of a shell that marks only that) changes nothing.
func (tv *TerminalView) restoreShellModes() {
	if !tv.shellModesPending {
		return
	}
	tv.shellModesPending = false
	tv.BracketedPasteMode = tv.shellModes.bracketedPaste
	tv.FocusReportingMode = tv.shellModes.focusReporting
	tv.MouseSGRMode = tv.shellModes.mouseSGR
	tv.Win32InputMode = tv.shellModes.win32Input
	tv.MouseTrackingMode = tv.shellModes.mouseTracking
}
