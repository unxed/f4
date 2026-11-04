package terminal

import (
	"encoding/base64"

	"github.com/unxed/vtinput"
)

const far2lFeatCompactInput uint64 = 1

// far2lInputEvent encodes one keyboard or mouse record as an event from the
// terminal to its client. Compact records are used only after negotiation and
// when they preserve the input; unlike terminal escape sequences, the events
// preserve key-up events and distinct left/right modifier bits.
func far2lInputEvent(e *vtinput.InputEvent, compactInput bool) []byte {
	if e == nil {
		return nil
	}

	stack := vtinput.Far2lStack{}
	var code byte
	switch e.Type {
	case vtinput.KeyEventType:
		repeat := e.RepeatCount
		if repeat == 0 {
			repeat = 1
		}
		if compactInput && repeat == 1 && e.VirtualScanCode != 54 && e.Char >= 0 && e.Char < 0x10000 &&
			uint32(e.ControlKeyState) < 0x10000 && e.VirtualKeyCode < 0x100 {
			stack.PushU8(uint8(e.VirtualKeyCode))    // #nosec G115 -- compact form selected only when the key code fits in 8 bits
			stack.PushU16(uint16(e.ControlKeyState)) // #nosec G115 -- compact form selected only when the state fits in 16 bits
			stack.PushU16(uint16(e.Char))            // #nosec G115 -- compact form selected only for non-negative characters below 0x10000
			code = 'c'
			if e.KeyDown {
				code = 'C'
			}
		} else {
			stack.PushU16(repeat)
			stack.PushU16(e.VirtualKeyCode)
			stack.PushU16(e.VirtualScanCode)
			stack.PushU32(uint32(e.ControlKeyState))
			stack.PushU32(uint32(e.Char)) // #nosec G115 -- rune is int32; encoding its bit pattern as the protocol's u32 preserves every value
			code = 'k'
			if e.KeyDown {
				code = 'K'
			}
		}
	case vtinput.MouseEventType:
		if compactInput && e.ButtonState&0xFF00FF00 == 0 && uint32(e.ControlKeyState) < 0x100 && e.MouseEventFlags < 0x100 {
			stack.PushU16(uint16(e.MouseX))                                           // #nosec G115 -- mouse coordinates are signed 16-bit values serialized as their u16 wire bit patterns
			stack.PushU16(uint16(e.MouseY))                                           // #nosec G115 -- mouse coordinates are signed 16-bit values serialized as their u16 wire bit patterns
			buttons := uint16(e.ButtonState&0xFF) | uint16((e.ButtonState>>8)&0xFF00) // #nosec G115 -- both masked fields fit in 16 bits
			stack.PushU16(buttons)
			stack.PushU8(uint8(e.ControlKeyState)) // #nosec G115 -- compact form selected only when the state fits in 8 bits
			stack.PushU8(uint8(e.MouseEventFlags)) // #nosec G115 -- compact form selected only when the flags fit in 8 bits
			code = 'm'
		} else {
			stack.PushU16(uint16(e.MouseX)) // #nosec G115 -- mouse coordinates are signed 16-bit values serialized as their u16 wire bit patterns
			stack.PushU16(uint16(e.MouseY)) // #nosec G115 -- mouse coordinates are signed 16-bit values serialized as their u16 wire bit patterns
			stack.PushU32(e.ButtonState)
			stack.PushU32(uint32(e.ControlKeyState))
			stack.PushU32(e.MouseEventFlags)
			code = 'M'
		}
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

// Far2lInputEvent returns the event encoding while a child has the far2l
// extensions active. Compact encodings are used only after the child requests
// FARTTY_FEAT_COMPACT_INPUT and only when they preserve every field.
func (tv *TerminalView) Far2lInputEvent(e *vtinput.InputEvent) []byte {
	if tv == nil || !tv.far2lExtensionsOn.Load() {
		return nil
	}
	return far2lInputEvent(e, tv.far2lCompactInput.Load())
}
