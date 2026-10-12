package app

import (
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestMacro_Far2lCompatibility(t *testing.T) {
	iniContent := `
[KeyMacros/Shell/CtrlW]
DisableOutput=0x1
Sequence=Up Up CtrlEnter Esc F5 Down ShiftF5 Esc Esc
`
	tmpFile := filepath.Join(t.TempDir(), "far2l_macros.ini")
	if err := os.WriteFile(tmpFile, []byte(iniContent), 0600); err != nil {
		t.Fatal(err)
	}

	mgr := macro.NewMacroManager(tmpFile)

	shellMacros, ok := mgr.Macros["Shell"]
	if !ok {
		t.Fatal("Shell macros not loaded")
	}

	seq, ok := shellMacros["CtrlW"]
	if !ok {
		t.Fatal("CtrlW macro not found in Shell")
	}

	if len(seq) != 9 {
		t.Fatalf("Expected 9 keys, got %d", len(seq))
	}

	if seq[0].VirtualKeyCode != vtinput.VK_UP {
		t.Errorf("Expected first key VK_UP, got %d", seq[0].VirtualKeyCode)
	}
	if seq[2].VirtualKeyCode != vtinput.VK_RETURN || !keymap.NormalizeMods(seq[2].ControlKeyState).Contains(vtinput.LeftCtrlPressed) {
		t.Errorf("Expected third key CtrlEnter, got %d", seq[2].VirtualKeyCode)
	}

	mgr.Save()

	savedData, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	savedStr := string(savedData)
	if !strings.Contains(savedStr, "[KeyMacros/Shell/CtrlW]") {
		t.Errorf("Saved config lost section name: %s", savedStr)
	}
	if !strings.Contains(savedStr, "Sequence=Up Up CtrlEnter Esc F5 Down ShiftF5 Esc Esc") {
		t.Errorf("Saved config lost sequence: %s", savedStr)
	}
}

func TestEnterAndNumEnterUseCorrectFarKeyNames(t *testing.T) {
	mainEnter := &vtinput.InputEvent{
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	numEnter := &vtinput.InputEvent{
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.EnhancedKey,
	}
	if got := keymap.EventToFarString(mainEnter); got != "CtrlEnter" {
		t.Fatalf("main Enter = %q, want CtrlEnter", got)
	}
	if got := keymap.EventToFarString(numEnter); got != "CtrlNumEnter" {
		t.Fatalf("numeric Enter = %q, want CtrlNumEnter", got)
	}

	parsedMain := keymap.ParseFarKey("CtrlEnter")
	if parsedMain.VirtualKeyCode != vtinput.VK_RETURN || parsedMain.ControlKeyState&vtinput.EnhancedKey != 0 {
		t.Fatalf("parsed CtrlEnter = %#v, want non-enhanced Return", parsedMain)
	}
	parsedNum := keymap.ParseFarKey("CtrlNumEnter")
	if parsedNum.VirtualKeyCode != vtinput.VK_RETURN || parsedNum.ControlKeyState&vtinput.EnhancedKey == 0 {
		t.Fatalf("parsed CtrlNumEnter = %#v, want enhanced Return", parsedNum)
	}
}

type mockAreaFrame struct {
	vtui.BaseFrame
	typ   vtui.FrameType
	title string
}

func (m *mockAreaFrame) GetType() vtui.FrameType { return m.typ }
func (m *mockAreaFrame) GetTitle() string        { return m.title }

func TestMacro_GetCurrentArea(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// 1. Empty FrameManager -> "Common"
	if area := macroCurrentArea(); area != "Common" {
		t.Errorf("Expected 'Common' for empty FrameManager, got %q", area)
	}

	// 2. Dialog -> "Dialog"
	fDialog := &mockAreaFrame{typ: vtui.TypeDialog}
	vtui.FrameManager.Push(fDialog)
	if area := macroCurrentArea(); area != "Dialog" {
		t.Errorf("Expected 'Dialog', got %q", area)
	}
	vtui.FrameManager.Pop()

	// 3. Menu -> "Menu"
	fMenu := &mockAreaFrame{typ: vtui.TypeMenu}
	vtui.FrameManager.Push(fMenu)
	if area := macroCurrentArea(); area != "Menu" {
		t.Errorf("Expected 'Menu', got %q", area)
	}
	vtui.FrameManager.Pop()

	// 4. editor.EditorView -> "Editor"
	fEditor := &mockAreaFrame{typ: vtui.TypeUser + 2}
	vtui.FrameManager.Push(fEditor)
	if area := macroCurrentArea(); area != "Editor" {
		t.Errorf("Expected 'Editor', got %q", area)
	}
	vtui.FrameManager.Pop()

	// 5. viewer.ViewerView -> "Viewer"
	fViewer := &mockAreaFrame{typ: vtui.TypeUser + 3}
	vtui.FrameManager.Push(fViewer)
	if area := macroCurrentArea(); area != "Viewer" {
		t.Errorf("Expected 'Viewer', got %q", area)
	}
	vtui.FrameManager.Pop()
}
func TestMacroRecordingAndPlayback(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpFile := "test_macros.ini"
	t.Cleanup(func() {
		if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove temporary macro file: %v", err)
		}
	})

	mgr := macro.NewMacroManager(tmpFile)

	// Trigger recording start (Ctrl+.)
	ctrlDot := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_PERIOD,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !macroFilter(mgr, ctrlDot) {
		t.Fatal("Ctrl+. should be filtered and start recording")
	}
	if !mgr.Recording {
		t.Fatal("Manager should be in recording state")
	}

	// Send a normal key 'A'
	keyA := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_A,
		Char:           'a',
	}
	macroFilter(mgr, keyA)

	if len(mgr.Buffer) != 1 {
		t.Fatalf("Expected 1 event in buffer, got %d", len(mgr.Buffer))
	}

	// Stop recording
	macroFilter(mgr, ctrlDot)
	if mgr.Recording {
		t.Fatal("Manager should stop recording")
	}

	// Simulate Assign Frame capturing Ctrl+F1
	ctrlF1 := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F1,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	assignFrame := macro.NewMacroAssignFrame(mgr)
	macroAssign(assignFrame, ctrlF1)

	f1Key := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F1, ControlKeyState: vtinput.LeftCtrlPressed})
	if _, ok := mgr.Macros["Common"][f1Key]; !ok {
		t.Fatal("Macro should be saved with Ctrl+F1 key in Common area")
	}

	// Test reloading from file
	mgr2 := macro.NewMacroManager(tmpFile)
	if _, ok := mgr2.Macros["Common"][f1Key]; !ok {
		t.Fatal("Macro was not correctly loaded from INI file")
	}
}

