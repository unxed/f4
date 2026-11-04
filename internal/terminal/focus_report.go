package terminal

// FocusReport is what the program in the terminal is told when the f4 window
// gains (CSI I) or loses (CSI O) focus: nothing, unless it switched focus
// reporting on with CSI ? 1004 h (docs/FAR2L_COMPAT.md, T-11; unxed/f4#2161).
// The mode is scoped like the other input modes: shell_modes.go puts the
// shell's value back when a command ends, ResetKeyboardProtocols and RIS
// switch it off.
func (tv *TerminalView) FocusReport(focused bool) []byte {
	if !tv.FocusReportingMode {
		return nil
	}
	if focused {
		return []byte("\x1b[I")
	}
	return []byte("\x1b[O")
}
