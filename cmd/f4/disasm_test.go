package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// movRaxRcx is "mov rax, rcx" in 64-bit mode. The same three bytes are
// "dec eax; mov eax, ecx" in 32-bit mode and "dec ax; mov ax, cx" in
// 16-bit mode: the REX prefix is a one-byte instruction there. So a run of
// them is a line every 3 bytes in one mode and lines of 1 and 2 bytes by
// turns in the other two, which is what every navigation test below leans
// on.
var movRaxRcx = []byte{0x48, 0x89, 0xC8}

func TestNextDisasmMode(t *testing.T) {
	for _, tc := range []struct{ from, want int }{
		{64, 32},
		{32, 16},
		{16, 64},
		// Undecided and garbage restart the cycle rather than jam it.
		{0, 64},
		{48, 64},
	} {
		if got := nextDisasmMode(tc.from); got != tc.want {
			t.Errorf("nextDisasmMode(%d) = %d, want %d", tc.from, got, tc.want)
		}
	}
}

func TestDisasmInstruction_ModeDecidesTheReading(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		mode int
		text string
		len  int
	}{
		{"rex is a prefix in 64", movRaxRcx, 64, "mov rax, rcx", 3},
		{"rex is dec in 32", movRaxRcx, 32, "dec eax", 1},
		{"rex is dec in 16", movRaxRcx, 16, "dec ax", 1},
		{"imm16 in 16", []byte{0xB8, 0x34, 0x12, 0x00, 0x00}, 16, "mov ax, 0x1234", 3},
		{"imm32 in 32", []byte{0xB8, 0x34, 0x12, 0x00, 0x00}, 32, "mov eax, 0x1234", 5},
		{"push es exists in 16", []byte{0x06}, 16, "push es", 1},
		// An opcode the mode does not have is shown as a byte and skipped,
		// never swallowed with what follows it.
		{"push es is invalid in 64", []byte{0x06, 0x90}, 64, "db 0x06", 1},
	} {
		text, n := disasmInstruction(tc.data, tc.mode, 0)
		if text != tc.text || n != tc.len {
			t.Errorf("%s: disasmInstruction(% X, %d) = (%q, %d), want (%q, %d)",
				tc.name, tc.data, tc.mode, text, n, tc.text, tc.len)
		}
		if got := disasmInstLen(tc.data, tc.mode); got != tc.len {
			t.Errorf("%s: disasmInstLen = %d, want %d", tc.name, got, tc.len)
		}
	}
	if text, n := disasmInstruction(nil, 64, 0); text != "" || n != 0 {
		t.Errorf("empty input decoded to (%q, %d)", text, n)
	}
}