func TestMacroFilterLetsCommandLineDeleteBeforeRemappedHotkey(t *testing.T) {
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
	keymap.GlobalHotkeysMgr.Bind("Shell", "Del", "Panel.Toggle")
	keymap.GlobalHotkeysMgr.Bind("Shell", "BS", "Panel.Toggle")
	keymap.GlobalKeyRemap = nil
	mgr := macro.NewMacroManager("")

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	for _, tc := range []struct {
		name string
		key  uint16
		pos  uint16
		want string
	}{
		{name: "Del", key: vtinput.VK_DELETE, pos: vtinput.VK_HOME, want: "bc"},
		{name: "Backspace", key: vtinput.VK_BACK, pos: vtinput.VK_END, want: "ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pf.CmdLine.Edit.SetText("abc")
			pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tc.pos})
			e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tc.key}
			if macroFilter(mgr, e) {
				t.Fatalf("macro filter consumed %s before command-line editing", tc.name)
			}
			if !pf.ProcessKey(e) {
				t.Fatalf("PanelsFrame did not consume %s", tc.name)
			}
			if got := pf.CmdLine.Edit.GetText(); got != tc.want {
				t.Fatalf("command line after %s = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestKeyNormalization(t *testing.T) {
	// Check that Left and Right Ctrl give same key string
	k1 := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, Char: 'a', ControlKeyState: vtinput.LeftCtrlPressed})
	k2 := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, Char: 'a', ControlKeyState: vtinput.RightCtrlPressed})
	if k1 != k2 {
		t.Errorf("Normalization failed: %s != %s", k1, k2)
	}

	// Check Ctrl+Shift combination
	k3 := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_B, Char: 'B', ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed})
	if k3 != "CtrlShiftB" {
		t.Errorf("Complex normalization failed: %s", k3)
	}
}

func TestEventToHotkeyStringPreservesRightCtrl(t *testing.T) {
	left := &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.LeftCtrlPressed}
	right := &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.RightCtrlPressed}
	rightAlt := &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.RightCtrlPressed | vtinput.LeftAltPressed}

	if got := keymap.EventToHotkeyString(left); got != "CtrlA" {
		t.Fatalf("left Ctrl hotkey = %q, want CtrlA", got)
	}
	if got := keymap.EventToHotkeyString(right); got != "RCtrlA" {
		t.Fatalf("right Ctrl hotkey = %q, want RCtrlA", got)
	}
	if got := keymap.EventToHotkeyString(rightAlt); got != "RCtrlAltA" {
		t.Fatalf("right Ctrl+Alt hotkey = %q, want RCtrlAltA", got)
	}
}

