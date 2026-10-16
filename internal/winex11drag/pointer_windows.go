//go:build windows

package winex11drag

import (
	"syscall"
	"unsafe"
)

var (
	winex11PointerUser32           = syscall.NewLazyDLL("user32.dll")
	winex11PointerGetCursorPos     = winex11PointerUser32.NewProc("GetCursorPos")
	winex11PointerGetAsyncKeyState = winex11PointerUser32.NewProc("GetAsyncKeyState")
)

const (
	winex11VKLButton = 0x01
	winex11KeyDown   = 0x8000
)

type winex11Point struct {
	X, Y int32
}

type systemPointer struct{}

func (systemPointer) Position() (x, y int, down bool) {
	var point winex11Point
	ok, _, _ := winex11PointerGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	if ok == 0 {
		return 0, 0, false
	}
	state, _, _ := winex11PointerGetAsyncKeyState.Call(winex11VKLButton)
	return pointerPosition(point, true, state)
}

func pointerPosition(point winex11Point, cursorOK bool, keyState uintptr) (x, y int, down bool) {
	if !cursorOK {
		return 0, 0, false
	}
	return int(point.X), int(point.Y), keyState&winex11KeyDown != 0
}
