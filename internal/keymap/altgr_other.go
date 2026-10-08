//go:build !windows

package keymap

import "github.com/unxed/vtinput"

// NormalizeAltGr leaves non-Windows backend events unchanged.
func NormalizeAltGr(e *vtinput.InputEvent) (text, consumed bool) {
	return IsAltGrText(e), false
}