// TestEventToHotkeyStringNamesPunctuationByVirtualKey covers issue #807: the
// kitty keyboard protocol reports the backslash key with the character it
// types, which is '\' with Ctrl and '|' with Ctrl+Shift, so the VK_DC
// bindings never matched there.
func TestEventToHotkeyStringNamesPunctuationByVirtualKey(t *testing.T) {
	cases := []struct {
		name  string
		event *vtinput.InputEvent
		want  string
	}{
		{
			name:  "kitty Ctrl+backslash",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_5, Char: '\\', ControlKeyState: vtinput.LeftCtrlPressed},
			want:  "CtrlVK_DC",
		},
		{
			name:  "kitty Ctrl+Shift+backslash",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_5, Char: '|', ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed},
			want:  "CtrlShiftVK_DC",
		},
		{
			name:  "far2l and legacy tty send no char",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_5, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed},
			want:  "CtrlShiftVK_DC",
		},
		{
			name:  "right Ctrl keeps its own spelling",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_5, Char: '\\', ControlKeyState: vtinput.RightCtrlPressed},
			want:  "RCtrlVK_DC",
		},
		{
			name:  "brackets",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_4, Char: '[', ControlKeyState: vtinput.LeftCtrlPressed},
			want:  "CtrlVK_DB",
		},
		{
			name:  "a layout that types another character on the same key",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_3, Char: 'ё', ControlKeyState: vtinput.LeftCtrlPressed},
			want:  "CtrlVK_C0",
		},
		{
			name:  "letters are untouched",
			event: &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_A, Char: 'a', ControlKeyState: vtinput.LeftCtrlPressed},
			want:  "CtrlA",
		},
	}
	for _, tc := range cases {
		if got := keymap.EventToHotkeyString(tc.event); got != tc.want {
			t.Errorf("%s: hotkey = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The macro layer keeps naming keys the way Far does, so a recorded
	// macro assigned to Ctrl+Shift+\ still answers to the same string it
	// was stored under.
	ctrlShiftBackslash := &vtinput.InputEvent{VirtualKeyCode: vtinput.VK_OEM_5, Char: '|', ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed}
	if got := keymap.EventToFarString(ctrlShiftBackslash); got != "CtrlShift|" {
		t.Errorf("macro key name = %q, want CtrlShift| (the macro layer must not change)", got)
	}
}

// TestKittyBackslashReachesBookmarks walks the reported key from the bytes a
// terminal in kitty keyboard mode sends to the action the default bindings
// give it.
func TestKittyBackslashReachesBookmarks(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()

	cases := []struct {
		seq  string
		want string
	}{
		{"\x1b[92:124;6u", "Panel.Bookmarks"}, // Ctrl+Shift+\
		{"\x1b[92;5u", "Panel.GoRoot"},        // Ctrl+\
	}
	for _, tc := range cases {
		event, _, err := vtinput.ParseKitty([]byte(tc.seq))
		if err != nil {
			t.Fatalf("ParseKitty(%q) failed: %v", tc.seq, err)
		}
		if event.VirtualKeyCode != vtinput.VK_OEM_5 {
			t.Fatalf("ParseKitty(%q) gave VK 0x%X, want VK_OEM_5", tc.seq, event.VirtualKeyCode)
		}
		key := keymap.EventToHotkeyString(event)
		if got := keymap.ConfiguredHotkeyAction(hm, "Shell", key); got != tc.want {
			t.Errorf("ParseKitty(%q) -> %q -> %q, want %q", tc.seq, key, got, tc.want)
		}
	}
}

func TestConfiguredHotkeyActionRightCtrlFallback(t *testing.T) {
	hm := keymap.NewHotkeyManager("")

	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlF3"); got != "Panel.SortByName" {
		t.Fatalf("right Ctrl should fall back to Ctrl binding: got %q, want Panel.SortByName", got)
	}

	// Unbinding the RCtrl spelling only drops the RCtrl-specific shortcut;
	// Right Ctrl then behaves like plain Ctrl rather than becoming a dead key.
	hm.Bind("Shell", "RCtrlF3", "None")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlF3"); got != "Panel.SortByName" {
		t.Fatalf("RCtrl unbind should fall back to the Ctrl binding: got %q, want Panel.SortByName", got)
	}

	// Silencing the key for both Ctrl spellings is done on the plain one.
	hm.Bind("Shell", "CtrlF3", "None")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlF3"); got != "None" {
		t.Fatalf("explicit CtrlF3 unbind should silence RCtrlF3 too: got %q, want None", got)
	}
}

// TestConfiguredHotkeyActionUnboundBuiltInRightCtrlFallsBackToCtrl covers
// #492: after the user unbinds the built-in RCtrlA AI shortcut, Right Ctrl+A
// must act as Ctrl+A (File.Attributes) instead of being swallowed.
func TestConfiguredHotkeyActionUnboundBuiltInRightCtrlFallsBackToCtrl(t *testing.T) {
	hm := keymap.NewHotkeyManager("")

	hm.Bind("Shell", "RCtrlA", "None")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "File.Attributes" {
		t.Fatalf("unbound RCtrlA = %q, want File.Attributes (the CtrlA default)", got)
	}
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "CtrlA"); got != "File.Attributes" {
		t.Fatalf("CtrlA = %q, want File.Attributes", got)
	}

	// The same holds for the removal form the settings dialog persists.
	hm.Unbind("Shell", "RCtrlA")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "File.Attributes" {
		t.Fatalf("removed RCtrlA = %q, want File.Attributes", got)
	}

	// An explicit RCtrl-specific action still wins over the Ctrl binding.
	hm.Bind("Shell", "RCtrlA", "Panel.Toggle")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "Panel.Toggle" {
		t.Fatalf("explicit RCtrlA = %q, want Panel.Toggle", got)
	}
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "CtrlA"); got != "File.Attributes" {
		t.Fatalf("CtrlA must stay File.Attributes next to an explicit RCtrlA: got %q", got)
	}

	// Ini round trip: RCtrlA=None written by the dialog loads the same way.
	dir := t.TempDir()
	path := filepath.Join(dir, "hotkeys.ini")
	if err := os.WriteFile(path, []byte("[Shell]\nRCtrlA=None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := keymap.NewHotkeyManager(path)
	loaded.Load()
	if got := keymap.ConfiguredHotkeyAction(loaded, "Shell", "RCtrlA"); got != "File.Attributes" {
		t.Fatalf("RCtrlA=None from ini = %q, want File.Attributes", got)
	}
}

func TestConfiguredHotkeyActionExplicitCtrlOverridesBuiltInRightCtrl(t *testing.T) {
	hm := keymap.NewHotkeyManager("")

	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "AI.TogglePanel" {
		t.Fatalf("built-in RCtrlA action = %q, want AI.TogglePanel", got)
	}

	hm.Bind("Shell", "CtrlA", "Panel.Toggle")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "Panel.Toggle" {
		t.Fatalf("explicit CtrlA should override built-in RCtrlA: got %q, want Panel.Toggle", got)
	}

	hm.Bind("Shell", "CtrlA", "None")
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrlA"); got != "None" {
		t.Fatalf("explicit CtrlA unbind should override built-in RCtrlA: got %q, want None", got)
	}
}

