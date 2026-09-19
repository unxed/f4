//go:build windows

package terminal

import (
	"syscall"
	"unsafe"

	"github.com/unxed/f4/internal/wincon"
	"github.com/unxed/vtui"
)

var (
	procGetConsoleScreenBufferInfoEx = kernel32SimpleExec.NewProc("GetConsoleScreenBufferInfoEx")
	procSetConsoleScreenBufferInfoEx = kernel32SimpleExec.NewProc("SetConsoleScreenBufferInfoEx")
)

// consoleBufferInfoEx is CONSOLE_SCREEN_BUFFER_INFOEX. Only ColorTable matters
// here; the rest is what the API insists on carrying along.
type consoleBufferInfoEx struct {
	Size                uint32
	BufferSize          overlayCoord
	CursorPosition      overlayCoord
	Attributes          uint16
	Window              simpleSmallRect
	MaximumWindowSize   overlayCoord
	PopupAttributes     uint16
	FullscreenSupported int32
	ColorTable          [16]uint32
}

// keepConsoleColorTable remembers the sixteen colours the console had before
// f4 touched it and returns the function that puts them back. Call the result
// once, after vtui has given the terminal back.
//
// f4 loads its theme into the console with OSC 4 and hands the terminal back
// with OSC 104, which asks for the terminal's default table. That leaves the
// colours to whatever the console host does with the sequence: one that does
// not know it keeps f4's theme, and one that does resets to its default, which
// need not be the table the window had when f4 started. Asking the console for
// its table on the way in and writing it back on the way out through the API
// does not depend on either (#1196).
//
// Only a classic console window owns a palette of its own. Under Windows
// Terminal the console on the far side is a pseudo console, and the table that
// matters is the terminal's, which OSC 104 is there for. Every failure is
// silent: a console that will not say what its colours are is left as it is.
func keepConsoleColorTable() (restore func()) {
	restore = func() {}
	if !consoleBufferInfoExAvailable() {
		return
	}
	if _, src := wincon.ConsoleWindow(); !src.Trusted() {
		return
	}
	h, ok := winOverlayHandle()
	if !ok {
		return
	}
	saved, ok := readConsoleBufferInfoEx(h)
	if !ok {
		return
	}
	vtui.DebugLog("PALETTE: console color table saved, %d entries", len(saved.ColorTable))
	return func() {
		restoreConsoleColorTable(h, saved.ColorTable)
	}
}

// consoleBufferInfoExAvailable says whether the console API has the two Ex
// calls. They came with Vista, and LazyProc.Call panics on a missing export
// where Find just reports it, which is the difference between the legacy
// windows/386 build starting on XP and not. There is nothing to keep there in
// any case: the console of XP has no OSC 4 for f4 to have loaded a theme with.
func consoleBufferInfoExAvailable() bool {
	return procGetConsoleScreenBufferInfoEx.Find() == nil &&
		procSetConsoleScreenBufferInfoEx.Find() == nil
}

func readConsoleBufferInfoEx(h syscall.Handle) (consoleBufferInfoEx, bool) {
	info := consoleBufferInfoEx{Size: uint32(unsafe.Sizeof(consoleBufferInfoEx{}))}
	r1, _, _ := procGetConsoleScreenBufferInfoEx.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	return info, r1 != 0
}

// restoreConsoleColorTable writes table into the console, unless the console
// already holds it. It reports whether it had to.
//
// Nothing is written when the table is already right, which is the usual case
// where OSC 104 did its job: SetConsoleScreenBufferInfoEx rewrites the window
// and buffer geometry along with the colours, and the fewer times that is
// asked the better.
func restoreConsoleColorTable(h syscall.Handle, table [16]uint32) bool {
	current, ok := readConsoleBufferInfoEx(h)
	if !ok || current.ColorTable == table {
		return false
	}
	// Set takes back the srWindow that Get gave it, and the two disagree by
	// one row and one column on some versions of Windows: the window comes
	// back smaller every time. The plain GetConsoleScreenBufferInfo is
	// reliable, so hold on to its answer and put the window back if the
	// write moved it.
	before, haveBefore := winOverlayInfo(h)

	current.ColorTable = table
	if r1, _, _ := procSetConsoleScreenBufferInfoEx.Call(uintptr(h), uintptr(unsafe.Pointer(&current))); r1 == 0 {
		vtui.DebugLog("PALETTE: SetConsoleScreenBufferInfoEx refused the saved color table")
		return false
	}
	vtui.DebugLog("PALETTE: console color table restored")

	if haveBefore {
		if after, ok := winOverlayInfo(h); ok && after.Window != before.Window {
			rect := before.Window
			procSetConsoleWindowInfoOverlay.Call(uintptr(h), uintptr(1), uintptr(unsafe.Pointer(&rect)))
		}
	}
	return true
}
