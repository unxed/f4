package app

import (
	"testing"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func commandPaletteCtrlK() *vtinput.InputEvent {
	e := commandPaletteKey(vtinput.VK_K)
	e.ControlKeyState = vtinput.LeftCtrlPressed
	return e
}

func TestCommandPaletteCtrlKOpensKeyDialogForAnAction(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })

	entries := []commandPaletteEntry{
		{Key: "act:file.attributes", Label: "Attributes", ID: "File.Attributes", source: commandPaletteSourceAction},
	}
	dialog, _ := newCommandPaletteUITestDialog(t, 100, 30, entries, nil)
	vtui.FrameManager.Push(dialog)

	if !dialog.ProcessKey(commandPaletteCtrlK()) {
		t.Fatal("Ctrl+K was not consumed")
	}
	if vtui.FrameManager.GetTopFrame() == vtui.Frame(dialog) {
		t.Fatal("Ctrl+K did not open the key assignment dialog over the palette")
	}
	if dialog.query.GetText() != "" {
		t.Fatalf("Ctrl+K typed into the query: %q", dialog.query.GetText())
	}
}

func TestCommandPaletteCtrlKOnPlainEntryOnlyExplains(t *testing.T) {
	entries := []commandPaletteEntry{
		{Key: "workspace:1", Label: "Workspace 1", run: func() bool { return true }},
	}
	dialog, _ := newCommandPaletteUITestDialog(t, 100, 30, entries, nil)
	vtui.FrameManager.Push(dialog)

	if !dialog.ProcessKey(commandPaletteCtrlK()) {
		t.Fatal("Ctrl+K was not consumed")
	}
	if _, ok := commandPaletteAssignTarget(entries[0]); ok {
		t.Fatal("an entry without an action offers a key")
	}
}

func TestCommandPaletteAssignChordIsOnlyPlainCtrlK(t *testing.T) {
	for name, state := range map[string]uint32{
		"plain K":    0,
		"Ctrl+Shift": uint32(vtinput.LeftCtrlPressed | vtinput.ShiftPressed),
		"Ctrl+Alt":   uint32(vtinput.LeftCtrlPressed | vtinput.LeftAltPressed),
	} {
		e := commandPaletteKey(vtinput.VK_K)
		e.ControlKeyState = vtinput.ControlKeyState(state)
		if commandPaletteAssignKey(e) {
			t.Errorf("%s was taken for the assign chord", name)
		}
	}
	if !commandPaletteAssignKey(commandPaletteCtrlK()) {
		t.Fatal("Ctrl+K is not the assign chord")
	}
}

func TestCommandPaletteReloadAfterAssignKeepsCursor(t *testing.T) {
	entries := []commandPaletteEntry{
		{Key: "a", Label: "Alpha"},
		{Key: "b", Label: "Bravo"},
	}
	dialog, _ := newCommandPaletteUITestDialog(t, 100, 30, entries, nil)
	dialog.rebuild = func() []commandPaletteEntry {
		return []commandPaletteEntry{
			{Key: "a", Label: "Alpha"},
			{Key: "b", Label: "Bravo", Shortcut: "Ctrl+Q"},
		}
	}
	dialog.table.SetSelectPos(1)
	dialog.reloadAfterAssign("b")
	if dialog.table.SelectPos != 1 || dialog.filtered[1].Shortcut != "Ctrl+Q" {
		t.Fatalf("cursor %d, shortcut %q", dialog.table.SelectPos, dialog.filtered[1].Shortcut)
	}
}

// #1836, f4#1842: Ctrl+Shift+K takes a key off a command, a default key too
// (the right Ctrl+A of the AI panel, wanted for Attributes).
func TestCommandPaletteCtrlShiftKRemovesAKey(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	hm := keymap.NewHotkeyManager("")
	hm.Bind("Common", "RCtrlA", "AI.TogglePanel")
	keymap.GlobalHotkeysMgr = hm
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })

	entries := []commandPaletteEntry{
		{Key: "act:ai.togglepanel", Label: "AI panel", ID: "AI.TogglePanel", source: commandPaletteSourceAction},
	}
	dialog, _ := newCommandPaletteUITestDialog(t, 100, 30, entries, nil)
	vtui.FrameManager.Push(dialog)

	e := commandPaletteKey(vtinput.VK_K)
	e.ControlKeyState = vtinput.RightCtrlPressed | vtinput.ShiftPressed
	if !dialog.ProcessKey(e) {
		t.Fatal("Ctrl+Shift+K was not consumed")
	}
	question, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok || vtui.FrameManager.GetTopFrame() == vtui.Frame(dialog) {
		t.Fatal("Ctrl+Shift+K asked nothing")
	}
	testutil.ClickDialogButton(t, question, keymap.FormatKeyForUI("RCtrlA"))
	if got, ok := hm.GetActiveBindings()["Common"]["RCtrlA"]; ok {
		t.Fatalf("RCtrlA is still bound to %q", got)
	}
	if hm.Bindings["Common"]["RCtrlA"] != "None" {
		t.Fatal("the removal is not kept as an override of the default")
	}
	if dialog.query.GetText() != "" {
		t.Fatalf("the chord typed into the query: %q", dialog.query.GetText())
	}
}
