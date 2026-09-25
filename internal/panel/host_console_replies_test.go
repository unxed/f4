package panel

import (
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
)

func TestHostConsoleReplyIsReturnedToPTYInsteadOfProcessedAsInput(t *testing.T) {
	pty := &mockPty{}
	pf := &PanelsFrame{
		Pty:               pty,
		ShellMode:         terminal.ShellModeHost,
		HostConsoleActive: true,
	}
	pf.noteHostConsoleQueries(pty, []byte("\x1b[6n"))

	const reply = "\x1b[7;12R"
	for _, ch := range reply {
		if !pf.consumeHostConsoleReply(&vtinput.InputEvent{
			Type:        vtinput.KeyEventType,
			KeyDown:     true,
			Char:        ch,
			InputSource: "ConPTY",
		}) {
			t.Fatalf("reply byte %q was not consumed", ch)
		}
	}
	if got := pty.String(); got != reply {
		t.Fatalf("PTY received %q, want %q", got, reply)
	}

	if !pf.consumeHostConsoleReply(&vtinput.InputEvent{
		Type:        vtinput.KeyEventType,
		InputSource: "ConPTY",
		KeyDown:     false,
	}) {
		t.Fatal("reply key-up was not swallowed")
	}
}

func TestHostConsoleReplyQueryCanCrossOutputReads(t *testing.T) {
	pty := &mockPty{}
	pf := &PanelsFrame{
		Pty:               pty,
		ShellMode:         terminal.ShellModeHost,
		HostConsoleActive: true,
	}
	pf.noteHostConsoleQueries(pty, []byte("\x1b["))
	pf.noteHostConsoleQueries(pty, []byte("6n"))

	for _, ch := range "\x1b[1;1R" {
		if !pf.consumeHostConsoleReply(&vtinput.InputEvent{
			Type:        vtinput.KeyEventType,
			KeyDown:     true,
			Char:        ch,
			InputSource: "ConPTY",
		}) {
			t.Fatalf("split-query reply byte %q was not consumed", ch)
		}
	}
	if got := pty.String(); got != "\x1b[1;1R" {
		t.Fatalf("PTY received %q, want %q", got, "\x1b[1;1R")
	}
}

func TestHostConsoleReplyNeedsOutstandingQuery(t *testing.T) {
	pf := &PanelsFrame{
		ShellMode:         terminal.ShellModeHost,
		HostConsoleActive: true,
	}
	if pf.consumeHostConsoleReply(&vtinput.InputEvent{
		Type:        vtinput.KeyEventType,
		KeyDown:     true,
		Char:        0x1b,
		InputSource: "ConPTY",
	}) {
		t.Fatal("plain Escape was mistaken for a terminal reply")
	}
}
