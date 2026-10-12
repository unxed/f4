package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"testing"
	"time"

	"bytes"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestSimpleInline_CommandExecution(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ResizeConsole(80, 25)

	dir := t.TempDir()
	pf.RunSimpleInlineCommand(dir, "echo simple_inline_test")

	for i := 0; i < 10; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// A command started from the panels hands the screen straight back to them
// when it exits, the way Far and far2l do: there is no "Press any key to
// return to f4..." pause any more (#897). Its output stays in the host
// console for Ctrl+O, whose far-style overlay covers the bottom rows of the
// console window -- so the output is first pushed out of those rows, and the
// window is fitted to the cursor after that. ReactOS does not move the window
// after the cursor by itself (WINE.md §17.6); a fit made before the push
// would leave the snapshot taken for Ctrl+O short of the output's end.
func TestSimpleInline_ReturnsWithoutPauseAndLeavesRoomForOverlay(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	t.Cleanup(func() { config.App = oldCfg })
	config.App.ConsoleMode = terminal.ConsoleViewFar

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ShowKeyBar = true
	pf.ResizeConsole(80, 25)

	n := pf.OverlayLines()
	if n != 2 {
		t.Fatalf("OverlayLines() in Far style with keybar = %d, want 2", n)
	}

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldStdout := os.Stdout
	os.Stdout = out
	t.Cleanup(func() { os.Stdout = oldStdout })

	const marker = "return_without_pause"
	room := strings.Repeat("\r\n", n)
	fits, fitsAfterRoom := 0, 0
	oldFit := panel.FitConsoleWindow
	panel.FitConsoleWindow = func() {
		fits++
		written, _ := os.ReadFile(out.Name())
		text := string(written)
		if i := strings.Index(text, marker); i >= 0 && strings.HasSuffix(text[i:], room) {
			fitsAfterRoom++
		}
	}
	t.Cleanup(func() { panel.FitConsoleWindow = oldFit })

	pf.RunSimpleInlineCommand(t.TempDir(), "echo "+marker)

	written, _ := os.ReadFile(out.Name())
	if strings.Contains(string(written), "Press any key") {
		t.Fatalf("a command run from the panels still pauses before returning to them: %q", written)
	}
	if !strings.Contains(string(written), marker) {
		t.Fatalf("the command's output did not reach the console: %q", written)
	}
	if fits == 0 {
		t.Fatal("the console window was never fitted to the cursor")
	}
	if fitsAfterRoom == 0 {
		t.Fatalf("the console window was fitted %d time(s), none of them after the output "+
			"was pushed out of the %d overlay rows; on ReactOS the snapshot for Ctrl+O "+
			"then misses the end of the output", fits, n)
	}
}

// f4#897 item 2 (Zeroes1, XP): the first command in a console window opened
// to run f4 printed its output from the top of the window, starting under
// "C:\>f4". Far starts it at the bottom. The cursor is moved to the window's
// last row before the command writes anything.
func TestSimpleInline_MovesCursorToBottomBeforeCommand_Issue897(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ResizeConsole(80, 25)

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	oldStdout := os.Stdout
	os.Stdout = out
	t.Cleanup(func() { os.Stdout = oldStdout })

	const marker = "output_starts_at_bottom"
	moves, movesBeforeOutput := 0, 0
	oldMove := panel.MoveConsoleCursorToBottom
	panel.MoveConsoleCursorToBottom = func() {
		moves++
		written, _ := os.ReadFile(out.Name())
		if !strings.Contains(string(written), marker) {
			movesBeforeOutput++
		}
	}
	t.Cleanup(func() { panel.MoveConsoleCursorToBottom = oldMove })

	pf.RunSimpleInlineCommand(t.TempDir(), "echo "+marker)

	written, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(written), marker) {
		t.Fatalf("the command's output did not reach the console: %q", written)
	}
	if moves != 1 || movesBeforeOutput != 1 {
		t.Fatalf("cursor moved to the bottom %d time(s), %d before the output; want once, before it",
			moves, movesBeforeOutput)
	}
}

func TestSimpleCaptured_CommandExecution(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleCaptured
	pf.ResizeConsole(80, 25)

	pf.RunSimpleCapturedCommand(t.TempDir(), "echo simple_captured_test")
	top := vtui.FrameManager.GetTopFrame()
	dlg, ok := top.(*vtui.Window)
	if !ok {
		t.Fatal("runSimpleCapturedCommand should open a captured output dialog")
	}
	var output *vtui.ListBox
	for _, child := range dlg.GetChildren() {
		if list, ok := child.(*vtui.ListBox); ok {
			output = list
			break
		}
	}
	if output == nil {
		t.Fatal("captured output dialog should contain an output list")
	}

	timer := time.NewTimer(time.Second)
	t.Cleanup(func() { timer.Stop() })
	for {
		for _, line := range output.Items {
			if line == "[exit status 0]" {
				return
			}
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timer.C:
			t.Fatalf("captured command did not finish, output: %q", output.Items)
		}
	}
}

