package terminal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

func init() {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
}

// mockPty captures writes to the PTY for testing parser responses
type mockPty struct {
	mu      sync.Mutex
	written []byte
	closed  bool
}

func (m *mockPty) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written = append(m.written, b...)
	return len(b), nil
}

func (m *mockPty) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.written)
}

func (m *mockPty) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written = nil
}
func (m *mockPty) Read(b []byte) (int, error) {
	for {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (m *mockPty) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
func (m *mockPty) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
func (m *mockPty) SetSize(cols, rows int)                {}
func (m *mockPty) Wait() error                           { return nil }
func (m *mockPty) Run(name string, args ...string) error { return nil }
func (m *mockPty) IsBusy() bool                          { return false }

func TestAnsiParser_CPR(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	Pty := &mockPty{}
	p := NewAnsiParser(tv, Pty)

	// 0-based coordinates in TerminalView: X=10, Y=5
	tv.SetCursor(10, 5)

	// Send Cursor Position Report (CPR) Request
	p.Process([]byte("\x1b[6n"))

	// Expected response: 1-based coordinates \x1b[row;colR
	expected := "\x1b[6;11R"
	if string(Pty.written) != expected {
		t.Errorf("Expected CPR response %q, got %q", expected, string(Pty.written))
	}
}
func TestAnsiParser_SGR_Advanced(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Test TrueColor Foreground (38;2;R;G;B)
	p.Process([]byte("\x1b[38;2;255;128;64m"))
	expectedRGB := uint32(0xFF8040)
	if vtui.GetRGBFore(p.Attr) != expectedRGB {
		t.Errorf("TrueColor Fore: expected %06X, got %06X", expectedRGB, vtui.GetRGBFore(p.Attr))
	}
	if (p.Attr & vtui.IsFgRGB) == 0 {
		t.Error("TrueColor Fore: IsFgRGB flag not set")
	}

	// 2. Test 256-color Background (48;5;Index)
	p.Process([]byte("\x1b[48;5;208m"))
	if vtui.GetIndexBack(p.Attr) != 208 {
		t.Errorf("256-color Back: expected 208, got %d", vtui.GetIndexBack(p.Attr))
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
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Change Palette index 1 (ANSI Red) to Pure Purple #FF00FF
	// Format: OSC 4 ; index ; color BEL
	p.Process([]byte("\x1b]4;1;#FF00FF\x07"))

	// 2. Set foreground to ANSI 31 (Red)
	p.Process([]byte("\x1b[31m"))

	gotColor := tv.Palette[vtui.GetIndexFore(p.Attr)]
	if gotColor != 0xFF00FF {
		t.Errorf("Dynamic Palette: expected Purple #FF00FF, got %06X", gotColor)
	}

	// 3. Test rgb:RR/GG/BB format (used by some versions of far2l)
	// Change index 4 (ANSI Blue) to #112233
	p.Process([]byte("\x1b]4;4;rgb:11/22/33\x07"))
	p.Process([]byte("\x1b[34m")) // SGR 34 is ANSI Blue
	gotColor = tv.Palette[vtui.GetIndexFore(p.Attr)]
	if gotColor != 0x112233 {
		t.Errorf("Dynamic Palette (rgb format): expected #112233, got %06X", gotColor)
	}
}

func TestAnsiParser_SaveRestoreCursor_ESC(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
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
	defer tv.Close()
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
	defer tv.Close()
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
	defer tv.Close()
	Pty := &mockPty{}
	p := NewAnsiParser(tv, Pty)

	// Request terminal status
	p.Process([]byte("\x1b[5n"))

	// Expected response: "Ready, no malfunction"
	expected := "\x1b[0n"
	if string(Pty.written) != expected {
		t.Errorf("Expected DSR status response %q, got %q", expected, string(Pty.written))
	}
}

func TestAnsiParser_OSC4_Palette(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
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
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Test REP (Repeat last char): write 'A' and repeat 5 times
	p.Process([]byte("A\x1b[5b"))
	line := tv.Lines[tv.CursorY]
	for i := 0; i < 6; i++ {
		if line[i].Char != 'A' {
			t.Errorf("REP failed at pos %d: expected 'A', got %c", i, testutil.Rune(line[i].Char))
		}
	}

	// 2. Test ECH (Erase characters): erase 3 characters from position 0
	tv.SetCursor(0, tv.CursorY)
	p.Process([]byte("\x1b[3X"))
	for i := 0; i < 3; i++ {
		if line[i].Char != ' ' {
			t.Errorf("ECH failed at pos %d: expected space, got %c", i, testutil.Rune(line[i].Char))
		}
	}
}
func TestAnsiParser_SplitUTF8(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// Symbol 'П' (0xD0 0x9F) sent in parts
	p.Process([]byte{0xD0})
	if tv.Lines[tv.CursorY][0].Char == 0xD0 {
		t.Error("Parser should not put incomplete UTF-8 byte on screen")
	}

	p.Process([]byte{0x9F})
	if tv.Lines[tv.CursorY][0].Char != 'П' {
		t.Errorf("Parser failed to assemble split UTF-8: expected 'П', got %c", testutil.Rune(tv.Lines[tv.CursorY][0].Char))
	}
}
func TestAnsiParser_MovementAndErase(t *testing.T) {
	tv := NewTerminalView(10, 5)
	defer tv.Close()
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
	if tv.CursorX != 8 {         // 3 + 5 = 8
		t.Errorf("CUF failed: expected X=8, got %d", tv.CursorX)
	}
	p.Process([]byte("\x1b[4D")) // Backward 4
	if tv.CursorX != 4 {         // 8 - 4 = 4
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
func TestAnsiParser_Win32PasteModes(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// Enable modes
	p.Process([]byte("\x1b[?9001h\x1b[?2004h"))
	if !tv.Win32InputMode || !tv.BracketedPasteMode {
		t.Error("Failed to enable Win32InputMode or BracketedPasteMode")
	}

	// Disable modes
	p.Process([]byte("\x1b[?9001l\x1b[?2004l"))
	if tv.Win32InputMode || tv.BracketedPasteMode {
		t.Error("Failed to disable Win32InputMode or BracketedPasteMode")
	}
}

func TestAnsiParser_AdvancedCSI(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// Ensure we are at the top-left
	tv.SetCursor(0, 0)

	// Test Delete Characters (P)
	p.Process([]byte("12345"))   // Write at (0,0). Cursor moves to (5,0)
	tv.SetCursor(1, 0)           // Move to '2'
	p.Process([]byte("\x1b[2P")) // Delete 2 characters ('2' and '3')
	// Result should be "145" at index 0, 1, 2 of line 0
	if tv.Lines[0][1].Char != '4' || tv.Lines[0][2].Char != '5' {
		t.Errorf("Delete characters failed. Found %c (U+%04X) at [0][1]", testutil.Rune(tv.Lines[0][1].Char), tv.Lines[0][1].Char)
	}

	// Test Insert Blank Characters (@)
	tv.SetCursor(1, 0)
	p.Process([]byte("\x1b[2@")) // Insert 2 blanks at pos 1
	// Result should be "1  45"
	if tv.Lines[0][1].Char != ' ' || tv.Lines[0][2].Char != ' ' || tv.Lines[0][3].Char != '4' {
		t.Errorf("Insert blank characters failed. Found %c at [0][3]", testutil.Rune(tv.Lines[0][3].Char))
	}
}

func TestAnsiParser_OSC_Advanced(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// Test window title OSC 2
	p.Process([]byte("\x1b]2;far2l console\x07"))
	if tv.Title != "far2l console" {
		t.Errorf("Window title failed: expected 'far2l console', got '%s'", tv.Title)
	}
}
func TestAnsiParser_SGR_IntensityPersistence(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Set Bold (Intensity)
	p.Process([]byte("\x1b[1m"))
	if (p.Attr & vtui.ForegroundIntensity) == 0 {
		t.Fatal("Intensity flag not set")
	}

	// 2. Set "Bright Red" using 90-range code
	// HYPOTHESIS: This should either clear the manual Intensity flag OR we must
	// ensure that Flush doesn't produce double-brightening.
	p.Process([]byte("\x1b[91m"))

	if vtui.GetIndexFore(p.Attr) != 9 {
		t.Errorf("Expected index 9, got %d", vtui.GetIndexFore(p.Attr))
	}

	// If Intensity flag is still there, attributesToANSI will produce "\x1b[1;38;5;9m"
	// which is "Bold + Bright Red".
	if (p.Attr & vtui.ForegroundIntensity) != 0 {
		t.Log("Note: Intensity flag persists after 90-range SGR. Check if this causes 'dirty' colors on host.")
	}
}

func TestAnsiParser_DefaultColorRestoration(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// Set some non-default colors
	p.Process([]byte("\x1b[32;44m")) // Green on Blue

	// Restore default foreground (39)
	p.Process([]byte("\x1b[39m"))
	if vtui.GetIndexFore(p.Attr) != vtui.GetIndexFore(DefaultTermAttr) {
		t.Errorf("SGR 39 failed to restore default index. Expected %d, got %d",
			vtui.GetIndexFore(DefaultTermAttr), vtui.GetIndexFore(p.Attr))
	}

	// Check if background is still blue
	if vtui.GetIndexBack(p.Attr) != 4 {
		t.Errorf("SGR 39 corrupted background. Expected 4, got %d", vtui.GetIndexBack(p.Attr))
	}
}
func TestAnsiParser_Robustness(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Truncated CSI: should stay in StateCSI
	p.Process([]byte("\x1b["))
	if p.State != StateCSI {
		t.Errorf("Expected state StateCSI, got %v", p.State)
	}

	// 2. Garbage inside CSI: should return to ground without crashing
	p.Process([]byte("1;?#@")) // '@' is a valid terminator but parameters are junk
	if p.State != StateGround {
		t.Errorf("Expected return to StateGround after junk CSI, got %v", p.State)
	}

	// 3. Truncated OSC
	p.Process([]byte("\x1b]"))
	if p.State != StateOSC {
		t.Errorf("Expected state StateOSC, got %v", p.State)
	}

	// 4. OSC terminated by ESC instead of BEL
	p.Process([]byte("2;Title\x1b"))
	// The handleOSC is called, then StateEsc is entered
	if p.State != StateEsc {
		t.Errorf("Expected transition from OSC to ESC, got %v", p.State)
	}
	if tv.Title != "Title" {
		t.Error("OSC title failed with ESC terminator")
	}
}

func TestAnsiParser_OSC52_Malformed(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// 1. Malformed Base64 (should not panic or crash)
	// OSC 52 ; c ; <invalid_base64> BEL
	p.Process([]byte("\x1b]52;c;!!!\x07"))

	// 2. Incomplete OSC 52
	p.Process([]byte("\x1b]52;c;"))
	p.Process([]byte{0x07}) // Just BEL

	// 3. Very large OSC 52 (Buffer overflow protection check)
	largeSeq := "\x1b]52;c;" + strings.Repeat("A", 10000) + "\x07"
	p.Process([]byte(largeSeq))

	// If we are here without panic, the test is passed.
}

func TestAnsiParser_UnrecognizedCSI(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// CSI ? 999 z is unrecognized.
	// The parser must consume it and return to Ground state without side effects.
	p.Process([]byte("\x1b[?999z"))

	if p.State != StateGround {
		t.Errorf("Parser stuck in state %v after unrecognized CSI", p.State)
	}
}
func TestAnsiParser_APC_Reset(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)
	if _, err := p.CurParam.WriteString("old_garbage"); err != nil {
		t.Fatal(err)
	}
	p.Process([]byte("\x1b_")) // Enter StateAPC
	if p.CurParam.Len() != 0 {
		t.Error("CurParam was not reset when entering APC state")
	}
}
func TestAnsiParser_DECRQM(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	Pty := &mockPty{}
	p := NewAnsiParser(tv, Pty)

	// --- 1. DEC Private Modes ---

	// Unknown Private Mode
	p.Process([]byte("\x1b[?7777$p"))
	if string(Pty.written) != "\x1b[?7777;0$y" {
		t.Errorf("Expected not recognized (0) for ?7777, got %q", string(Pty.written))
	}
	Pty.written = nil

	// Mode 1: Application Cursor Keys
	tv.ApplicationCursorKeys = false
	p.Process([]byte("\x1b[?1$p"))
	if string(Pty.written) != "\x1b[?1;2$y" {
		t.Errorf("Mode 1 Reset fail: %q", string(Pty.written))
	}
	Pty.written = nil
	tv.ApplicationCursorKeys = true
	p.Process([]byte("\x1b[?1$p"))
	if string(Pty.written) != "\x1b[?1;1$y" {
		t.Errorf("Mode 1 Set fail: %q", string(Pty.written))
	}
	Pty.written = nil

	// Mode 47 & 1049: Alt Screen
	tv.UseAltScreen = false
	p.Process([]byte("\x1b[?47$p"))
	if string(Pty.written) != "\x1b[?47;2$y" {
		t.Errorf("Mode 47 Reset fail")
	}
	Pty.written = nil
	tv.UseAltScreen = true
	p.Process([]byte("\x1b[?1049$p"))
	if string(Pty.written) != "\x1b[?1049;1$y" {
		t.Errorf("Mode 1049 Set fail")
	}
	Pty.written = nil

	// Mode 2004: Bracketed Paste
	tv.BracketedPasteMode = false
	p.Process([]byte("\x1b[?2004$p"))
	if string(Pty.written) != "\x1b[?2004;2$y" {
		t.Errorf("Mode 2004 Reset fail")
	}
	Pty.written = nil
	tv.BracketedPasteMode = true
	p.Process([]byte("\x1b[?2004$p"))
	if string(Pty.written) != "\x1b[?2004;1$y" {
		t.Errorf("Mode 2004 Set fail")
	}
	Pty.written = nil

	// Mode 9001: Win32 Input
	tv.Win32InputMode = false
	p.Process([]byte("\x1b[?9001$p"))
	if string(Pty.written) != "\x1b[?9001;2$y" {
		t.Errorf("Mode 9001 Reset fail")
	}
	Pty.written = nil
	tv.Win32InputMode = true
	p.Process([]byte("\x1b[?9001$p"))
	if string(Pty.written) != "\x1b[?9001;1$y" {
		t.Errorf("Mode 9001 Set fail")
	}
	Pty.written = nil

	// --- 2. Standard Modes ---

	// Standard mode (always 0/not recognized in our current implementation)
	p.Process([]byte("\x1b[20$p"))
	if string(Pty.written) != "\x1b[20;0$y" {
		t.Errorf("Expected not recognized for standard mode 20, got %q", string(Pty.written))
	}
	Pty.written = nil

	// --- 3. Edge Cases & Negative Tests ---

	// Wrong intermediate byte (e.g. # instead of $)
	p.Process([]byte("\x1b[?1#p"))
	if len(Pty.written) > 0 {
		t.Error("Should not respond to DECRQM with wrong intermediate byte")
	}
	Pty.written = nil

	// Missing parameters
	p.Process([]byte("\x1b[$p"))
	if len(Pty.written) > 0 {
		t.Error("Should not respond to DECRQM without parameters")
	}
}
func TestAnsiParser_TechnicalCommandFilter(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	tv.BracketedPasteMode = true // Изменим стейт, чтобы убедиться, что trailingANSI корректно отработает

	// Имитация технической команды, которую генерирует f4 для Unix (set +H...).
	// Обратите внимание: она содержит эхо самой команды и trailing ANSI.
	techCmd := "set +H; cd '/tmp' && { printf \"\\033]133;C\\007\"; ./script.sh ; printf \"\\033]133;D\\007\"; }\r\n"
	trailingANSI := "\x1b[?2004l" // Отключение bracketed paste и т.д.

	p.Process([]byte(techCmd + trailingANSI))

	// Парсер должен был перехватить и вырезать `techCmd`,
	// поэтому на экране терминала (Active Grid) не должно быть текста скрипта 's', 'e', 't'.
	if tv.Lines[tv.CursorY][0].Char == 's' {
		t.Error("Technical command was leaked to the visual screen!")
	}

	// Убеждаемся, что trailingANSI не был утерян вместе с вырезанной командой
	// и благополучно отработал, отключив режим bracketed paste.
	if tv.BracketedPasteMode {
		t.Error("Trailing ANSI sequence was ignored/cut off during technical command filtering!")
	}
}
func TestAnsiParser_WindowsAbsoluteJumpRobustness(t *testing.T) {
	// Типичный "грязный" чанк от ConPTY: очистка экрана + прыжок в середину + текст
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	// \x1b[2J (Clear) \x1b[10;5H (Jump to row 10, col 5)
	chunk := "\x1b[2J\x1b[10;5HData"
	p.Process([]byte(chunk))

	// After writing 4 bytes "Data", X should be 4 + 4 = 8
	if tv.CursorY != 9 || tv.CursorX != 8 {
		t.Errorf("Absolute jump failed. Expected (8,9), got (%d,%d)", tv.CursorX, tv.CursorY)
	}

	if tv.Lines[9][4].Char != 'D' {
		t.Errorf("Data landed in wrong place. Expected 'D' at [9][4], got '%c'", tv.Lines[9][4].Char)
	}
}
func TestAnsiParser_WindowsExcision_CrossPlatform(t *testing.T) {
	// Этот тест проверяет логику вырезания технических команд Windows,
	// даже если тест запущен на Linux/macOS.
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple CD excision",
			input:    "cd /d \"C:\\Windows\" & dir\r\n",
			expected: "dir",
		},
		{
			name:     "Excision with prompt (screen scraping simulation)",
			input:    "C:\\Users\\f4>cd /d \"D:\\Data\" & echo 123\r\n",
			expected: "C:\\Users\\f4>echo 123",
		},
		{
			name:     "Multiple excisions in one buffer",
			input:    "Prompt1>cd /d \"A\" & cmd1\r\nPrompt2>cd /d \"B\" & cmd2",
			expected: "Prompt1>cmd1\nPrompt2>cmd2",
		},
		{
			name:     "Path with spaces and special chars",
			input:    "C:\\>cd /d \"C:\\My Folder & Stuff\" & whoami\r\n",
			expected: "C:\\>whoami",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tv.ResetBuffer(80, 24)
			tv.Pt = piecetable.New([]byte{}) // Reset history
			tv.Li.Rebuild(tv.Pt)

			p.Process([]byte(tt.input))

			logBytes := tv.GetAllLogBytes()
			// Очищаем от лишних пробелов в конце строк сетки
			result := strings.TrimSpace(string(logBytes))

			if !strings.Contains(result, tt.expected) {
				t.Errorf("Excision failed for [%s].\nExpected to contain: %q\nGot log: %q", tt.name, tt.expected, result)
			}

			if strings.Contains(result, "cd /d") {
				t.Errorf("Excision failed for [%s]: technical 'cd' command leaked into log!", tt.name)
			}
		})
	}
}

func TestAnsiParser_ExcisionExtra(t *testing.T) {
	for _, tt := range []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Windows background sync excision",
			input:    "C:\\Old>cd /d \"C:\\New\" & rem f4_sync\r\n",
			expected: "",
		},
		{
			name:     "Windows technical command excision",
			input:    "C:\\Old>cd /d \"C:\\New\" & whoami\r\n",
			expected: "C:\\Old>whoami\n",
		},
		{
			name:     "Unix background sync excision",
			input:    "user@host:~$ cd '/new/path' && true f4_sync\r\n",
			expected: "",
		},
		{
			name:     "Unix technical command excision",
			input:    "set +H; cd '/new/path' && { printf \"\\033]133;C\\007\"; ./'cmd' ; printf \"\\033]133;D\\007\"; }\r\n",
			expected: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tv := NewTerminalView(80, 24)
			parser := NewAnsiParser(tv, nil)
			parser.Process([]byte(tt.input))

			logBytes := tv.GetAllLogBytes()
			logStr := string(logBytes)

			// Normalize newlines for cross-platform comparison
			logStr = strings.ReplaceAll(logStr, "\r\n", "\n")

			if !strings.Contains(logStr, tt.expected) {
				t.Errorf("Expected log to contain %q, but got %q", tt.expected, logStr)
			}

			// expected "" makes the check above vacuous; the real assertion
			// is that the sync marker never leaks into the visible log.
			if strings.Contains(logStr, "f4_sync") {
				t.Errorf("Sync marker leaked into log: %q", logStr)
			}
		})
	}
}

