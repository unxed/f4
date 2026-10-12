//go:build !linux

package vfs

import "time"

// ReadBirthTime is only needed where listings carry no birth time of their own:
// Windows, macOS and the BSDs fill VFSItem.BTime at stat time, the other Unixes
// have no such call. It reports "unknown".
func ReadBirthTime(path string) (time.Time, bool) { return time.Time{}, false }
