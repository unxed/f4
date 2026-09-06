package main

// Finding the programs f4 leans on, and saying how to get them when they are
// not there.
//
// A file manager that answers "cannot play this" is answering the wrong
// question. The user does not want to know that f4 has no video decoder of its
// own; they want to know what to type. So a missing tool is reported with the
// command that installs it on the system they are actually running.

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// ExternalTool is one program f4 can use, under any of the names it goes by.
type ExternalTool struct {
	// Names are tried in order. The first is the one the messages use.
	Names []string

	// What it is for, in the message.
	Purpose string

	// Install is the package to ask for, per package manager. The key is
	// the manager's own command; the value is the package name, which is
	// not always the same as the program.
	Install map[string]string
}

// Tools f4 knows about.
var (
	// toolFFmpeg. avconv is the Libav fork, command line compatible for
	// everything used here, and still what some older distributions ship
	// instead.
	toolFFmpeg = ExternalTool{
		Names:   []string{"ffmpeg", "avconv"},
		Purpose: "decoding video and the audio formats it has no decoder of its own for",
		Install: map[string]string{
			"apt":     "ffmpeg",
			"dnf":     "ffmpeg",
			"pacman":  "ffmpeg",
			"zypper":  "ffmpeg",
			"apk":     "ffmpeg",
			"brew":    "ffmpeg",
			"winget":  "Gyan.FFmpeg",
			"choco":   "ffmpeg",
			"pkg":     "ffmpeg",
			"pkg_add": "ffmpeg",
		},
	}

	toolMPV = ExternalTool{
		Names:   []string{"mpv"},
		Purpose: "playing video",
		Install: map[string]string{
			"apt":     "mpv",
			"dnf":     "mpv",
			"pacman":  "mpv",
			"zypper":  "mpv",
			"apk":     "mpv",
			"brew":    "mpv",
			"winget":  "mpv.net",
			"choco":   "mpv.net",
			"pkg":     "mpv",
			"pkg_add": "mpv",
		},
	}
)

var (
	toolPathMu sync.Mutex
	toolPaths  = make(map[string]string)
)

// Find returns the path to the tool, looking only once per name per run:
// walking the PATH on every frame of a file listing is not free, and the
// answer does not change while f4 is running.
func (t ExternalTool) Find() (string, bool) {
	key := strings.Join(t.Names, ",")

	toolPathMu.Lock()
	cached, seen := toolPaths[key]
	toolPathMu.Unlock()
	if seen {
		return cached, cached != ""
	}

	found := ""
	for _, name := range t.Names {
		if p, err := exec.LookPath(name); err == nil {
			found = p
			break
		}
	}

	toolPathMu.Lock()
	toolPaths[key] = found
	toolPathMu.Unlock()
	return found, found != ""
}

// Available is Find without the path.
func (t ExternalTool) Available() bool {
	_, ok := t.Find()
	return ok
}

// MissingMessage is what the user is shown instead of the thing they asked
// for: what is missing, what it was for, and the command that gets it.
func (t ExternalTool) MissingMessage() string {
	var sb strings.Builder
	sb.WriteString(t.Names[0])
	sb.WriteString(" was not found, and it is what f4 uses for ")
	sb.WriteString(t.Purpose)
	sb.WriteString(".")
	if cmd := t.InstallCommand(); cmd != "" {
		sb.WriteString("\n\nInstall it with:\n\n    ")
		sb.WriteString(cmd)
	}
	return sb.String()
}

// InstallCommand builds the line to type, from whichever package manager is
// actually on this machine. Guessing from the operating system alone would be
// wrong on half of Linux; asking the PATH which manager exists is not.
func (t ExternalTool) InstallCommand() string {
	return t.installCommandWith(lookPathExists, runtime.GOOS)
}

func lookPathExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// managers are tried in this order. The first one present wins, so a machine
// with both a system manager and a user one is told about the system one.
var managers = []struct {
	cmd  string
	line string
}{
	{"apt", "sudo apt install %s"},
	{"dnf", "sudo dnf install %s"},
	{"pacman", "sudo pacman -S %s"},
	{"zypper", "sudo zypper install %s"},
	{"apk", "sudo apk add %s"},
	{"brew", "brew install %s"},
	{"pkg", "sudo pkg install %s"},
	{"pkg_add", "doas pkg_add %s"},
	{"winget", "winget install %s"},
	{"choco", "choco install %s"},
}

func (t ExternalTool) installCommandWith(exists func(string) bool, goos string) string {
	for _, m := range managers {
		pkg, known := t.Install[m.cmd]
		if !known || !exists(m.cmd) {
			continue
		}
		return strings.Replace(m.line, "%s", pkg, 1)
	}

	// Nothing recognised. Say something true rather than nothing: on
	// Windows there may be no package manager at all, and on a Unix nobody
	// has ever seen there is still a website.
	switch goos {
	case "windows":
		if pkg, ok := t.Install["winget"]; ok {
			return "winget install " + pkg
		}
	case "darwin":
		if pkg, ok := t.Install["brew"]; ok {
			return "brew install " + pkg
		}
	}
	return ""
}

// toolEnv is the environment a spawned tool gets. It is the one f4 runs in,
// minus the variables that would make a graphical program try to draw into
// the terminal instead of onto the screen.
func toolEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "TERM="),
			strings.HasPrefix(kv, "TERM_PROGRAM="),
			strings.HasPrefix(kv, "KITTY_WINDOW_ID="):
			continue
		}
		out = append(out, kv)
	}
	return out
}
