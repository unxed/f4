package main

import (
	"strings"
	"testing"
)

// The message a user gets instead of the thing they asked for has to contain
// the command they should type, chosen from the package manager that is
// actually on the machine rather than guessed from the operating system.
func TestInstallCommandPicksThePackageManagerThatExists(t *testing.T) {
	only := func(name string) func(string) bool {
		return func(s string) bool { return s == name }
	}

	cases := map[string]string{
		"apt":    "sudo apt install ffmpeg",
		"dnf":    "sudo dnf install ffmpeg",
		"pacman": "sudo pacman -S ffmpeg",
		"brew":   "brew install ffmpeg",
		"apk":    "sudo apk add ffmpeg",
	}
	for manager, want := range cases {
		if got := toolFFmpeg.installCommandWith(only(manager), "linux"); got != want {
			t.Errorf("%s: got %q, want %q", manager, got, want)
		}
	}
}

// A machine with several managers is told about the system one, not the last
// one in the map.
func TestInstallCommandPrefersTheSystemManager(t *testing.T) {
	both := func(s string) bool { return s == "apt" || s == "brew" }
	if got := toolFFmpeg.installCommandWith(both, "linux"); got != "sudo apt install ffmpeg" {
		t.Errorf("got %q", got)
	}
}

// With no manager at all there is still something true to say on the systems
// that have one obvious answer.
func TestInstallCommandWithoutAManager(t *testing.T) {
	none := func(string) bool { return false }
	if got := toolMPV.installCommandWith(none, "windows"); !strings.Contains(got, "winget") {
		t.Errorf("windows: got %q", got)
	}
	if got := toolMPV.installCommandWith(none, "darwin"); !strings.Contains(got, "brew") {
		t.Errorf("darwin: got %q", got)
	}
	if got := toolMPV.installCommandWith(none, "plan9"); got != "" {
		t.Errorf("somewhere unknown: got %q, want nothing", got)
	}
}

// The message says what is missing, what it was for, and what to type.
func TestMissingMessage(t *testing.T) {
	msg := toolFFmpeg.MissingMessage()
	if !strings.Contains(msg, "ffmpeg") {
		t.Errorf("the name must be in it: %q", msg)
	}
	if !strings.Contains(msg, "decoding video") || !strings.Contains(msg, "audio") {
		t.Errorf("what it is for must be in it: %q", msg)
	}
}

// ffmpeg goes by two names: avconv is the Libav fork, command line compatible
// for everything used here, and still what some distributions ship.
func TestFFmpegKnowsItsFork(t *testing.T) {
	if len(toolFFmpeg.Names) < 2 || toolFFmpeg.Names[1] != "avconv" {
		t.Errorf("names: %v", toolFFmpeg.Names)
	}
}

// A spawned graphical tool must not think it is being run inside a terminal
// that can draw pictures: it draws on the screen, not into the grid.
func TestToolEnvDropsTheTerminalVariables(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("KITTY_WINDOW_ID", "3")
	t.Setenv("F4_TOOLENV_MARKER", "kept")

	var sawTerm, sawKitty, sawMarker bool
	for _, kv := range toolEnv() {
		switch {
		case strings.HasPrefix(kv, "TERM="):
			sawTerm = true
		case strings.HasPrefix(kv, "KITTY_WINDOW_ID="):
			sawKitty = true
		case kv == "F4_TOOLENV_MARKER=kept":
			sawMarker = true
		}
	}
	if sawTerm || sawKitty {
		t.Error("the terminal variables must not be handed on")
	}
	if !sawMarker {
		t.Error("everything else must be")
	}
}
