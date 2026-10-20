package macro

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func macroTestKey(vk uint16, mods vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: mods}
}

func macroTestEvents() []*vtinput.InputEvent {
	return []*vtinput.InputEvent{{Type: vtinput.KeyEventType, KeyDown: true, Char: 'a'}}
}

// f4#1870 (DkmS1953): the list of free keys leaves out what a macro or a
// command already uses, in the area and in Common, which reaches every area.
func TestFreeKeysLeaveOutKeysInUse(t *testing.T) {
	mgr := &MacroManager{Macros: map[string]map[string][]*vtinput.InputEvent{
		"Shell":  {"CtrlF5": macroTestEvents()},
		"Common": {"F7": macroTestEvents()},
		"Editor": {"F8": macroTestEvents()},
	}}
	hm := &keymap.HotkeyManager{
		Defaults: map[string]map[string]string{
			"Shell": {"ShiftF2": "Some.Command", "F9": "Other.Command"},
		},
		Bindings: map[string]map[string]string{
			"Shell":  {"altf3": "Some.Command", "F9": "None"},
			"Common": {"CtrlB": "Some.Command"},
			"Viewer": {"F4": "Some.Command"},
		},
	}

	free := mgr.FreeKeys("Shell", hm)
	for _, taken := range []string{"CtrlF5", "F7", "ShiftF2", "AltF3", "CtrlB"} {
		if slices.Contains(free, taken) {
			t.Errorf("%s is in use in Shell but is offered as free", taken)
		}
	}
	// What another area uses, and what the user unbound, is free here.
	for _, ok := range []string{"F1", "F4", "F8", "F9", "CtrlF4", "AltA", "Ctrl0", "CtrlZ"} {
		if !slices.Contains(free, ok) {
			t.Errorf("%s is free in Shell but is not offered", ok)
		}
	}
	if free[0] != "F1" {
		t.Errorf("the list starts with %q, want F1", free[0])
	}

	// In Common itself the area's own macros are Common's.
	if slices.Contains(mgr.FreeKeys("Common", hm), "F7") {
		t.Error("F7 is a Common macro but is offered as free in Common")
	}
	if got := mgr.FreeKeys("Shell", nil); !slices.Contains(got, "ShiftF2") || slices.Contains(got, "F7") {
		t.Error("without a hotkey manager only the macros count")
	}
}

func TestFreeKeysLeaveOutWorkspaceShortcuts(t *testing.T) {
	mgr := &MacroManager{}
	free := mgr.FreeKeys("Shell", nil)
	for _, key := range []string{"Alt1", "Alt4", "Alt9"} {
		if slices.Contains(free, key) {
			t.Errorf("workspace shortcut %q offered as a free macro key", key)
		}
	}
	if !slices.Contains(free, "Alt0") {
		t.Error("Alt0 should remain available; it is not a workspace shortcut")
	}
}

// A Lua macro takes its key like a recorded one.
func TestFreeKeysLeaveOutLuaMacros(t *testing.T) {
	engine := newTestMacroEngine(t, newFakeMacroHost(), `
		Macro { area = "Shell"; key = "CtrlF9"; action = function() end }
		Macro { area = "Common"; key = "AltQ"; action = function() end }
	`)
	mgr := &MacroManager{Lua: engine}
	free := mgr.FreeKeys("Shell", nil)
	for _, taken := range []string{"CtrlF9", "AltQ"} {
		if slices.Contains(free, taken) {
			t.Errorf("%s is a Lua macro of Shell but is offered as free", taken)
		}
	}
	if !slices.Contains(free, "CtrlF8") {
		t.Error("CtrlF8 is free but is not offered")
	}
}

func macroTestHotkeys(t *testing.T, hm *keymap.HotkeyManager) {
	t.Helper()
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = hm
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })
}

func macroTestScreen() *vtui.ScreenBuf {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	return scr
}

