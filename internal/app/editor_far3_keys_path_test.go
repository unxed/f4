package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The FAR3 editor keys of f4#1729 have to work when they are pressed, not only
// resolve in the hotkey table: the key goes through the same filter as a real
// key press (macroFilter), then to the editor.
func TestEditorFAR3KeysWorkThroughTheKeyPath(t *testing.T) {
	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacro := macro.MacroMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr.InitDefaults()
	macro.MacroMgr = &macro.MacroManager{}
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacro
	})

	press := func(ev *editor.EditorView, vk uint16, char rune) {
		e := &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, Char: char,
			ControlKeyState: vtinput.LeftCtrlPressed,
		}
		if !macroFilter(macro.MacroMgr, e) {
			ev.ProcessKey(e)
		}
	}
	open := func(text string) *editor.EditorView {
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
		ev := editor.NewEditorView(piecetable.New([]byte(text)), nil, "")
		t.Cleanup(func() { ev.Close() })
		ev.SetPosition(0, 0, 80, 24)
		vtui.FrameManager.Push(ev)
		return ev
	}
	text := func(ev *editor.EditorView) string {
		data, _ := ev.Pt.Bytes()
		return string(data)
	}

	// Ctrl+K deletes to the end of the line.
	ev := open("hello world\nnext\n")
	ev.CursorPos = 5
	press(ev, vtinput.VK_K, 0x0b)
	if got := text(ev); got != "hello\nnext\n" {
		t.Errorf("Ctrl+K left %q, want %q", got, "hello\nnext\n")
	}

	// Ctrl+Backspace deletes the word before the cursor.
	ev = open("hello world\n")
	ev.CursorPos = 11
	press(ev, vtinput.VK_BACK, 0x7f)
	// The binding is spelled CtrlBS, the name the key table gives Backspace;
	// "CtrlBack" never matched a key press. How much space goes with the
	// word is DeleteWordBackward's business, not this test's.
	if got := text(ev); !strings.HasPrefix(got, "hello") || strings.Contains(got, "world") {
		t.Errorf("Ctrl+Backspace left %q, want the word before the cursor gone", got)
	}
}
