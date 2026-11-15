package panel

import (
	"testing"

	"github.com/unxed/vtinput"
)

func TestTempPanelParentActivationReturnsToSourceFolder(t *testing.T) {
	pf, fsp := livePanels(t)
	parent := fsp.Vfs
	parentPath := parent.GetPath()

	pf.SwitchToVFS(fsp, NewTempPanelVFS(nil, &TempPanelStore{}, 0))
	waitForLoad(t, fsp)
	for i, entry := range fsp.Entries {
		if entry.Name == ".." {
			fsp.CursorIdx = i
			break
		}
	}
	if got := fsp.GetRawSelectedName(); got != ".." {
		t.Fatalf("selected row = %q, want parent row", got)
	}

	if !pf.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	}) {
		t.Fatal("activation was not handled")
	}
	if fsp.Vfs != parent || fsp.Vfs.GetPath() != parentPath {
		t.Fatalf("parent activation left panel on %T at %q; want source %T at %q", fsp.Vfs, fsp.Vfs.GetPath(), parent, parentPath)
	}
}
