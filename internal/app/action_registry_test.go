package app

import (
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

func TestActionRegistry(t *testing.T) {
	preserveActionRegistry(t)
	called := false
	testAction := action.Action{
		Name:        "Test.Action",
		Label:       "Test Label",
		Description: "Test Description",
		Handler: func() bool {
			called = true
			return true
		},
	}

	action.RegisterAction(testAction)

	// Test GetAction
	a, ok := GetAction("test.action")
	if !ok {
		t.Fatal("Expected to find Test.Action")
	}
	if a.Label != "Test Label" || a.Description != "Test Description" {
		t.Errorf("Action fields mismatch. Got %+v", a)
	}

	// Test action.AllSorted
	actions := action.AllSorted()
	found := false
	for _, act := range actions {
		if act.Name == "Test.Action" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Action not found in GetActions() result")
	}

	// Test RunAction
	if !RunAction("Test.action") {
		t.Error("RunAction failed")
	}
	if !called {
		t.Error("Action handler was not executed")
	}

	// Test missing action
	if RunAction("Missing.Action") {
		t.Error("RunAction should return false for missing action")
	}
}
func TestRegistry_HexModeAndWorkspaceActions(t *testing.T) {
	viewHex, ok := GetAction("File.ViewHex")
	if !ok {
		t.Fatal("File.ViewHex should be registered")
	}
	if len(viewHex.DefaultKeys) != 2 || viewHex.DefaultKeys[0] != "AltF3" || viewHex.DefaultKeys[1] != "CtrlShiftF3" {
		t.Fatalf("File.ViewHex default keys = %v, want [AltF3 CtrlShiftF3]", viewHex.DefaultKeys)
	}
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()
	if got := hm.GetAction("Shell", "AltF3"); got != "File.ViewHex" {
		t.Fatalf("Shell/AltF3 = %q, want File.ViewHex", got)
	}
	if got := hm.GetAction("Shell", "CtrlShiftF3"); got != "File.ViewHex" {
		t.Fatalf("Shell/CtrlShiftF3 = %q, want File.ViewHex", got)
	}

	a, ok := GetAction("Editor.HexMode")
	if !ok {
		t.Fatal("Editor.HexMode should be registered")
	}
	if a.MenuPath != "Options" {
		t.Errorf("MenuPath = %q, want Options", a.MenuPath)
	}

	_, ok = GetAction("Workspace.New")
	if !ok {
		t.Fatal("Workspace.New should be registered")
	}
	fork, ok := GetAction("Workspace.Fork")
	if !ok {
		t.Fatal("Workspace.Fork should be registered")
	}
	if len(fork.DefaultKeys) != 1 || fork.DefaultKeys[0] != "CtrlF11" {
		t.Fatalf("Workspace.Fork default keys = %v, want [CtrlF11]", fork.DefaultKeys)
	}
}

func TestHotkeyManager_BookmarksDefault(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()

	if got := hm.GetAction("Shell", "CtrlShiftVK_DC"); got != "Panel.Bookmarks" {
		t.Fatalf("Shell/CtrlShiftVK_DC = %q, want Panel.Bookmarks", got)
	}
}

func TestHotkeyManager_PanelPathDefaults(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()

	if got := hm.GetAction("Shell", "CtrlD"); got != "Panel.CopyPath" {
		t.Fatalf("Shell/CtrlD = %q, want Panel.CopyPath", got)
	}
	if got := hm.GetAction("Shell", "CtrlF"); got != "Panel.InsertPath" {
		t.Fatalf("Shell/CtrlF = %q, want Panel.InsertPath", got)
	}
}

func TestHotkeyManager_SyncPanelsDoesNotStealAltI(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()

	if got := hm.GetAction("Shell", "AltI"); got == "Panel.SyncPanels" {
		t.Fatal("Shell/AltI must remain available to panel fast find")
	}
	if got := hm.GetAction("Shell", "AltShiftI"); got != "Panel.SyncPanels" {
		t.Fatalf("Shell/AltShiftI = %q, want Panel.SyncPanels", got)
	}
}

func TestHotkeyManager_ViewerEditorSearchDirections(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()

	cases := []struct {
		area, key, want string
	}{
		{"Editor", "CtrlEnter", "Editor.SearchForward"},
		{"Editor", "AltF7", "Editor.SearchPrevious"},
		{"Editor", "CtrlShiftEnter", "Editor.InsertPassivePanelFileName"},
		{"Viewer", "CtrlEnter", "Viewer.SearchNext"},
		{"Viewer", "ShiftF7", "Viewer.SearchNext"},
		{"Viewer", "CtrlShiftEnter", "Viewer.SearchPrevious"},
	}
	for _, tc := range cases {
		if got := hm.GetAction(tc.area, tc.key); got != tc.want {
			t.Errorf("%s/%s = %q, want %q", tc.area, tc.key, got, tc.want)
		}
	}
}

func TestAction_PanelToggleHidden(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	original := config.App.ShowHiddenFiles
	defer func() { config.App.ShowHiddenFiles = original }()

	if !RunAction("Panel.ToggleHidden") {
		t.Fatal("Panel.ToggleHidden did not run")
	}
	if config.App.ShowHiddenFiles == original {
		t.Errorf("Panel.ToggleHidden did not flip ShowHiddenFiles (was %v, still %v)", original, config.App.ShowHiddenFiles)
	}

	if !RunAction("Panel.ToggleHidden") {
		t.Fatal("Panel.ToggleHidden did not run on second call")
	}
	if config.App.ShowHiddenFiles != original {
		t.Errorf("Panel.ToggleHidden second call did not restore ShowHiddenFiles (want %v, got %v)", original, config.App.ShowHiddenFiles)
	}
}

func TestActionPanelToggleTargetsActiveWorkspace(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	first := &panel.PanelsFrame{ShowPanels: true, ShowLeftPanel: true, ShowRightPanel: true}
	active := &panel.PanelsFrame{ShowPanels: true, ShowLeftPanel: true, ShowRightPanel: true}
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{
		{Number: 1, Frames: []vtui.Frame{first}},
		{Number: 2, Frames: []vtui.Frame{active}},
	}, 1))

	if !RunAction("Panel.Toggle") {
		t.Fatal("Panel.Toggle did not run")
	}
	if active.ShowPanels {
		t.Fatal("Panel.Toggle did not hide panels in the active workspace")
	}
	if !first.ShowPanels {
		t.Fatal("Panel.Toggle changed panels in the first, inactive workspace")
	}
}

