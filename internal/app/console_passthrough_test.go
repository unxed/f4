package app

import (
	"bytes"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestMutedPTY_SilencesWrites(t *testing.T) {
	mock := &paneltest.MockPty{}
	muted := panel.MutedPTY{Backend: mock}

	payload := []byte("\x1b[?1;2c")
	n, err := muted.Write(payload)
	if err != nil {
		t.Fatalf("muted.Write error: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("muted.Write returned n=%d, want %d", n, len(payload))
	}
	if len(mock.Written) != 0 {
		t.Fatalf("mutedPTY leaked write to underlying backend: %q", string(mock.Written))
	}
}

func TestHostConsole_Transitions(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ResizeConsole(80, 25)

	// 1. Enter host console
	pf.EnterHostConsole()
	if !pf.IsHostConsoleActive() {
		t.Fatal("hostConsoleActive should be true after enterHostConsole")
	}
	if !pf.IsBusy() {
		t.Fatal("pf.IsBusy() should be true in host console mode to suppress UI redraws")
	}

	// 2. Leave host console
	out.Reset()
	pf.LeaveHostConsole()
	if pf.IsHostConsoleActive() {
		t.Fatal("hostConsoleActive should be false after leaveHostConsole")
	}
	if pf.IsBusy() {
		t.Fatal("pf.IsBusy() should be false after leaveHostConsole")
	}

	// Verify protective reset sequence was written via passthrough
	written := out.String()
	if !strings.Contains(written, "\x1b[?1000l") || !strings.Contains(written, "\x1b[?2004l") || !strings.Contains(written, "\x1b[0m") {
		t.Fatalf("leaveHostConsole missing protective reset sequences: %q", written)
	}
}

func TestHostConsole_OverlaySuppressesRegisteredKeyBar(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "host"
	config.App.ConsoleOverlayUI = true

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	defer func() { vtui.FrameManager.KeyBar = nil }()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ShowPanels = false
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.KeyBar = pf.KeyBar

	pf.EnterHostConsole()
	if vtui.FrameManager.KeyBar != nil {
		t.Fatal("host console overlay must unregister the ScreenBuf keybar")
	}

	pf.LeaveHostConsole()
}

func TestChildEnv_HostModeLeavesTERMUntouched(t *testing.T) {
	oldProbeGUI := terminal.ProbeGUIBackend
	oldProbeTTY := terminal.ProbeHostTTY
	oldProbePTY := terminal.ProbePTYUsable
	defer func() {
		terminal.ProbeGUIBackend = oldProbeGUI
		terminal.ProbeHostTTY = oldProbeTTY
		terminal.ProbePTYUsable = oldProbePTY
	}()

	terminal.ProbeGUIBackend = func() string { return "" }
	terminal.ProbeHostTTY = func() bool { return true }
	terminal.ProbePTYUsable = func() bool { return true }

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "host"

	t.Setenv("TERM", "xterm-256color")

	env := terminal.TerminalChildEnv()
	if envHasKey(env, "KITTY_WINDOW_ID") {
		t.Errorf("host mode must not advertise KITTY_WINDOW_ID: %v", env)
	}
	if !envHas(env, "TERM=xterm-256color") {
		t.Errorf("host mode must preserve host TERM: %v", env)
	}
	if !envHas(env, "F4_NESTED=1") || !envHas(env, "TERM_PROGRAM=f4") {
		t.Errorf("host mode must export F4_NESTED and TERM_PROGRAM: %v", env)
	}
}
func TestHostConsole_PanelToggleAction(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	if !pf.ShowPanels {
		t.Fatal("panels should be visible initially")
	}

	// 1. Run Panel.Toggle -> should hide panels and enter host console
	if !RunAction("Panel.Toggle") {
		t.Fatal("Panel.Toggle action failed")
	}
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle did not hide panels")
	}
	if !pf.IsHostConsoleActive() {
		t.Fatal("hostConsoleActive should be true after toggling panels off in host mode")
	}

	// 2. Run Panel.Toggle again -> should show panels and leave host console
	if !RunAction("Panel.Toggle") {
		t.Fatal("Panel.Toggle second action failed")
	}
	if !pf.ShowPanels {
		t.Fatal("Panel.Toggle second action did not show panels")
	}
	if pf.IsHostConsoleActive() {
		t.Fatal("hostConsoleActive should be false after toggling panels back on in host mode")
	}
}

func TestHostConsole_InputForwardingWhenIdle(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ShowPanels = false
	pf.EnterHostConsole()

	mock := pf.Pty.(*paneltest.MockPty)
	mock.Reset()

	// Send key 'x'
	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'x',
	})

	if got := mock.String(); got != "x" {
		t.Errorf("Host mode idle forwarding: got %q, want %q", got, "x")
	}
}

