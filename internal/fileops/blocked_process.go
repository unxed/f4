package fileops

import "errors"

// ErrProcessLookupUnsupported means this platform does not provide the
// file-holder lookup used by f4.
var ErrProcessLookupUnsupported = errors.New("file-holder lookup is unsupported on this platform")

// FileProcess describes a process reported by the operating system as using a file.
// It is best-effort information; an empty result does not prove that no process
// holds the file.
type FileProcess struct {
	PID  uint32
	Name string
}