func TestConfigurableHotkeyCanOverrideRightCtrlBookmark(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	for _, key := range []uint16{vtinput.VK_1, vtinput.VK_2, vtinput.VK_3, vtinput.VK_4} {
		e := &vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  key,
			ControlKeyState: vtinput.RightCtrlPressed,
		}
		if keymap.ConfigurableHotkeyOwnsPanelBookmark(hm, "Shell", e) {
			t.Fatalf("built-in Ctrl%c should leave RightCtrl+%c owned by bookmarks", key, key)
		}
	}

	rightCtrl3 := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_3,
		ControlKeyState: vtinput.RightCtrlPressed,
	}

	hm.Bind("Shell", "Ctrl3", "File.Attributes")
	if !keymap.ConfigurableHotkeyOwnsPanelBookmark(hm, "Shell", rightCtrl3) {
		t.Fatal("explicit Ctrl3 should make RightCtrl+3 configurable")
	}
	if got := keymap.ConfiguredHotkeyAction(hm, "Shell", "RCtrl3"); got != "File.Attributes" {
		t.Fatalf("RightCtrl+3 fallback = %q, want File.Attributes", got)
	}
}

func TestPanelBookmarkHotkeysKeepRightCtrlDistinct(t *testing.T) {
	tests := []struct {
		name string
		key  uint16
		mods vtinput.ControlKeyState
		want bool
	}{
		{name: "right ctrl goto", key: vtinput.VK_1, mods: vtinput.RightCtrlPressed, want: true},
		{name: "right ctrl save", key: vtinput.VK_2, mods: vtinput.RightCtrlPressed | vtinput.ShiftPressed, want: true},
		{name: "left ctrl view mode", key: vtinput.VK_3, mods: vtinput.LeftCtrlPressed, want: false},
		{name: "left ctrl alt alias", key: vtinput.VK_4, mods: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed, want: true},
		{name: "right ctrl home", key: vtinput.VK_OEM_3, mods: vtinput.RightCtrlPressed, want: true},
		{name: "right ctrl shifted home", key: vtinput.VK_OEM_3, mods: vtinput.RightCtrlPressed | vtinput.ShiftPressed, want: false},
		{name: "unrelated right ctrl key", key: vtinput.VK_A, mods: vtinput.RightCtrlPressed, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &vtinput.InputEvent{
				Type:            vtinput.KeyEventType,
				KeyDown:         true,
				VirtualKeyCode:  tc.key,
				ControlKeyState: tc.mods,
			}
			if got := keymap.IsPanelBookmarkHotkey(e); got != tc.want {
				t.Fatalf("isPanelBookmarkHotkey() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMacroFastFindEscapeBypassesPanelToggle(t *testing.T) {
	oldCfg := config.App
	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacroMgr := macro.MacroMgr
	defer func() {
		config.App = oldCfg
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacroMgr
	}()

	config.App.NavigationMode = config.NavigationClassic
	macro.MacroMgr = nil
	pf, left, _ := newSearchFirstTestFrame(t)
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.SyncCurrentScreen()
	defer func() {
		pf.Close()
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	}()

	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr.Bind("Shell", "Esc", "Panel.Toggle")
	mgr := macro.NewMacroManager("")
	escape := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	}

	left.FastFindMode = true
	left.FastFindStr = "a"
	if macroFilter(mgr, escape) {
		t.Fatal("macro filter consumed Esc while Fast Find was active")
	}
	if !pf.ShowPanels {
		t.Fatal("Panel.Toggle hid panels before Fast Find handled Esc")
	}
	if !pf.ProcessKey(escape) {
		t.Fatal("PanelsFrame did not handle Fast Find Esc")
	}
	if left.FastFindMode || left.FastFindStr != "" || !pf.ShowPanels {
		t.Fatalf("Esc result: mode=%v text=%q panels=%v", left.FastFindMode, left.FastFindStr, pf.ShowPanels)
	}

	// The bypass is contextual: without Fast Find, the configured Esc action
	// must still run normally.
	pf.ShowPanels = true
	if !macroFilter(mgr, escape) {
		t.Fatal("Esc without Fast Find did not invoke Panel.Toggle")
	}
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle did not hide panels without Fast Find")
	}
}

func TestMacroFastFindDeleteBypassesPanelToggle(t *testing.T) {
	oldCfg := config.App
	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacroMgr := macro.MacroMgr
	defer func() {
		config.App = oldCfg
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacroMgr
	}()

	config.App.NavigationMode = config.NavigationClassic
	config.App.EscTogglePanels = true
	macro.MacroMgr = nil
	pf, left, _ := newSearchFirstTestFrame(t)
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.SyncCurrentScreen()
	defer func() {
		pf.Close()
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	}()

	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	mgr := macro.NewMacroManager("")
	deleteKey := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_DELETE,
		ControlKeyState: vtinput.EnhancedKey,
	}

	left.FastFindMode = true
	left.FastFindStr = "a"
	if macroFilter(mgr, deleteKey) {
		t.Fatal("macro filter consumed Delete while Fast Find was active")
	}
	if !pf.ProcessKey(deleteKey) {
		t.Fatal("PanelsFrame did not handle Fast Find Delete")
	}
	if !left.FastFindMode || left.FastFindStr != "a" || !pf.ShowPanels {
		t.Fatalf("Delete changed Fast Find: mode=%v text=%q panels=%v", left.FastFindMode, left.FastFindStr, pf.ShowPanels)
	}

	left.FastFindMode = false
	if !macroFilter(mgr, deleteKey) {
		t.Fatal("Delete without Fast Find did not invoke Panel.Toggle")
	}
	if pf.ShowPanels {
		t.Fatal("Panel.Toggle did not hide panels without Fast Find")
	}
}

func TestMacroShellDoesNotRunDuringFastFind(t *testing.T) {
	oldCfg := config.App
	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacroMgr := macro.MacroMgr
	defer func() {
		config.App = oldCfg
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacroMgr
	}()

	config.App.NavigationMode = config.NavigationClassic
	macro.MacroMgr = nil
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	pf, left, _ := newSearchFirstTestFrame(t)
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.SyncCurrentScreen()
	t.Cleanup(func() {
		pf.Close()
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	})

	mgr := macro.NewMacroManager("")
	key := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_A,
		Char:           'a',
	}
	keyStr := keymap.EventToFarString(key)
	mgr.Macros["Shell"] = map[string][]*vtinput.InputEvent{
		keyStr: {{Type: vtinput.KeyEventType, KeyDown: true, Char: 'x', VirtualKeyCode: vtinput.VK_X}},
	}

	left.FastFindMode = true
	if macroFilter(mgr, key) {
		t.Fatal("Shell macro consumed input while Fast Find was active")
	}
}

