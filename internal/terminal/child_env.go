package terminal

// The environment the built-in terminal hands to the program it starts. This
// is where the terminal says what it can do: a program that draws pictures
// picks its protocol long before it prints anything, and it picks by looking
// at these variables.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// kittyGraphicsEnv is the variable kitty exports and image tools look for.
const kittyGraphicsEnv = "KITTY_WINDOW_ID"

// kittyPidEnv is the other one kitty exports, and the one that actually gets
// chafa to find the protocol on its own. Its terminal table matches kitty on
// TERM being exactly xterm-kitty *or* on this being set, and looks at
// KITTY_WINDOW_ID not at all — so a machine without the kitty terminfo entry
// installed, which is most machines that do not have kitty, saw nothing to go
// on and drew characters. See chafa/chafa-term-db.c.
const kittyPidEnv = "KITTY_PID"

// kittyTermName is the terminal type a program looks up when it wants to
// know whether it may draw pictures. Tools written before terminals could be
// asked directly, chafa 1.14 among them, know no other way to find out.
const kittyTermName = "xterm-kitty"

// terminalGraphicsSeen remembers that the screen f4 draws on can show
// Images. A shell can be started before the first frame is drawn, and one
// that missed the news would keep the wrong environment for its whole life.
var terminalGraphicsSeen atomic.Bool

// PrivateToThisProcess lists variables that describe how *this* process was
// started and are meaningless -- or fatal -- to anything it starts.
//
// The universal Linux build (-tags goffi_universal, the single artifact that
// runs on glibc and musl alike) carries no PT_INTERP and no DT_NEEDED, so
// nothing maps a libc into it. goffi's bridge re-execs the process through
// the host dynamic loader with the host libc pre-loaded, before main, and
// leaves GOFFI_UNIVERSAL_REEXEC behind to say the job is done. Before goffi
// v0.1.11 the bridge in a child read that variable, concluded it too already
// came through the loader, and bound no libc -- so the child died before
// main, on the first libc symbol it touched (issue #87: `./f4` from f4's own
// terminal died with SIGSEGV, frame #0 at address zero -- a call through the
// function pointer the bridge never filled in). v0.1.11 tags the guard with
// the pid it was written for, so a child of this f4 runs the bridge itself;
// but the terminal starts other people's programs, including universal
// builds linked against an older goffi, so the variables stay out.
// update.SelfCommand does the same for copies of f4.
//
// GOFFI_UNIVERSAL_EXE and _ARGV0 are the identity the re-exec destroyed,
// recorded before it happened. They are tagged with the pid they describe, so
// a child cannot mistake them for its own, but they describe a process the
// child has nothing to do with either. F4_EXE is f4's own version of the same
// record and is not tagged: passing it on would tell a *different* f4 binary
// that it lives at this one's path, which is an answer the updater acts on.
//
// The names are goffi's. A rename there leaves this list stripping nothing
// rather than stripping the wrong thing; the ones f4 itself reads are tied to
// the constants that read them by a test in the linux build, so those cannot
// drift apart unnoticed.
//
// F4_DETACHED says that *this* process is the copy checkAndDetach started: no
// controlling terminal, stdout on /dev/null, and therefore stdout pointed at
// the crash log by redirectDetachedStdout. A GUI f4 carries it for its whole
// life, so before this it reached the shell in the built-in terminal and every
// program started from there. The one program that reads it is f4 itself, and
// a nested f4 believed it: `f4 --version` and `f4 --help` typed at the command
// line printed into the outer session's crash log instead of the terminal and
// showed nothing at all (issue #1151). Whatever the terminal starts is not the
// detached copy, whichever way this copy was started.
var PrivateToThisProcess = []string{
	"GOFFI_UNIVERSAL_REEXEC",
	"GOFFI_UNIVERSAL_EXE",
	"GOFFI_UNIVERSAL_ARGV0",
	"F4_EXE",
	"F4_DETACHED",
}

// PrivateEnvEntry reports whether an environment entry names one of them.
func PrivateEnvEntry(kv string) bool {
	name, _, ok := strings.Cut(kv, "=")
	if !ok {
		return false
	}
	for _, key := range PrivateToThisProcess {
		if name == key {
			return true
		}
	}
	return false
}

