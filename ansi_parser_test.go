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
func TestAnsiParser_SGR_Advanced(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// 1. Test TrueColor Foreground (38;2;R;G;B)
	p.Process([]byte("\x1b[38;2;255;128;64m"))
	expectedRGB := uint32(0xFF8040)
	if vtui.GetRGBFore(p.Attr) != expectedRGB {
		t.Errorf("TrueColor Fore: expected %06X, got %06X", expectedRGB, vtui.GetRGBFore(p.Attr))
	}
	if (p.Attr & vtui.ForegroundTrueColor) == 0 {
		t.Error("TrueColor Fore: ForegroundTrueColor flag not set")
	}

	// 2. Test 256-color Background (48;5;Index)
	// Index 208 is usually orange #ff8700
	p.Process([]byte("\x1b[48;5;208m"))
	expectedBG := uint32(0xFF8700)
	if vtui.GetRGBBack(p.Attr) != expectedBG {
		t.Errorf("256-color Back: expected %06X, got %06X", expectedBG, vtui.GetRGBBack(p.Attr))
	}

	// 3. Test Styles: Bold (1) and Underline (4)
	p.Process([]byte("\x1b[1;4m"))
	if (p.Attr & vtui.ForegroundIntensity) == 0 {
		t.Error("Style: Bold flag not set")
	}
	if (p.Attr & vtui.CommonLvbUnderscore) == 0 {
		t.Error("Style: Underline flag not set")
	}

	// 4. Test Reset (0)
	p.Process([]byte("\x1b[0m"))
	if p.Attr != DefaultTermAttr {
		t.Errorf("Reset: expected %v, got %v", DefaultTermAttr, p.Attr)
	}
}
func TestAnsiParser_DynamicPalette(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// 1. Change Palette index 1 (ANSI Red) to Pure Purple #FF00FF
	// Format: OSC 4 ; index ; color BEL
	p.Process([]byte("\x1b]4;1;#FF00FF\x07"))

	// 2. Set foreground to ANSI 31 (Red)
	p.Process([]byte("\x1b[31m"))

	gotColor := vtui.GetRGBFore(p.Attr)
	if gotColor != 0xFF00FF {
		t.Errorf("Dynamic Palette: expected Purple #FF00FF, got %06X", gotColor)
	}

	// 3. Test rgb:RR/GG/BB format (used by some versions of far2l)
	// Change index 4 (ANSI Blue) to #112233
	p.Process([]byte("\x1b]4;4;rgb:11/22/33\x07"))
	p.Process([]byte("\x1b[34m")) // SGR 34 is ANSI Blue
	gotColor = vtui.GetRGBFore(p.Attr)
	if gotColor != 0x112233 {
		t.Errorf("Dynamic Palette (rgb format): expected #112233, got %06X", gotColor)
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

func TestAnsiParser_OSC4_Palette(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// ANSI Color 1 — Red. By default in f4 palette it's 0xA00000.
	// Change it via OSC 4 to bright green #00FF00
	// Format: ESC ] 4 ; index ; color BEL
	oscSeq := "\x1b]4;1;#00FF00\x07"
	p.Process([]byte(oscSeq))

	if tv.Palette[1] != 0x00FF00 {
		t.Errorf("OSC 4 palette update failed. Expected #00FF00, got %06X", tv.Palette[1])
	}
}
func TestAnsiParser_REP_ECH(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// 1. Test REP (Repeat last char): write 'A' and repeat 5 times
	p.Process([]byte("A\x1b[5b"))
	line := tv.Lines[tv.CursorY]
	for i := 0; i < 6; i++ {
		if line[i].Char != 'A' {
			t.Errorf("REP failed at pos %d: expected 'A', got %c", i, rune(line[i].Char))
		}
	}

	// 2. Test ECH (Erase characters): erase 3 characters from position 0
	tv.SetCursor(0, tv.CursorY)
	p.Process([]byte("\x1b[3X"))
	for i := 0; i < 3; i++ {
		if line[i].Char != ' ' {
			t.Errorf("ECH failed at pos %d: expected space, got %c", i, rune(line[i].Char))
		}
	}
}
func TestAnsiParser_SplitUTF8(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// Symbol 'П' (0xD0 0x9F) sent in parts
	p.Process([]byte{0xD0})
	if tv.Lines[tv.CursorY][0].Char == 0xD0 {
		t.Error("Parser should not put incomplete UTF-8 byte on screen")
	}

	p.Process([]byte{0x9F})
	if tv.Lines[tv.CursorY][0].Char != 'П' {
		t.Errorf("Parser failed to assemble split UTF-8: expected 'П', got %c", rune(tv.Lines[tv.CursorY][0].Char))
	}
}
func TestAnsiParser_MovementAndErase(t *testing.T) {
	tv := NewTerminalView(10, 5)
	p := NewAnsiParser(tv, nil)

	// 1. Test CUP (H) - Cursor Position
	p.Process([]byte("\x1b[3;4H")) // 1-based, so should be 2,3
	if tv.CursorY != 2 || tv.CursorX != 3 {
		t.Errorf("CUP failed: expected (3,2), got (%d,%d)", tv.CursorX, tv.CursorY)
	}

	// 2. Test relative movements (A, B, C, D)
	p.Process([]byte("\x1b[2A")) // Up 2
	if tv.CursorY != 0 {
		t.Errorf("CUU failed: expected Y=0, got %d", tv.CursorY)
	}
	p.Process([]byte("\x1b[3B")) // Down 3
	if tv.CursorY != 3 {
		t.Errorf("CUD failed: expected Y=3, got %d", tv.CursorY)
	}
	p.Process([]byte("\x1b[5C")) // Forward 5
	if tv.CursorX != 8 { // 3 + 5 = 8
		t.Errorf("CUF failed: expected X=8, got %d", tv.CursorX)
	}
	p.Process([]byte("\x1b[4D")) // Backward 4
	if tv.CursorX != 4 { // 8 - 4 = 4
		t.Errorf("CUB failed: expected X=4, got %d", tv.CursorX)
	}

	// 3. Test ED (Erase Display) and EL (Erase Line)
	tv.PutChar('X', DefaultTermAttr)
	p.Process([]byte("\x1b[2J")) // Erase entire screen
	if tv.Lines[3][5].Char != ' ' {
		t.Error("ED(2) failed to clear screen")
	}
	tv.SetCursor(0, 0)

	// 4. Test Alternate Screen Buffer
	p.Process([]byte("Main"))
	p.Process([]byte("\x1b[?1049h")) // Switch to alt
	if !tv.UseAltScreen {
		t.Fatal("Failed to switch to alternate screen")
	}
	if tv.Lines[0][0].Char != 'M' {
		t.Error("Main screen content was affected by alt screen switch")
	}
	p.Process([]byte("Alt")) // Write to alt screen
	if tv.AltLines[0][0].Char != 'A' {
		t.Error("Failed to write to alt screen")
	}
	p.Process([]byte("\x1b[?1049l")) // Switch back to main
	if tv.UseAltScreen {
		t.Fatal("Failed to switch back to main screen")
	}
	if tv.Lines[0][0].Char != 'M' {
		t.Error("Main screen content was lost")
	}
}
