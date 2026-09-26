package app

import (
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/terminal"
	"testing"

	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// TestHotkeys_ShellActionsBoundInTerminalArea guards the fix for issue #354.
// With panels hidden (area="Terminal") the file-manager F-keys used to
// fall through, so F2/F7/Shift+F4/Shift+F9/F10/Alt+F1/Alt+F2/Ctrl+P did
// nothing. They now carry DefaultAreas: []string{"Terminal"} with a
// NoTerminalApp condition so they fire in the panels-hidden idle
// terminal but stay out of the way of a running program (#1376).
func TestHotkeys_ShellActionsBoundInTerminalArea_Issue354(t *testing.T) {
	hm := keymap.NewHotkeyManager("")

	cases := []struct {
		key    string
		action string
	}{
		{"F2", "Panel.UserMenu"},
		{"F7", "File.MakeDir"},
		{"F10", "App.Quit"},
		{"ShiftF4", "File.New"},
		{"ShiftF9", "App.SaveSettings"},
		{"AltF1", "Panel.LeftDriveMenu"},
		{"AltF2", "Panel.RightDriveMenu"},
		{"CtrlP", "Panel.TogglePassivePanel"},
		{"CtrlF1", "Panel.ToggleLeftPanel"},
		{"CtrlF2", "Panel.ToggleRightPanel"},
	}
	for _, tc := range cases {
		got, ok := hm.Bindings["Terminal"][tc.key]
		if !ok {
			t.Errorf("Terminal area missing binding for %s (expected %s:NoTerminalApp)", tc.key, tc.action)
			continue
		}
		want := tc.action + ":NoTerminalApp"
		if got != want {
			t.Errorf("Terminal/%s = %q, want %q", tc.key, got, want)
		}
	}
}

// TestPanelsFrame_CtrlF1CtrlF2RestoreAfterBothHidden_Issue927 covers the
// state reached after hiding both side panels. The side toggles must remain
// available in the idle terminal area and showing either side must bring the
// panels frame back on screen; an AltScreen application must keep both keys.
func TestPanelsFrame_CtrlF1CtrlF2RestoreAfterBothHidden_Issue927(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	previousHotkeys := keymap.GlobalHotkeysMgr
	previousMacros := macro.MacroMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	macro.MacroMgr = macro.NewMacroManager("")
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = previousHotkeys
		macro.MacroMgr = previousMacros
	})

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowLeftPanel = false
	pf.ShowRightPanel = false
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = false
	vtui.FrameManager.Push(pf)

	press := func(vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	press(vtinput.VK_F1)
	if !pf.ShowPanels || !pf.ShowLeftPanel || pf.ShowRightPanel {
		t.Fatalf("Ctrl+F1 from both panels hidden: show=%v left=%v right=%v; want true,true,false",
			pf.ShowPanels, pf.ShowLeftPanel, pf.ShowRightPanel)
	}
	press(vtinput.VK_F2)
	if !pf.ShowPanels || !pf.ShowLeftPanel || !pf.ShowRightPanel {
		t.Fatalf("Ctrl+F2 after Ctrl+F1: show=%v left=%v right=%v; want true,true,true",
			pf.ShowPanels, pf.ShowLeftPanel, pf.ShowRightPanel)
	}

	pf.ShowPanels = false
	pf.ShowLeftPanel = false
	pf.ShowRightPanel = false
	pf.TermView.UseAltScreen = true
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "CtrlF1"); got != "" {
		t.Fatalf("Terminal CtrlF1 with AltScreen active = %q, want empty", got)
	}
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "CtrlF2"); got != "" {
		t.Fatalf("Terminal CtrlF2 with AltScreen active = %q, want empty", got)
	}
}

