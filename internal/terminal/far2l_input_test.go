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
			if got := string(far2lInputEvent(tt.event)); got != tt.want {
				t.Fatalf("far2l event = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFar2lInputEventRejectsNonInput(t *testing.T) {
	if got := far2lInputEvent(&vtinput.InputEvent{Type: vtinput.FocusEventType}); got != nil {
		t.Fatalf("non-input event = %q, want nil", got)
	}
	if got := far2lInputEvent(nil); got != nil {
		t.Fatalf("nil event = %q, want nil", got)
	}
}
