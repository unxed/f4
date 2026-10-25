package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// readTerminalKey turns raw into a key the way f4 reads it from a terminal.
func readTerminalKey(t *testing.T, raw string) *vtinput.InputEvent {
	t.Helper()
	r := vtinput.NewReader(strings.NewReader(raw), false)
	defer r.Close()
	e, err := r.ReadEvent()
	if err != nil || e == nil {
		t.Fatalf("%q: no key read (%v)", raw, err)
	}
	return e
}

// Ctrl+Y clears the command line in every form the key reaches f4 in, and
// with the completion list open too (unxed/f4#2187). The list is what broke
// it: with a history to match, it opens as soon as anything is typed, takes
// every key ahead of the command line, and used to drop Ctrl+Y, on macOS or
// anywhere else.
func TestCtrlYClearsTheCommandLine(t *testing.T) {
	keys := []struct {
		name string
		key  func(t *testing.T) *vtinput.InputEvent
	}{
		{"terminal (Terminal.app, iTerm2)", func(t *testing.T) *vtinput.InputEvent { return readTerminalKey(t, "\x19") }},
		{"kitty keyboard protocol (kitty, iTerm2 CSI u)", func(t *testing.T) *vtinput.InputEvent { return readTerminalKey(t, "\x1b[121;5u") }},
		{"win32-input-mode", func(t *testing.T) *vtinput.InputEvent { return readTerminalKey(t, "\x1b[89;21;25;1;8;1_") }},
		// The macOS window hands the physical Control key over on the right
		// Ctrl channel (vtui's cocoaControlKeyState, gogpu's channel split).
		{"macOS window", func(*testing.T) *vtinput.InputEvent {
			return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_Y,
				ControlKeyState: vtinput.RightCtrlPressed | vtinput.NumLockOn}
		}},
	}
	for _, list := range []bool{false, true} {
		for _, k := range keys {
			name := k.name
			if list {
				name += ", completion list open"
			}
			t.Run(name, func(t *testing.T) {
				t.Cleanup(paneltest.SwapFrameManager(t))
				old := config.App
				t.Cleanup(func() { config.App = old })
				config.App.CommandLineAutoComplete = true

				pf := seedPanelForRestore(t, nil)
				pf.CmdLine.Edit.History = []string{"echo hello"}
				typed := "zzq"
				if list {
					typed = "ec"
				}
				for _, ch := range typed {
					pressKey(vtui.FrameManager.GetTopFrame(), &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: ch})
				}
				_, open := cmdline.AsCompletionMenu(vtui.FrameManager.GetTopFrame())
				if open != list {
					t.Fatalf("completion list open = %v after typing %q, want %v", open, typed, list)
				}

				pressKey(vtui.FrameManager.GetTopFrame(), k.key(t))
				for range 4 {
					vtui.FrameManager.Step(0)
				}
				if got := pf.CmdLine.Edit.GetText(); got != "" {
					t.Errorf("Ctrl+Y left %q in the command line", got)
				}
				if top := vtui.FrameManager.GetTopFrame(); top != pf {
					t.Errorf("Ctrl+Y left %T on top of the panels", top)
				}
			})
		}
	}
}
