package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
)

func TestPanelsFrameRoutesFar2lKeyboardEventsToChild(t *testing.T) {
	pty := &mockPty{}
	tv := terminal.NewTerminalView(80, 24)
	defer tv.Close()
	tv.UseAltScreen = true
	tv.Pty = pty
	tv.HandleFar2lAPC("far2l1")
	pty.Reset() // exclude the activation acknowledgement

	pf := &PanelsFrame{Pty: pty, TermView: tv, ShowPanels: false}
	event := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: false, Char: 'a',
		VirtualKeyCode: 0x41, VirtualScanCode: 0x1e, RepeatCount: 1,
	}
	if !pf.ProcessKey(event) {
		t.Fatal("ProcessKey did not consume the far2l key-up event")
	}
	if got, want := pty.String(), "\x1b_f2lAQBBAB4AAAAAAGEAAABr\x07"; got != want {
		t.Fatalf("child input = %q, want full-form far2l key-up %q", got, want)
	}
}

func TestPanelsFrameRoutesFar2lMouseEventsToChild(t *testing.T) {
	pty := &mockPty{}
	tv := terminal.NewTerminalView(80, 24)
	defer tv.Close()
	tv.Pty = pty
	tv.HandleFar2lAPC("far2l1")
	pty.Reset() // exclude the activation acknowledgement

	pf := &PanelsFrame{Pty: pty, TermView: tv, ShowPanels: false}
	event := &vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true, MouseX: 12, MouseY: 3,
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}
	if !pf.ProcessMouse(event) {
		t.Fatal("ProcessMouse did not consume the far2l mouse event")
	}
	got := pty.String()
	if !strings.HasPrefix(got, "\x1b_f2l") || !strings.HasSuffix(got, "\x07") || strings.Contains(got, "\x1b[") {
		t.Fatalf("child input = %q, want one far2l mouse event", got)
	}
}
