package app

import (
	"bytes"
	"context"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var movRaxRcx = []byte{0x48, 0x89, 0xC8}

// The disassembler itself is tested in internal/viewer; these three drive the
// editor and the hotkey manager, which stayed here.

func TestDisasmModeIsBoundToShiftF4InBothAreas(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
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
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	Pt := piecetable.New(bytes.Repeat(movRaxRcx, 8))
	ev := editor.NewEditorView(Pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	vtui.FrameManager.Push(ev)
	ev.DecodeMode = true
	ev.HexTopOffset = 0

	// Nothing in the buffer names a mode, so the editor decodes as 64.
	if got := ev.EffectiveDisasmMode(); got != 64 {
		t.Fatalf("initial mode = %d, want 64", got)
	}
	if st := ev.EditorStatusText(); !strings.Contains(st, "Dec:64") {
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
	if st := ev.EditorStatusText(); !strings.Contains(st, "Dec:32") {
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

// TestEditorView_DisasmMode_DoesNothingOutsideTheDecodeView: Shift+F4 in the
// text view used to switch the hidden mode and announce "Disassembler: 32-bit"
// although no disassembler was on screen (f4#1704).
func TestEditorView_DisasmMode_DoesNothingOutsideTheDecodeView(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := editor.NewEditorView(piecetable.New([]byte("plain text\n")), nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	vtui.FrameManager.Push(ev)

	before := ev.DisasmMode
	if !RunAction("Editor.DisasmMode") {
		t.Fatal("Editor.DisasmMode did not run on the editor")
	}
	if ev.DisasmMode != before {
		t.Fatalf("the mode changed from %d to %d in the text view", before, ev.DisasmMode)
	}
}

// TestEditorView_DecodeStepSeesTheLastBytes: GetRange refuses a window that
// runs past the end of the buffer, and the decode step used to ask for
// fifteen bytes regardless, so Down did nothing on the last instructions of
// a file.
func TestEditorView_DecodeStepSeesTheLastBytes(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := editor.NewEditorView(piecetable.New(bytes.Repeat(movRaxRcx, 2)), nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.DecodeMode = true
	ev.DisasmMode = 64

	if got := ev.DecodeStep(3); got != 3 {
		t.Fatalf("decodeStep(3) on a 6-byte buffer = %d, want 3", got)
	}
	if got := ev.DecodeStep(6); got != 0 {
		t.Fatalf("decodeStep at the end of the buffer = %d, want 0", got)
	}
}

// TestViewerView_DisasmMode_PageDownWalksTheSelectedMode: PgDn used to
// decode as 64-bit whatever the view was set to, so after a switch the page
// step and the lines on screen disagreed about where instructions start.
func TestViewerView_DisasmMode_PageDownWalksTheSelectedMode(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	tmpDir := t.TempDir()
	tmp := filepath.Join(tmpDir, "code.bin")
	// The zero tail is what makes the file binary to the viewer; text goes
	// through a codepage and the decode view would see the decoded bytes.
	code := append(bytes.Repeat(movRaxRcx, 64), make([]byte, 16)...)
	if err := os.WriteFile(tmp, code, 0600); err != nil {
		t.Fatal(err)
	}

	vv, err := viewer.NewViewerView(context.Background(), vfs.NewOSVFS(tmpDir), tmp)
	if err != nil {
		t.Fatalf("viewer.NewViewerView: %v", err)
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
		if _, err := vv.Backend.ReadAt(0, 1); err != piecetable.ErrLoading {
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
	if bar := vv.TopBar.GetRight(); !strings.Contains(bar, "Dec:32") {
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