func TestDetectX86Mode(t *testing.T) {
	pe := func(machine uint16) []byte {
		hdr := make([]byte, 0x80)
		copy(hdr, "MZ")
		hdr[0x3C] = 0x40 // e_lfanew
		copy(hdr[0x40:], "PE\x00\x00")
		hdr[0x44] = byte(machine)
		hdr[0x45] = byte(machine >> 8)
		return hdr
	}
	for _, tc := range []struct {
		name string
		data []byte
		want int
	}{
		{"ELF32", []byte("\x7fELF\x01\x01\x01\x00"), 32},
		{"ELF64", []byte("\x7fELF\x02\x01\x01\x00"), 64},
		{"PE i386", pe(0x014C), 32},
		{"PE amd64", pe(0x8664), 64},
		// A DOS executable has no PE header; nothing says 16 bits, so it
		// gets the default and the user switches from there.
		{"MZ only", append([]byte("MZ"), make([]byte, 0x40)...), 64},
		{"raw bytes", movRaxRcx, 64},
		{"nothing", nil, 64},
	} {
		if got := detectX86Mode(tc.data); got != tc.want {
			t.Errorf("%s: detectX86Mode = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestDisasmModeIsBoundToShiftF4InBothAreas(t *testing.T) {
	hm := NewHotkeyManager("")
	for _, area := range []string{"Editor", "Viewer"} {
		if got := hm.GetAction(area, "ShiftF4"); got != area+".DisasmMode" {
			t.Errorf("%s ShiftF4 -> %q, want %s.DisasmMode", area, got, area)
		}
	}
}

// TestEditorView_DisasmMode_CycleRedecodesUnderTheCursor: the action walks
// 64 -> 32 -> 16 -> 64, the status line follows, and a Down arrow steps by
// the instruction length of the mode in effect, not of the one the file
// opened in.
func TestEditorView_DisasmMode_CycleRedecodesUnderTheCursor(t *testing.T) {
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New(bytes.Repeat(movRaxRcx, 8))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	vtui.FrameManager.Push(ev)
	ev.DecodeMode = true
	ev.HexTopOffset = 0

	// Nothing in the buffer names a mode, so the editor decodes as 64.
	if got := ev.disasmMode(); got != 64 {
		t.Fatalf("initial mode = %d, want 64", got)
	}
	if st := ev.editorStatusText(); !strings.Contains(st, "Dec:64") {
		t.Fatalf("status %q does not show Dec:64", st)
	}
	down := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}
	ev.ProcessKey(down)
	if ev.CursorPos != 3 {
		t.Fatalf("Down in 64-bit mode moved the cursor to %d, want 3 (one mov rax, rcx)", ev.CursorPos)
	}

	if !RunAction("Editor.DisasmMode") {
		t.Fatal("Editor.DisasmMode did not run on the editor")
	}
	if ev.DisasmMode != 32 {
		t.Fatalf("after one switch mode = %d, want 32", ev.DisasmMode)
	}
	if st := ev.editorStatusText(); !strings.Contains(st, "Dec:32") {
		t.Fatalf("status %q does not show Dec:32", st)
	}
	ev.ProcessKey(down)
	if ev.CursorPos != 4 {
		t.Fatalf("Down in 32-bit mode moved the cursor to %d, want 4 (one dec eax)", ev.CursorPos)
	}

	RunAction("Editor.DisasmMode")
	RunAction("Editor.DisasmMode")
	if ev.DisasmMode != 64 {
		t.Fatalf("after three switches mode = %d, want 64 again", ev.DisasmMode)
	}
}

// TestEditorView_DecodeStepSeesTheLastBytes: GetRange refuses a window that
// runs past the end of the buffer, and the decode step used to ask for
// fifteen bytes regardless, so Down did nothing on the last instructions of
// a file.
func TestEditorView_DecodeStepSeesTheLastBytes(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := NewEditorView(piecetable.New(bytes.Repeat(movRaxRcx, 2)), nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.DecodeMode = true
	ev.DisasmMode = 64

	if got := ev.decodeStep(3); got != 3 {
		t.Fatalf("decodeStep(3) on a 6-byte buffer = %d, want 3", got)
	}
	if got := ev.decodeStep(6); got != 0 {
		t.Fatalf("decodeStep at the end of the buffer = %d, want 0", got)
	}
}

// TestViewerView_DisasmMode_PageDownWalksTheSelectedMode: PgDn used to
// decode as 64-bit whatever the view was set to, so after a switch the page
// step and the lines on screen disagreed about where instructions start.
func TestViewerView_DisasmMode_PageDownWalksTheSelectedMode(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	tmpDir := t.TempDir()
	tmp := filepath.Join(tmpDir, "code.bin")
	// The zero tail is what makes the file binary to the viewer; text goes
	// through a codepage and the decode view would see the decoded bytes.
	code := append(bytes.Repeat(movRaxRcx, 64), make([]byte, 16)...)
	if err := os.WriteFile(tmp, code, 0600); err != nil {
		t.Fatal(err)
	}

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(tmpDir), tmp)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(81, 6)
	vtui.FrameManager.Init(scr)
	vtui.FrameManager.Push(vv)
	vv.SetPosition(0, 0, 80, 5) // status line plus five rows of code
	vv.HexMode = false
	vv.DecodeMode = true

	// The header carried nothing that names a mode: 64 is decided at open,
	// not left to the first render.
	if vv.DisasmMode != 64 {
		t.Fatalf("mode after open = %d, want 64", vv.DisasmMode)
	}

	// The backend fetches in the background; wait for the first window.
	vv.Show(scr)
	deadline := time.After(2 * time.Second)
	for {
		if _, err := vv.backend.ReadAt(0, 1); err != piecetable.ErrLoading {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("timeout waiting for the viewer backend")
		}
	}

	pgdn := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT}
	vv.ProcessKey(pgdn)
	if vv.TopOffset != 5*3 {
		t.Fatalf("PgDn in 64-bit mode moved to %d, want 15 (five 3-byte instructions)", vv.TopOffset)
	}

	vv.TopOffset = 0
	if !RunAction("Viewer.DisasmMode") {
		t.Fatal("Viewer.DisasmMode did not run on the viewer")
	}
	if vv.DisasmMode != 32 {
		t.Fatalf("after one switch mode = %d, want 32", vv.DisasmMode)
	}
	if bar := vv.topBar.GetRight(); !strings.Contains(bar, "Dec:32") {
		t.Fatalf("top bar %q does not show Dec:32", bar)
	}
	vv.ProcessKey(pgdn)
	if vv.TopOffset != 1+2+1+2+1 {
		t.Fatalf("PgDn in 32-bit mode moved to %d, want 7 (dec, mov, dec, mov, dec)", vv.TopOffset)
	}
	vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if vv.TopOffset != 9 {
		t.Fatalf("Down in 32-bit mode moved to %d, want 9 (past a 2-byte mov eax, ecx)", vv.TopOffset)
	}
}

// TestViewerView_OpenDecidesDisasmModeFromTheHeader: an ELF32 opens as
// 32-bit code straight away, and the switch to the editor carries the mode
// over instead of leaving the editor to detect it again from a buffer that
// may not have its first bytes yet.
func TestViewerView_OpenDecidesDisasmModeFromTheHeader(t *testing.T) {
	tmpDir := t.TempDir()
	tmp := filepath.Join(tmpDir, "elf32.bin")
	data := append([]byte("\x7fELF\x01\x01\x01\x00"), make([]byte, 64)...)
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}
	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(tmpDir), tmp)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	if vv.DisasmMode != 32 {
		t.Fatalf("ELF32 opened as %d-bit, want 32", vv.DisasmMode)
	}
}