func TestMacroPlaybackLogic(t *testing.T) {
	mgr := macro.NewMacroManager("unused.ini")

	// Create macro: print "hi" on F2 press
	f2Key := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F2})
	macroSeq := []*vtinput.InputEvent{
		{Type: vtinput.KeyEventType, KeyDown: true, Char: 'h', VirtualKeyCode: vtinput.VK_H},
		{Type: vtinput.KeyEventType, KeyDown: true, Char: 'i', VirtualKeyCode: vtinput.VK_I},
	}
	mgr.Macros["Common"] = make(map[string][]*vtinput.InputEvent)
	mgr.Macros["Common"][f2Key] = macroSeq

	// Simulate F2 press
	pressF2 := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F2,
	}

	// Hack for test: intercepting InjectEvents by replacing global FrameManager is not easy,
	// but we can check that Filter returned true (event consumed to be replaced by macro)
	if !macroFilter(mgr, pressF2) {
		t.Error("Filter should return true when triggering a macro")
	}
}
func TestMacro_FilterTriggerSwallowing_Order(t *testing.T) {
	mgr := macro.NewMacroManager("unused.ini")
	mgr.Recording = true
	mgr.Buffer = make([]*vtinput.InputEvent, 0)

	// Trigger stop recording via Ctrl+.
	stopEvent := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		Char: '.', ControlKeyState: vtinput.LeftCtrlPressed,
	}

	res := macroFilter(mgr, stopEvent)

	if !res {
		t.Error("Filter should swallow the stop trigger even if recording is active")
	}
	if len(mgr.Buffer) != 0 {
		t.Errorf("Stop trigger should NOT be added to macro buffer, but buffer size is %d", len(mgr.Buffer))
	}
}
func TestMacro_TriggerSwallowing(t *testing.T) {
	mgr := macro.NewMacroManager("unused.ini")

	// 1. Start recording via Ctrl+. (using Char for compatibility)
	startEvent := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		Char: '.', ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, startEvent)
	if !mgr.Recording {
		t.Fatal("Should be recording")
	}

	// 2. Type 'A'
	macroFilter(mgr, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'a', VirtualKeyCode: vtinput.VK_A})

	// 3. Stop recording via Ctrl+.
	stopEvent := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		Char: '.', ControlKeyState: vtinput.LeftCtrlPressed,
	}
	res := macroFilter(mgr, stopEvent)

	if !res {
		t.Error("Stop trigger should be consumed (return true)")
	}
	if mgr.Recording {
		t.Error("Should have stopped recording")
	}

	// 4. Verify buffer: should ONLY contain 'a', NOT the trigger dot
	if len(mgr.Buffer) != 1 || mgr.Buffer[0].Char != 'a' {
		t.Errorf("Macro buffer polluted or incomplete. Items: %d", len(mgr.Buffer))
	}
}