func TestAnsiParser_WindowsExcision_SplitAcrossChunks(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
	}{
		{
			name: "long path split after path bytes",
			chunks: []string{
				"C:\\Old>cd /d \"C:\\Users\\Administrator\\AppData\\Roaming\\Microsoft\\Internet Explorer\\",
				"Quick Launch\" & rem f4_sync\r",
				"\nafter\r\n",
			},
		},
		{
			name: "command marker split",
			chunks: []string{
				"C:\\Old>cd /",
				"d \"C:\\New\" & rem f4_sync\r\n",
				"after\r\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tv := NewTerminalView(80, 24)
			defer tv.Close()
			parser := NewAnsiParser(tv, nil)

			for _, chunk := range tt.chunks {
				parser.Process([]byte(chunk))
			}

			logStr := string(tv.GetAllLogBytes())
			if strings.Contains(logStr, "cd /d") || strings.Contains(logStr, "f4_sync") || strings.Contains(logStr, "Quick Launch") {
				t.Fatalf("split technical command leaked into terminal log: %q", logStr)
			}
			if !strings.Contains(logStr, "after") {
				t.Fatalf("data after split technical command was lost: %q", logStr)
			}
		})
	}
}

type mockClipAuthManager struct {
	authorized bool
}

func (m *mockClipAuthManager) Authorize(id string) int {
	if m == nil {
		return 0
	}
	if m.authorized {
		return 1 // Allow Once
	}
	return 0 // Deny
}

