package panel

import "github.com/unxed/vtinput"

// ProcessKey routes a key of the panels frame (processKey) and then drops a
// command-line selection the key went past: a key that moved the panel cursor
// or marked a file means the user is back in the panel, and an old selection
// must not keep Ctrl+C for the line from then on (commandLineOwnsCopy). The
// highlight goes with it, so what Ctrl+C will copy stays visible.
func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if pf.CmdLine == nil || !leavesCommandLineSelection(e) {
		return pf.processKey(e)
	}
	before := pf.CmdLine.KeysTaken()
	handled := pf.processKey(e)
	if pf.CmdLine.KeysTaken() == before {
		pf.CmdLine.ClearSelection()
	}
	return handled
}

// DropCommandLineSelection is for the hotkey dispatcher: a configured action
// took a key in the Shell area, so the key did not go to the command line
// and its selection is no longer the one the user is working with.
func (pf *PanelsFrame) DropCommandLineSelection() {
	if pf.CmdLine != nil {
		pf.CmdLine.ClearSelection()
	}
}

// leavesCommandLineSelection reports the keys that end a command-line
// selection when they go anywhere but the line: every key press except a
// modifier on its own. Shift, Ctrl and Alt arrive as presses of their own
// before the chord they start, and Ctrl pressed on its way to Ctrl+C must not
// drop the selection Ctrl+C is about to copy.
func leavesCommandLineSelection(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	switch e.VirtualKeyCode {
	case 0,
		vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_LWIN, vtinput.VK_RWIN, vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK:
		return false
	}
	return true
}

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
// interrupt. A selection the user has left behind does not count either:
// ProcessKey and DropCommandLineSelection drop it once a key goes elsewhere.
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

// CommandLineOwnsCopy is commandLineOwnsCopy for the macro layer: a recorded
// macro on Ctrl+C must not play over the copy of a command-line selection,
// as one on Backspace or Delete must not over an edit (f4#1797).
func (pf *PanelsFrame) CommandLineOwnsCopy(e *vtinput.InputEvent) bool {
	return pf.commandLineOwnsCopy(e)
}
