package panel

import (
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

// #128 (montoner0): the key bar of a terminal-only workspace names only the
// keys that still do something there; Copy, Move, MkDir, Delete and Quit have
// no panels to act on.
func TestTerminalOnlyWorkspaceKeyBarNamesOnlyWorkingKeys(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
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

	labels := clone.GetKeyLabels().Normal
	for _, i := range []int{1, 4, 5, 6, 7, 9, 10} {
		if labels[i] != "" {
			t.Errorf("F%d is labelled %q in the terminal-only workspace", i+1, labels[i])
		}
	}
	for _, i := range []int{0, 8, 11} {
		if labels[i] == "" {
			t.Errorf("F%d lost its label in the terminal-only workspace", i+1)
		}
	}
	if pf.GetKeyLabels().Normal[4] == "" {
		t.Error("the panels workspace lost its F5 label")
	}
}
