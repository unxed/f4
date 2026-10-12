//go:build windows

package terminal

import (
	"math"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32SimpleExec      = syscall.NewLazyDLL("kernel32.dll")
	procReadConsoleOutputW  = kernel32SimpleExec.NewProc("ReadConsoleOutputW")
	procWriteConsoleOutputW = kernel32SimpleExec.NewProc("WriteConsoleOutputW")
)

type simpleCharInfo struct {
	UnicodeChar uint16
	Attributes  uint16
}

type simpleSmallRect struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}

var (
	savedHostConsoleBuffer []simpleCharInfo
	savedHostConsoleW      int
	savedHostConsoleH      int
	savedHostConsoleTop    int16
	savedHostConsoleMu     sync.Mutex
)

// currentWindowTop reads srWindow.Top off the live console, defaulting to 0
// (the old, always-absolute-row-0 behavior) when the read fails. Snapshotting
// and restoring at the buffer's row 0 only happens to match the visible
// window when there is no scrollback (dwSize == srWindow); the moment
// scrollback exists -- the ordinary case under wineconsole, see WINE.md
// §2g -- row 0 is whatever was printed first in the session, not what the
// user is currently looking at.
func currentWindowTop(h syscall.Handle) int16 {
	var info overlayBufferInfo
	if r1, _, _ := procGetConsoleScreenBufferInfoOverlay.Call(uintptr(h), uintptr(unsafe.Pointer(&info))); r1 != 0 {
		return info.Window.Top
	}
	return 0
}

func CaptureHostConsoleBuffer(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	hOut, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || hOut == 0 || hOut == syscall.InvalidHandle {
		return
	}
	top := currentWindowTop(hOut)

	savedHostConsoleMu.Lock()
	defer savedHostConsoleMu.Unlock()
	size := w * h
	if len(savedHostConsoleBuffer) != size {
		savedHostConsoleBuffer = make([]simpleCharInfo, size)
	}
	savedHostConsoleW = w
	savedHostConsoleH = h
	savedHostConsoleTop = top

	consoleBlit(procReadConsoleOutputW, hOut, savedHostConsoleBuffer, w, h, top)
}

// consoleBlit reads (ReadConsoleOutputW) or writes (WriteConsoleOutputW) the
// w*h cells of buf at rows top.. of the console, in strips
// (ConsoleStrips). One call for a whole large window does not fit the
// console's 64 KB shared heap on XP (f4#897): the read left the snapshot for
// Ctrl+O empty and the write did not restore it. Failures stay silent, as they
// always were here.
func consoleBlit(proc *syscall.LazyProc, h syscall.Handle, buf []simpleCharInfo, w, rowsTotal int, top int16) {
	// A console is at most 32767 cells on a side; anything else did not come
	// from one, and the conversions below rely on it.
	if w <= 0 || w > math.MaxInt16 || rowsTotal > math.MaxInt16 || len(buf) < w*rowsTotal {
		return
	}
	for _, strip := range ConsoleStrips(w, rowsTotal) {
		row, rows := strip.Row, strip.Rows
		bufSize := uintptr(uint32(uint16(w)) | (uint32(uint16(rows)) << 16)) //nolint:gosec // w and rows are within int16, checked above
		region := simpleSmallRect{
			Left:   0,
			Top:    top + int16(row),        //nolint:gosec // row < rowsTotal <= MaxInt16
			Right:  int16(w - 1),            //nolint:gosec // w <= MaxInt16
			Bottom: top + int16(row+rows-1), //nolint:gosec // below rowsTotal <= MaxInt16
		}
		_, _, _ = proc.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&buf[row*w])),
			bufSize,
			0,
			uintptr(unsafe.Pointer(&region)),
		)
	}
}

func RestoreHostConsoleBuffer() {
	savedHostConsoleMu.Lock()
	defer savedHostConsoleMu.Unlock()
	if len(savedHostConsoleBuffer) == 0 || savedHostConsoleW <= 0 || savedHostConsoleH <= 0 {
		return
	}
	hOut, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || hOut == 0 || hOut == syscall.InvalidHandle {
		return
	}
	w, h := savedHostConsoleW, savedHostConsoleH
	// Write back at the window's *current* Top, not the Top the snapshot was
	// taken at. The two calls are normally back-to-back (see simple_exec.go),
	// but if anything scrolled the console in between, blitting at the old
	// absolute rows would land the saved content off the currently visible
	// window instead of on it. Falls back to row 0 if the live read fails,
	// same as the old unconditional behavior.
	top := currentWindowTop(hOut)
	consoleBlit(procWriteConsoleOutputW, hOut, savedHostConsoleBuffer, w, h, top)
}

// HostConsoleBufferMatches reports whether the saved snapshot still describes a
// screen of the given size.
func HostConsoleBufferMatches(w, h int) bool {
	savedHostConsoleMu.Lock()
	defer savedHostConsoleMu.Unlock()
	return len(savedHostConsoleBuffer) > 0 && savedHostConsoleW == w && savedHostConsoleH == h
}
