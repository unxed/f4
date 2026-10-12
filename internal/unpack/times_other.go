//go:build !windows

package unpack

import "time"

// setCreationTime does nothing off Windows: there is no creation time to set.
func setCreationTime(string, time.Time) error { return nil }
