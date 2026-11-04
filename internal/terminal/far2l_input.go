package terminal

import (
	"encoding/base64"

	"github.com/unxed/vtinput"
)

// far2lInputEvent encodes one keyboard or mouse record as an event from the
// terminal to its client. The full records are used until compact input
// negotiation is tracked; unlike terminal escape sequences, these preserve
// key-up events and the distinct left/right modifier bits.
func far2lInputEvent(e *vtinput.InputEvent) []byte {
	if e == nil {
		return nil
	}

	stack := vtinput.Far2lStack{}
	var code byte
	switch e.Type {
	case vtinput.KeyEventType:
		stack.PushU16(e.VirtualKeyCode)
		stack.PushU16(e.VirtualScanCode)
		stack.PushU32(uint32(e.ControlKeyState))
		stack.PushU32(uint32(e.Char))
		repeat := e.RepeatCount
		if repeat == 0 {
			repeat = 1
		}
		stack.PushU16(repeat)
		code = 'k'
		if e.KeyDown {
			code = 'K'
		}
	case vtinput.MouseEventType:
		stack.PushU16(uint16(e.MouseX))
		stack.PushU16(uint16(e.MouseY))
		stack.PushU32(e.ButtonState)
		stack.PushU32(uint32(e.ControlKeyState))
		stack.PushU32(e.MouseEventFlags)
		code = 'M'
	default:
		return nil
	}
	stack.PushU8(code)

	out := make([]byte, 0, len(stack)*4/3+12)
	out = append(out, "\x1b_f2l"...)
	out = base64.StdEncoding.AppendEncode(out, stack)
	out = append(out, '\x07')
	return out
}
