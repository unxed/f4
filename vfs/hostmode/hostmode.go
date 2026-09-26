//go:build !lite

// Package hostmode holds the single, once-decided answer to "which
// personality does the file layer run in" (WINE.md §13, Part E): posix
// (real POSIX paths and syscalls via libwinescape) or windows (today's
// Win32/os.* behavior, unchanged). vfs/hostfs and vfs/hostpath both consult
// it so the decision is made in exactly one place, not duplicated.
//
// A lite build never links libwinescape at all (see hostmode_lite.go): Wine
// support is out of scope for a lite build ("no separate Wine-specific code
// or the winescape dependency" per f4#1178), and this package is the single
// point of truth for that dependency, exactly as its own doc comment above
// already says for the posix/windows decision.
package hostmode

import (
	"os"
	"sync"

	winescape "github.com/unxed/libwinescape/go"
	"github.com/unxed/vtui"
)

var (
	once  sync.Once
	posix bool

	// allowedMu guards the UseWinescape setting. It is read once, inside
	// once.Do, and written once, from startup -- the mutex is here so that
	// a late write is seen as a write rather than as a data race.
	allowedMu sync.Mutex
	allowed   = true
	decided   bool
)

// SetAllowed records the user's UseWinescape setting: false means the file
// layer must not use libwinescape at all, whatever the probe below would say.
// Call it before anything touches the file layer; f4 does that right after the
// configuration is read.
//
// A call that arrives after the personality has been decided cannot change it
// -- WINE.md 13.4 forbids switching live, because panel goroutines would race
// through a half-switched layer -- so it only takes effect from the next
// start, which is what the settings dialog tells the user.
func SetAllowed(v bool) {
	allowedMu.Lock()
	defer allowedMu.Unlock()
	allowed = v
	if decided {
		vtui.DebugLog("[f4-wine] UseWinescape=%v arrived after the personality was decided (posix=%v); it applies from the next start", v, posix)
	}
}

// Allowed reports the setting as it stands. Callers outside the file layer --
// the one other place libwinescape is used, the detached copy's stderr fix --
// ask this before reaching for it, so that the setting covers every use and
// not merely most of them.
func Allowed() bool {
	allowedMu.Lock()
	defer allowedMu.Unlock()
	return allowed
}

// Posix reports whether the host-filesystem layer should run in posix
// personality. Decided once, the first time this is called, and never
// changes afterward: WINE.md §13.4 requires this, because switching live
// would race panel goroutines into a half-switched state. A future config
// setting (WINE.md Stage D7/E6) applies from the next restart, not live --
// this function is where that restriction is enforced by construction, not
// just by convention.
//
// On every non-Windows GOOS this is unconditionally false: os.*/path/filepath
// already speak POSIX there, so hostfs/hostpath never have a second
// personality to switch to (winescape.IsWine/Available are themselves
// hard-wired false outside Windows, so this would resolve to false even
// without the GOOS check -- it's here for clarity, not correctness).
func Posix() bool {
	once.Do(func() {
		allowedMu.Lock()
		on := allowed
		decided = true
		allowedMu.Unlock()
		if !on {
			vtui.DebugLog("[f4-wine] posix mode: off, libwinescape is disabled by UseWinescape")
			return
		}
		// Escape hatch ahead of the real config setting (WINE.md Stage D7):
		// F4_WINE_POSIX=1 forces posix mode on for debugging/testing even if
		// the automatic probe below would say no; =0 forces it off. Anything
		// else (unset, or any other value) falls through to auto-detection.
		switch os.Getenv("F4_WINE_POSIX") {
		case "1":
			posix = true
			vtui.DebugLog("[f4-wine] posix mode: forced ON via F4_WINE_POSIX=1")
			return
		case "0":
			posix = false
			vtui.DebugLog("[f4-wine] posix mode: forced OFF via F4_WINE_POSIX=0")
			return
		}
		isWine := winescape.IsWine()
		available := winescape.Available()
		hostOS := winescape.HostOS()
		posix = isWine && available
		// The answer to "why didn't posix mode turn on" (WINE.md §13) goes
		// to the debug log, not to stderr. In f4's startup, by the time
		// anything asks, stderr is already the session's crash log
		// (vtui.SetupStderrLog), so a print here never reached a terminal;
		// it only made that file non-empty, and a non-empty crash log is
		// kept on exit -- every start on Windows and under Wine left one
		// behind (issue #474).
		vtui.DebugLog("[f4-wine] posix mode probe: IsWine=%v Available=%v HostOS=%q -> posix=%v",
			isWine, available, hostOS, posix)
	})
	return posix
}

// HomeDir answers "$HOME" for posix personality (WINE.md §18.2, "$HOME,
// $XDG_CONFIG_HOME"): the host's real Unix home directory, read straight from
// the host environment via libwinescape's /proc/self/environ path, bypassing
// Wine's own Win32 environment block entirely.
//
// That bypass is not a style choice: Wine's get_initial_environment
// (dlls/ntdll/unix/env.c) treats HOME as one of exactly four Unix-only
// variables -- alongside PATH, TEMP and TMP -- that it strips from the Win32
// process environment on principle, so os.Getenv("HOME") and os.UserHomeDir()
// never see it under Wine at all; only the WINE-prefixed shadow (WINEHOMEDIR)
// crosses over, and that is Wine's prefix-relative notion of home, not the
// host user's.
//
// The second result is false when there is nothing to report: either the
// file layer is not in posix personality, or (rarely) the host process
// itself has no HOME set. Either way the caller's existing os.UserHomeDir()
// fallback is the right answer, exactly as it was before Part E -- see
// UserHomeDir below for the drop-in version of that pattern.
func HomeDir() (string, bool) {
	if !Posix() {
		return "", false
	}
	home := winescape.HostGetenv("HOME")
	return home, home != ""
}

// UserHomeDir is a drop-in replacement for os.UserHomeDir: it answers with
// the host's real $HOME while the file layer runs in posix personality (see
// HomeDir), and falls back to os.UserHomeDir() in every other case --
// native Windows, native Unix, or Windows persona under Wine -- so a caller
// that only ever meant "the directory the user thinks of as home" can switch
// unconditionally without a branch of its own.
func UserHomeDir() (string, error) {
	if home, ok := HomeDir(); ok {
		return home, nil
	}
	return os.UserHomeDir()
}