func TestAnsiParser_OSC52_Read_Security(t *testing.T) {
	tv := NewTerminalView(80, 24)
	Pty := &mockPtyForTerminal{}
	parser := NewAnsiParser(tv, Pty)

	// Test 1: Denied access
	vtui.GlobalClipboardAccessManager = &mockClipAuthManager{authorized: false}
	parser.Process([]byte("\x1b]52;c;?\x07"))
	parser.waitOSC52()

	if Pty.Len() > 0 {
		t.Errorf("Expected no output when clipboard read is denied, got %q", Pty.String())
	}

	// Test 2: Allowed access
	vtui.GlobalClipboardAccessManager = &mockClipAuthManager{authorized: true}
	vtui.SetClipboard("secret_data")
	for i := 0; i < 50; i++ {
		if vtui.GetClipboard() == "secret_data" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	parser.Process([]byte("\x1b]52;c;?\x07"))
	parser.waitOSC52()

	out := Pty.String()

	if !strings.Contains(out, "\x1b]52;c;") {
		t.Errorf("Expected OSC 52 reply containing clipboard data, got %q", out)
	}

	// Reset global state
	vtui.GlobalClipboardAccessManager = nil
}

func TestAnsiParser_OSC52_Write_Success(t *testing.T) {
	tv := NewTerminalView(80, 24)
	parser := NewAnsiParser(tv, nil)

	testStr := "Hello OSC 52"
	b64 := base64.StdEncoding.EncodeToString([]byte(testStr))

	// OSC 52 ; selection (c) ; data (b64) BEL
	parser.Process([]byte(fmt.Sprintf("\x1b]52;c;%s\x07", b64)))

	for i := 0; i < 50; i++ {
		if vtui.GetClipboard() == testStr {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := vtui.GetClipboard()
	if got != testStr {
		t.Errorf("Expected clipboard to be %q, got %q", testStr, got)
	}
}
func TestAnsiParser_Excision_UTF8_Safety(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// Simulation: Windows sync command followed by the first byte of 'П' (0xD0)
	// In the old implementation string(data) would have corrupted 0xD0 if it was at the end of a chunk.
	syncCmd := []byte("cd /d \"C:\\\" & rem f4_sync\r\n\r\n")
	partialUTF8 := []byte{0xD0}

	p.Process(append(syncCmd, partialUTF8...))

	// Sync command must be excised
	logStr := string(tv.GetAllLogBytes())
	if strings.Contains(logStr, "f4_sync") {
		t.Error("Sync command was not correctly excised from byte buffer")
	}

	// Partial UTF-8 must be preserved in the parser's internal buffer
	if len(p.runeBuf) != 1 || p.runeBuf[0] != 0xD0 {
		t.Errorf("Partial UTF-8 byte was lost or corrupted during excision. Buffer: %v", p.runeBuf)
	}

	// Completing the sequence: sending 0x9F (second byte of 'П')
	p.Process([]byte{0x9F})
	if tv.Lines[tv.CursorY][0].Char != 'П' {
		t.Errorf("UTF-8 sequence was not correctly assembled after excision. Got: %c", testutil.Rune(tv.Lines[tv.CursorY][0].Char))
	}
}

func TestAnsiParser_DECSET_Modes(t *testing.T) {
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	// 1. AutoWrap (CSI ? 7 h/l)
	tv.AutoWrap = true
	p.Process([]byte("\x1b[?7l"))
	if tv.AutoWrap {
		t.Error("CSI ? 7 l failed to disable AutoWrap")
	}
	p.Process([]byte("\x1b[?7h"))
	if !tv.AutoWrap {
		t.Error("CSI ? 7 h failed to enable AutoWrap")
	}

	// 2. Mouse Tracking (1000, 1002, 1003)
	p.Process([]byte("\x1b[?1000h"))
	if tv.MouseTrackingMode != 1000 {
		t.Errorf("Expected MouseTrackingMode 1000, got %d", tv.MouseTrackingMode)
	}
	p.Process([]byte("\x1b[?1002h"))
	if tv.MouseTrackingMode != 1002 {
		t.Errorf("Expected MouseTrackingMode 1002, got %d", tv.MouseTrackingMode)
	}
	p.Process([]byte("\x1b[?1000l"))
	if tv.MouseTrackingMode != 0 {
		t.Error("Mouse tracking was not disabled by CSI ? 1000 l")
	}

	// 3. Mouse SGR Mode (1006)
	tv.MouseSGRMode = false
	p.Process([]byte("\x1b[?1006h"))
	if !tv.MouseSGRMode {
		t.Error("CSI ? 1006 h failed to enable MouseSGRMode")
	}

	// htop enables SGR encoding and normal mouse tracking in one CSI.
	tv.MouseTrackingMode = 0
	tv.MouseSGRMode = false
	p.Process([]byte("\x1b[?1006;1000h"))
	if tv.MouseTrackingMode != 1000 || !tv.MouseSGRMode {
		t.Errorf("CSI ? 1006;1000 h = mode %d, sgr %t; want normal tracking with SGR", tv.MouseTrackingMode, tv.MouseSGRMode)
	}
}

func TestAnsiParser_DECTCEMControlsTerminalCursor(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)

	if !tv.CursorVisible {
		t.Fatal("new terminal must start with a visible cursor")
	}
	p.Process([]byte("\x1b[?25l"))
	if tv.CursorVisible {
		t.Fatal("CSI ? 25 l did not hide the terminal cursor")
	}
	p.Process([]byte("\x1b[?25h"))
	if !tv.CursorVisible {
		t.Fatal("CSI ? 25 h did not show the terminal cursor")
	}

	// The parser accepts the non-private spelling as well; it is useful for
	// applications that emit the mode without the DEC private marker.
	p.Process([]byte("\x1b[25l"))
	if tv.CursorVisible {
		t.Fatal("CSI 25 l did not hide the terminal cursor")
	}
	p.Process([]byte("\x1bc"))
	if !tv.CursorVisible {
		t.Fatal("RIS did not restore the terminal cursor visibility")
	}
}

// recordingPty collects everything written back to it.
type recordingPty struct {
	mockPty
	mu   sync.Mutex
	data []byte
}

func (p *recordingPty) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data = append(p.data, b...)
	return len(b), nil
}

func (p *recordingPty) written() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.data)
}

