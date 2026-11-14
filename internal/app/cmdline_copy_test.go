package app

import (
	"testing"

	"github.com/unxed/f4/internal/config"
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

// selectCommandLineTail selects the last n characters of text in the command
// line with Shift+Left.
func selectCommandLineTail(pf *panel.PanelsFrame, text string, n int) {
	pf.CmdLine.Edit.SetText(text)
	pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	for range n {
		pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.ShiftPressed})
	}
}

func cmdlineCopyKey() *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_C, ControlKeyState: vtinput.LeftCtrlPressed}
}

// A recorded macro on Ctrl+C must not play over a command-line selection,
// as one on Backspace must not over a command line that holds text (f4#1797).
func TestMacroFilterLetsCommandLineCopyBeforeRecordedMacro(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	oldConfig := config.App
	oldHotkeys, oldMacros, oldRemap := keymap.GlobalHotkeysMgr, macro.MacroMgr, keymap.GlobalKeyRemap
	t.Cleanup(func() {
		config.App = oldConfig
		keymap.GlobalHotkeysMgr, macro.MacroMgr, keymap.GlobalKeyRemap = oldHotkeys, oldMacros, oldRemap
	})
	config.App.NavigationMode = config.NavigationClassic
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	keymap.GlobalKeyRemap = nil
	mgr := macro.NewMacroManager("")
	mgr.Macros["Shell"] = map[string][]*vtinput.InputEvent{
		keymap.EventToFarString(cmdlineCopyKey()): {{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR, ControlKeyState: vtinput.LeftCtrlPressed}},
	}

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	selectCommandLineTail(pf, "echo selected", len("selected"))
	vtui.SetClipboard("")
	if macroFilter(mgr, cmdlineCopyKey()) {
		t.Fatal("the recorded Ctrl+C macro played over a command-line selection")
	}
	if !pf.ProcessKey(cmdlineCopyKey()) || vtui.GetClipboard() != "selected" {
		t.Fatalf("clipboard after Ctrl+C = %q, want %q", vtui.GetClipboard(), "selected")
	}

	pf.CmdLine.ClearSelection()
	if !macroFilter(mgr, cmdlineCopyKey()) {
		t.Fatal("the recorded Ctrl+C macro did not play with nothing selected")
	}
}

// A Shell action that takes a key leaves the command-line selection behind:
// the key went past the line, and Ctrl+C belongs to the panel again.
func TestShellActionDropsCommandLineSelection(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	previousHotkeys, previousMacro := keymap.GlobalHotkeysMgr, macro.MacroMgr
	keymap.GlobalHotkeysMgr = &keymap.HotkeyManager{
		Defaults: map[string]map[string]string{"Shell": {"F12": "Panel.SplitReset"}},
		Bindings: map[string]map[string]string{"Shell": {"F12": "Panel.SplitReset"}},
	}
	manager := &macro.MacroManager{}
	macro.MacroMgr = manager
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = previousHotkeys
		macro.MacroMgr = previousMacro
	})

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	selectCommandLineTail(pf, "echo selected", len("selected"))
	f12 := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F12}
	if !macroFilter(manager, f12) {
		t.Fatal("test premise: the Shell action did not take F12")
	}
	if pf.CmdLine.HasSelection() {
		t.Fatal("the command-line selection survived a key that went to a Shell action")
	}
	if pf.VetoActionKey(cmdlineCopyKey()) {
		t.Fatal("Ctrl+C still belongs to the command line after the user left it")
	}
}
