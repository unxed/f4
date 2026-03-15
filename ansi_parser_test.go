package main

import (
	"testing"

	"github.com/unxed/vtui"
)

func init() {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
}

// mockPty captures writes to the PTY for testing parser responses
type mockPty struct {
	written []byte
}

func (m *mockPty) Write(b []byte) (int, error) {
	m.written = append(m.written, b...)
	return len(b), nil
}
func (m *mockPty) Read(b []byte) (int, error)            { return 0, nil }
func (m *mockPty) SetSize(cols, rows int)                {}
func (m *mockPty) Wait() error                           { return nil }
func (m *mockPty) Run(name string, args ...string) error { return nil }

func TestAnsiParser_CPR(t *testing.T) {
	tv := NewTerminalView(80, 24)
	pty := &mockPty{}
	p := NewAnsiParser(tv, pty)

	// 0-based coordinates in TerminalView: X=10, Y=5
	tv.SetCursor(10, 5)

	// Send Cursor Position Report (CPR) request
	p.Process([]byte("\x1b[6n"))

	// Expected response: 1-based coordinates \x1b[row;colR
	expected := "\x1b[6;11R"
	if string(pty.written) != expected {
		t.Errorf("Expected CPR response %q, got %q", expected, string(pty.written))
	}
}

func TestAnsiParser_SaveRestoreCursor_ESC(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	tv.SetCursor(15, 8)

	// ESC 7 saves the cursor
	p.Process([]byte("\x1b7"))

	// Move away
	tv.SetCursor(0, 0)

	// ESC 8 restores the cursor
	p.Process([]byte("\x1b8"))

	if tv.CursorX != 15 || tv.CursorY != 8 {
		t.Errorf("Expected cursor at (15, 8) after restore, got (%d, %d)", tv.CursorX, tv.CursorY)
	}
}

func TestAnsiParser_SaveRestoreCursor_CSI(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	tv.SetCursor(22, 11)

	// CSI s saves the cursor
	p.Process([]byte("\x1b[s"))

	// Move away
	tv.SetCursor(0, 0)

	// CSI u restores the cursor
	p.Process([]byte("\x1b[u"))

	if tv.CursorX != 22 || tv.CursorY != 11 {
		t.Errorf("Expected cursor at (22, 11) after restore, got (%d, %d)", tv.CursorX, tv.CursorY)
	}
}

func TestAnsiParser_StringTerminator(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// Trigger APC state (Application Program Command)
	p.Process([]byte("\x1b_"))
	if p.State != StateAPC {
		t.Fatalf("Expected state to be StateAPC, got %v", p.State)
	}

	// Send ESC \ (String Terminator)
	p.Process([]byte("\x1b\\"))

	// Parser should return to ground state
	if p.State != StateGround {
		t.Errorf("Expected state to return to StateGround after ST, got %v", p.State)
	}
}

func TestAnsiParser_DSR_Status(t *testing.T) {
	tv := NewTerminalView(80, 24)
	pty := &mockPty{}
	p := NewAnsiParser(tv, pty)

	// Request terminal status
	p.Process([]byte("\x1b[5n"))

	// Expected response: "Ready, no malfunction"
	expected := "\x1b[0n"
	if string(pty.written) != expected {
		t.Errorf("Expected DSR status response %q, got %q", expected, string(pty.written))
	}
}