// TestHotkeys_ShellActions_TerminalArea_GatedByAltScreen ensures the
// Terminal-area bindings do NOT fire when a full-screen application
// (mc, htop, vim, less) is active — those keys belong to the app.
// The gate is NoTerminalApp, which returns true when panels are
// shown OR neither an AltScreen mode nor a busy child is engaged.
func TestHotkeys_ShellActions_TerminalArea_GatedByAltScreen_Issue354(t *testing.T) {
	// Register a hidden-panels panel.PanelsFrame with an AltScreen app active —
	// that's the state where the condition must fail. It is never popped, so
	// it needs a manager of its own: left on the shared one it would keep
	// answering for the top frame in every test that runs afterwards.
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = true
	vtui.FrameManager.Push(pf)

	if keymap.GlobalHotkeysMgr == nil {
		keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	}

	// With AltScreen active the condition NoTerminalApp is false, so
	// the Terminal-area binding must resolve to "" (fall-through to term.PTY).
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "F2"); got != "" {
		t.Errorf("Terminal F2 with AltScreen active: got %q, want empty (must fall through to app)", got)
	}
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "F10"); got != "" {
		t.Errorf("Terminal F10 with AltScreen active: got %q, want empty (must fall through to app)", got)
	}

	// Clear AltScreen — condition now passes and the actions surface.
	pf.TermView.UseAltScreen = false
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "F2"); got != "Panel.UserMenu" {
		t.Errorf("Terminal F2 without AltScreen: got %q, want Panel.UserMenu", got)
	}
	if got := keymap.GlobalHotkeysMgr.GetAction("Terminal", "F10"); got != "App.Quit" {
		t.Errorf("Terminal F10 without AltScreen: got %q, want term.App.Quit", got)
	}
}

// TestHotkeys_TerminalQuiet_SimpleInline_IgnoresStrayAltScreen_Issue897 covers
// f4#897 (item 3 of 3): F3/F4 (Terminal.ViewLog/EditLog) stopped opening the
// captured terminal output under ShellModeSimpleInline (the legacy
// Windows/ReactOS build with no usable ConPTY, CONSOLE_MODES.md §4.1).
// pf.TermView is a leftover background object in that mode -- nothing feeds
// it, so its UseAltScreen field does not track what's actually on screen --
// and an earlier stray flip of that same field already broke Ctrl+O this
// way once (f4#1376, see noaltscreenapp/TerminalOwnsKeyboard). The
// terminalquiet condition read UseAltScreen directly without the same
// short-circuit, so it silently went permanently false and F3/F4 stopped
// resolving to Terminal.ViewLog/EditLog under SimpleInline.
func TestHotkeys_TerminalQuiet_SimpleInline_IgnoresStrayAltScreen_Issue897(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previousHotkeys })

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeSimpleInline
	pf.ShowPanels = false
	// The stray flip: nothing meaningful sets this in SimpleInline, but
	// nothing prevents it either, and #1376 showed it happens in practice.
	pf.TermView.UseAltScreen = true
	vtui.FrameManager.Push(pf)

	hm := keymap.GlobalHotkeysMgr
	if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", "F3"); got != "Terminal.ViewLog" {
		t.Errorf("Terminal F3 under SimpleInline with a stray UseAltScreen flip = %q, want Terminal.ViewLog", got)
	}
	if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", "F4"); got != "Terminal.EditLog" {
		t.Errorf("Terminal F4 under SimpleInline with a stray UseAltScreen flip = %q, want Terminal.EditLog", got)
	}

	// A genuinely busy child (f4 itself running a command) still means the
	// keyboard is not free -- but that state routes through IsPtyBusy/
	// Executing, never through the background TermView, in this mode too.
	pf.Executing = true
	if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", "F3"); got != "Terminal.ViewLog" {
		t.Errorf("Terminal F3 under SimpleInline while f4.Executing = %q, want Terminal.ViewLog (unaffected)", got)
	}
}

