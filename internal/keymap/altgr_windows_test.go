package keymap

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/unxed/vtinput"
	"golang.org/x/sys/windows"
)

func altGrTestLayout(t *testing.T, name string) uintptr {
	t.Helper()
	list := altGrUser32.NewProc("GetKeyboardLayoutList")
	var loaded [64]uintptr
	n, _, _ := list.Call(uintptr(len(loaded)), uintptr(unsafe.Pointer(&loaded[0])))
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	layout, _, err := altGrUser32.NewProc("LoadKeyboardLayoutW").Call(uintptr(unsafe.Pointer(name16)), 0)
	if layout == 0 {
		t.Fatalf("load keyboard layout %s: %v", name, err)
	}
	for _, h := range loaded[:min(n, uintptr(len(loaded)))] {
		if h == layout {
			return layout
		}
	}
	t.Cleanup(func() { altGrUser32.NewProc("UnloadKeyboardLayout").Call(layout) })
	return layout
}

func TestAltGrWindowsLayoutText(t *testing.T) {
	german := altGrTestLayout(t, "00000407")
	us := altGrTestLayout(t, "00000409")
	french := altGrTestLayout(t, "0000040C")
	polish := altGrTestLayout(t, "00000415")
	mods := vtinput.RightAltPressed | vtinput.LeftCtrlPressed
	tests := []struct {
		name                  string
		layout                uintptr
		e                     vtinput.InputEvent
		split, text, consumed bool
	}{
		{"console at", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: '@', ControlKeyState: mods}, false, true, false},
		{"shift altgr letter", polish, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, Char: 'Ą', ControlKeyState: mods | vtinput.ShiftPressed}, false, true, false},
		{"console euro", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_E, Char: '€', ControlKeyState: mods}, false, true, false},
		{"console spacing dead key", french, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_2, Char: '~', ControlKeyState: mods}, false, true, false},
		{"GUI spacing dead key", french, vtinput.InputEvent{Char: '~', ControlKeyState: mods}, true, true, false},
		{"GUI dead key preliminary", french, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_2, Char: '2', ControlKeyState: mods}, true, false, true},
		{"right alt without synthetic ctrl", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: '@', ControlKeyState: vtinput.RightAltPressed}, false, true, false},
		{"GUI preliminary", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: 'q', ControlKeyState: mods}, true, false, true},
		{"GUI char", german, vtinput.InputEvent{Char: '@', ControlKeyState: mods}, true, true, false},
		{"GUI charless preliminary", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, ControlKeyState: mods}, true, false, true},
		{"left alt shortcut", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: '@', ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed}, false, false, false},
		{"extra right ctrl", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: '@', ControlKeyState: mods | vtinput.RightCtrlPressed}, false, false, false},
		{"unassigned layout", us, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: '@', ControlKeyState: mods}, false, false, false},
		{"unassigned GUI", us, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: 'q', ControlKeyState: mods}, true, false, false},
		{"console no text", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, ControlKeyState: mods}, false, false, false},
		{"wrong character", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_Q, Char: 'q', ControlKeyState: mods}, false, false, false},
		{"function shortcut", german, vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F5, ControlKeyState: mods}, true, false, false},
		{"control character", german, vtinput.InputEvent{Char: '\t', ControlKeyState: mods}, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.e
			e.Type, e.KeyDown = vtinput.KeyEventType, true
			original := e
			text, consumed := normalizeAltGrWithLayout(&e, tt.layout, tt.split)
			if text != tt.text || consumed != tt.consumed {
				t.Fatalf("got text=%v consumed=%v, want %v %v", text, consumed, tt.text, tt.consumed)
			}
			if !text {
				if !reflect.DeepEqual(e, original) {
					t.Fatal("non-text event changed")
				}
				return
			}
			if !IsAltGrText(&e) || e.Char != original.Char || e.VirtualKeyCode != 0 || e.ControlKeyState&keyRemapMods != 0 {
				t.Fatalf("text not normalized: %+v", e)
			}
			if EventToHotkeyString(&e) != "" {
				t.Fatal("text resolves to a hotkey")
			}
		})
	}
}