func TestMacro_AssignRobustness(t *testing.T) {
	// Clean manager for testing
	mgr := &macro.MacroManager{
		Macros:    make(map[string]map[string][]*vtinput.InputEvent),
		StartArea: "Common",
	}
	mgr.Buffer = []*vtinput.InputEvent{{Char: 'x', KeyDown: true}}
	f := macro.NewMacroAssignFrame(mgr)

	// 1. Standalone modifiers should be ignored (dialog stays open)
	f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_SHIFT})
	if f.Done {
		t.Error("Assign dialog should ignore standalone Shift")
	}

	// 2. Esc SHOULD now cancel the dialog without assignment
	f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE})
	if !f.Done {
		t.Error("Assign dialog should close after pressing Esc")
	}

	escKey := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_ESCAPE})
	if _, ok := mgr.Macros["Common"][escKey]; ok {
		t.Error("Esc should cancel, not assign a macro")
	}

	// 3. Test Alt+X assignment
	f.Done = false
	mgr.Buffer = []*vtinput.InputEvent{{Char: 'y', KeyDown: true}}
	macroAssign(f, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_X,
		Char:            'x',
		ControlKeyState: vtinput.LeftAltPressed,
	})

	altXKey := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_X, Char: 'x', ControlKeyState: vtinput.LeftAltPressed})
	if _, ok := mgr.Macros["Common"][altXKey]; !ok {
		t.Error("Macro failed to assign to Alt+X")
	}
}

func TestMacro_KeyUpConsumption(t *testing.T) {
	mgr := macro.NewMacroManager("unused.ini")

	// Start recording
	ctrlDot := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_OEM_PERIOD, ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, ctrlDot)

	// Release trigger (KeyUp)
	ctrlDotUp := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: false,
		VirtualKeyCode: vtinput.VK_OEM_PERIOD, ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !macroFilter(mgr, ctrlDotUp) {
		t.Error("KeyUp for Ctrl+. should be consumed by the filter")
	}

	// Normal key release during recording should NOT be added to buffer
	keyAUp := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: false,
		VirtualKeyCode: vtinput.VK_A, Char: 'a',
	}
	macroFilter(mgr, keyAUp)
	if len(mgr.Buffer) != 0 {
		t.Errorf("KeyUp should not be recorded in macro buffer, got length %d", len(mgr.Buffer))
	}
}

func TestMacro_CancelEsc(t *testing.T) {
	tmpPath := filepath.Join(t.TempDir(), "esc.ini")

	mgr := macro.NewMacroManager(tmpPath)
	mgr.Recording = true
	mgr.Buffer = []*vtinput.InputEvent{{Char: 'h', KeyDown: true}}

	assign := macro.NewMacroAssignFrame(mgr)
	escEvent := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	}

	assign.ProcessKey(escEvent)

	key := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_ESCAPE})
	if _, ok := mgr.Macros["Common"][key]; ok {
		t.Error("Esc should cancel, not assign a macro")
	}
	if !assign.Done {
		t.Error("Assign frame should be Done after cancellation")
	}
}

func TestMacro_Clear(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "clear_macros.ini")
	mgr := macro.NewMacroManager(tmpFile)

	mgr.StartArea = "Common"

	// 1. Assign a macro first
	key := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F3})
	mgr.Macros["Common"] = make(map[string][]*vtinput.InputEvent)
	mgr.Macros["Common"][key] = []*vtinput.InputEvent{
		{Type: vtinput.KeyEventType, KeyDown: true, Char: 'x'},
	}
	mgr.Save()

	// 2. Simulate empty recording and assigning to F3 (to clear it)
	mgr.Buffer = nil // Empty recording

	assignFrame := macro.NewMacroAssignFrame(mgr)
	macroAssign(assignFrame, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F3,
	})

	// 3. Verify it is deleted from active map
	if _, ok := mgr.Macros["Common"][key]; ok {
		t.Error("Macro should be completely deleted from map when assigned an empty buffer")
	}

	// 4. Verify it is deleted from saved file
	mgr2 := macro.NewMacroManager(tmpFile)
	if _, ok := mgr2.Macros["Common"][key]; ok {
		t.Error("Cleared macro should not persist in the saved INI file")
	}
}

func TestMacro_CharTrigger(t *testing.T) {
	mgr := macro.NewMacroManager("unused.ini")

	// Test trigger using Char instead of VK (for terminals that map dot differently)
	event := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		Char: '.', VirtualKeyCode: 0, ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !macroFilter(mgr, event) {
		t.Error("Macro recording should start via Char '.' detection")
	}
	if !mgr.Recording {
		t.Error("Manager failed to enter recording state via Char trigger")
	}
}

