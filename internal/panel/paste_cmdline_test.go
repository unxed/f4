package panel

import (
	"testing"

	"github.com/unxed/vtinput"
)

// A paste is text, not typing (docs/FAR2L_COMPAT.md, B-03, B-04, B-06): a line
// break inside a paste into the command line never acts as Enter, so nothing
// of the pasted text runs before the user presses Enter themselves
// (unxed/f4#2155).

func pasteKey(char rune, vk uint16, ctrl bool) *vtinput.InputEvent {
	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char, VirtualKeyCode: vk}
	if ctrl {
		e.ControlKeyState = vtinput.LeftCtrlPressed
	}
	return e
}

// pasteInto sends a bracketed paste of the given key events to the frame and
// returns the text the command line holds afterwards.
func pasteInto(t *testing.T, f *issue2049Fixture, keys ...*vtinput.InputEvent) string {
	t.Helper()
	f.pf.CmdLine.Edit.SetText("")
	pressKey(f.pf, &vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
	for _, k := range keys {
		pressKey(f.pf, k)
	}
	pressKey(f.pf, &vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: false})
	return f.pf.CmdLine.Edit.GetText()
}

func TestPasteIntoCommandLineCRIsNotEnter(t *testing.T) {
	f := newIssue2049Fixture(t)
	got := pasteInto(t, f,
		pasteKey('a', vtinput.VK_A, false),
		pasteKey('\r', vtinput.VK_RETURN, false),
		pasteKey('b', vtinput.VK_B, false),
	)
	if got != "a b" {
		t.Fatalf("command line after a paste of a CR b: %q, want %q (the CR ran the first line?)", got, "a b")
	}
}

func TestTerminalPasteForwardsCarriageReturn(t *testing.T) {
	for _, bracketed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "bracketed"}[bracketed], func(t *testing.T) {
			f := newIssue2049Fixture(t)
			f.pf.ShowPanels = false
			f.pf.Executing = true
			pty := &mockPty{}
			f.pf.Pty = pty
			f.pf.TermView.BracketedPasteMode = bracketed

			f.pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
			f.pf.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, Char: '\r',
				VirtualKeyCode: vtinput.VK_RETURN, IsLegacy: true, InputSource: "legacy_ctrl",
			})
			f.pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: false})

			want := "\r"
			if bracketed {
				want = "\x1b[200~\r\x1b[201~"
			}
			if got := pty.String(); got != want {
				t.Fatalf("terminal paste bytes %q, want %q", got, want)
			}
		})
	}
}

func TestPasteIntoCommandLineLFIsNotLost(t *testing.T) {
	f := newIssue2049Fixture(t)
	got := pasteInto(t, f,
		pasteKey('a', vtinput.VK_A, false),
		pasteKey(0, vtinput.VK_J, true), // the byte 0x0A arrives as Ctrl+J
		pasteKey('b', vtinput.VK_B, false),
	)
	if got != "a b" {
		t.Fatalf("command line after a paste of a LF b: %q, want %q", got, "a b")
	}
}

func TestPasteIntoCommandLineCRLFIsOneBreak(t *testing.T) {
	f := newIssue2049Fixture(t)
	got := pasteInto(t, f,
		pasteKey('a', vtinput.VK_A, false),
		pasteKey('\r', vtinput.VK_RETURN, false),
		pasteKey(0, vtinput.VK_J, true),
		pasteKey('b', vtinput.VK_B, false),
	)
	if got != "a b" {
		t.Fatalf("command line after a paste of a CR LF b: %q, want %q", got, "a b")
	}
}

func TestPasteIntoCommandLineEndsWithTheMarker(t *testing.T) {
	f := newIssue2049Fixture(t)
	pasteInto(t, f, pasteKey('x', vtinput.VK_X, false))
	// after the paste Enter is Enter again: a typed 'y' lands in the line
	pressKey(f.pf, pasteKey('y', vtinput.VK_Y, false))
	if got := f.pf.CmdLine.Edit.GetText(); got != "xy" {
		t.Fatalf("typing after a paste: %q, want %q", got, "xy")
	}
}

// The edit line is measured and drawn for every key it takes, so a long paste
// typed into it key by key took minutes; the text is put in at the end of the
// paste (unxed/f4#2157).
func TestPasteIntoCommandLineLongTextArrivesWhole(t *testing.T) {
	f := newIssue2049Fixture(t)
	var keys []*vtinput.InputEvent
	want := ""
	for i := 0; i < 3000; i++ {
		keys = append(keys, pasteKey('x', vtinput.VK_X, false))
		want += "x"
	}
	if got := pasteInto(t, f, keys...); got != want {
		t.Fatalf("command line holds %d characters after a paste of %d", len(got), len(want))
	}
}

func TestPasteIntoCommandLineKeepsTheOrderWithOtherKeys(t *testing.T) {
	f := newIssue2049Fixture(t)
	got := pasteInto(t, f,
		pasteKey('a', vtinput.VK_A, false),
		pasteKey('b', vtinput.VK_B, false),
		pasteKey(0, vtinput.VK_LEFT, false),
		pasteKey('c', vtinput.VK_C, false),
	)
	if got != "acb" {
		t.Fatalf("command line after a, b, Left, c: %q, want %q", got, "acb")
	}
}
