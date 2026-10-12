package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#1832: the menu bar is asked for on every key press and every frame, so
// what it costs must not grow with the size of the folder. After the change of
// f4#1814 each call walked the listing once per row that has an Enabled
// function, and moving the cursor in a big folder became slow. These tests
// count the walks instead of timing them, so they hold on a slow runner too.
func bigFolderFrame(t *testing.T, entries int) (*panel.PanelsFrame, *panel.FileSystemPanel) {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	src := pf.Panels[0].(*panel.FileSystemPanel)
	if err := src.Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pf.ActiveIdx = 0
	vtui.FrameManager.Push(pf)
	list := []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: ".."}}}
	for i := 0; i < entries; i++ {
		list = append(list, &panel.FileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("file%06d.txt", i)}})
	}
	src.Entries = list
	src.SetCursorIndex(1)
	return pf, src
}

func TestMenuBarCostDoesNotGrowWithTheFolder(t *testing.T) {
	pf, src := bigFolderFrame(t, 50000)
	// The rows are also asked again every menuRowRefreshEvery; on a slow runner
	// 200 calls can outlast it, so the test turns the timer off and counts only
	// what the context decides.
	menuRowEnabledMu.Lock()
	savedEvery := menuRowRefreshEvery
	menuRowRefreshEvery = time.Hour
	menuRowEnabledMu.Unlock()
	t.Cleanup(func() {
		menuRowEnabledMu.Lock()
		menuRowRefreshEvery = savedEvery
		menuRowEnabledMu.Unlock()
	})
	pf.GetMenuBar() // the first call builds the menu

	// Asked again and again with nothing changed: no walk of the listing.
	before := panel.SelectionWalks()
	for i := 0; i < 200; i++ {
		pf.GetMenuBar()
	}
	if walks := panel.SelectionWalks() - before; walks != 0 {
		t.Errorf("200 menu bar calls in one state walked the listing %d times, want 0", walks)
	}

	// One step of the cursor: a couple of walks for the whole menu, not one
	// per row, and the rows still follow the cursor.
	before = panel.SelectionWalks()
	const steps = 20
	for i := 0; i < steps; i++ {
		src.SetCursorIndex(2 + i)
		pf.GetMenuBar()
		pf.GetMenuBar()
		pf.GetMenuBar() // vtui asks two or three times per key
	}
	if walks := panel.SelectionWalks() - before; walks > 3*steps {
		t.Errorf("%d cursor steps walked the listing %d times, want at most %d (a few per step, not one per menu row)", steps, walks, 3*steps)
	}
	if menuRowDisabled(t, pf, "Edit") {
		t.Error("Edit is dimmed with the cursor on a file")
	}
	src.SetCursorIndex(0)
	if !menuRowDisabled(t, pf, "Edit") {
		t.Error("Edit must be dimmed with the cursor on \"..\"")
	}
}
