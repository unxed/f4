package keymap

import (
	"fmt"
	"github.com/unxed/f4/internal/numeric"
	"github.com/unxed/vtinput"
)

func formatCSI(mod int, char string, appCursorKeys bool) string {
	if mod > 1 {
		return fmt.Sprintf("\x1b[1;%d%s", mod, char)
	}
	if appCursorKeys {
		return "\x1bO" + char
	}
	return "\x1b[" + char
}

func formatCSIOrSS3(mod int, char string) string {
	if mod > 1 {
		return fmt.Sprintf("\x1b[1;%d%s", mod, char)
	}
	return "\x1bO" + char
}

func formatTilde(mod int, code int) string {
	if mod > 1 {
		return fmt.Sprintf("\x1b[%d;%d~", code, mod)
	}
	return fmt.Sprintf("\x1b[%d~", code)
}

// RebaseTerminalMouseEvent converts a screen-coordinate mouse event into the
// coordinate space of the terminal viewport before it is handed to the child
// process. The viewport does not start at the top-left corner of the screen
// whenever rows above it are taken by the workspace tab bar or by an
// always-visible menu bar, while the PTY is sized to the viewport alone: a
// child therefore reads the raw screen row as its own row and acts on the
// cell below the one that was clicked. That is the "nested f4 misses by one
// row while tabs are shown, and stops missing when they are hidden" report in
// issue #87.
//
// The event is copied rather than adjusted in place, because the caller keeps
// using the original screen coordinates for hit-testing and selection.
func RebaseTerminalMouseEvent(e *vtinput.InputEvent, x1, y1, width, height int) *vtinput.InputEvent {
	if e == nil || (x1 == 0 && y1 == 0) {
		return e
	}
	local := *e
	local.MouseX = int16(clampMouseCoord(int(e.MouseX)-x1, width))
	local.MouseY = int16(clampMouseCoord(int(e.MouseY)-y1, height))
	return &local
}

// clampMouseCoord keeps a rebased coordinate inside the viewport. Events can
// legitimately arrive from the rows above it (the tab bar itself), and a
// negative column or row would be encoded as a wildly out-of-range cell.
func clampMouseCoord(v, size int) int {
	if v < 0 {
		return 0
	}
	if size > 0 && v > size-1 {
		return size - 1
	}
	return v
}

