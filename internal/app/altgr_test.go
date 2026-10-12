package app

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
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
	for _, key := range []string{"@", "CtrlAlt@", "CtrlAltQ"} {
		keymap.GlobalHotkeysMgr.Bind("Common", key, "None")
	}
	m := macro.NewMacroManager("")
	m.Macros["Common"] = map[string][]*vtinput.InputEvent{"@": {{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F10}}}
	keymap.GlobalKeyRemap = &keymap.KeyRemap{Exact: map[string]map[string]string{
		"Common": {"ctrlaltq": "F10", "@": "F10"},
	}}
	// The producer preserves physical data. Consumers must honor text intent
	// without losing the original event needed by a child terminal.
	e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '@', InputSource: "win32",
		TextInput: true, VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed, RepeatCount: 1}
	original := e
	if macroFilter(m, &e) || macroLookupHotkey(m, &e) {
		t.Fatal("AltGr text was consumed by a binding")
	}
	m.Recording = true
	if macroFilter(m, &e) || len(m.Buffer) != 1 || !reflect.DeepEqual(*m.Buffer[0], original) {
		t.Fatal("recording did not retain the confirmed event")
	}
	m.Recording = false
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
	if !reflect.DeepEqual(e, original) {
		t.Fatal("routing mutated source metadata")
	}
}

func TestConfirmedTextPanelRouting(t *testing.T) {
	oldConfig := config.App
	t.Cleanup(func() { config.App = oldConfig })
	config.App.CommandLineAutoComplete = false
	for _, mode := range []config.PanelNavigationMode{config.NavigationClassic, config.NavigationVim, config.NavigationSearchFirst} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			config.App.NavigationMode = mode
			pf, _, _ := newSearchFirstTestFrame(t)
			if pf.SearchFirstMode() {
				pf.SetCommandLineFocus(true)
			}
			// C must not run the Ctrl+Alt+C crash shortcut; a composed grave
			// must not toggle search focus; h must not trigger Vim navigation.
			for _, ch := range "@€Ą~`h" {
				e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
					Char: ch, VirtualKeyCode: vtinput.VK_C,
					ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
				original := e
				if !pf.ProcessKey(&e) {
					t.Fatalf("did not consume %q", ch)
				}
				if !reflect.DeepEqual(e, original) {
					t.Fatal("panel mutated input")
				}
			}
			if got := pf.CmdLine.Edit.GetText(); got != "@€Ą~`h" {
				t.Fatalf("command text = %q", got)
			}
		})
	}
}
