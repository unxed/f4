package macro

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
)

// Ordinary macro keys keep Far's readable spelling. Confirmed text needs a
// distinct token: spelling it as CtrlAltQ would replay a command instead of @.
// The versioned token also works inside Keys() in an exported f4 Lua macro.
const textEventPrefix = "f4-text-v1:"

type recordedTextEvent struct {
	Char      rune                    `json:"char"`
	VK        uint16                  `json:"vk,omitempty"`
	Scan      uint16                  `json:"scan,omitempty"`
	Unshifted rune                    `json:"unshifted,omitempty"`
	Mods      vtinput.ControlKeyState `json:"mods,omitempty"`
	Repeat    uint16                  `json:"repeat,omitempty"`
	Source    string                  `json:"source,omitempty"`
	Legacy    bool                    `json:"legacy,omitempty"`
}

func encodeMacroEvent(event *vtinput.InputEvent) string {
	if event == nil {
		return ""
	}
	if !event.TextInput {
		return keymap.EventToFarString(event)
	}
	if event.Type != vtinput.KeyEventType || !event.KeyDown || !validTextRune(event.Char) {
		return ""
	}
	data, err := json.Marshal(recordedTextEvent{
		Char: event.Char, VK: event.VirtualKeyCode, Scan: event.VirtualScanCode,
		Unshifted: event.UnshiftedChar, Mods: event.ControlKeyState,
		Repeat: event.RepeatCount, Source: event.InputSource, Legacy: event.IsLegacy,
	})
	if err != nil {
		return ""
	}
	return textEventPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeMacroEvent(token string) *vtinput.InputEvent {
	if !strings.HasPrefix(token, textEventPrefix) {
		if strings.HasPrefix(token, "f4-text-") {
			return nil
		}
		return keymap.ParseFarKey(token)
	}
	// A keyboard event is small. Reject damaged or unbounded encoded tokens
	// before allocating their decoded payload.
	if len(token) > 4096 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, textEventPrefix))
	if err != nil {
		return nil
	}
	var text recordedTextEvent
	if json.Unmarshal(data, &text) != nil || !validTextRune(text.Char) ||
		(text.Unshifted != 0 && !utf8.ValidRune(text.Unshifted)) {
		return nil
	}
	return &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: text.Char, VirtualKeyCode: text.VK, VirtualScanCode: text.Scan,
		UnshiftedChar: text.Unshifted, ControlKeyState: text.Mods,
		RepeatCount: text.Repeat, InputSource: text.Source, IsLegacy: text.Legacy,
	}
}

func validTextRune(char rune) bool {
	return char >= ' ' && char != 0x7f && utf8.ValidRune(char)
}
