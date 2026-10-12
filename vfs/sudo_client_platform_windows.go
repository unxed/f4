//go:build windows

package vfs

// Windows elevates through UAC: SudoClient.Connect starts the elevated
// dispatcher of f4#1768 (connectsByUAC), never sudo.
func sudoClientSupported() bool { return true }
