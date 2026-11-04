package terminal

import (
	"testing"

	"github.com/unxed/vtinput"
)

func TestFar2lInputEvent(t *testing.T) {
	tests := []struct {
		name  string
		event *vtinput.InputEvent
		want  string
	}{
		{
			name: "key down",
			event: &vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, Char: 'a',
				ControlKeyState: vtinput.NoKeyPressed,
				VirtualKeyCode:  0x41, VirtualScanCode: 0x1e, RepeatCount: 1,
			},
			want: "\x1b_f2lAQBBAB4AAAAAAGEAAABL\x07",
		},
		{
			name: "key up",
			event: &vtinput.InputEvent{
				Type: vtinput.KeyEventType, Char: 'a', VirtualKeyCode: 0x41,
				VirtualScanCode: 0x1e, RepeatCount: 1,
			},
			want: "\x1b_f2lAQBBAB4AAAAAAGEAAABr\x07",
		},
		{
			name: "mouse",
			event: &vtinput.InputEvent{
				Type: vtinput.MouseEventType, MouseX: 10, MouseY: 5,
				ButtonState: vtinput.FromLeft1stButtonPressed,
			},
			want: "\x1b_f2lCgAFAAEAAAAAAAAAAAAAAE0=\x07",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(far2lInputEvent(tt.event, false)); got != tt.want {
				t.Fatalf("far2l event = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFar2lInputEventRejectsNonInput(t *testing.T) {
	if got := far2lInputEvent(&vtinput.InputEvent{Type: vtinput.FocusEventType}, false); got != nil {
		t.Fatalf("non-input event = %q, want nil", got)
	}
	if got := far2lInputEvent(nil, false); got != nil {
		t.Fatalf("nil event = %q, want nil", got)
	}
}

func TestFar2lInputEventUsesCompactFormsOnlyWithoutLoss(t *testing.T) {
	key := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, Char: 'a',
		ControlKeyState: vtinput.NoKeyPressed,
		VirtualKeyCode:  0x41, VirtualScanCode: 0x1e, RepeatCount: 1,
	}
	if got, want := string(far2lInputEvent(key, true)), "\x1b_f2lQQAAAGEAQw==\x07"; got != want {
		t.Fatalf("compact key event = %q, want %q", got, want)
	}

	key.VirtualScanCode = 54 // Right Shift's scan code is not representable compactly.
	if got := string(far2lInputEvent(key, true)); got != "\x1b_f2lAQBBADYAAAAAAGEAAABL\x07" {
		t.Fatalf("right Shift key event = %q, want full form", got)
	}

	mouse := &vtinput.InputEvent{
		Type: vtinput.MouseEventType, MouseX: 10, MouseY: 5,
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}
	if got, want := string(far2lInputEvent(mouse, true)), "\x1b_f2lCgAFAAEAbQ==\x07"; got != want {
		t.Fatalf("compact mouse event = %q, want %q", got, want)
	}

	mouse.ButtonState = 0xFFFF0000 // wheel-down delta cannot fit the compact encoding.
	if got := string(far2lInputEvent(mouse, true)); got != "\x1b_f2lCgAFAAAA//8AAAAAAAAAAE0=\x07" {
		t.Fatalf("wheel-down event = %q, want full form", got)
	}
}
