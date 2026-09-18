//go:build windows

package terminal

import (
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/unxed/vtui"
)

// LogConsoleState answers, in one compact line per handle, "which console is
// this process actually writing to, and what is on it".
//
// It exists for WINE.md 17.3a: f4's own fmt.Print reaches the screen while a
// child's output does not, although both go to os.Stdout of the same process.
// The two candidate explanations differ in exactly these numbers -- Go caches
// os.Stdout at process start, so if anything called SetStdHandle afterwards
// the cached file and STD_OUTPUT_HANDLE are different buffers, and code that
// reads the console through GetStdHandle is looking somewhere else again.
//
// Output is one line per distinct handle plus the text of the row above the
// cursor, which is where a child's last line of output would sit. Keep it
// short: these logs are meant to be readable, and to be pasted into a ticket.
func LogConsoleState(tag string) {
	fd := syscall.Handle(os.Stdout.Fd())
	std, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	vtui.DebugLog("EXECDIAG[%s] os.Stdout=%#x stdHandle=%#x same=%v", tag, fd, std, fd == std)
	logOneConsole(tag, "os.Stdout", fd)
	if std != fd {
		logOneConsole(tag, "stdHandle", std)
	}
}

func logOneConsole(tag, name string, h syscall.Handle) {
	if h == 0 || h == syscall.InvalidHandle {
		vtui.DebugLog("EXECDIAG[%s] %s: no handle", tag, name)
		return
	}
	var info overlayBufferInfo
	if r1, _, e := procGetConsoleScreenBufferInfoOverlay.Call(uintptr(h),
		uintptr(unsafe.Pointer(&info))); r1 == 0 {
		vtui.DebugLog("EXECDIAG[%s] %s=%#x: not a console (%v)", tag, name, h, e)
		return
	}
	vtui.DebugLog("EXECDIAG[%s] %s=%#x buf=%dx%d win=T%d,B%d cur=(%d,%d) last=%q",
		tag, name, h, info.Size.X, info.Size.Y, info.Window.Top, info.Window.Bottom,
		info.CursorPosition.X, info.CursorPosition.Y,
		readConsoleRow(h, info.CursorPosition.Y-1, info.Size.X))
}

// readConsoleRow returns one row of a screen buffer as trimmed text, so the
// log can say what is actually sitting where the child's output should be.
func readConsoleRow(h syscall.Handle, row, width int16) string {
	if row < 0 || width <= 0 {
		return ""
	}
	if width > 120 {
		width = 120
	}
	buf := make([]simpleCharInfo, width)
	region := simpleSmallRect{Left: 0, Top: row, Right: width - 1, Bottom: row}
	r1, _, _ := procReadConsoleOutputW.Call(uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(uint32(uint16(width))|(uint32(1)<<16)), 0,
		uintptr(unsafe.Pointer(&region)))
	if r1 == 0 {
		return "<read failed>"
	}
	var sb strings.Builder
	for i := range buf {
		c := rune(buf[i].UnicodeChar)
		if c == 0 {
			c = ' '
		}
		sb.WriteRune(c)
	}
	return strings.TrimRight(sb.String(), " ")
}
