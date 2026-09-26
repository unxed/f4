package terminal

import (
	"github.com/unxed/f4/internal/config"
	"testing"
)

func TestResolveShellMode_Matrix(t *testing.T) {
	oldProbeGUI := ProbeGUIBackend
	oldProbeTTY := ProbeHostTTY
	oldProbePTY := ProbePTYUsable
	oldProbeGOOS := probeGOOS
	defer func() {
		ProbeGUIBackend = oldProbeGUI
		ProbeHostTTY = oldProbeTTY
		ProbePTYUsable = oldProbePTY
		probeGOOS = oldProbeGOOS
	}()

	tests := []struct {
		name        string
		cfg         ShellModeConfig
		ptyUsable   bool
		hostTTY     bool
		guiBackend  string
		goos        string
		wantMode    ShellMode
		wantModeStr string
	}{
		{
			name:        "PTY unusable + Host TTY + Windows -> SimpleInline",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   false,
			hostTTY:     true,
			guiBackend:  "",
			goos:        "windows",
			wantMode:    ShellModeSimpleInline,
			wantModeStr: "simple-inline",
		},
		{
			name:        "PTY unusable + Host TTY + Linux -> SimpleCaptured",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   false,
			hostTTY:     true,
			guiBackend:  "",
			goos:        "linux",
			wantMode:    ShellModeSimpleCaptured,
			wantModeStr: "simple-captured",
		},
		{
			name:        "PTY unusable + GUI -> SimpleCaptured",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   false,
			hostTTY:     false,
			guiBackend:  "x11",
			goos:        "linux",
			wantMode:    ShellModeSimpleCaptured,
			wantModeStr: "simple-captured",
		},
		{
			name:        "PTY usable + Config own + Host TTY -> Own",
			cfg:         ShellModeConfig{ConsoleMode: "own"},
			ptyUsable:   true,
			hostTTY:     true,
			guiBackend:  "",
			goos:        "linux",
			wantMode:    ShellModeOwn,
			wantModeStr: "own",
		},
		{
			name:        "PTY usable + Config host + GUI backend -> Own",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   true,
			hostTTY:     true,
			guiBackend:  "gogpu",
			goos:        "linux",
			wantMode:    ShellModeOwn,
			wantModeStr: "own",
		},
		{
			name:        "PTY usable + Config host + No Host TTY -> Own",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   true,
			hostTTY:     false,
			guiBackend:  "",
			goos:        "linux",
			wantMode:    ShellModeOwn,
			wantModeStr: "own",
		},
		{
			name:        "PTY usable + Config host + Host TTY + No GUI -> Host",
			cfg:         ShellModeConfig{ConsoleMode: "host"},
			ptyUsable:   true,
			hostTTY:     true,
			guiBackend:  "",
			goos:        "linux",
			wantMode:    ShellModeHost,
			wantModeStr: "host",
		},
		{
			name:        "PTY usable + Config host (case insensitive) + Host TTY -> Host",
			cfg:         ShellModeConfig{ConsoleMode: "HoSt"},
			ptyUsable:   true,
			hostTTY:     true,
			guiBackend:  "",
			goos:        "windows",
			wantMode:    ShellModeHost,
			wantModeStr: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ProbePTYUsable = func() bool { return tt.ptyUsable }
			ProbeHostTTY = func() bool { return tt.hostTTY }
			ProbeGUIBackend = func() string { return tt.guiBackend }
			probeGOOS = func() string { return tt.goos }

			got := ResolveShellMode(tt.cfg)
			if got != tt.wantMode {
				t.Errorf("ResolveShellMode() = %v (%s), want %v (%s)", got, got.String(), tt.wantMode, tt.wantModeStr)
			}
			if got.String() != tt.wantModeStr {
				t.Errorf("ShellMode.String() = %q, want %q", got.String(), tt.wantModeStr)
			}
		})
	}
}

func TestConsoleViewStyleOf(t *testing.T) {
	cases := []struct {
		name string
		cfg  ShellModeConfig
		want string
	}{
		{"default", ShellModeConfig{ConsoleMode: "own"}, ConsoleViewOwn},
		{"empty", ShellModeConfig{}, ConsoleViewOwn},
		{"far", ShellModeConfig{ConsoleMode: "far"}, ConsoleViewFar},
		{"mc", ShellModeConfig{ConsoleMode: "mc"}, ConsoleViewMc},
		{"legacy host with overlay", ShellModeConfig{ConsoleMode: "host", ConsoleOverlayUI: true}, ConsoleViewFar},
		{"legacy host without overlay", ShellModeConfig{ConsoleMode: "host"}, ConsoleViewMc},
		{"case insensitive", ShellModeConfig{ConsoleMode: "FAR"}, ConsoleViewFar},
	}
	for _, c := range cases {
		if got := ConsoleViewStyleOf(c.cfg); got != c.want {
			t.Errorf("%s: ConsoleViewStyleOf() = %q, want %q", c.name, got, c.want)
		}
	}
}

