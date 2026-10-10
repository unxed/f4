package app

import (
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestAltGrTextBypassesBindingsAndReachesInputs(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldHotkeys, oldRemap := keymap.GlobalHotkeysMgr, keymap.GlobalKeyRemap
	t.Cleanup(func() { keymap.GlobalHotkeysMgr, keymap.GlobalKeyRemap = oldHotkeys, oldRemap })
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr.Bind("Common", "@", "None")
	m := macro.NewMacroManager("")
	m.Macros["Common"] = map[string][]*vtinput.InputEvent{"@": {{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F10}}}
	// A confirmed, translated text event must not become a bare-character
	// binding after its physical modifiers have been removed.
	e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '@', InputSource: "f4-altgr"}
	if macroFilter(m, &e) || macroLookupHotkey(m, &e) {
		t.Fatal("AltGr text was consumed by a binding")
	}
	edit := vtui.NewEdit(0, 0, 20, "")
	edit.SetFocus(true)
	edit.ProcessKey(&e)
	if edit.GetText() != "@" {
		t.Fatalf("dialog text = %q", edit.GetText())
	}
	cl := cmdline.NewCommandLine("")
	cl.ProcessKey(&e)
	if cl.Edit.GetText() != "@" {
		t.Fatalf("command text = %q", cl.Edit.GetText())
	}
	ev := editor.NewEditorView(piecetable.New(nil), nil, "altgr.txt")
	defer ev.Close()
	ev.ProcessKey(&e)
	if ev.Pt.String() != "@" {
		t.Fatalf("editor text = %q", ev.Pt.String())
	}
}
