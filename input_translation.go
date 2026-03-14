package main

import "github.com/unxed/vtinput"

// TranslateInput converts f4 input events into ANSI sequences that interactive shell apps expect.
func TranslateInput(e *vtinput.InputEvent) string {
	if e.Char != 0 && (e.ControlKeyState&0xFF) == 0 {
		return string(e.Char)
	}

	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	
	if ctrl && e.VirtualKeyCode >= 'A' && e.VirtualKeyCode <= 'Z' {
		// Basic Ctrl+Key mapping (Ctrl+A = 1, ..., Ctrl+O = 15, ...)
		return string(byte(e.VirtualKeyCode - 'A' + 1))
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_RETURN: return "\r"
	case vtinput.VK_UP:     return "\x1b[A"
	case vtinput.VK_DOWN:   return "\x1b[B"
	case vtinput.VK_RIGHT:  return "\x1b[C"
	case vtinput.VK_LEFT:   return "\x1b[D"
	case vtinput.VK_BACK:   return "\x7f"
	case vtinput.VK_TAB:    return "\t"
	case vtinput.VK_ESCAPE: return "\x1b"
	case vtinput.VK_F1:     return "\x1bOP"
	case vtinput.VK_F2:     return "\x1bOQ"
	case vtinput.VK_F3:     return "\x1bOR"
	case vtinput.VK_F4:     return "\x1bOS"
	case vtinput.VK_F5:     return "\x1b[15~"
	case vtinput.VK_F6:     return "\x1b[17~"
	case vtinput.VK_F7:     return "\x1b[18~"
	case vtinput.VK_F8:     return "\x1b[19~"
	case vtinput.VK_F9:     return "\x1b[20~"
	case vtinput.VK_F10:    return "\x1b[21~"
	case vtinput.VK_F11:    return "\x1b[23~"
	case vtinput.VK_F12:    return "\x1b[24~"
	case vtinput.VK_HOME:	return "\x1b[1~"
	case vtinput.VK_END:	return "\x1b[4~"
	case vtinput.VK_PRIOR:	return "\x1b[5~" // PgUp
	case vtinput.VK_NEXT:	return "\x1b[6~" // PgDn
	case vtinput.VK_INSERT:	return "\x1b[2~"
	case vtinput.VK_DELETE:	return "\x1b[3~"
	}
	return ""
}
