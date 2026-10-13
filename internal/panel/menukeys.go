package panel

import "github.com/unxed/vtinput"

// isAddItemKey reports whether e is the key that adds a new entry to one of
// the list menus: plain Ins, or Ctrl+N for keyboards that have no Insert key.
// far2l accepts Ctrl+N alongside Ins in the same menus (elfmz/far2l#3645);
// the user menu already did so here.
func isAddItemKey(e *vtinput.InputEvent) bool {
	if e == nil || !e.KeyDown || e.TextInput {
		return false
	}
	const ctrlMask = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed
	const otherMask = vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed
	if e.ControlKeyState&otherMask != 0 {
		return false
	}
	ctrl := e.ControlKeyState&ctrlMask != 0
	switch e.VirtualKeyCode {
	case vtinput.VK_INSERT:
		return !ctrl
	case vtinput.VK_N:
		return ctrl
	}
	return false
}