var currentHostShellMode = func() ShellMode {
	return ResolveShellMode(ShellModeConfig{
		ConsoleMode:      config.App.ConsoleMode,
		ConsoleOverlayUI: config.App.ConsoleOverlayUI,
	})
}

// TerminalChildEnv builds the environment of a program started in the
// built-in terminal.
func TerminalChildEnv() []string {
	if currentHostShellMode() == ShellModeHost {
		return BuildChildEnv(os.Environ(), false, false)
	}
	graphics := terminalShowsImages()
	return BuildChildEnv(os.Environ(), graphics, graphics && announceKittyTerm())
}

// BuildChildEnv is the half of TerminalChildEnv that depends on nothing but
// its arguments.
func BuildChildEnv(env []string, graphics, kittyTerm bool) []string {
	out := make([]string, 0, len(env)+4)
	for _, kv := range env {
		// Whatever we inherited describes the terminal that started f4; the
		// program we are about to start talks to us instead.
		if strings.HasPrefix(kv, kittyGraphicsEnv+"=") ||
			strings.HasPrefix(kv, kittyPidEnv+"=") ||
			strings.HasPrefix(kv, "TERM_PROGRAM=") {
			continue
		}
		if kittyTerm && strings.HasPrefix(kv, "TERM=") {
			continue
		}
		if PrivateEnvEntry(kv) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "F4_NESTED=1", "TERM_PROGRAM=f4")

	if graphics {
		// The built-in terminal speaks the kitty graphics protocol, so it
		// says so the way kitty itself does — both variables, because
		// different tools look at different ones and the one chafa looks
		// at is the pid. Claiming either while the screen f4 draws on
		// cannot show a picture would only make programs produce output
		// nobody sees.
		out = append(out, kittyGraphicsEnv+"=1")
		out = append(out, kittyPidEnv+"="+strconv.Itoa(os.Getpid()))
	}
	if kittyTerm {
		out = append(out, "TERM="+kittyTermName)
	}
	return out
}

// announceKittyTerm decides whether to introduce the built-in terminal as
// kitty. The description has to be installed: a TERM the system cannot look
// up breaks every program that opens the terminfo database, which is a far
// worse trade than a picture drawn with characters.
func announceKittyTerm() bool {
	if !config.App.AnnounceKittyTerm {
		return false
	}
	return TerminfoExists(kittyTermName)
}

// terminalShowsImages reports whether the screen f4 draws on can display
// Images at all.
func terminalShowsImages() bool {
	if scr := vtui.FrameManager.Screen(); scr != nil {
		graphics := scr.SupportsGraphics()
		terminalGraphicsSeen.Store(graphics)
		return graphics
	}
	return terminalGraphicsSeen.Load()
}

// TerminfoExists reports whether the compiled description of a terminal is
// installed.
func TerminfoExists(name string) bool {
	if name == "" {
		return false
	}
	// An entry lives either under the first letter of its name or under the
	// hexadecimal code of that letter, depending on the file system the
	// database was built for.
	subdirs := []string{name[:1], strconv.FormatUint(uint64(name[0]), 16)}
	for _, dir := range terminfoDirs() {
		for _, sub := range subdirs {
			st, err := os.Stat(filepath.Join(dir, sub, name))
			if err == nil && st.Mode().IsRegular() {
				return true
			}
		}
	}
	return false
}

// terminfoDirs lists the places ncurses looks for terminal descriptions, in
// the order it looks at them.
func terminfoDirs() []string {
	var dirs []string
	if v := os.Getenv("TERMINFO"); v != "" {
		dirs = append(dirs, v)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".terminfo"))
	}
	for _, v := range filepath.SplitList(os.Getenv("TERMINFO_DIRS")) {
		if v == "" {
			// An empty entry stands for the built in default.
			v = "/usr/share/terminfo"
		}
		dirs = append(dirs, v)
	}
	return append(dirs, "/etc/terminfo", "/lib/terminfo", "/usr/share/terminfo",
		"/usr/lib/terminfo", "/usr/local/share/terminfo")
}
