package panel

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// Issue #128: a workspace opened by Ctrl+Shift+O is a terminal and nothing
// else -- no key may bring panels into it, and the keys that would (Ctrl+O,
// Esc, Del, the file-manager keys) have to reach the shell instead.
func TestTerminalOnlyWorkspaceHasNoWayBackToPanels(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	oldEsc := config.App.EscTogglePanels
	config.App.EscTogglePanels = true
	defer func() { config.App.EscTogglePanels = oldEsc }()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeOwn
	fm.Push(pf)

	if !ActionWorkspaceNewTerminal() {
		t.Fatal("Workspace.NewTerminal reported the request as unhandled")
	}
	clone, ok := fm.GetTopFrame().(*PanelsFrame)
	if !ok || clone == pf {
		t.Fatalf("top frame = %T, want the forked *PanelsFrame", fm.GetTopFrame())
	}
	defer clone.Close()

	if !clone.TerminalOnly || !clone.PanelsLocked() {
		t.Fatal("the terminal workspace is not marked as terminal-only")
	}
	if pf.TerminalOnly || pf.PanelsLocked() {
		t.Fatal("the original panels must stay a normal workspace")
	}

	clone.TogglePanelsVisibility()
	if clone.ShowPanels {
		t.Error("Ctrl+O brought panels into the terminal-only workspace")
	}
	clone.SetWidePanel(0)
	if clone.ShowPanels {
		t.Error("a wide panel request brought panels into the terminal-only workspace")
	}

	// The keys gated by these conditions belong to the shell here.
	for _, name := range []string{"esctoggle", "noaltscreenapp", "noterminalapp"} {
		if hotkeyConditions[name]() {
			t.Errorf("condition %q is true in the terminal-only workspace; the key would not reach the shell", name)
		}
	}

	// The workspace the terminal was opened from keeps its Ctrl+O.
	fm.SwitchScreen(0)
	pf.ShowPanels = true
	pf.TogglePanelsVisibility()
	if pf.ShowPanels {
		t.Error("Ctrl+O no longer hides the panels of an ordinary workspace")
	}
}