func TestHostConsole_CloseLeavesHostConsole(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	pf.ShellMode = terminal.ShellModeHost
	pf.EnterHostConsole()
	if !pf.IsHostConsoleActive() {
		t.Fatal("host console must be active before Close")
	}

	pf.Close()
	if pf.IsHostConsoleActive() {
		t.Fatal("Close must leave host console")
	}
}
func TestHostConsole_OverlayLines(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()

	pf := panel.NewPanelsFrame()
	defer pf.Close()

	// 1. ConsoleOverlayUI disabled -> 0 lines
	config.App.ConsoleMode = "host"
	config.App.ConsoleOverlayUI = false
	if got := pf.OverlayLines(); got != 0 {
		t.Errorf("overlayLines() with ConsoleOverlayUI=false = %d, want 0", got)
	}

	// 2. ConsoleOverlayUI enabled, showKeyBar = true -> 2 lines
	config.App.ConsoleOverlayUI = true
	pf.ShowKeyBar = true
	if got := pf.OverlayLines(); got != 2 {
		t.Errorf("overlayLines() with showKeyBar=true = %d, want 2", got)
	}

	// 3. ConsoleOverlayUI enabled, showKeyBar = false -> 1 line
	pf.ShowKeyBar = false
	if got := pf.OverlayLines(); got != 1 {
		t.Errorf("overlayLines() with showKeyBar=false = %d, want 1", got)
	}
}

func TestHostConsole_FarStyleScrollRegion(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "host"
	config.App.ConsoleOverlayUI = true

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ShowKeyBar = true
	pf.ResizeConsole(80, 25)

	out.Reset()
	pf.EnterHostConsole()

	// Scroll region for 25 lines with 2 overlay lines should be rows 1..23 (\x1b[1;23r)
	written := out.String()
	wantScrollRegion := "\x1b[1;23r"
	if !strings.Contains(written, wantScrollRegion) {
		t.Errorf("enterHostConsole with overlay missing scroll region %q: %q", wantScrollRegion, written)
	}

	out.Reset()
	pf.LeaveHostConsole()

	// Leaving must restore scroll region (\x1b[r)
	written = out.String()
	if !strings.Contains(written, "\x1b[r") {
		t.Errorf("leaveHostConsole missing scroll region reset \\x1b[r: %q", written)
	}
}

// TestHostConsole_ResizeLargerClearsNewArea covers f4#1376: growing the
// console while Far (or any child) owns the screen through the host console
// must not leave the rows/columns the resize newly exposed showing whatever
// the real terminal's own buffer last held there (f4's own panels, in the
// reporter's case) until the child gets around to repainting them.
func TestHostConsole_ResizeLargerClearsNewArea(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(130, 30)
	vtui.FrameManager.Init(scr)

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "host"
	config.App.ConsoleOverlayUI = false

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ResizeConsole(130, 30)
	pf.EnterHostConsole()

	out.Reset()
	pf.ResizeConsole(140, 40)

	written := out.String()
	if written == "" {
		t.Fatal("resizing larger while the host console is active wrote nothing to the terminal")
	}
	// Every newly exposed row (31..40) must be erased.
	for row := 31; row <= 40; row++ {
		want := fmt.Sprintf("\x1b[%d;1H\x1b[0m\x1b[2K", row)
		if !strings.Contains(written, want) {
			t.Errorf("resize-larger did not clear new row %d: missing %q in %q", row, want, written)
		}
	}
	// Every surviving row (1..30) must have its newly exposed columns (131..140) erased.
	for row := 1; row <= 30; row++ {
		want := fmt.Sprintf("\x1b[%d;131H\x1b[0m\x1b[0K", row)
		if !strings.Contains(written, want) {
			t.Errorf("resize-larger did not clear new columns on row %d: missing %q in %q", row, want, written)
		}
	}

	// A resize that does not grow the console must not touch the screen this way.
	out.Reset()
	pf.ResizeConsole(140, 40)
	if out.String() != "" {
		t.Errorf("resizing to the same size wrote %q, want nothing", out.String())
	}
}

