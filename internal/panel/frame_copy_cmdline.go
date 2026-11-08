package panel

import "github.com/unxed/vtinput"

// commandLineOwnsCopy reports whether Ctrl+C must copy the command line's
// selected text rather than reach a configurable hotkey. Ctrl+C is bound in
// the Shell area (Panel.CopyFilesToClipboard, Panel.SplitReset), and the
// hotkey dispatcher consumes a bound key even when its action cannot run, so
// without this the selection was never copied. Cmd+C on macOS arrives as the
// same Ctrl+C. Ctrl+Ins needs no claim: no panel action takes it from the
// line while the line holds text.
//
// Only a selection claims the key: with nothing selected Ctrl+C keeps its
// configured meaning, and while a program runs in the terminal it stays the
// interrupt.
func (pf *PanelsFrame) commandLineOwnsCopy(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown || e.VirtualKeyCode != vtinput.VK_C {
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	if !ctrl || alt || shift {
		return false
	}
	if pf.CmdLine == nil || !pf.CmdLine.IsVisible() || !pf.CmdLine.HasSelection() {
		return false
	}
	if pf.SearchFirstMode() && pf.ShowPanels && !pf.CommandLineFocused {
		return false
	}
	return !pf.IsPtyBusy()
}