// One parser serves every shell shown in the panel, so a device query has to
// be answered into the shell that asked. Answering into the local shell types
// the reply into its stdin as if the user had entered it, and leaves the
// program that asked (vim over ssh, say) waiting for an answer that never
// comes.
func TestAnsiParser_RepliesGoToTheAskingShell(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	local := &recordingPty{}
	remote := &recordingPty{}
	p := NewAnsiParser(tv, local)

	var target PtyBackend
	p.ReplyTo = func() PtyBackend { return target }

	// No remote session: the local shell is the one driving the terminal.
	p.Process([]byte("\x1b[c"))
	if local.written() == "" {
		t.Fatal("device attributes query was not answered at all")
	}
	if remote.written() != "" {
		t.Fatalf("reply reached a shell that did not ask: %q", remote.written())
	}

	// A remote shell is now driving; its queries must come back to it.
	target = remote
	before := local.written()
	p.Process([]byte("\x1b[6n"))
	if remote.written() == "" {
		t.Error("cursor position report was not sent to the remote shell")
	}
	if local.written() != before {
		t.Errorf("reply leaked into the local shell's stdin: %q", local.written()[len(before):])
	}
}

// With no shell wired in and none driving, a query is dropped rather than
// panicking the read goroutine.
func TestAnsiParser_ReplyWithoutAnyPtyIsDropped(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, nil)
	p.ReplyTo = func() PtyBackend { return nil }
	p.Process([]byte("\x1b[c\x1b[6n\x1b[?1;1;0S"))
}

