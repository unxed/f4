//go:build !windows

package main

// resolveWindowsCommand is a no-op on non-Windows platforms.
func resolveWindowsCommand(cmd string) string {
	return cmd
}

// isBatchCommand is always false on non-Windows platforms.
func isBatchCommand(cmd string) bool {
	return false
}