func TranslateMouseInput(e *vtinput.InputEvent) string {
	cb := 0
	isRelease := false
	isMove := e.MouseEventFlags&vtinput.MouseMoved != 0

	if e.WheelDirection != 0 {
		if e.WheelDirection > 0 {
			cb = 64 // Wheel up
		} else {
			cb = 65 // Wheel down
		}
	} else if isMove {
		// X11 and Wayland report motion with KeyDown=false. Motion is
		// nevertheless an SGR mouse event ending in M; m denotes a button
		// release. Wayland also keeps the held button in ButtonState, so
		// preserve it for drag events.
		switch e.ButtonState {
		case vtinput.FromLeft1stButtonPressed:
			cb = 0
		case vtinput.FromLeft2ndButtonPressed:
			cb = 1
		case vtinput.RightmostButtonPressed:
			cb = 2
		default:
			cb = 3 // Motion with no button pressed
		}
		cb += 32
	} else {
		if !e.KeyDown {
			cb = 3 // Release
			isRelease = true
		} else {
			switch e.ButtonState {
			case vtinput.FromLeft1stButtonPressed:
				cb = 0
			case vtinput.FromLeft2ndButtonPressed:
				cb = 1
			case vtinput.RightmostButtonPressed:
				cb = 2
			default:
				cb = 3
				isRelease = true
			}
		}
	}

	if (e.ControlKeyState & vtinput.ShiftPressed) != 0 {
		cb += 4
	}
	if (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0 {
		cb += 8
	}
	if (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0 {
		cb += 16
	}

	endChar := "M"
	if isRelease {
		endChar = "m"
	}

	return fmt.Sprintf("\x1b[<%d;%d;%d%s", cb, e.MouseX+1, e.MouseY+1, endChar)
}

func TranslateLegacySpecialKey(e *vtinput.InputEvent, appCursorKeys bool) string {
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	mod := 1
	if shift {
		mod += 1
	}
	if alt {
		mod += 2
	}
	if ctrl {
		mod += 4
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_UP:
		return formatCSI(mod, "A", appCursorKeys)
	case vtinput.VK_DOWN:
		return formatCSI(mod, "B", appCursorKeys)
	case vtinput.VK_RIGHT:
		return formatCSI(mod, "C", appCursorKeys)
	case vtinput.VK_LEFT:
		return formatCSI(mod, "D", appCursorKeys)
	case vtinput.VK_HOME:
		if appCursorKeys && mod == 1 {
			return "\x1bOH"
		}
		return formatCSI(mod, "H", false)
	case vtinput.VK_END:
		if appCursorKeys && mod == 1 {
			return "\x1bOF"
		}
		return formatCSI(mod, "F", false)

	case vtinput.VK_F1:
		return formatCSIOrSS3(mod, "P")
	case vtinput.VK_F2:
		return formatCSIOrSS3(mod, "Q")
	case vtinput.VK_F3:
		return formatCSIOrSS3(mod, "R")
	case vtinput.VK_F4:
		return formatCSIOrSS3(mod, "S")

	case vtinput.VK_F5:
		return formatTilde(mod, 15)
	case vtinput.VK_F6:
		return formatTilde(mod, 17)
	case vtinput.VK_F7:
		return formatTilde(mod, 18)
	case vtinput.VK_F8:
		return formatTilde(mod, 19)
	case vtinput.VK_F9:
		return formatTilde(mod, 20)
	case vtinput.VK_F10:
		return formatTilde(mod, 21)
	case vtinput.VK_F11:
		return formatTilde(mod, 23)
	case vtinput.VK_F12:
		return formatTilde(mod, 24)

	case vtinput.VK_INSERT:
		return formatTilde(mod, 2)
	case vtinput.VK_DELETE:
		return formatTilde(mod, 3)
	case vtinput.VK_PRIOR:
		return formatTilde(mod, 5)
	case vtinput.VK_NEXT:
		return formatTilde(mod, 6)

	case vtinput.VK_RETURN:
		if alt {
			return "\x1b\r"
		}
		return "\r"
	case vtinput.VK_BACK:
		if alt {
			return "\x1b\x7f"
		}
		return "\x7f"
	case vtinput.VK_TAB:
		if shift {
			return "\x1b[Z"
		}
		if alt {
			return "\x1b\t"
		}
		return "\t"
	case vtinput.VK_ESCAPE:
		if alt {
			return "\x1b\x1b"
		}
		return "\x1b"
	}

	return ""
}

// ctrlCharFromVK maps a virtual key code to the C0 control byte produced
// by Ctrl+key, following xterm conventions. Returns -1 for keys without a
// standard Ctrl mapping. Used only when the input backend could not supply
// the character itself (Char == 0), e.g. in the gogpu GUI host.
func ctrlCharFromVK(vk uint16) int {
	switch {
	case vk >= vtinput.VK_A && vk <= vtinput.VK_Z:
		return int(vk-vtinput.VK_A) + 1
	case vk == vtinput.VK_2:
		return 0 // Ctrl+2 = NUL
	case vk == vtinput.VK_3:
		return 27 // Ctrl+3 = ESC
	case vk == vtinput.VK_4:
		return 28 // Ctrl+4 = FS
	case vk == vtinput.VK_5:
		return 29 // Ctrl+5 = GS
	case vk == vtinput.VK_6:
		return 30 // Ctrl+6 = RS
	case vk == vtinput.VK_7:
		return 31 // Ctrl+7 = US
	case vk == vtinput.VK_8:
		return 127 // Ctrl+8 = DEL
	case vk == vtinput.VK_PAUSE, vk == vtinput.VK_CANCEL:
		return 3 // zoin-bot: Ctrl+Pause/Ctrl+Break = same as Ctrl+C = ETX
	case vk == vtinput.VK_OEM_4:
		return 27 // Ctrl+[ = ESC
	case vk == vtinput.VK_OEM_5:
		return 28 // Ctrl+\ = FS
	case vk == vtinput.VK_OEM_6:
		return 29 // Ctrl+] = GS
	case vk == vtinput.VK_OEM_MINUS:
		return 31 // Ctrl+_ = US
	}
	return -1
}

// win32UnicodeChar is the character field of a win32-input-mode record. A
// console key event of Enter, Backspace, Tab, Escape or Space carries its
// control character there, and a program that reads a line in cooked mode (as
// DiskPart does) ignores such a key without it. Some backends deliver these
// keys with no character, so it is filled in, unless Ctrl or Alt changes what
// the key means (#207).
func win32UnicodeChar(e *vtinput.InputEvent) rune {
	if e.Char != 0 {
		return e.Char
	}
	if e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0 {
		return 0
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_RETURN:
		return '\r'
	case vtinput.VK_BACK:
		return '\b'
	case vtinput.VK_TAB:
		return '\t'
	case vtinput.VK_ESCAPE:
		return 0x1b
	case vtinput.VK_SPACE:
		return ' '
	}
	return 0
}

// TranslateInput converts f4 input events into ANSI sequences that interactive shell apps expect.
func TranslateInput(e *vtinput.InputEvent, win32Mode bool, kittyFlags int, appCursorKeys bool) string {
	if win32Mode && e.Type == vtinput.KeyEventType {
		kd := 0
		if e.KeyDown {
			kd = 1
		}
		// Format: CSI Vk ; Sc ; Uc ; Kd ; Cs ; Rc _
		return fmt.Sprintf("\x1b[%d;%d;%d;%d;%d;%d_",
			e.VirtualKeyCode, e.VirtualScanCode, win32UnicodeChar(e), kd, e.ControlKeyState, e.RepeatCount)
	}

	if kittyFlags != 0 {
		seq := TranslateKeyToKitty(e, kittyFlags, appCursorKeys)
		if seq != "" {
			return seq
		}
		if !e.KeyDown {
			return ""
		}
	}

	// Ignore standalone modifier key presses
	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return ""
	}

	if !e.KeyDown {
		return ""
	}

	if spec := TranslateLegacySpecialKey(e, appCursorKeys); spec != "" {
		return spec
	}

	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0

	// Some GUI hosts (gogpu window) deliver Ctrl+letter with Char == 0:
	// their text-input path filters control characters (WM_CHAR < 0x20),
	// so not even Ctrl+C arrives as a character, only as a modified key.
	// In console mode Windows puts the C0 byte itself (0x03 = ETX for
	// Ctrl+C) into the input record, so synthesizing from the virtual key
	// code here is a no-op there. Real terminals (xterm, Windows Terminal)
	// derive the control byte from the key too; without this a busy PTY
	// child never receives Ctrl+C and e.g. `dir /s` cannot be interrupted.
	if ctrl && e.Char == 0 {
		if ch := ctrlCharFromVK(e.VirtualKeyCode); ch >= 0 {
			controlRune, ok := numeric.BoundedRune(ch)
			if !ok {
				return ""
			}
			out := ""
			if alt {
				out += "\x1b"
			}
			out += string(controlRune)
			return out
		}
	}

	// Handle Character Input
	if e.Char != 0 {
		if !ctrl && !alt {
			return string(e.Char)
		}

		ch := e.Char
		if ctrl {
			if ch >= 'a' && ch <= 'z' {
				ch = ch - 'a' + 1
			} else if ch >= 'A' && ch <= 'Z' {
				ch = ch - 'A' + 1
			} else if ch == '[' || ch == '{' {
				ch = 27
			} else if ch == '\\' || ch == '|' {
				ch = 28
			} else if ch == ']' || ch == '}' {
				ch = 29
			} else if ch == '^' || ch == '~' {
				ch = 30
			} else if ch == '_' || ch == '?' {
				ch = 31
			} else if ch == '@' {
				ch = 0
			}
		}

		out := ""
		if alt {
			out += "\x1b"
		}
		out += string(ch)
		return out
	}

	return ""
}