// screenText is the text part of vtui's screen dump (the format Ctrl+Shift+P
// writes), without the blank rows around what is drawn.
func screenText(scr *vtui.ScreenBuf) []string {
	var buf bytes.Buffer
	scr.Dump(&buf)
	lines := strings.Split(buf.String(), "\n")
	var rows []string
	for _, line := range lines[2 : 2+25] {
		if row := strings.TrimRight(line, " "); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// f4#1870 (DkmS1953): the assign dialog drops down the free keys, and picking
// one makes it the macro's key; the key field still takes any key pressed.
func TestAssignDialogPicksAFreeKey(t *testing.T) {
	macroTestScreen()
	macroTestHotkeys(t, &keymap.HotkeyManager{
		Defaults: map[string]map[string]string{"Shell": {"F1": "Help"}},
	})
	path := t.TempDir() + "/macros.ini"
	mgr := NewMacroManager(path)
	mgr.StartArea = "Shell"
	mgr.Macros["Shell"] = map[string][]*vtinput.InputEvent{"F2": macroTestEvents()}
	mgr.Buffer = macroTestEvents()
	f := NewMacroAssignFrame(mgr)
	vtui.FrameManager.Push(f)

	listed := f.keyField.Menu.Items
	if len(listed) == 0 || listed[0].Text != "F3" {
		t.Fatalf("the dialog lists %d keys starting with %v; want F1 (a command) and F2 (a macro) left out", len(listed), listed)
	}
	for _, item := range listed {
		if item.Text == "F1" || item.Text == "F2" {
			t.Errorf("%s is in use but is listed", item.Text)
		}
	}

	if !f.ProcessKey(macroTestKey(vtinput.VK_DOWN, vtinput.LeftCtrlPressed)) {
		t.Fatal("Ctrl+Down was not taken")
	}
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("Ctrl+Down left %T on top, want the list of keys", vtui.FrameManager.GetTopFrame())
	}
	if f.Key() != "" {
		t.Errorf("opening the list chose %q as the key", f.Key())
	}
	menu.ProcessKey(macroTestKey(vtinput.VK_DOWN, 0))
	menu.ProcessKey(macroTestKey(vtinput.VK_RETURN, 0))
	if f.Key() != "F4" || f.IsDone() {
		t.Fatalf("picking the second listed key gave %q (done %v), want F4 and the dialog open", f.Key(), f.IsDone())
	}
	if got := f.keyField.Edit.GetText(); got != "F4" {
		t.Errorf("the key field shows %q, want F4", got)
	}

	// The focus moved to OK: Enter assigns the macro to the picked key.
	f.ProcessKey(macroTestKey(vtinput.VK_RETURN, 0))
	if !f.IsDone() {
		t.Fatal("Enter after picking a key did not confirm")
	}
	if len(mgr.Macros["Shell"]["F4"]) != 1 {
		t.Fatal("the macro was not assigned to the picked key")
	}
}

// Only Ctrl+Down opens the list: with another modifier the key is a macro's
// key like any other, so the dialog keeps every key that can be pressed.
func TestAssignDialogCtrlDownOnlyOpensTheList(t *testing.T) {
	macroTestScreen()
	macroTestHotkeys(t, nil)
	mgr := &MacroManager{Buffer: macroTestEvents()}
	f := NewMacroAssignFrame(mgr)
	vtui.FrameManager.Push(f)

	f.ProcessKey(macroTestKey(vtinput.VK_DOWN, vtinput.LeftCtrlPressed|vtinput.ShiftPressed))
	if f.Key() != "CtrlShiftDown" {
		t.Errorf("Ctrl+Shift+Down gave key %q, want CtrlShiftDown", f.Key())
	}
	if _, open := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); open {
		t.Error("Ctrl+Shift+Down opened the list")
	}

	// A bare modifier is neither a key nor a way to the list.
	g := NewMacroAssignFrame(mgr)
	g.ProcessKey(macroTestKey(vtinput.VK_CONTROL, vtinput.LeftCtrlPressed))
	if g.Key() != "" {
		t.Errorf("pressing Ctrl chose %q", g.Key())
	}
}

// The assign dialog of a macro just recorded (f4#1870) shows an arrow after
// the key field, and Ctrl+Down opens the list of free keys under the field,
// over the buttons. Both dumps are logged by this test and were checked there.
//
// Screen Dump (80x25, the dialog and what it covers):
//
//	╔══════════════════════════ Assign Macro ══════════════════════════╗
//	║                                                                  ║
//	║ Macro description                                                ║
//	║                                                                  ║
//	║ Press the desired key combination                              ↓ ║
//	║──────────────────────────────────────────────────────────────────║
//	║                        [ Ok ]  [ Cancel ]                        ║
//	║                                                                  ║
//	╚══════════════════════════════════════════════════════════════════╝
//
// Screen Dump with the list open:
//
//	╔══════════════════════════ Assign Macro ══════════════════════════╗
//	║                                                                  ║
//	║ Macro description                                                ║
//	║                                                                  ║
//	║ Press the desired key combination                              ↓ ║
//	║─────────────────────────────────────┌─────── Free keys ────────┐─║
//	║                        [ Ok ]  [ Can│ F1                       ▲ ║
//	║                                     │ F2                       ▓ ║
//	╚═════════════════════════════════════│ F3                       ░═╝
//	                                      │ F4                       ░
//	                                      │ F5                       ░
//	                                      │ F6                       ░
//	                                      │ F7                       ░
//	                                      │ F8                       ▼
//	                                      └──────────────────────────┘
func TestAssignDialogShowsTheListAffordance(t *testing.T) {
	scr := macroTestScreen()
	macroTestHotkeys(t, nil)
	mgr := &MacroManager{Buffer: macroTestEvents(), StartArea: "Shell"}
	f := NewMacroAssignFrame(mgr)
	for _, e := range vtui.ValidateLayout(f.Window) {
		t.Errorf("layout: %v", e)
	}
	vtui.FrameManager.Push(f)
	f.Show(scr)
	rows := screenText(scr)
	t.Logf("assign dialog:\n%s", strings.Join(rows, "\n"))

	var keyRow string
	for _, row := range rows {
		if strings.Contains(row, i18n.Msg("Macro.AssignPrompt")) {
			keyRow = row
		}
	}
	if !strings.Contains(keyRow, "↓") {
		t.Errorf("no arrow of the list in the key row: %q", keyRow)
	}

	f.ProcessKey(macroTestKey(vtinput.VK_DOWN, vtinput.RightCtrlPressed))
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatal("the list did not open")
	}
	f.Show(scr)
	menu.Show(scr)
	rows = screenText(scr)
	t.Logf("assign dialog with the list open:\n%s", strings.Join(rows, "\n"))
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, i18n.Msg("Macro.AssignFreeKeys")) {
		t.Error("the open list has no title")
	}
	if !strings.Contains(joined, " F1 ") {
		t.Error("the open list does not show its first key")
	}
}
