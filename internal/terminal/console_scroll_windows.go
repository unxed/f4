//go:build windows

package terminal

import (
	"syscall"
	"unsafe"

	"github.com/unxed/vtui"
)

var (
	procSetConsoleWindowInfoScroll = kernel32SimpleExec.NewProc("SetConsoleWindowInfo")
	procGetConsoleWindowScroll     = kernel32SimpleExec.NewProc("GetConsoleWindow")
	procInvalidateRectScroll       = syscall.NewLazyDLL("user32.dll").NewProc("InvalidateRect")
)

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
	r1, _, callErr := procSetConsoleWindowInfoScroll.Call(uintptr(hOut), 1, uintptr(unsafe.Pointer(&rect)))
	// Only reached where the console left the cursor outside its window, which
	// real Windows never does, so this line is quiet there and says exactly
	// what happened where it matters.
	vtui.DebugLog("CONSOLE: window T%d..B%d -> T%d..B%d for cursor row %d: ok=%v err=%v",
		win.Top, win.Bottom, want.Top, want.Bottom, info.CursorPosition.Y, r1 != 0, callErr)
	if r1 != 0 {
		repaintConsoleWindow()
	}
}

// repaintConsoleWindow asks the console window to paint itself again.
//
// Moving the window is not enough on ReactOS 0.4.16: SetConsoleWindowInfo
// moves it -- GetConsoleScreenBufferInfo reads the new rectangle back, and
// the next call starts from it -- but the console does not repaint, so the
// screen keeps the pixels of wherever the window was before. Measured: the
// log recorded T36..B60 -> T78..B102 and then T78..B102 -> T79..B103, both
// accepted, while the screen still showed rows 36 to 60 (WINE.md §17.6).
//
// InvalidateRect only marks the window for painting; the WM_PAINT arrives
// through the console host's own message loop in its own time. Nothing here
// sends a message to the console window or waits for it -- the rule
// internal/wincon keeps for the same window, for the reason written there.
// A zero window handle is skipped on purpose: InvalidateRect(NULL, ...)
// would repaint every window on the desktop.
func repaintConsoleWindow() {
	hwnd, _, _ := procGetConsoleWindowScroll.Call()
	if hwnd == 0 {
		return
	}
	procInvalidateRectScroll.Call(hwnd, 0, 1) // whole client area, erase background
}