func TestSimpleInline_ToggleAndAnyKeyReturn(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	t.Cleanup(func() { config.App = oldCfg })
	config.App.ConsoleMode = terminal.ConsoleViewMc

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	// 1. Panel.Toggle hides panels in view-only primary screen
	RunAction("Panel.Toggle")
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle should hide panels in SimpleInline mode")
	}

	// 2. Any key in SimpleInline with hidden panels returns to panels
	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    ' ',
	})
	if !pf.ShowPanels {
		t.Fatal("Any keypress while viewing primary screen in SimpleInline mode must restore panels")
	}
}

// TestSimpleInline_CtrlOKeyUpDoesNotRestorePanels reproduces a flicker seen
// under Wine: Ctrl+O's KeyDown correctly hides the panels via the hotkey
// dispatcher, but its trailing KeyUp event (delivered as a separate
// InputEvent, e.KeyDown == false) used to fall through to the "any key
// returns to panels" fallback below unfiltered, immediately undoing the
// toggle within the same keystroke.
func TestSimpleInline_CtrlOKeyUpDoesNotRestorePanels(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	t.Cleanup(func() { config.App = oldCfg })
	config.App.ConsoleMode = terminal.ConsoleViewMc

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	RunAction("Panel.Toggle")
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle should hide panels in SimpleInline mode")
	}

	// The KeyUp of the same Ctrl+O press that triggered the toggle above.
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         false,
		VirtualKeyCode:  vtinput.VK_O,
		Char:            0x0F,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if pf.ShowPanels {
		t.Fatal("Ctrl+O's KeyUp event must not restore panels on its own")
	}

	// A genuine subsequent keypress should still restore panels as normal.
	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    ' ',
	})
	if !pf.ShowPanels {
		t.Fatal("A real keypress after Ctrl+O's KeyUp should still restore panels")
	}
}

func TestSimpleCaptured_ToggleShowsToast(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleCaptured
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	vtui.FrameManager.Push(pf)

	RunAction("Panel.Toggle")
	if !pf.ShowPanels {
		t.Fatal("Panel.Toggle should not hide panels in SimpleCaptured mode")
	}

	// ShowToast is posted to the UI task queue; pump it like the main loop
	// would and wait for the expected toast.
	want := i18n.Msg("Terminal.NotAvailableInEnv")
	var toast string
	timeout := time.After(1 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if toast = vtui.FrameManager.GetActiveToast(); toast == want {
				break Loop
			}
		case <-timeout:
			t.Fatalf("Timeout waiting for toast %q, last seen %q", want, toast)
		}
	}
	testutil.WaitForToastExpiry(t, 4*time.Second)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}

// TestSimpleInline_FarStyleKeepsConsoleAndTypes covers the Ctrl+O screen users
// actually get under Wine: the console stays visible, the f4 command line is
// drawn on it, and typing edits that command line instead of throwing the user
// back to the panels.
func TestSimpleInline_FarStyleKeepsConsoleAndTypes(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	var out bytes.Buffer
	scr.Writer = &out
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	t.Cleanup(func() { config.App = oldCfg })
	config.App.ConsoleMode = terminal.ConsoleViewFar
	oldGetTerminalSize := vtui.GetTerminalSize
	vtui.GetTerminalSize = func() (int, int, error) { return 80, 25, nil }
	t.Cleanup(func() { vtui.GetTerminalSize = oldGetTerminalSize })

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ShowKeyBar = true
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	if got := pf.OverlayLines(); got != 2 {
		t.Fatalf("overlayLines() in Far style with keybar = %d, want 2", got)
	}

	out.Reset()
	RunAction("Panel.Toggle")
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle should hide panels in SimpleInline mode")
	}
	// Command line row of an 80x25 screen with a two line overlay is row 24.
	if written := out.String(); !strings.Contains(written, "\x1b[24;1H") {
		t.Errorf("entering the Far-style console must draw the overlay, got %q", written)
	}

	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'd',
	})
	if pf.ShowPanels {
		t.Fatal("typing in the Far-style console must not restore panels")
	}
	if got := pf.CmdLine.Edit.GetText(); got != "d" {
		t.Errorf("typed character should reach the command line, got %q", got)
	}
}
