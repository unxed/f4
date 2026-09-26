package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"testing"

	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// Issue #128: the console should be able to live in a workspace of its own, so
// that Ctrl+Tab flips between files and terminal, instead of always taking the
// place of the panels it was opened from.
func TestPanelsFrame_NewTerminalWorkspaceKeepsPanelsWhereTheyAre(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeOwn
	fm.Push(pf)

	if !panel.ActionWorkspaceNewTerminal() {
		t.Fatal("Workspace.NewTerminal reported the request as unhandled")
	}
	if len(fm.Screens) != 2 {
		t.Fatalf("action created %d workspaces, want 2", len(fm.Screens))
	}
	clone, ok := fm.GetTopFrame().(*panel.PanelsFrame)
	if !ok {
		t.Fatalf("new workspace top frame = %T, want *PanelsFrame", fm.GetTopFrame())
	}
	defer clone.Close()
	if clone == pf {
		t.Fatal("the action reused the original panels instead of forking them")
	}
	if clone.ShowPanels {
		t.Error("the new workspace did not switch to its console view")
	}
	if !pf.ShowPanels {
		t.Error("opening a terminal workspace must leave the original panels visible")
	}
	if got := clone.GetWorkspaceTabTitle(); got != "Terminal" {
		t.Errorf("workspace tab title = %q, want \"Terminal\"", got)
	}

	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[1].(*panel.FileSystemPanel))
}

// Where command output can only be captured into a dialog there is no console
// view to hand a workspace to. The action then reports the environment, as
// Ctrl+O does, rather than leaving a second identical copy of the panels open.
func TestPanelsFrame_NewTerminalWorkspaceSkippedWithoutConsoleView(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeSimpleCaptured
	fm.Push(pf)

	if !panel.ActionWorkspaceNewTerminal() {
		t.Fatal("Workspace.NewTerminal reported the request as unhandled")
	}
	if len(fm.Screens) != 1 {
		t.Fatalf("captured-output environment created %d workspaces, want 1", len(fm.Screens))
	}
	if !pf.ShowPanels {
		t.Error("panels must stay visible where no console view exists")
	}

	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}

// Issue #128 (follow-up): the left/right panel menu's "Terminal in New
// Workspace" item must fire the very same Workspace.NewTerminal action as its
// Ctrl+Shift+O hotkey. Reported bug: selecting the menu item did nothing,
// while the hotkey kept working. The side menus carry this item as a plain
// appcmd.Command (like Ctrl+N's "New Workspace" next to it), which a real
// selection resolves through vtui's FireAction fallback into
// FrameManager.EmitCommand(item.Command, ...) -- so that is what this test
// drives, instead of calling panel.ActionWorkspaceNewTerminal directly (which
// already has its own, passing, coverage in TestPanelsFrame_NewTerminal*
// above and would not have caught this wiring gap).
func TestWorkspaceNewTerminalMenuItemOpensTerminalWorkspace(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeOwn
	fm.Push(pf)

	item, ok := findMenuItemByCommand(pf.LeftMenu().SubItems, appcmd.CmWorkspaceNewTerminal)
	if !ok {
		t.Fatal("left panel menu has no \"Terminal in New Workspace\" item")
	}
	if item.OnClick != nil {
		// Side menu items dispatch through their Command field, not OnClick;
		// if that ever changes this test needs to drive the click the new way.
		t.Fatal("\"Terminal in New Workspace\" item unexpectedly has OnClick set")
	}

	if !fm.EmitCommand(item.Command, 0) {
		t.Fatal("selecting \"Terminal in New Workspace\" was not handled")
	}
	if len(fm.Screens) != 2 {
		t.Fatalf("menu selection created %d workspaces, want 2", len(fm.Screens))
	}
	clone, ok := fm.GetTopFrame().(*panel.PanelsFrame)
	if !ok {
		t.Fatalf("new workspace top frame = %T, want *PanelsFrame", fm.GetTopFrame())
	}
	defer clone.Close()
	if clone == pf {
		t.Fatal("the menu item reused the original panels instead of forking them")
	}
	if clone.ShowPanels {
		t.Error("the new workspace did not switch to its console view")
	}
	if !pf.ShowPanels {
		t.Error("opening a terminal workspace must leave the original panels visible")
	}

	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[1].(*panel.FileSystemPanel))
}

func findMenuItemByCommand(items []vtui.MenuItem, command int) (vtui.MenuItem, bool) {
	for _, item := range items {
		if item.Command == command {
			return item, true
		}
	}
	return vtui.MenuItem{}, false
}
