//go:build windows

package terminal

import (
	"syscall"
	"unsafe"
)

var procSetConsoleWindowInfoScroll = kernel32SimpleExec.NewProc("SetConsoleWindowInfo")

// ScrollHostConsoleToCursor brings the host console's visible window down to
// wherever the cursor ended up, when the console did not do it itself.
//
// Real Windows scrolls the window as output passes the bottom row, so this is
// a no-op there. ReactOS 0.4.16 does not -- measured on the live system, see
// WindowFollowingCursor and WINE.md (issue #513) -- and the result was a
// command whose output ran off the bottom of the window into buffer rows
// nobody could see, while the screen kept showing what had been there before.
//
// Call it after a child process has written to the host console and before
// anything snapshots that console: CaptureHostConsoleBuffer reads the
// rectangle at srWindow.Top, so without this it faithfully preserves the stale
// rows instead of the output.
//
// Every failure is silent on purpose. This is a display nicety on a path that
// must not fail a command that already ran: if the handle is not a console
// (f4's stdout redirected to a file), if the info read fails, or if the
// platform refuses the call, leaving the window where it is costs exactly the
// behaviour we had before.
func ScrollHostConsoleToCursor() {
	hOut, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || hOut == 0 || hOut == syscall.InvalidHandle {
		return
	}
	var info overlayBufferInfo
	if r1, _, _ := procGetConsoleScreenBufferInfoOverlay.Call(uintptr(hOut),
		uintptr(unsafe.Pointer(&info))); r1 == 0 {
		return
	}
	win := ConsoleWindowRect{
		Left:   info.Window.Left,
		Top:    info.Window.Top,
		Right:  info.Window.Right,
		Bottom: info.Window.Bottom,
	}
	want, ok := WindowFollowingCursor(win, info.CursorPosition.Y, info.Size.Y)
	if !ok {
		return
	}
	rect := simpleSmallRect{
		Left:   want.Left,
		Top:    want.Top,
		Right:  want.Right,
		Bottom: want.Bottom,
	}
	// absolute = TRUE: rect is buffer coordinates, not a delta.
	procSetConsoleWindowInfoScroll.Call(uintptr(hOut), 1, uintptr(unsafe.Pointer(&rect)))
}
