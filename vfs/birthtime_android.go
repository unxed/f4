//go:build android

package vfs

import "time"

// Android's app sandbox may terminate the process with SIGSYS when statx is
// called (for example, from the file-attributes dialog in Termux). Treat the
// birth time as unavailable rather than making an unsupported syscall.
func ReadBirthTime(string) (time.Time, bool) { return time.Time{}, false }