// Keyboard encodings belong to the program that switched them on. When its
// session ends they have to go with it, or f4 keeps encoding keystrokes for a
// shell that never asked and cannot read them.
func TestTerminalView_ResetKeyboardProtocols(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, &mockPty{})
	p.Process([]byte("\x1b[?9001h\x1b[?1h"))
	tv.KittyFlags.Store(1)
	if !tv.Win32InputMode {
		t.Fatal("win32 input mode was not enabled by DECSET 9001")
	}

	tv.ResetKeyboardProtocols()
	if tv.Win32InputMode || tv.KittyFlags.Load() != 0 || tv.ApplicationCursorKeys {
		t.Errorf("modes survived the reset: win32=%v kitty=%d appcursor=%v",
			tv.Win32InputMode, tv.KittyFlags.Load(), tv.ApplicationCursorKeys)
	}
}

// f4#128, RUP step 1: KittyEnableDisambiguateSeq is what f4 itself feeds
// through the ansi parser right after a fresh local shell's PTY comes up, so
// that a bare shell -- which never asks for the kitty keyboard protocol on
// its own -- still starts with the disambiguate flag on. This is the same
// parsing path a real program's own request (far2l's, for one) goes through;
// this test only checks that the sequence itself parses into exactly the
// flag it claims to.
func TestKittyEnableDisambiguateSeq_SetsDisambiguateFlag(t *testing.T) {
	tv := NewTerminalView(80, 24)
	defer tv.Close()
	p := NewAnsiParser(tv, &mockPty{})

	if tv.KittyFlags.Load() != 0 {
		t.Fatalf("fresh TerminalView already reports kitty flags: %d", tv.KittyFlags.Load())
	}

	p.Process([]byte(KittyEnableDisambiguateSeq))

	if tv.KittyFlags.Load() != 1 {
		t.Fatalf("KittyEnableDisambiguateSeq set flags=%d, want exactly 1 (disambiguate escape codes)", tv.KittyFlags.Load())
	}
}

