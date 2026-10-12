//go:build windows

package terminal

import (
	"strings"
	"unsafe"

	"github.com/unxed/vtui"
)

// ReadHostConsoleFullText reads the entire host console screen buffer --
// not just the visible window, but info.Size, which under a console that
// actually has scrollback (e.g. wineconsole: dwSize taller than srWindow,
// see WINE.md §2g) covers everything scrolled off the top too -- and
// returns it as text, one line per buffer row with trailing blanks trimmed.
//
// This is the F3/F4 (Terminal.ViewLog/EditLog) data source for
// ShellModeSimpleInline: there is no PTY in that mode, so pf.termView is
// never fed by anything the user actually ran. runSimpleInlineCommand()
// execs the child with inherited stdio straight into this same console
// buffer, bypassing the ANSI parser and termView entirely -- so termView's
// own log is always empty here, and the console buffer itself is the only
// real record of command history to show. See simple_exec.go and
// TerminalLogVFS.
//
// overlayLines excludes the trailing rows the console-view overlay itself
// owns (the command line and, if shown, the keybar -- see
// PanelsFrame.overlayLines()): those are f4's own UI chrome, redrawn fresh
// on every keystroke, not history. Without excluding them, F3/F4 would
// include a Snapshot of whatever the command line/keybar happened to say at
// the moment the log was opened, right in the middle of the text.
func ReadHostConsoleFullText(overlayLines int) []byte {
	hOut, ok := winOverlayHandle()
	if !ok {
		return nil
	}
	info, ok := winOverlayInfo(hOut)
	if !ok {
		return nil
	}
	w := int(info.Size.X)
	h := int(info.Size.Y)
	if w <= 0 || h <= 0 {
		return nil
	}

	// The overlay is pinned to the bottom of the *visible window*
	// (info.Window.Bottom), not necessarily the bottom of the full buffer --
	// same arithmetic as WinDrawConsoleOverlay's cmdRow. Exclude that same
	// range from the log text.
	excludeFrom := h
	if overlayLines > 0 {
		cmdRow := int(info.Window.Bottom) - overlayLines + 1
		if cmdRow >= 0 && cmdRow < h {
			excludeFrom = cmdRow
		}
	}

	// Read in horizontal strips small enough for the console's shared heap
	// (ConsoleReadRows): one call for the whole (possibly tall) scrollback
	// buffer, or even 200 rows of it, fails on XP (f4#897, item 3).
	rowsPerCall := ConsoleReadRows(w)
	cells := make([]simpleCharInfo, w*h)
	for top := 0; top < h; top += rowsPerCall {
		bottom := top + rowsPerCall - 1
		if bottom > h-1 {
			bottom = h - 1
		}
		rows := bottom - top + 1
		chunk := make([]simpleCharInfo, w*rows)
		bufSize := uintptr(uint32(uint16(w)) | (uint32(uint16(rows)) << 16))
		region := simpleSmallRect{Left: 0, Top: int16(top), Right: int16(w - 1), Bottom: int16(bottom)}
		r1, _, callErr := procReadConsoleOutputW.Call(
			uintptr(hOut),
			uintptr(unsafe.Pointer(&chunk[0])),
			bufSize,
			0,
			uintptr(unsafe.Pointer(&region)),
		)
		if r1 == 0 {
			// The rows stay NUL and read as blank lines (consoleRowText).
			vtui.DebugLog("CONSOLE_LOG: ReadConsoleOutputW rows %d..%d of %dx%d failed: %v", top, bottom, w, h, callErr)
		}
		copy(cells[top*w:(top+rows)*w], chunk)
	}

	var sb strings.Builder
	chars := make([]uint16, w)
	for row := 0; row < excludeFrom; row++ {
		for i, c := range cells[row*w : (row+1)*w] {
			chars[i] = c.UnicodeChar
		}
		sb.WriteString(consoleRowText(chars))
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}