func TestMacro_BoundActionConsumesEventWhenHandlerFails_Issue983(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	preserveActionRegistry(t)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	calls := 0
	action.RegisterAction(action.Action{
		Name: "Test.FailedHotkey",
		Area: "Common",
		Handler: func() bool {
			calls++
			return false
		},
	})
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = &keymap.HotkeyManager{
		Bindings: map[string]map[string]string{
			"Common": {"CtrlF9": "Test.FailedHotkey"},
		},
		Defaults: map[string]map[string]string{},
	}
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previousHotkeys })

	mgr := &macro.MacroManager{Macros: make(map[string]map[string][]*vtinput.InputEvent)}
	event := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F9,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	if !macroFilter(mgr, event) {
		t.Fatal("Filter passed through a configured action whose handler failed")
	}
	if calls != 1 {
		t.Fatalf("failed action calls after Filter = %d, want 1", calls)
	}

	if !macroLookupHotkey(mgr, event) {
		t.Fatal("LookupHotkey passed through a configured action whose handler failed")
	}
	if calls != 2 {
		t.Fatalf("failed action calls after LookupHotkey = %d, want 2", calls)
	}
}

func TestMacro_AssignFrame_Structure(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	mgr := &macro.MacroManager{
		Macros:    make(map[string]map[string][]*vtinput.InputEvent),
		StartArea: "Common",
	}
	f := macro.NewMacroAssignFrame(mgr)

	// Check that it's a proper window with a child (the prompt text)
	if len(f.GetChildren()) == 0 {
		t.Error("macro.MacroAssignFrame should have at least one child (prompt)")
	}

	// Validate Layout
	vtui.AssertLayout(t, f)

	// Verify focus logic: it should NOT allow Tab to cycle away
	// because any key (including Tab) must be captured as a macro.
	tabEvent := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_TAB,
	}

	mgr.Buffer = []*vtinput.InputEvent{{Char: 't', KeyDown: true}}
	handled := macroAssign(f, tabEvent)

	if !handled {
		t.Error("macro.MacroAssignFrame should handle (capture) Tab key")
	}
	if !f.IsDone() {
		t.Error("macro.MacroAssignFrame should close after capturing a key")
	}

	// Verify that macro was assigned to Tab
	tabKey := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_TAB})
	if _, ok := mgr.Macros["Common"][tabKey]; !ok {
		t.Error("Macro failed to assign to Tab key")
	}
}

func TestMacroKeyStrDistinguishesEnhancedKeys(t *testing.T) {
	// Standard Delete has the EnhancedKey modifier in modern protocols
	delKeyStr := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_DELETE, ControlKeyState: vtinput.EnhancedKey})

	// Numpad Delete (NumDel) does not have the EnhancedKey modifier
	numDelKeyStr := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_DELETE, ControlKeyState: 0})

	if delKeyStr == numDelKeyStr {
		t.Errorf("Expected different KeyStr representations for standard Del (%q) and NumDel (%q), but they are identical", delKeyStr, numDelKeyStr)
	}
}

func TestMacroIgnoresStandaloneModifiers(t *testing.T) {
	mgr := macro.NewMacroManager("")
	mgr.Recording = true
	mgr.Buffer = nil

	// Simulate pressing Ctrl, then Shift, then a letter 'A', then releasing them
	events := []*vtinput.InputEvent{
		{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_CONTROL},
		{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_SHIFT},
		{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_A, Char: 'A'},
		{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_A, Char: 'A'},
		{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_SHIFT},
		{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_CONTROL},
	}

	for _, ev := range events {
		macroFilter(mgr, ev)
	}

	if len(mgr.Buffer) != 1 {
		t.Errorf("Expected exactly 1 event in macro buffer, but got %d", len(mgr.Buffer))
	} else if mgr.Buffer[0].VirtualKeyCode != vtinput.VK_A {
		t.Errorf("Expected recorded event to be VK_A, but got %v", vtinput.VKString(mgr.Buffer[0].VirtualKeyCode))
	}
}

func TestMacroClearRecordingIsEmpty(t *testing.T) {
	mgr := macro.NewMacroManager("")

	// Start recording
	startEvent := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_PERIOD,
		Char:            '.',
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, startEvent)

	if !mgr.Recording {
		t.Error("Expected macro.MacroManager to be in Recording state")
	}

	// Pressing Ctrl key before pressing '.' to stop recording
	ctrlDown := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_CONTROL,
	}
	macroFilter(mgr, ctrlDown)

	// Pressing '.' to stop recording
	stopEvent := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_PERIOD,
		Char:            '.',
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, stopEvent)

	if mgr.Recording {
		t.Error("Expected macro.MacroManager to stop Recording")
	}

	if len(mgr.Buffer) != 0 {
		t.Errorf("Expected macro buffer to be empty for immediate stop recording, but got %d items", len(mgr.Buffer))
	}
}

func TestMacroClearResetsExisting(t *testing.T) {
	mgr := macro.NewMacroManager("")
	clearKeyStr := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_CLEAR})
	mgr.Macros = map[string]map[string][]*vtinput.InputEvent{
		"Common": {
			clearKeyStr: {{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_CLEAR}},
		},
	}
	mgr.StartArea = "Common"

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	// 1. Начинаем запись макроса
	startEvent := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_PERIOD,
		Char:            '.',
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, startEvent)

	if !mgr.Recording {
		t.Error("Expected macro.MacroManager to be in Recording state")
	}

	// 2. Останавливаем запись (буфер пуст)
	stopEvent := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_PERIOD,
		Char:            '.',
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	macroFilter(mgr, stopEvent)

	if mgr.Recording {
		t.Error("Expected macro.MacroManager to stop Recording")
	}

	// Выполняем все накопившиеся асинхронные задачи, пока не включится нужный режим
	timeout := time.After(1 * time.Second)
