package keymap

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/unxed/f4/internal/numeric"
	"github.com/unxed/vtinput"
)

func TranslateKeyToKitty(e *vtinput.InputEvent, flags int, appCursorKeys bool) string {
	if e.TextInput && e.KeyDown && e.RepeatCount > 1 {
		single := *e
		single.RepeatCount = 1
		return strings.Repeat(TranslateKeyToKitty(&single, flags, appCursorKeys), int(e.RepeatCount))
	}
	if e.VirtualKeyCode == 0xFC { // VK_NONAME
		if e.KeyDown {
			if e.Char != 0 {
				return string(e.Char)
			}
		}
		return ""
	}

	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	enhanced := (e.ControlKeyState & vtinput.EnhancedKey) != 0

	disambiguate := (ctrl && !alt && !shift) || (!ctrl && alt && !shift) || (ctrl && alt && !shift) || (!ctrl && alt && shift)
	isTextKey := e.Char > 0x1F && e.Char != 0x7F && ((!ctrl && !alt) || e.TextInput)
	// Without associated text support, send committed text literally rather
	// than turn AltGr into a Ctrl+Alt shortcut in the child application.
	if e.TextInput && e.KeyDown && flags&16 == 0 {
		return string(e.Char)
	}

	if (flags&2) != 0 && (flags&8) == 0 && !e.KeyDown {
		if (e.VirtualKeyCode == vtinput.VK_RETURN && !ctrl && !alt && !shift) ||
			(e.VirtualKeyCode == vtinput.VK_TAB && !ctrl && !alt && !shift) ||
			(e.VirtualKeyCode == vtinput.VK_BACK && !ctrl && !alt && !shift) {
			return ""
		}
	}

	if (flags&2) == 0 && !e.KeyDown {
		return ""
	}

	legacy := TranslateLegacySpecialKey(e, appCursorKeys)
	nolegacy := false

	kitty := (flags&8) != 0 || ((flags&2) != 0 && !e.KeyDown)

	if !kitty && (flags&1) != 0 {
		if e.VirtualKeyCode == vtinput.VK_ESCAPE {
			kitty = true
		} else {
			isAtoZ := e.VirtualKeyCode >= 0x41 && e.VirtualKeyCode <= 0x5A
			is0to9 := e.VirtualKeyCode >= 0x30 && e.VirtualKeyCode <= 0x39
			isOem := false
			switch e.VirtualKeyCode {
			case vtinput.VK_OEM_3, vtinput.VK_OEM_MINUS, vtinput.VK_OEM_PLUS, vtinput.VK_OEM_4, vtinput.VK_OEM_6, vtinput.VK_OEM_5, vtinput.VK_OEM_1, vtinput.VK_OEM_7, vtinput.VK_OEM_COMMA, vtinput.VK_OEM_PERIOD, vtinput.VK_OEM_2:
				isOem = true
			}
			if (isAtoZ || is0to9 || isOem) && disambiguate {
				kitty = true
			} else if e.VirtualKeyCode >= vtinput.VK_NUMPAD0 && e.VirtualKeyCode <= vtinput.VK_NUMPAD9 && e.VirtualKeyCode != vtinput.VK_NUMPAD5 {
				kitty = true
			} else {
				switch e.VirtualKeyCode {
				case vtinput.VK_DECIMAL, vtinput.VK_SEPARATOR, vtinput.VK_CLEAR, vtinput.VK_MULTIPLY, vtinput.VK_ADD, vtinput.VK_SUBTRACT, vtinput.VK_DIVIDE, vtinput.VK_F1, vtinput.VK_F2, vtinput.VK_F3, vtinput.VK_F4:
					kitty = true
				}
				if e.VirtualKeyCode == vtinput.VK_RETURN && enhanced {
					kitty = true
				}
			}
		}
	}

	if (flags&1) != 0 && !kitty {
		if legacy == "" && !isTextKey {
			kitty = true
		} else if !isTextKey &&
			(e.VirtualKeyCode != vtinput.VK_RETURN || ctrl || alt || shift) &&
			(e.VirtualKeyCode != vtinput.VK_TAB || ctrl || alt || shift) &&
			(e.VirtualKeyCode != vtinput.VK_BACK || ctrl || alt || shift) {
			kitty = true
		}

		if !kitty && e.KeyDown && legacy != "" && len(legacy) > 1 && (flags&8) == 0 {
			return ""
		}
	}

	if !kitty {
		return ""
	}

	if (flags & 8) == 0 {
		switch e.VirtualKeyCode {
		case vtinput.VK_NUMLOCK, vtinput.VK_CAPITAL, vtinput.VK_SCROLL,
			vtinput.VK_LSHIFT, vtinput.VK_RSHIFT, vtinput.VK_SHIFT,
			vtinput.VK_LCONTROL, vtinput.VK_RCONTROL, vtinput.VK_CONTROL,
			vtinput.VK_LMENU, vtinput.VK_RMENU, vtinput.VK_MENU,
			vtinput.VK_LWIN, vtinput.VK_RWIN:
			return ""
		}
	}

	var modifiers uint
	if shift {
		modifiers |= 1
	}
	if alt {
		modifiers |= 2
	}
	if ctrl {
		modifiers |= 4
	}
	modifiers++

	if (e.ControlKeyState & vtinput.CapsLockOn) != 0 {
		modifiers |= 64
		nolegacy = true
	}
	if (e.ControlKeyState & vtinput.NumLockOn) != 0 {
		modifiers |= 128
		nolegacy = true
	}

	var shifted uint
	var keycode uint
	var base uint
	suffix := byte('u')

	isLetter := (e.Char >= 'A' && e.Char <= 'Z') || (e.Char >= 'a' && e.Char <= 'z')
	caps := (e.ControlKeyState & vtinput.CapsLockOn) != 0
	isSpecial := ctrl && (e.Char < 32)

	if shift && (!caps || (!isLetter && !isSpecial)) && e.Char != ' ' {
		shifted, _ = numeric.RuneCodepoint(unicode.ToUpper(e.Char))
	}
	keycode, _ = numeric.RuneCodepoint(unicode.ToLower(e.Char))

	if (e.VirtualKeyCode >= 'A' && e.VirtualKeyCode <= 'Z') || (e.VirtualKeyCode >= '0' && e.VirtualKeyCode <= '9') {
		base = uint(e.VirtualKeyCode)
		if e.VirtualKeyCode >= 'A' && e.VirtualKeyCode <= 'Z' {
			base += 'a' - 'A'
		}
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_OEM_3:
		base = '`'
	case vtinput.VK_OEM_MINUS:
		base = '-'
	case vtinput.VK_OEM_PLUS:
		base = '='
	case vtinput.VK_OEM_4:
		base = '['
	case vtinput.VK_OEM_6:
		base = ']'
	case vtinput.VK_OEM_1:
		base = ';'
	case vtinput.VK_OEM_7:
		base = '\''
	case vtinput.VK_OEM_5:
		base = '\\'
	case 0xE1, vtinput.VK_OEM_2:
		base = '/'
	case vtinput.VK_OEM_COMMA:
		base = ','
	case vtinput.VK_OEM_PERIOD:
		base = '.'
	}

	// A control character in Char (Ctrl+C arrives as 0x03) is not the key: the
	// protocol names the key that was pressed, 'c' (99), never ETX (3). Tab,
	// Enter, Backspace and Escape keep their own codes (9, 13, 8, 27), which
	// existing programs and tests rely on for Ctrl+I, Ctrl+M and Ctrl+H.
	keepsControlCode := keycode == 8 || keycode == 9 || keycode == 13 || keycode == 27
	if base != 0 && (keycode == 0 || (isSpecial && keycode < 32 && !keepsControlCode)) {
		keycode = base
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE:
		if keycode == 0 {
			keycode = 27
		}
	case vtinput.VK_RETURN:
		if keycode == 0 {
			keycode = 13
		}
	case vtinput.VK_TAB:
		if keycode == 0 {
			keycode = 9
		}
	case vtinput.VK_SPACE:
		if keycode == 0 {
			keycode = 32
		}
	case vtinput.VK_SNAPSHOT:
		keycode = 57361
		suffix = 'u'
	case vtinput.VK_SCROLL:
		keycode = 57359
		suffix = 'u'
	case vtinput.VK_PAUSE:
		keycode = 57362
		suffix = 'u'
	case vtinput.VK_APPS:
		keycode = 57363
		suffix = 'u'
	case vtinput.VK_BACK:
		keycode = 127
	case vtinput.VK_NUMPAD0:
		if keycode == 0 {
			keycode = 57399
		}
	case vtinput.VK_NUMPAD1:
		if keycode == 0 {
			keycode = 57400
		}
	case vtinput.VK_NUMPAD2:
		if keycode == 0 {
			keycode = 57401
		}
	case vtinput.VK_NUMPAD3:
		if keycode == 0 {
			keycode = 57402
		}
	case vtinput.VK_NUMPAD4:
		if keycode == 0 {
			keycode = 57403
		}
	case vtinput.VK_NUMPAD5:
		if keycode == 0 {
			keycode = 57404
		}
	case vtinput.VK_NUMPAD6:
		if keycode == 0 {
			keycode = 57405
		}
	case vtinput.VK_NUMPAD7:
		if keycode == 0 {
			keycode = 57406
		}
	case vtinput.VK_NUMPAD8:
		if keycode == 0 {
			keycode = 57407
		}
	case vtinput.VK_NUMPAD9:
		if keycode == 0 {
			keycode = 57408
		}
	case vtinput.VK_DECIMAL:
		if keycode == 0 {
			keycode = 57409
		}
	case vtinput.VK_DIVIDE:
		if keycode == 0 {
			keycode = 57410
		}
	case vtinput.VK_MULTIPLY:
		if keycode == 0 {
			keycode = 57411
		}
	case vtinput.VK_SUBTRACT:
		if keycode == 0 {
			keycode = 57412
		}
	case vtinput.VK_ADD:
		if keycode == 0 {
			keycode = 57413
		}
	case vtinput.VK_CLEAR:
		keycode = 57427
		suffix = '~'
	case vtinput.VK_INSERT:
		if enhanced {
			keycode = 2
			suffix = '~'
		} else {
			keycode = 57425
			suffix = 'u'
		}
	case vtinput.VK_DELETE:
		if enhanced {
			keycode = 3
			suffix = '~'
		} else {
			keycode = 57426
			suffix = 'u'
		}
	case vtinput.VK_LEFT:
		if enhanced {
			keycode = 1
			suffix = 'D'
		} else {
			keycode = 57417
			suffix = 'u'
		}
	case vtinput.VK_RIGHT:
		if enhanced {
			keycode = 1
			suffix = 'C'
		} else {
			keycode = 57418
			suffix = 'u'
		}
	case vtinput.VK_UP:
		if enhanced {
			keycode = 1
			suffix = 'A'
		} else {
			keycode = 57419
			suffix = 'u'
		}
	case vtinput.VK_DOWN:
		if enhanced {
			keycode = 1
			suffix = 'B'
		} else {
			keycode = 57420
			suffix = 'u'
		}
	case vtinput.VK_PRIOR:
		if enhanced {
			keycode = 5
			suffix = '~'
		} else {
			keycode = 57421
			suffix = 'u'
		}
	case vtinput.VK_NEXT:
		if enhanced {
			keycode = 6
			suffix = '~'
		} else {
			keycode = 57422
			suffix = 'u'
		}
	case vtinput.VK_HOME:
		if enhanced {
			keycode = 1
			suffix = 'H'
		} else {
			keycode = 57423
			suffix = 'u'
		}
	case vtinput.VK_END:
		if enhanced {
			keycode = 1
			suffix = 'F'
		} else {
			keycode = 57424
			suffix = 'u'
		}
	case vtinput.VK_F1:
		if kitty {
			keycode = 1
			suffix = 'P'
			nolegacy = true
		} else {
			keycode = 11
			suffix = '~'
		}
	case vtinput.VK_F2:
		if kitty {
			keycode = 1
			suffix = 'Q'
			nolegacy = true
		} else {
			keycode = 12
			suffix = '~'
		}
	case vtinput.VK_F3:
		keycode = 13
		suffix = '~'
		nolegacy = true
	case vtinput.VK_F4:
		if kitty {
			keycode = 1
			suffix = 'S'
			nolegacy = true
		} else {
			keycode = 14
			suffix = '~'
		}
	case vtinput.VK_F5:
		keycode = 15
		suffix = '~'
	case vtinput.VK_F6:
		keycode = 17
		suffix = '~'
	case vtinput.VK_F7:
		keycode = 18
		suffix = '~'
	case vtinput.VK_F8:
		keycode = 19
		suffix = '~'
	case vtinput.VK_F9:
		keycode = 20
		suffix = '~'
	case vtinput.VK_F10:
		keycode = 21
		suffix = '~'
	case vtinput.VK_F11:
		keycode = 23
		suffix = '~'
	case vtinput.VK_F12:
		keycode = 24
		suffix = '~'

	case vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU:
		if (flags & 8) == 0 {
			return ""
		}
		if enhanced {
			keycode = 57449
			suffix = 'u'
		} else {
			keycode = 57443
			suffix = 'u'
		}
	case vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL:
		if (flags & 8) == 0 {
			return ""
		}
		if enhanced {
			keycode = 57448
			suffix = 'u'
		} else {
			keycode = 57442
			suffix = 'u'
		}
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT:
		if (flags & 8) == 0 {
			return ""
		}
		if e.VirtualScanCode == vtinput.ScanCodeRightShift {
			keycode = 57447
			suffix = 'u'
		} else {
			keycode = 57441
			suffix = 'u'
		}
	}

	if suffix != 'u' && e.KeyDown && !nolegacy && legacy != "" && len(legacy) > 1 && (flags&8) == 0 {
		return ""
	}

	if base == keycode {
		base = 0
	}

	if keycode == 0 && shifted == 0 {
		return ""
	}

	out := "\x1b["
	skipped := false

	if keycode != 1 || suffix == 'u' || suffix == '~' || modifiers != 1 || ((flags&2) != 0 && !e.KeyDown) {
		out += strconv.Itoa(int(keycode))
	}

	if (flags&4) != 0 && (shifted != 0 || base != 0) {
		out += ":"
		if shifted != 0 {
			out += strconv.Itoa(int(shifted))
		}
		if base != 0 {
			out += ":"
			out += strconv.Itoa(int(base))
		}
	}

	if modifiers > 1 || ((flags&2) != 0 && !e.KeyDown) {
		out += ";" + strconv.Itoa(int(modifiers))
		if (flags&2) != 0 && !e.KeyDown {
			out += ":3"
		}
	} else {
		skipped = true
	}

	if (flags&16) != 0 && e.KeyDown && e.Char >= 32 && ((!alt && !ctrl) || e.TextInput) {
		if skipped {
			out += ";"
		}
		out += ";" + strconv.Itoa(int(e.Char))
	}

	out += string(suffix)
	return out
}