func TestHostConsole_FarStylePTYSizing(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "host"
	config.App.ConsoleOverlayUI = true

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	pf.ShowKeyBar = true

	pf.ResizeConsole(80, 25)

	// terminal.PTY should receive height 25 - 2 = 23
	if pf.TermView.Height != 23 {
		t.Errorf("termView height in Far-style host mode = %d, want 23", pf.TermView.Height)
	}
}
func TestHostConsole_DetachCleanupSimulation(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeHost
	vtui.FrameManager.Push(pf)
	pf.EnterHostConsole()

	if !pf.IsHostConsoleActive() {
		t.Fatal("host console must be active")
	}

	// Simulate session server cleanup loop before detach/restore
	for _, s := range vtui.FrameManager.Screens {
		if s == nil {
			continue
		}
		for _, f := range s.Frames {
			if frame, ok := f.(*panel.PanelsFrame); ok && frame != nil {
				if frame.ShellMode == terminal.ShellModeHost && frame.IsHostConsoleActive() {
					frame.LeaveHostConsole()
				}
			}
		}
	}

	if pf.IsHostConsoleActive() {
		t.Error("host console must be left after server detach cleanup loop")
	}
}

// TestOverlayKeybarSlots_MatchesVtuiLayout pins the overlay keybar to the same
// column math vtui.KeyBar uses. The overlay used to cut every label to five
// runes regardless of width, which is why it read "RenMo" while the panel
// keybar on the same screen had room for the full label.
func TestOverlayKeybarSlots_MatchesVtuiLayout(t *testing.T) {
	labels := vtui.KeyBarLabels{
		"Help", "User menu", "View", "Edit", "Copy", "Rename or move",
		"Make folder", "Delete", "ConfMenu", "Quit", "Plugin commands", "Screens",
	}

	slots := panel.OverlayKeybarSlots(labels, 120)
	if len(slots) != 12 {
		t.Fatalf("overlayKeybarSlots(width=120) returned %d slots, want 12", len(slots))
	}
	// width/12 = 10, minus one column for the number and one for the gap.
	// Ten columns per slot, minus one for the number and one for the gap,
	// leaves eight — the same cut vtui.KeyBar makes at this width.
	if got := strings.TrimRight(slots[1].Label, " "); got != "User men" {
		t.Errorf("F2 label at width 120 = %q, want %q", got, "User men")
	}
	if got := len([]rune(slots[1].Label)); got != 8 {
		t.Errorf("F2 label width at width 120 = %d, want 8", got)
	}
	for i, s := range slots {
		if want := i * 10; s.Col != want {
			t.Errorf("slot %d starts at column %d, want %d", i+1, s.Col, want)
		}
	}
	// The last slot takes the remainder so the bar reaches the right edge.
	last := slots[11]
	if got := last.Col + len([]rune(last.Num)) + len([]rune(last.Label)); got != 120 {
		t.Errorf("last slot ends at column %d, want 120", got)
	}

	// A narrow console must not panic or produce negative widths.
	for _, w := range []int{0, 1, 12, 24, 79, 80} {
		for _, s := range panel.OverlayKeybarSlots(labels, w) {
			if s.Col < 0 || s.Col >= w {
				t.Fatalf("width %d: slot column %d out of range", w, s.Col)
			}
			if end := s.Col + len([]rune(s.Num)) + len([]rune(s.Label)); end > w {
				t.Fatalf("width %d: slot at column %d ends at column %d", w, s.Col, end)
			}
		}
	}
}