WaitLoop:
	for !mgr.Assigning {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for Assigning state")
			break WaitLoop
		}
	}

	if !mgr.Assigning {
		t.Error("Expected macro.MacroManager to be in Assigning state")
	}

	// Пытаемся нажать 'Clear' (VK_CLEAR = 0x0C)
	clearEvent := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_CLEAR,
	}

	// Фильтр НЕ должен поглотить событие воспроизведением старого макроса,
	// так как активен режим назначения (Assigning == true)
	consumed := macroFilter(mgr, clearEvent)
	if consumed {
		t.Error("Expected Filter to not consume VK_CLEAR while Assigning is active")
	}

	// Симулируем обработку нажатия диалогом
	frame := macro.NewMacroAssignFrame(mgr)
	macroAssign(frame, clearEvent)

	// После обработки флаг назначения должен сброситься, а макрос удалиться
	if mgr.Assigning {
		t.Error("Expected Assigning state to be cleared after key processing")
	}

	if _, exists := mgr.Macros["Common"][clearKeyStr]; exists {
		t.Error("Expected macro to be deleted")
	}
}
func TestMacro_ReassignAndCleanup(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "reassign_macros.ini")
	mgr := macro.NewMacroManager(tmpFile)
	mgr.StartArea = "Common"

	key := keymap.EventToFarString(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F3})
	mgr.Macros["Common"] = map[string][]*vtinput.InputEvent{
		key: {
			&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3},
		},
	}
	mgr.Save()

	Engine, err := macro.NewLuaMacroEngine(f4MacroHost{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Engine.Close(); err != nil {
			t.Errorf("close Lua macro engine: %v", err)
		}
	})
	mgr.Lua = Engine

	scriptDir := filepath.Join(config.GetF4ConfigDir(), "Macros", "scripts")
	if err := os.MkdirAll(scriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, macro.RecordedMacroFileName("Common", key))
	if err := os.WriteFile(scriptPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Engine.LoadString("test", `Macro { area = "Common"; key = "F3"; action = function() end }`); err != nil {
		t.Fatal(err)
	}

	mgr.Buffer = nil
	assignFrame := macro.NewMacroAssignFrame(mgr)
	macroAssign(assignFrame, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F3,
	})

	if _, ok := mgr.Macros["Common"][key]; ok {
		t.Error("Macro should be deleted from INI")
	}
	if Engine.Find("Common", "F3") != nil {
		t.Error("Macro should be deleted from Lua Engine")
	}
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Error("Lua script file should be deleted from disk")
	}
}

// A recorded macro on Backspace (the user's "go up" for an empty command line)
// must not run while the command line holds text: Backspace edits it (f4#1797).
func TestMacroFilterLetsCommandLineDeleteBeforeRecordedMacro(t *testing.T) {
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
		"BS": {{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR, ControlKeyState: vtinput.LeftCtrlPressed}},
	}

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	backspace := func() *vtinput.InputEvent {
		return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK}
	}

	pf.CmdLine.Edit.SetText("abc")
	pf.CmdLine.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	if macroFilter(mgr, backspace()) {
		t.Fatal("the recorded Backspace macro ran over a command line that holds text")
	}
	if !pf.ProcessKey(backspace()) || pf.CmdLine.Edit.GetText() != "ab" {
		t.Fatalf("command line after Backspace = %q, want %q", pf.CmdLine.Edit.GetText(), "ab")
	}

	pf.CmdLine.Clear()
	if !macroFilter(mgr, backspace()) {
		t.Fatal("the recorded Backspace macro did not run on an empty command line")
	}
}

// f4#1870 (DkmS1953): with the workspace tab strip shown, the recording mark
// was written on row 0, which vtui paints the strip over right after OnRender,
// so it never showed. It now goes on the first row under the strip.
func TestMacroRecordingIndicatorBelowWorkspaceStrip_Issue1870(t *testing.T) {
	scr := initFrameworkActionTestScreen(t)
	vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabsAlways, vtui.WorkspaceCtrlTabDirect)
	inset := vtui.FrameManager.WorkspaceTopInset()
	if inset == 0 {
		t.Fatal("the tab strip reserves no row; the case of the report is not set up")
	}
	previous := macro.MacroMgr
	t.Cleanup(func() { macro.MacroMgr = previous })
	macro.MacroMgr = macro.NewMacroManager(filepath.Join(t.TempDir(), "macros.ini"))

	drawMacroRecordingIndicator(scr)
	if got := scr.GetCell(1, inset).Char; got == 'R' {
		t.Fatal("the mark is drawn while nothing is being recorded")
	}

	macro.MacroMgr.Recording = true
	drawMacroRecordingIndicator(scr)
	if got := scr.GetCell(1, inset).Char; got != 'R' {
		t.Fatalf("row %d under the tab strip has U+%04X, want the R mark", inset, got)
	}
}

// macroAssign presses e in the assign dialog's key field and then Enter on
// OK, the two steps the far2-style dialog takes (f4#1870). It reports what
// the key field did with e.
func macroAssign(f *macro.MacroAssignFrame, e *vtinput.InputEvent) bool {
	handled := f.ProcessKey(e)
	f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	return handled
}
