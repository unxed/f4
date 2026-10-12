package panel

import (
	"testing"

	"github.com/unxed/vtui"
)

type pastingAltPanel struct {
	*InfoPanel
	pasted int
}

func (p *pastingAltPanel) PasteClipboard() bool { p.pasted++; return true }

// f4#1842: Ctrl+V with a focused panel that has a paste of its own (the AI
// chat) goes to that panel, not to the folder or the command line.
func TestPasteGoesToFocusedAltPanelWithItsOwnPaste(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	src, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel)
	if !ok {
		t.Fatalf("active panel is %T", pf.Panels[pf.ActiveIdx])
	}
	alt := &pastingAltPanel{InfoPanel: NewInfoPanel(src)}
	pf.AltPanels[pf.ActiveIdx] = alt
	alt.SetFocus(true)
	if !ActionPasteClipboard(pf) || alt.pasted != 1 {
		t.Fatalf("the paste did not reach the focused panel (pasted %d times)", alt.pasted)
	}
	// An unfocused panel is not asked: ActionPasteClipboard then reads the
	// real clipboard in the background, which would outlive this test and
	// race the next one for vtui.FrameManager (train f4#1880), so that path
	// is left to the existing paste tests.
}
