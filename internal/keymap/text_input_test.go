package keymap

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/vtinput"
)

func TestConfirmedTextTranslation(t *testing.T) {
	for _, ch := range "@€Ą~😀" {
		e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
			Char: ch, VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
			ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed, RepeatCount: 1, InputSource: "win32"}
		original := e
		if got := TranslateInput(&e, false, 0, false); got != string(ch) {
			t.Errorf("legacy %q = %q", ch, got)
		}
		if got := EventToHotkeyString(&e); got != "" {
			t.Errorf("text became shortcut %q", got)
		}
		if !reflect.DeepEqual(e, original) {
			t.Fatal("translation changed source event")
		}
	}
}

func TestConfirmedTextTerminalProtocolsAndRepeat(t *testing.T) {
	e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '@', VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed, RepeatCount: 2, InputSource: "win32"}
	for _, tc := range []struct {
		name  string
		win32 bool
		kitty int
		want  string
	}{
		{"legacy", false, 0, "@@"},
		{"win32", true, 0, "\x1b[81;16;64;1;9;2_"},
		{"kitty-no-text", false, 1, "@@"},
		{"kitty-associated-text", false, 29, strings.Repeat("\x1b[64::113;7;64u", 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranslateInput(&e, tc.win32, tc.kitty, false); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	e.TextInput = false
	if got := TranslateInput(&e, false, 0, false); got != "\x1b\x00" {
		t.Fatalf("ordinary Ctrl+Alt changed: %q", got)
	}
}

func TestConfirmedTextWin32SupplementaryRoundTrip(t *testing.T) {
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '😀', VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed, RepeatCount: 2}
	encoded := TranslateInput(&event, true, 0, false)
	want := "\x1b[81;16;55357;1;9;2_\x1b[81;16;56832;1;9;2_"
	if encoded != want {
		t.Fatalf("Win32 UnicodeChar must contain UTF-16 units: %q", encoded)
	}
	reader := vtinput.NewReader(strings.NewReader(encoded), false)
	defer reader.Close()
	decoded, err := reader.ReadEvent()
	if err != nil || decoded == nil || !decoded.TextInput || decoded.Char != event.Char ||
		decoded.VirtualKeyCode != event.VirtualKeyCode || decoded.VirtualScanCode != event.VirtualScanCode ||
		decoded.ControlKeyState != event.ControlKeyState || decoded.RepeatCount != event.RepeatCount {
		t.Fatalf("supplementary text roundtrip: %+v, %v", decoded, err)
	}
}
