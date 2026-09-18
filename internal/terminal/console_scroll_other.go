//go:build !windows

package terminal

// ScrollHostConsoleToCursor is a no-op off Windows: a Unix terminal scrolls
// with its own output and there is no window inside a larger buffer to move.
func ScrollHostConsoleToCursor() {}
