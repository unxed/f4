//go:build !windows

package terminal

// LogConsoleState is a no-op off Windows: there is one stream and no second
// screen buffer to confuse it with.
func LogConsoleState(tag string) {}