// Without a PTY the "own terminal" choice cannot be honoured, and leaving it in
// place is what used to give users a blank screen on Ctrl+O.
func TestConsoleViewStyleFor_OwnDegradesWithoutPTY(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = ConsoleViewOwn
	config.App.ConsoleOverlayUI = false

	if got := ConsoleViewStyleFor(ShellModeSimpleInline); got != ConsoleViewFar {
		t.Errorf("ConsoleViewStyleFor(SimpleInline) = %q, want %q", got, ConsoleViewFar)
	}
	if got := ConsoleViewStyleFor(ShellModeOwn); got != ConsoleViewOwn {
		t.Errorf("ConsoleViewStyleFor(Own) = %q, want %q", got, ConsoleViewOwn)
	}
}

// config.Defaults hard-codes ConsoleMode to "own" for every platform (f4#1488):
// a plain struct literal cannot vary per-OS, so a stored "own" the user never
// touched is indistinguishable from one they chose deliberately. Where no PTY
// is usable that value cannot be honoured, so consoleViewStyle() (and
// everything that goes through it, i.e. every ConsoleViewStyleFor caller, not
// only the Ctrl+O SimpleInline/SimpleCaptured degrade already covered above)
// must resolve it to the Far overlay -- without touching the stored config.
func TestConsoleViewStyleFor_DefaultOwnResolvesByPTYAvailability(t *testing.T) {
	oldCfg := config.App
	oldProbePTY := ProbePTYUsable
	defer func() {
		config.App = oldCfg
		ProbePTYUsable = oldProbePTY
	}()

	config.App.ConsoleMode = ConsoleViewOwn
	config.App.ConsoleOverlayUI = false

	ProbePTYUsable = func() bool { return false }
	if got := ConsoleViewStyleFor(ShellModeOwn); got != ConsoleViewFar {
		t.Errorf("stored own + PTY unusable: ConsoleViewStyleFor(Own) = %q, want %q", got, ConsoleViewFar)
	}

	ProbePTYUsable = func() bool { return true }
	if got := ConsoleViewStyleFor(ShellModeOwn); got != ConsoleViewOwn {
		t.Errorf("stored own + PTY usable: ConsoleViewStyleFor(Own) = %q, want %q", got, ConsoleViewOwn)
	}
}

// An explicit "far" or "mc" choice is a real, unambiguous user preference --
// unlike bare "own", it is never second-guessed by PTY availability.
func TestConsoleViewStyleFor_ExplicitFarAndMcNeverOverridden(t *testing.T) {
	oldCfg := config.App
	oldProbePTY := ProbePTYUsable
	defer func() {
		config.App = oldCfg
		ProbePTYUsable = oldProbePTY
	}()

	for _, ptyUsable := range []bool{true, false} {
		ProbePTYUsable = func() bool { return ptyUsable }

		config.App.ConsoleMode = ConsoleViewFar
		config.App.ConsoleOverlayUI = false
		if got := ConsoleViewStyleFor(ShellModeHost); got != ConsoleViewFar {
			t.Errorf("ptyUsable=%v: stored far = %q, want %q", ptyUsable, got, ConsoleViewFar)
		}

		config.App.ConsoleMode = ConsoleViewMc
		if got := ConsoleViewStyleFor(ShellModeHost); got != ConsoleViewMc {
			t.Errorf("ptyUsable=%v: stored mc = %q, want %q", ptyUsable, got, ConsoleViewMc)
		}
	}
}

func TestResolveShellMode_LegacyWindowsDegradesToSimpleInlineAndFarOverlay(t *testing.T) {
	oldProbeGUI := ProbeGUIBackend
	oldProbeTTY := ProbeHostTTY
	oldProbePTY := ProbePTYUsable
	oldProbeGOOS := probeGOOS
	defer func() {
		ProbeGUIBackend = oldProbeGUI
		ProbeHostTTY = oldProbeTTY
		ProbePTYUsable = oldProbePTY
		probeGOOS = oldProbeGOOS
	}()

	ProbeGUIBackend = func() string { return "" }
	ProbeHostTTY = func() bool { return true }
	ProbePTYUsable = func() bool { return false } // Simulates Windows 7 / 8 / 8.1 without ConPTY
	probeGOOS = func() string { return "windows" }

	mode := ResolveShellMode(ShellModeConfig{ConsoleMode: "own"})
	if mode != ShellModeSimpleInline {
		t.Fatalf("ResolveShellMode on legacy Windows = %v, want ShellModeSimpleInline", mode)
	}

	style := ConsoleViewStyleFor(mode)
	if style != ConsoleViewFar {
		t.Fatalf("ConsoleViewStyleFor(SimpleInline) = %q, want %q (Far overlay)", style, ConsoleViewFar)
	}
}
