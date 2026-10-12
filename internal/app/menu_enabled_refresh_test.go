package app

import (
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// menuRowDisabled finds the row with the given plain label anywhere in the
// frame's menu bar and reports whether it is dimmed.
func menuRowDisabled(t *testing.T, pf *panel.PanelsFrame, label string) bool {
	t.Helper()
	bar := pf.GetMenuBar()
	if bar == nil {
		t.Fatal("no menu bar")
	}
	for _, top := range bar.Items {
		for _, item := range top.SubItems {
			if action.PlainLabel(item.Text) == label {
				return item.Disabled
			}
			for _, sub := range item.SubItems {
				if action.PlainLabel(sub.Text) == label {
					return sub.Disabled
				}
			}
		}
	}
	t.Fatalf("menu has no row %q", label)
	return false
}

// The menu is cached between frames, but whether a row is dimmed depends on
// where the cursor stands, so it must follow the cursor and not keep what it
// was when the menu was first built (f4#1814: at start every row that acts on a
// file stayed dimmed until the user switched panels).
func TestMenuRowsFollowTheCursorBetweenFrames(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	src := pf.Panels[0].(*panel.FileSystemPanel)
	if err := src.Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pf.ActiveIdx = 0
	vtui.FrameManager.Push(pf)

	// The directory is still being read: nothing under the cursor yet.
	src.Entries = nil
	if !menuRowDisabled(t, pf, "Edit") {
		t.Fatal("Edit must be dimmed while the panel has no entries")
	}

	// The listing arrives and the cursor stands on a file.
	src.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "test.txt"}},
	}
	src.SetCursorIndex(1)
	if menuRowDisabled(t, pf, "Edit") {
		t.Error("Edit stayed dimmed after the cursor moved onto a file")
	}

	// And back onto "..": dimmed again.
	src.SetCursorIndex(0)
	if !menuRowDisabled(t, pf, "Edit") {
		t.Error("Edit must be dimmed with the cursor on \"..\"")
	}
}
