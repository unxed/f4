package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtinput"
)

// The open quick search or autofilter has the first say on a key, ahead of
// the command line that answers many of the same keys (#2186).

// charKey is a character typed on the key vk with mods held.
func charKey(vk uint16, ch rune, mods vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, Char: ch, ControlKeyState: mods}
}

// quickSearchOverCommand types command into the command line of panels that
// list foo, fob and bar, then searches for "fo" with Alt+F and O.
func quickSearchOverCommand(t *testing.T, mode config.PanelNavigationMode, command string) (*panel.PanelsFrame, *panel.FileSystemPanel) {
	t.Helper()
	old := config.App
	t.Cleanup(func() { config.App = old })
	config.App.NavigationMode = mode

	pf := seedPanelForRestore(t, []string{"foo", "fob", "bar"})
	fsp := pf.GetActivePanel()
	for _, ch := range command {
		pressKey(pf, charKey(0, ch, 0))
	}
	pressKey(pf, charKey(vtinput.VK_F, 'f', vtinput.LeftAltPressed))
	pressKey(pf, charKey(vtinput.VK_O, 'o', 0))
	if !fsp.FastFindMode || fsp.FastFindStr != "fo" || pf.CmdLine.Edit.GetText() != command {
		t.Fatalf("setup: search open=%v text=%q, command line %q, want open, %q and %q",
			fsp.FastFindMode, fsp.FastFindStr, pf.CmdLine.Edit.GetText(), "fo", command)
	}
	return pf, fsp
}

// commandLineCursor shows where the cursor of the command line stands, as a
// '|' in its text.
func commandLineCursor(pf *panel.PanelsFrame) string {
	pf.CmdLine.InsertString("|")
	return pf.CmdLine.Edit.GetText()
}

// Backspace erased the command line while the search box was open, until the
// line was empty; only then did it reach the search.
func TestQuickSearchBackspaceLeavesCommandLine(t *testing.T) {
	for _, mode := range []config.PanelNavigationMode{config.NavigationClassic, config.NavigationVim} {
		t.Run(mode.String(), func(t *testing.T) {
			pf, fsp := quickSearchOverCommand(t, mode, "abc")
			pressKey(pf, charKey(vtinput.VK_BACK, 0x7f, 0))
			if !fsp.FastFindMode || fsp.FastFindStr != "f" {
				t.Fatalf("Backspace left the search open=%v with %q, want it open with %q", fsp.FastFindMode, fsp.FastFindStr, "f")
			}
			if got := pf.CmdLine.Edit.GetText(); got != "abc" {
				t.Fatalf("Backspace in the search changed the command line to %q", got)
			}
			// Erasing the last character ends the search, and the next
			// Backspace is the command line's again.
			pressKey(pf, charKey(vtinput.VK_BACK, 0x7f, 0))
			if fsp.FastFindMode {
				t.Fatalf("the search survived erasing its last character: %q", fsp.FastFindStr)
			}
			pressKey(pf, charKey(vtinput.VK_BACK, 0x7f, 0))
			if got := pf.CmdLine.Edit.GetText(); got != "ab" {
				t.Fatalf("Backspace after the search = command line %q, want %q", got, "ab")
			}
		})
	}
}

// Ctrl+Y erases the text of the search, as in any edit field of Far, and
// not the command line.
func TestQuickSearchCtrlYErasesSearchNotCommandLine(t *testing.T) {
	pf, fsp := quickSearchOverCommand(t, config.NavigationClassic, "abc")
	pressKey(pf, keyDown(vtinput.VK_Y, vtinput.LeftCtrlPressed))
	if fsp.FastFindMode || fsp.FastFindStr != "" {
		t.Fatalf("Ctrl+Y left the search open=%v with %q, want it erased and closed", fsp.FastFindMode, fsp.FastFindStr)
	}
	if got := pf.CmdLine.Edit.GetText(); got != "abc" {
		t.Fatalf("Ctrl+Y in the search changed the command line to %q", got)
	}
}

