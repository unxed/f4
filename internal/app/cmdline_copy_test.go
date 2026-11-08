package app

import (
	"testing"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Ctrl+C (Cmd+C in the macOS window, which arrives on the same left Ctrl
// channel) on a selection in the command line copies it. The Shell area binds
// Ctrl+C to panel actions, and the hotkey dispatcher used to consume the key
// for them before the command line saw it.
func TestCtrlCCopiesCommandLineSelectionThroughHotkeys(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	previousHotkeys, previousMacro := keymap.GlobalHotkeysMgr, macro.MacroMgr
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()
	keymap.GlobalHotkeysMgr = hm
	manager := &macro.MacroManager{}
	macro.MacroMgr = manager
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = previousHotkeys
		macro.MacroMgr = previousMacro
	})
	if hm.GetAction("Shell", "CtrlC") == "" {
		t.Fatal("test premise: Ctrl+C is no longer bound in the Shell area")
	}

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	vtui.FrameManager.Push(pf)

	pf.CmdLine.Edit.SetText("echo selected")
	pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	for range len("selected") {
		pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.ShiftPressed})
	}
	vtui.SetClipboard("")

	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed}
	if macroFilter(manager, e) {
		t.Fatal("the hotkey dispatcher consumed Ctrl+C over a command-line selection")
	}
	if !pf.ProcessKey(e) {
		t.Fatal("the panels frame did not take Ctrl+C for the command line")
	}
	if got := vtui.GetClipboard(); got != "selected" {
		t.Fatalf("clipboard after Ctrl+C = %q, want %q", got, "selected")
	}
}