// TestHotkeys_ShellActions_TerminalArea_BusyChildOwnsKeys_Issue1376 covers
// a program that draws full screen without the alternate screen, as Far
// Manager does in a Windows console: while it runs, its keys are its own,
// Ctrl+O included, as in far2l. Ctrl+Alt+Z, far2l's key for leaving a
// running command, stays with f4, so the panels can always be reached (#50).
func TestHotkeys_ShellActions_TerminalArea_BusyChildOwnsKeys_Issue1376(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previousHotkeys })

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = false
	pf.Executing = true
	vtui.FrameManager.Push(pf)

	hm := keymap.GlobalHotkeysMgr
	keys := []string{"F2", "F7", "F10", "ShiftF4", "ShiftF9", "CtrlF1", "RCtrlF1", "CtrlF2", "RCtrlF2",
		"CtrlP", "AltF1", "AltF2", "CtrlShiftLeft", "CtrlShiftRight", "CtrlO", "RCtrlO",
		// Common-area bindings follow f4 everywhere except into a running program.
		"ShiftF10", "AltF9", "CtrlShiftP", "CtrlF11"}
	for _, key := range keys {
		if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", key); got != "" {
			t.Errorf("Terminal %s while a child is busy = %q, want empty (the key belongs to the program)", key, got)
		}
	}
	if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", "CtrlAltZ"); got != "Panel.Toggle" {
		t.Errorf("Terminal CtrlAltZ while a child is busy = %q, want Panel.Toggle (the escape hatch)", got)
	}

	// Panels raised over the running program: the keyboard is f4's again,
	// so its keys work in its panels and Ctrl+O hands the terminal back.
	// With the panels shown the dispatcher resolves keys in the Shell area.
	pf.ShowPanels = true
	for key, want := range map[string]string{"F7": "File.MakeDir", "F10": "App.Quit", "CtrlO": "Panel.Toggle"} {
		if got := keymap.ConfiguredHotkeyAction(hm, "Shell", key); got != want {
			t.Errorf("Shell %s over a busy child = %q, want %s", key, got, want)
		}
	}

	// The same keys come back to f4 once the child is gone (#354).
	pf.ShowPanels = false
	pf.Executing = false
	for key, want := range map[string]string{"F7": "File.MakeDir", "F10": "App.Quit", "CtrlO": "Panel.Toggle",
		"ShiftF10": "App.LastMenuItem"} {
		if got := keymap.ConfiguredHotkeyAction(hm, "Terminal", key); got != want {
			t.Errorf("Terminal %s in the idle terminal = %q, want %s", key, got, want)
		}
	}
}

// TestPanelsFrame_F2_OpensUserMenu_WhenPanelsHidden is the end-to-end
// half of the issue #354 fix: with panels hidden, pressing F2 must
// push the user-menu frame onto the top of the stack.
func TestPanelsFrame_F2_OpensUserMenu_WhenPanelsHidden_Issue354(t *testing.T) {
	// The assertion below compares the top frame type before and after F2, so a
	// menu left on the shared manager by an earlier test makes the push
	// invisible. Start from a manager of our own.
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	previousHotkeys := keymap.GlobalHotkeysMgr
	previousMacros := macro.MacroMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	macro.MacroMgr = macro.NewMacroManager("")
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = previousHotkeys
		macro.MacroMgr = previousMacros
	})
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = false

	before := topFrameType()

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F2,
	})

	after := topFrameType()
	if after == before {
		t.Fatalf("F2 with hidden panels did not push a new top frame (still %v)", after)
	}
	if after != vtui.TypeMenu {
		t.Errorf("F2 with hidden panels pushed frame of type %v, want VMenu (Panel.UserMenu)", after)
	}
}

// TestPanelsFrame_CtrlL_RevealsHiddenPassivePanel_Issue354 exercises
// the second bug from issue #354: with the passive panel hidden
// (Ctrl+F1 or Ctrl+F2), Ctrl+L installed the panel.InfoPanel into the
// invisible slot and looked like a no-op. It must now un-hide the
// slot so the info panel is actually visible.
func TestPanelsFrame_CtrlL_RevealsHiddenPassivePanel_Issue354(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// paneltest.SetupMockPanelsFrame gives activeIdx=1 (right). Hide the passive
	// left panel — the state a user reaches via Ctrl+F1 in real life.
	pf.ShowLeftPanel = false

	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_L,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+L must install InfoPanel on the passive (left) slot")
	}
	if _, ok := pf.AltPanels[0].(*panel.InfoPanel); !ok {
		t.Errorf("expected *InfoPanel on left slot, got %T", pf.AltPanels[0])
	}
	if !pf.ShowLeftPanel {
		t.Error("Ctrl+L must un-hide the passive slot — otherwise the info panel is invisible")
	}
	if pf.ActiveIdx != 1 {
		t.Errorf("Ctrl+L must not move active side; got activeIdx=%d, want 1", pf.ActiveIdx)
	}
}

// topFrameType returns the type of the top frame across all screens
// (or -1 if none) — small helper for the F2 assertion above.
func topFrameType() vtui.FrameType {
	if vtui.FrameManager == nil {
		return -1
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		return -1
	}
	return top.GetType()
}
