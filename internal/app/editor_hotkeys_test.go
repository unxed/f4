package app

// Two editor tests whose subject is the hotkey manager: one asks that a key-bar
// click honours a user binding, the other that the key bar shows the bound
// captions. internal/editor reaches the manager only through its
// editor.HotkeyAction and editor.KeyBarLabels seams, which the root fills in
// here, so the wiring is what these exercise.

import (
	"github.com/unxed/f4/internal/keymap"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// TestEditorView_HexMode_KeyBarClickDispatchesConfiguredAction guards the
// injected-event path used by vtui.KeyBar.ProcessMouse. Real key presses pass
// through macro.MacroMgr.Filter, but a key-bar click calls editor.EditorView.ProcessKey
// directly; Hex mode must still honor a user binding such as F9 -> Hex Mode.
func TestEditorView_HexMode_KeyBarClickDispatchesConfiguredAction(t *testing.T) {
	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacro := macro.MacroMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	macro.MacroMgr = &macro.MacroManager{}
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacro
	})

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := editor.NewEditorView(piecetable.New([]byte("abc")), nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	vtui.FrameManager.Push(ev)

	if !RunAction("Editor.HexMode") {
		t.Fatal("failed to enter Hex mode")
	}
	keymap.GlobalHotkeysMgr.Bind("Editor", "F9", "Editor.HexMode")
	// "Hex", not "Hex Mode": f4#1218 added the "Action.Editor.HexMode" i18n
	// key (mirroring Viewer.HexMode's own "&Hex"), so DisplayLabel() now
	// resolves through it instead of silently falling back to the English
	// Action.Label field this assertion used to pin down.
	if labels := ev.GetKeyLabels(); labels.Normal[8] != "Hex" {
		t.Fatalf("F9 key-bar label = %q, want Hex", labels.Normal[8])
	}

	// This is the exact key event synthesized for an F9 key-bar click.
	if !ev.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	}) {
		t.Fatal("injected F9 key-bar event was not handled")
	}
	if ev.HexMode || !ev.DecodeMode {
		t.Fatalf("injected F9 did not run Editor.HexMode: hex=%v decode=%v", ev.HexMode, ev.DecodeMode)
	}
}

func TestEditorView_Labels(t *testing.T) {
	oldHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHotkeys })

	Pt := piecetable.New([]byte(""))
	ev := editor.NewEditorView(Pt, nil, "test.txt")
	defer ev.Close()
	ks := ev.GetKeyLabels()

	if ks == nil {
		t.Fatal("EditorView.GetKeyLabels() returned nil")
	}

	if ks.Normal[1] != "Save" { // F2
		t.Errorf("Expected F2 to be 'Save', got %q", ks.Normal[1])
	}
	if ks.Normal[9] != "Exit" { // F10
		t.Errorf("Expected F10 to be 'Exit', got %q", ks.Normal[9])
	}
}
