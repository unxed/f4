package terminal

// ConsoleWindowRect is the geometry of a console's visible window inside its
// screen buffer, in buffer rows/columns. It mirrors Win32's SMALL_RECT and is
// declared without a build tag so the arithmetic below can be tested on any
// platform.
type ConsoleWindowRect struct {
	Left, Top, Right, Bottom int16
}

// WindowFollowingCursor answers "where should the visible window sit so that
// the cursor is on screen", given the window it sits in now, the cursor row,
// and the height of the whole buffer. ok is false when the window already
// contains the cursor and nothing needs to move.
//
// Why this exists: a Windows console scrolls its window down as output passes
// the bottom row, so the cursor is always visible. ReactOS 0.4.16 does not --
// measured on the live system (WINE.md; issue #513): after a command printed
// past the window's last row the window stayed exactly where it was
// (L0 T12 R79 B36 before and after) while the cursor moved from row 36 to row
// 48. The command's entire output sat in buffer rows 37-48, below the visible
// window, which is why "the command runs, the file appears, and the screen
// still shows what was there before" was reported as invisible output.
//
// The window keeps its height and its columns; only Top/Bottom move, and only
// downward. The result is clamped to the buffer so a cursor near the end of a
// 300-row buffer cannot ask for a window that runs off it.
func WindowFollowingCursor(win ConsoleWindowRect, cursorY, bufferH int16) (ConsoleWindowRect, bool) {
	height := win.Bottom - win.Top
	if height < 0 {
		return win, false
	}
	if cursorY <= win.Bottom && cursorY >= win.Top {
		return win, false
	}
	top := cursorY - height
	if top < 0 {
		top = 0
	}
	// Never scroll upward: the caller's case is output running off the
	// bottom, and a cursor above the window (a program that homed it) is
	// not something to chase -- moving there would hide the output that
	// was the reason to look.
	if top <= win.Top {
		return win, false
	}
	if max := bufferH - 1 - height; max >= 0 && top > max {
		top = max
	}
	if top == win.Top {
		return win, false
	}
	return ConsoleWindowRect{
		Left:   win.Left,
		Top:    top,
		Right:  win.Right,
		Bottom: top + height,
	}, true
}
