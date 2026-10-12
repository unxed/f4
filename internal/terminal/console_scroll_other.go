//go:build !windows

package terminal

// ScrollHostConsoleToCursor is a no-op off Windows: a Unix terminal scrolls
// with its own output and there is no window inside a larger buffer to move.
func ScrollHostConsoleToCursor() {}

// MoveHostConsoleCursorToBottom is a no-op off Windows: there is no host
// console window inside a larger buffer to place the cursor in.
func MoveHostConsoleCursorToBottom() {}
