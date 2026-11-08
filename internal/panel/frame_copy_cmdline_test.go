package panel

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// selectCommandLinePrefix puts text into the command line and selects its
// first n characters with Shift+Right, the way a user does it.
func selectCommandLinePrefix(pf *PanelsFrame, text string, n int) {
	pf.CmdLine.Edit.SetText(text)
	pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_HOME})
	for range n {
		pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.ShiftPressed})
	}
}

func TestPanelsFrame_CtrlCCopiesCommandLineSelection(t *testing.T) {
	oldConfig := config.App
	config.App.NavigationMode = config.NavigationClassic
	t.Cleanup(func() { config.App = oldConfig })
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)

	ctrlC := func() *vtinput.InputEvent {
		return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed}
	}

	selectCommandLinePrefix(pf, "hello world", 5)
	if !pf.CmdLine.HasSelection() {
		t.Fatal("Shift+Right left no selection in the command line")
	}
	vtui.SetClipboard("")
	if e := ctrlC(); !pf.VetoActionKey(e) {
		t.Fatal("VetoActionKey(Ctrl+C) = false with a command-line selection; a Shell hotkey would take it")
	}
	if !pf.ProcessKey(ctrlC()) {
		t.Fatal("ProcessKey(Ctrl+C) did not consume the copy")
	}
	if got := vtui.GetClipboard(); got != "hello" {
		t.Fatalf("clipboard after Ctrl+C = %q, want %q", got, "hello")
	}
	if got := pf.CmdLine.Edit.GetText(); got != "hello world" {
		t.Fatalf("Ctrl+C changed the command line to %q", got)
	}

	// Without a selection Ctrl+C keeps its configured meaning.
	pf.CmdLine.Edit.SetText("hello world")
	if pf.CmdLine.HasSelection() {
		t.Fatal("SetText left a selection behind")
	}
	if pf.VetoActionKey(ctrlC()) {
		t.Fatal("VetoActionKey(Ctrl+C) claimed a command line with nothing selected")
	}
}
