//go:build !linux

package app

// setProcessName is a no-op outside Linux: Windows and macOS already show a
// sensible process name (f4 #1390 is Linux-specific — see procname_linux.go).
func setProcessName() {}