// zsh's line editor echoes the line f4 typed to follow the panel a character
// or two at a time, and redraws it where it wraps; the echo never sat in one
// chunk and stayed on the console (#1886). The chunks are the ones zsh 5.9
// wrote for " cd '/tmp' && true f4_sync" and for a path that wraps at 100
// columns.
func TestAnsiParser_UnixSyncEchoInZshPieces(t *testing.T) {
	short := []string{" \x08 cd '/t", "m", "p' && tru", "e", " f", "4", "_s", "y", "nc",
		"\x1b[?2004l\r\r\n",
		"\x1b[1m\x1b[7m#\x1b[27m\x1b[1m\x1b[0m       \r \r\r\x1b[0m\x1b[27m\x1b[24m\x1b[Jroot@host /tmp # \x1b[K", "\x1b[?2004h"}
	long := []string{" \x08 cd '/tmp", "/cl", "aud", "e-0/scratchpad/h1", "88", "6 \r\x1b[K/", "\r/d1", "/d", "2'", " ", "&", "&", " t", "ru", "e", " ",
		"f", "4_", "sy", "nc", "\x1b[?2004l\r\r\n", "\x1b[0m\x1b[27m\x1b[24m\x1b[Jroot@host d2 # \x1b[K\x1b[?2004h"}
	bash := []string{" cd '/tmp' && true f4_sync\r\n\x1b[?2004l\r\x1b[?2004hroot@host:/tmp# "}
	for name, chunks := range map[string][]string{"short": short, "wrapped": long, "bash in one piece": bash} {
		t.Run(name, func(t *testing.T) {
			tv := NewTerminalView(100, 24)
			parser := NewAnsiParser(tv, nil)
			parser.Process([]byte("root@host ~ # "))
			parser.ExpectUnixSyncEcho()
			for _, c := range chunks {
				parser.Process([]byte(c))
			}
			log := string(tv.GetAllLogBytes())
			if strings.Contains(log, "f4_sync") || strings.Contains(log, "cd '") || strings.Contains(log, "true") {
				t.Fatalf("the typed line leaked: %q", log)
			}
			if !strings.Contains(log, "root@host") {
				t.Fatalf("the prompt is gone: %q", log)
			}
			if parser.unixEchoArms.Load() != 0 || parser.unixEchoHeld != nil {
				t.Fatalf("echo still awaited (%d) or held (%q)", parser.unixEchoArms.Load(), parser.unixEchoHeld)
			}
		})
	}
}

// Nothing is held unless f4 typed a sync line: what the user types, even a
// " cd '" of their own, is on the screen as it comes; and a line that does
// not end in the marker is let through when it ends.
func TestAnsiParser_UnixSyncEchoHoldsOnlyItsOwnLine(t *testing.T) {
	tv := NewTerminalView(80, 24)
	parser := NewAnsiParser(tv, nil)
	for _, c := range []string{" ", "\x08 cd '", "/x", "'"} {
		if got := parser.ProcessFiltered([]byte(c)); len(got) == 0 {
			t.Fatalf("user input %q was held back", c)
		}
	}

	parser.ExpectUnixSyncEcho()
	var shown []byte
	for _, c := range []string{"\r\n$ ", " cd '/y'", " && ls", "\r\n", "a b\r\n"} {
		shown = append(shown, parser.ProcessFiltered([]byte(c))...)
	}
	if !bytes.Contains(shown, []byte(" cd '/y' && ls\r\n")) || !bytes.Contains(shown, []byte("a b")) {
		t.Fatalf("a line without the marker was not let through: %q", shown)
	}
}
