package keymap

import (
	"testing"

	"github.com/unxed/vtinput"
)

func TestTranslateInput(t *testing.T) {
	tests := []struct {
		name       string
		e          *vtinput.InputEvent
		kittyFlags int
		want       string
	}{
		{"Char 'a'", &vtinput.InputEvent{Char: 'a', KeyDown: true}, 0, "a"},
		{"Ctrl+a", &vtinput.InputEvent{Char: 'a', ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, string(rune(1))},
		{"Up Arrow", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_UP, KeyDown: true}, 0, "\x1b[A"},
		{"Shift+Up Arrow", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_UP, ControlKeyState: vtinput.ShiftPressed, KeyDown: true}, 0, "\x1b[1;2A"},
		{"F1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F1, KeyDown: true}, 0, "\x1bOP"},
		{"F5", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F5, KeyDown: true}, 0, "\x1b[15~"},
		{"Alt+a", &vtinput.InputEvent{Char: 'a', ControlKeyState: vtinput.LeftAltPressed, KeyDown: true}, 0, "\x1ba"},
		{"Ctrl+C no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, string(rune(3))},
		{"Ctrl+Z no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Z, ControlKeyState: vtinput.RightCtrlPressed, KeyDown: true}, 0, string(rune(26))},
		{"Ctrl+[ no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_4, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, "\x1b"},
		{"Ctrl+Alt+C no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed, KeyDown: true}, 0, "\x1b\x03"},
		{"Ctrl+2 no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_2, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, string(rune(0))},
		{"Ctrl+Pause no char (gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_PAUSE, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, string(rune(3))},
		{"Ctrl+Break no char (Win32/gogpu)", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_CANCEL, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 0, string(rune(3))},
		{"Pause no ctrl", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_PAUSE, KeyDown: true}, 0, ""},
		{"Alt+Enter", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: vtinput.LeftAltPressed, KeyDown: true}, 0, "\x1b\r"},
		{"Standalone Modifier", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_SHIFT, KeyDown: true}, 0, ""},

		// Kitty fallback tests via TranslateInput (should generate final legacy strings since TranslateKeyToKitty returned "")
		{"Numpad Insert (Enhanced) Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_INSERT, ControlKeyState: vtinput.EnhancedKey, KeyDown: true}, 1, "\x1b[2~"},
		{"Left Arrow (Enhanced) Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.EnhancedKey, KeyDown: true}, 1, "\x1b[D"},
		{"Ctrl+I Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_I, Char: 9, ControlKeyState: vtinput.LeftCtrlPressed, KeyDown: true}, 1, "\x1b[9;5u"},
		{"Tab Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_TAB, Char: '\t', KeyDown: true}, 1, "\t"},
		{"Shift+Tab Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_TAB, Char: '\t', ControlKeyState: vtinput.ShiftPressed, KeyDown: true}, 1, "\x1b[9;2u"},
		{"Ctrl+Alt+A Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, Char: 0, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed, KeyDown: true}, 1, "\x1b[97;7u"},
		{"Shift+Space Kitty Mode 1", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_SPACE, Char: ' ', ControlKeyState: vtinput.ShiftPressed, KeyDown: true}, 1, " "},
		{"Shift+Space Kitty Mode 8", &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_SPACE, Char: ' ', ControlKeyState: vtinput.ShiftPressed, KeyDown: true}, 8, "\x1b[32;2u"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TranslateInput(tt.e, false, tt.kittyFlags, false)
			if got != tt.want {
				t.Errorf("TranslateInput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateInput_Win32(t *testing.T) {
	e := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		VirtualKeyCode:  65,
		VirtualScanCode: 30,
		Char:            'A',
		KeyDown:         true,
		ControlKeyState: 8,
		RepeatCount:     1,
	}
	res := TranslateInput(e, true, 0, false)
	expected := "\x1b[65;30;65;1;8;1_"
	if res != expected {
		t.Errorf("Expected %q, got %q", expected, res)
	}
}
func TestTranslateMouseInput(t *testing.T) {
	tests := []struct {
		name string
		e    *vtinput.InputEvent
		want string
	}{
		{
			"Left Click Press",
			&vtinput.InputEvent{KeyDown: true, MouseX: 10, MouseY: 5, ButtonState: vtinput.FromLeft1stButtonPressed},
			"\x1b[<0;11;6M",
		},
		{
			"Left Click Release",
			&vtinput.InputEvent{KeyDown: false, MouseX: 10, MouseY: 5},
			"\x1b[<3;11;6m",
		},
		{
			"Right Click Move",
			&vtinput.InputEvent{KeyDown: true, MouseX: 20, MouseY: 15, ButtonState: vtinput.RightmostButtonPressed, MouseEventFlags: vtinput.MouseMoved},
			"\x1b[<34;21;16M",
		},
		{
			"X11/Wayland Move without Button",
			&vtinput.InputEvent{KeyDown: false, MouseX: 20, MouseY: 15, MouseEventFlags: vtinput.MouseMoved},
			"\x1b[<35;21;16M",
		},
		{
			"Wayland Drag Move",
			&vtinput.InputEvent{KeyDown: false, MouseX: 20, MouseY: 15, ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved},
			"\x1b[<32;21;16M",
		},
		{
			"Wheel Up with Shift",
			&vtinput.InputEvent{WheelDirection: 1, MouseX: 0, MouseY: 0, ControlKeyState: vtinput.ShiftPressed},
			"\x1b[<68;1;1M",
		},
		{
			"Wheel Down with Ctrl+Alt",
			&vtinput.InputEvent{WheelDirection: -1, MouseX: 5, MouseY: 5, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed},
			"\x1b[<89;6;6M",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TranslateMouseInput(tt.e)
			if got != tt.want {
				t.Errorf("TranslateMouseInput() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A program that reads a line in cooked mode ignores an Enter or a Backspace
// whose record has no character: DiskPart never took the "exit" typed into it
// under `su` (#207). Backends that deliver these keys without one get it added.
func TestTranslateInputWin32AddsTheControlCharacter(t *testing.T) {
	for _, tc := range []struct {
		name string
		vk   uint16
		mods vtinput.ControlKeyState
		want string
	}{
		{"Enter", vtinput.VK_RETURN, 0, "\x1b[13;0;13;1;0;1_"},
		{"Backspace", vtinput.VK_BACK, 0, "\x1b[8;0;8;1;0;1_"},
		{"Tab", vtinput.VK_TAB, 0, "\x1b[9;0;9;1;0;1_"},
		{"Escape", vtinput.VK_ESCAPE, 0, "\x1b[27;0;27;1;0;1_"},
		{"Space", vtinput.VK_SPACE, 0, "\x1b[32;0;32;1;0;1_"},
		{"Shift+Enter", vtinput.VK_RETURN, vtinput.ShiftPressed, "\x1b[13;0;13;1;16;1_"},
		// With Ctrl or Alt the key means something else; nothing is invented.
		{"Ctrl+Enter", vtinput.VK_RETURN, vtinput.LeftCtrlPressed, "\x1b[13;0;0;1;8;1_"},
		{"Alt+Backspace", vtinput.VK_BACK, vtinput.LeftAltPressed, "\x1b[8;0;0;1;2;1_"},
		{"F5", vtinput.VK_F5, 0, "\x1b[116;0;0;1;0;1_"},
	} {
		e := &vtinput.InputEvent{Type: vtinput.KeyEventType, VirtualKeyCode: tc.vk, KeyDown: true, ControlKeyState: tc.mods, RepeatCount: 1}
		if got := TranslateInput(e, true, 0, false); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// A character the backend did supply is passed on untouched.
	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, VirtualKeyCode: vtinput.VK_RETURN, Char: '\n', KeyDown: true, RepeatCount: 1}
	if got := TranslateInput(e, true, 0, false); got != "\x1b[13;0;10;1;0;1_" {
		t.Errorf("a supplied character was replaced: %q", got)
	}
}
