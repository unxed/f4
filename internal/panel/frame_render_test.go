package panel

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtui"
)

func TestBusyOwnTerminalPaintsReservedKeybarRow(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShellMode = terminal.ShellModeOwn
	pf.Pty = &mockPty{}
	pf.ShowKeyBar = true
	pf.ShowPanels = false
	pf.Executing = true
	pf.ResizeConsole(80, 25)
	pf.LastAutoRefresh = time.Now()
	vtui.FrameManager.Push(pf)

	// Reproduce a stale blue row left outside the terminal's viewport.
	blue := vtui.SetIndexBoth(0, 7, 1)
	scr.FillRect(0, 24, 79, 24, 'X', blue)
	scr.SetOverlayMode(true)
	pf.Show(scr)
	if pf.TermView.Y2 != 23 || vtui.FrameManager.KeyBar != nil {
		t.Fatal("busy terminal changed its reserved geometry or exposed the keybar")
	}
	for x := 0; x < 80; x++ {
		cell := scr.GetCell(x, 24)
		if vtui.CellString(cell.Char) != " " || cell.Attributes != terminal.DefaultTermAttr {
			t.Fatalf("reserved cell %d: %+v, want a blank terminal background", x, cell)
		}
	}

	// Alternate-screen applications own the last row, so it must not be
	// cleared by the reserved-row repair.
	pf.TermView.SetAltScreen(true)
	pf.ResizeConsole(80, 25)
	pf.TermView.SetCursor(0, pf.TermView.Height-1)
	pf.TermView.PutChar('Z', terminal.DefaultTermAttr)
	pf.Show(scr)
	if pf.TermView.Y2 != 24 {
		t.Fatal("alternate screen lost the last terminal row")
	}
	if got := vtui.CellString(scr.GetCell(0, 24).Char); got != "Z" {
		t.Fatalf("alternate screen's bottom row was erased: %q", got)
	}
}
