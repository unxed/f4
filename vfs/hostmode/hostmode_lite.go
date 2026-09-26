//go:build lite

// See hostmode.go: this is the same package, built instead of it under the
// lite tag so that "github.com/unxed/libwinescape/go" is never imported, let
// alone linked, in a lite build. Every exported name here mirrors the real
// file's signature, so vfs/hostfs and vfs/hostpath (and anything else that
// asks hostmode a question) need no changes: they get the same answer a
// non-Windows build of the real file already gives — Posix is always
// false — just without the dependency that answer never needed anyway.
package hostmode

import (
	"os"
	"sync"
)

var (
	allowedMu sync.Mutex
	allowed   = true
)

// SetAllowed mirrors the real one's contract; a lite build has no posix
// personality to allow or disallow, but keeps the setting so callers that
// read it back (Allowed) see what they set.
func SetAllowed(v bool) {
	allowedMu.Lock()
	defer allowedMu.Unlock()
	allowed = v
}

// Allowed mirrors the real one's contract.
func Allowed() bool {
	allowedMu.Lock()
	defer allowedMu.Unlock()
	return allowed
}

// Posix is always false in a lite build: there is no libwinescape linked in
// to detect Wine with, and Wine support itself is out of scope for lite
// (f4#1178).
func Posix() bool { return false }

// HomeDir mirrors the real one's contract for every platform where Posix()
// is false, which in a lite build is every platform.
func HomeDir() (string, bool) { return "", false }

// UserHomeDir mirrors the real drop-in replacement for os.UserHomeDir.
func UserHomeDir() (string, error) { return os.UserHomeDir() }
