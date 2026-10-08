package keymap

import "github.com/unxed/vtinput"

const altGrTextSource = "f4-altgr"

// IsAltGrText identifies layout-confirmed text even after its synthetic
// Ctrl/Alt flags have been removed. It must never resolve to a hotkey.
func IsAltGrText(e *vtinput.InputEvent) bool {
	return e != nil && e.Type == vtinput.KeyEventType && e.KeyDown && e.InputSource == altGrTextSource
}
