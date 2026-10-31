package panel

import (
	"unicode"

	"github.com/unxed/vtinput"
)

// The keys of an open quick search or autofilter (#2186).
//
// Far's quick search is a modal loop: it reads the keys itself, keeps the
// ones that edit its text and closes on any other, which then acts as if the
// search had never been open (far2l's Panel::FastFind). f4's search lives in
// the file panel instead, next to a command line that answers many of the
// same keys -- Backspace, the arrows, Ctrl+Y. Whoever routes a key asks here
// first: the key router before the macros and the hotkey table, the panels
// frame before its own keys and the command line's. Otherwise a key reached
// the command line while the search box was open, and Backspace erased the
// command instead of the search.

// fastFindKeeps reports whether the open quick search or autofilter takes e.
// A character, with or without Alt, extends the text; Backspace, Ctrl+Y, F2,
// Ctrl+Enter and Ctrl+Shift+Enter work on it; Esc closes the search, and Del
// does nothing, so that neither reaches the panels. The filter also keeps the
// cursor keys and Ins, which walk and mark the rows it leaves, and Ctrl+E, its
// exact-match option.
func (fp *FileSystemPanel) fastFindKeeps(e *vtinput.InputEvent) bool {
	if !fp.FastFindMode || e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE, vtinput.VK_DELETE, vtinput.VK_F2:
		return !ctrl && !alt && !shift
	case vtinput.VK_BACK:
		return !alt
	case vtinput.VK_RETURN:
		return ctrl && !alt
	}
	if isFastFindClearKey(e) {
		return true
	}
	if fp.autoFilterMode {
		switch e.VirtualKeyCode {
		case vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_LEFT, vtinput.VK_RIGHT,
			vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
			return !ctrl && !alt
		case vtinput.VK_INSERT:
			return !ctrl && !alt && !shift
		}
		if e.VirtualKeyCode == vtinput.VK_E && ctrl && !alt && !shift {
			return true
		}
	}
	return fastFindChar(e) != 0
}

// fastFindClosesOn reports whether e closes the open quick search or
// autofilter: any key press the search does not keep, except a modifier on
// its own -- the Shift of a capital letter, the Alt of Alt+letter or of the
// filter's lone Alt -- and an event with neither a key nor a character.
func (fp *FileSystemPanel) fastFindClosesOn(e *vtinput.InputEvent) bool {
	if !fp.FastFindMode || e.Type != vtinput.KeyEventType || !e.KeyDown || fp.fastFindKeeps(e) {
		return false
	}
	switch e.VirtualKeyCode {
	case 0,
		vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_LWIN, vtinput.VK_RWIN,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return false
	}
	return true
}

// fastFindChar is the character e types into the search, in lower case, or 0:
// a printable character without Ctrl. Alt does not matter -- the search
// starts with Alt+letter and goes on with or without it.
func fastFindChar(e *vtinput.InputEvent) rune {
	if e.Char == 0 || (e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0 || !unicode.IsPrint(e.Char) {
		return 0
	}
	return unicode.ToLower(e.Char)
}

// isFastFindClearKey is Ctrl+Y, which erases the text of the search, as it
// does in any edit field of Far.
func isFastFindClearKey(e *vtinput.InputEvent) bool {
	if e.VirtualKeyCode != vtinput.VK_Y {
		return false
	}
	mods := e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed |
		vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed)
	return mods == vtinput.LeftCtrlPressed || mods == vtinput.RightCtrlPressed
}

// FastFindClosesOn is fastFindClosesOn of the active file panel, for the key
// router: a key the open search does not keep closes it before the macros
// and the hotkey table see the key.
func (pf *PanelsFrame) FastFindClosesOn(e *vtinput.InputEvent) bool {
	if !pf.ShowPanels {
		return false
	}
	fsp := pf.GetActivePanel()
	return fsp != nil && fsp.fastFindClosesOn(e)
}