// A key the search does not keep closes it and then does what it does with
// no search open: with text on the command line Left and Ctrl+Left move its
// cursor, as in Far. They used to move the hidden cursor of the command line
// and leave the box open.
func TestQuickSearchClosesOnCommandLineKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  *vtinput.InputEvent
		want string
	}{
		{"Left", keyDown(vtinput.VK_LEFT, 0), "ab|c"},
		{"CtrlLeft", keyDown(vtinput.VK_LEFT, vtinput.LeftCtrlPressed), "|abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pf, fsp := quickSearchOverCommand(t, config.NavigationClassic, "abc")
			pressKey(pf, tc.key)
			if fsp.FastFindMode {
				t.Fatalf("%s left the search open with %q", tc.name, fsp.FastFindStr)
			}
			if got := commandLineCursor(pf); got != tc.want {
				t.Fatalf("%s with the search open: command line %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// Enter with text on the command line runs the command, as in Far, and the
// search closes first. The box used to stay open and came back with the
// panels.
func TestQuickSearchEnterRunsCommandAndClosesSearch(t *testing.T) {
	pf, fsp := quickSearchOverCommand(t, config.NavigationClassic, "echo f4-2186")
	pressKey(pf, charKey(vtinput.VK_RETURN, '\r', 0))
	if fsp.FastFindMode {
		t.Fatalf("Enter left the search open with %q", fsp.FastFindStr)
	}
	if got := pf.CmdLine.Edit.GetText(); got != "" {
		t.Fatalf("Enter did not run the command line: it still holds %q", got)
	}
	if sent := pf.Pty.(*paneltest.MockPty).String(); !strings.Contains(sent, "f4-2186") {
		t.Fatalf("the shell got %q, want the command", sent)
	}
}

// A modifier key on its own -- the Shift of a capital letter, the Alt of
// Alt+letter -- is part of typing, not a key that closes the search.
func TestQuickSearchSurvivesModifierKeys(t *testing.T) {
	pf, fsp := quickSearchOverCommand(t, config.NavigationClassic, "")
	pressKey(pf, keyDown(vtinput.VK_SHIFT, vtinput.ShiftPressed))
	pressKey(pf, keyDown(vtinput.VK_MENU, vtinput.LeftAltPressed))
	if !fsp.FastFindMode {
		t.Fatal("a modifier key on its own closed the search")
	}
	pressKey(pf, charKey(vtinput.VK_B, 'B', vtinput.ShiftPressed))
	if fsp.FastFindStr != "fob" {
		t.Fatalf("Shift+B after Shift alone: search %q, want %q", fsp.FastFindStr, "fob")
	}
}

// The filter keeps the cursor keys, which walk the rows it leaves, also with
// text on the command line, which used to take Left and Right.
func TestAutoFilterKeepsCursorKeysOverCommandLine(t *testing.T) {
	old := config.App
	t.Cleanup(func() { config.App = old })
	config.App.NavigationMode = config.NavigationClassic

	pf := seedPanelForRestore(t, []string{"foo", "fob", "bar"})
	fsp := pf.GetActivePanel()
	for _, ch := range "abc" {
		pressKey(pf, charKey(0, ch, 0))
	}
	pressKey(pf, keyDown(vtinput.VK_F, vtinput.LeftCtrlPressed|vtinput.LeftAltPressed))
	pressKey(pf, charKey(vtinput.VK_F, 'f', 0))
	pressKey(pf, charKey(vtinput.VK_O, 'o', 0))
	if !fsp.AutoFilterActive() || fsp.FastFindStr != "*fo" {
		t.Fatalf("setup: filter open=%v with %q, want it open with %q", fsp.AutoFilterActive(), fsp.FastFindStr, "*fo")
	}
	start := fsp.GetSelectedName()
	pressKey(pf, keyDown(vtinput.VK_RIGHT, 0))
	if !fsp.AutoFilterActive() {
		t.Fatal("Right closed the filter")
	}
	if fsp.GetSelectedName() == start {
		t.Fatalf("Right did not move the cursor off %q in the filtered rows", start)
	}
	pressKey(pf, charKey(vtinput.VK_BACK, 0x7f, 0))
	if fsp.FastFindStr != "*f" {
		t.Fatalf("Backspace in the filter: %q, want %q", fsp.FastFindStr, "*f")
	}
	pressKey(pf, keyDown(vtinput.VK_Y, vtinput.LeftCtrlPressed))
	if !fsp.AutoFilterActive() || fsp.FastFindStr != "*" {
		t.Fatalf("Ctrl+Y in the filter: open=%v with %q, want it open and empty", fsp.AutoFilterActive(), fsp.FastFindStr)
	}
	if got := commandLineCursor(pf); got != "abc|" {
		t.Fatalf("the filter's keys reached the command line: %q", got)
	}
}

// Alt+F9 maximizes the window and leaves the search open, so the router must
// not close the search before the action runs; a file action closes it.
func TestHotkeyKeepsFastFind(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	if !hotkeyKeepsFastFind("Shell", keyDown(vtinput.VK_F9, vtinput.LeftAltPressed)) {
		t.Error("Alt+F9 must leave the search open")
	}
	if hotkeyKeepsFastFind("Shell", keyDown(vtinput.VK_F3, 0)) {
		t.Error("F3 must close the search")
	}
}