// Issue #927: Ctrl+F1 / Ctrl+F2 hide one panel and leave the other one on
// its own half, as far2l does; it must not grow to the whole width.
func TestActionPanelToggleSidePanelKeepsOtherPanelHalfWidth_Issue927(t *testing.T) {
	for _, tc := range []struct {
		action  string
		hidden  int
		visible int
		wantX1  int
		wantX2  int
	}{
		{action: "Panel.ToggleRightPanel", hidden: 1, visible: 0, wantX1: 0, wantX2: 39},
		{action: "Panel.ToggleLeftPanel", hidden: 0, visible: 1, wantX1: 40, wantX2: 79},
	} {
		t.Run(tc.action, func(t *testing.T) {
			t.Cleanup(paneltest.SwapFrameManager(t))
			vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
			pf := paneltest.SetupMockPanelsFrame(t)
			defer pf.Close()
			pf.ResizeConsole(80, 25)
			vtui.FrameManager.Push(pf)

			shown := func(idx int) bool {
				if idx == 0 {
					return pf.ShowLeftPanel
				}
				return pf.ShowRightPanel
			}

			if !RunAction(tc.action) {
				t.Fatalf("%s did not run", tc.action)
			}
			if shown(tc.hidden) {
				t.Fatalf("%s did not hide panel %d", tc.action, tc.hidden)
			}
			if x1, _, x2, _ := pf.Panels[tc.visible].GetPosition(); x1 != tc.wantX1 || x2 != tc.wantX2 {
				t.Fatalf("visible panel geometry = %d..%d, want %d..%d", x1, x2, tc.wantX1, tc.wantX2)
			}

			if !RunAction(tc.action) {
				t.Fatalf("%s second call did not run", tc.action)
			}
			if !shown(tc.hidden) {
				t.Fatalf("%s second call did not restore panel %d", tc.action, tc.hidden)
			}
			if x1, _, x2, _ := pf.Panels[tc.visible].GetPosition(); x1 != tc.wantX1 || x2 != tc.wantX2 {
				t.Fatalf("restored panel geometry = %d..%d, want %d..%d", x1, x2, tc.wantX1, tc.wantX2)
			}
		})
	}
}
