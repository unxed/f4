package keymap

import (
	"unicode"
	"unicode/utf16"
	"unsafe"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"golang.org/x/sys/windows"
)

var (
	altGrUser32                   = windows.NewLazySystemDLL("user32.dll")
	altGrGetKeyboardLayout        = altGrUser32.NewProc("GetKeyboardLayout")
	altGrGetForegroundWindow      = altGrUser32.NewProc("GetForegroundWindow")
	altGrGetWindowThreadProcessID = altGrUser32.NewProc("GetWindowThreadProcessId")
	altGrToUnicodeEx              = altGrUser32.NewProc("ToUnicodeEx")
	altGrVkKeyScanEx              = altGrUser32.NewProc("VkKeyScanExW")
	// Older Windows versions ignore ToUnicodeEx's non-mutating flag. Do
	// not probe their dead-key state while TranslateMessage is composing.
	altGrCanTranslate = windows.RtlGetVersion().BuildNumber >= 14393
)

// NormalizeAltGr translates only keys the active Windows layout actually
// assigns to AltGr. GUI hosts emit a physical key before WM_CHAR; that first
// event is consumed, leaving the OS-generated character to be inserted once.
func NormalizeAltGr(e *vtinput.InputEvent) (text, consumed bool) {
	if IsAltGrText(e) {
		return true, false
	}
	if !altGrCandidate(e) {
		return false, false
	}
	if !altGrCanTranslate {
		return false, false
	}
	hwnd, _, _ := altGrGetForegroundWindow.Call()
	thread, _, _ := altGrGetWindowThreadProcessID.Call(hwnd, 0)
	layout, _, _ := altGrGetKeyboardLayout.Call(thread)
	backend := vtui.ActiveBackend()
	split := backend == "win32" || backend == "gogpu"
	return normalizeAltGrWithLayout(e, layout, split && e.InputSource == "")
}

func altGrCandidate(e *vtinput.InputEvent) bool {
	return e != nil && e.Type == vtinput.KeyEventType && e.KeyDown &&
		e.ControlKeyState&vtinput.RightAltPressed != 0 &&
		e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightCtrlPressed) == 0
}

func normalizeAltGrWithLayout(e *vtinput.InputEvent, layout uintptr, split bool) (text, consumed bool) {
	if !altGrCandidate(e) || layout == 0 {
		return false, false
	}
	vk := e.VirtualKeyCode
	if vk == 0 {
		// WM_CHAR has no physical key. Ask the layout which key generates
		// this character, rather than assuming every RightAlt glyph is text.
		if !unicode.IsPrint(e.Char) || e.Char > 0xffff {
			return false, false
		}
		key, _, _ := altGrVkKeyScanEx.Call(uintptr(e.Char), layout)
		if uint16(key) == 0xffff || (key>>8)&6 != 6 {
			return false, false
		}
		vk = uint16(key & 0xff)
	}
	var state [256]byte
	state[vtinput.VK_CONTROL], state[vtinput.VK_LCONTROL] = 0x80, 0x80
	state[vtinput.VK_MENU], state[vtinput.VK_RMENU] = 0x80, 0x80
	if e.ControlKeyState&vtinput.ShiftPressed != 0 {
		state[vtinput.VK_SHIFT] = 0x80
	}
	if e.ControlKeyState&vtinput.CapsLockOn != 0 {
		state[vtinput.VK_CAPITAL] = 1
	}
	var buf [8]uint16
	n, _, _ := altGrToUnicodeEx.Call(uintptr(vk), uintptr(e.VirtualScanCode),
		uintptr(unsafe.Pointer(&state[0])), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		4, layout) // Do not mutate Windows' dead-key state (Windows 10 1607+).
	if int32(n) < 0 {
		// A GUI dead key must wait for composition, never run an accelerator.
		if split && e.VirtualKeyCode != 0 {
			return false, true
		}
		// Windows also returns the spacing glyph for a dead key. A real
		// input record can contain that glyph after pressing the key twice.
		n = 1
	}
	if n == 0 || n > uintptr(len(buf)) {
		return false, false
	}
	runes := utf16.Decode(buf[:n])
	if len(runes) != 1 || !unicode.IsPrint(runes[0]) {
		return false, false
	}
	if split && e.VirtualKeyCode != 0 {
		vtui.DebugLog("[FIX:1815] AltGr physical key deferred to Windows text input: vk=%d", vk)
		return false, true
	}
	if e.Char != runes[0] {
		return false, false
	}
	markAltGrText(e)
	syncKeyBarModifiers(e)
	vtui.DebugLog("[FIX:1815] AltGr layout text accepted: U+%04X", e.Char)
	return true, false
}
