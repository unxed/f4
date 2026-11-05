package cmdline

import (
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func ctrlKey(vk uint16) *vtinput.InputEvent {
	return &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
}

func plainKey(vk uint16) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk}
}

func farKeysLine(text string) *CommandLine {
	vtui.SetDefaultPalette()
	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 40, 0)
	cl.Edit.SetText(text)
	cl.Edit.SetCursorPos(len([]rune(text)))
	return cl
}

// The Far command-line keys of K-72 (docs/FAR2L_COMPAT.md; unxed/f4#2139):
// Ctrl+Backspace and Ctrl+Delete delete a word, Ctrl+K deletes to the end.
func TestCommandLine_CtrlBackspaceDeletesWordLeft(t *testing.T) {
	cl := farKeysLine("abc def")
	if !cl.ProcessKey(ctrlKey(vtinput.VK_BACK)) || cl.Edit.GetText() != "abc " {
		t.Fatalf("Ctrl+Backspace: text %q, want %q", cl.Edit.GetText(), "abc ")
	}
}

func TestCommandLine_CtrlDeleteDeletesWordRight(t *testing.T) {
	cl := farKeysLine("abc def")
	cl.Edit.SetCursorPos(4)
	if !cl.ProcessKey(ctrlKey(vtinput.VK_DELETE)) || cl.Edit.GetText() != "abc " {
		t.Fatalf("Ctrl+Delete: text %q, want %q", cl.Edit.GetText(), "abc ")
	}
}

func TestCommandLine_CtrlKDeletesToTheEnd(t *testing.T) {
	cl := farKeysLine("abc def")
	cl.ProcessKey(plainKey(vtinput.VK_HOME))
	if cl.Edit.CursorPos() != 0 {
		t.Fatalf("Home left the cursor at %d", cl.Edit.CursorPos())
	}
	if !cl.ProcessKey(ctrlKey(vtinput.VK_K)) || cl.Edit.GetText() != "" {
		t.Fatalf("Ctrl+K after Home: text %q, want the line emptied", cl.Edit.GetText())
	}
	cl = farKeysLine("abc def")
	cl.Edit.SetCursorPos(3)
	cl.ProcessKey(ctrlKey(vtinput.VK_K))
	if cl.Edit.GetText() != "abc" {
		t.Fatalf("Ctrl+K in the middle: text %q, want %q", cl.Edit.GetText(), "abc")
	}
}

func TestCommandLine_FarKeysLeaveTheHistoryMode(t *testing.T) {
	cl := farKeysLine("abc def")
	cl.Edit.HistoryPos = 2
	cl.ProcessKey(ctrlKey(vtinput.VK_BACK))
	if cl.Edit.HistoryPos != -1 {
		t.Fatalf("Ctrl+Backspace left the history position at %d", cl.Edit.HistoryPos)
	}
}
