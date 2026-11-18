package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestActionFoldersHistoryWithoutProvider(t *testing.T) {
	old := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = nil
	t.Cleanup(func() { vtui.GlobalHistoryProvider = old })

	actionFoldersHistory(nil)
}

func TestActionCommandHistoryEmpty(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.CmdLine.Edit.History = nil

	actionCommandHistory(pf)
	if vtui.FrameManager.GetTopFrame() == nil {
		t.Fatal("empty command history did not show a dialog")
	}
	vtui.FrameManager.Pop()
}

func TestActionSortMenuWithoutPanel(t *testing.T) {
	actionSortMenuForPanel(nil, nil)
}

func TestActionSortMenuSelectsEveryMode(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	if fsp == nil {
		t.Fatal("panels frame has no active panel")
	}

	actionSortMenuForPanel(pf, fsp)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want sort menu", vtui.FrameManager.GetTopFrame())
	}
	defer menu.Close()
	for i, want := range []panel.SortMode{panel.SortName, panel.SortExt, panel.SortTime, panel.SortSize, panel.SortUnsorted} {
		menu.OnAction(i)
		if fsp.SortMode != want {
			t.Fatalf("sort mode after action %d = %v, want %v", i, fsp.SortMode, want)
		}
	}
}

func TestActionSortMenuTogglesGroups(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	if fsp == nil {
		t.Fatal("panels frame has no active panel")
	}

	actionSortMenuForPanel(pf, fsp)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	defer menu.Close()
	want := !fsp.UseSortGroups
	menu.OnAction(6)
	if fsp.UseSortGroups != want {
		t.Fatalf("UseSortGroups after toggle = %v, want %v", fsp.UseSortGroups, want)
	}
}

func TestFindOpenedEditorWithoutFrameManager(t *testing.T) {
	old := vtui.FrameManager
	vtui.FrameManager = nil
	t.Cleanup(func() { vtui.FrameManager = old })

	if got, idx := findOpenedEditor(nil, "file.txt"); got != nil || idx != -1 {
		t.Fatalf("findOpenedEditor without frame manager = (%v, %d), want (nil, -1)", got, idx)
	}
}

func TestFindOpenedViewerWithoutFrameManager(t *testing.T) {
	old := vtui.FrameManager
	vtui.FrameManager = nil
	t.Cleanup(func() { vtui.FrameManager = old })

	if got, idx := findOpenedViewer(nil, "file.txt"); got != nil || idx != -1 {
		t.Fatalf("findOpenedViewer without frame manager = (%v, %d), want (nil, -1)", got, idx)
	}
}

func TestFindOpenedEditorMatchesLocalPath(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	ev := &editor.EditorView{Vfs: v, FilePath: "target.txt"}
	vtui.FrameManager.Screens = []*vtui.AppScreen{{Number: 1, Frames: []vtui.Frame{ev}}}

	got, idx := findOpenedEditor(v, "target.txt")
	if got != ev || idx != 0 {
		t.Fatalf("findOpenedEditor local match = (%v, %d), want (%v, 0)", got, idx, ev)
	}
	if got, idx := findOpenedEditor(v, "other.txt"); got != nil || idx != -1 {
		t.Fatalf("findOpenedEditor local miss = (%v, %d), want (nil, -1)", got, idx)
	}
}

func TestFindOpenedViewerMatchesLocalPath(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())
	vv := &viewer.ViewerView{VFS: v, Path: "target.txt"}
	vtui.FrameManager.Screens = []*vtui.AppScreen{{Number: 1, Frames: []vtui.Frame{vv}}}

	got, idx := findOpenedViewer(v, "target.txt")
	if got != vv || idx != 0 {
		t.Fatalf("findOpenedViewer local match = (%v, %d), want (%v, 0)", got, idx, vv)
	}
	if got, idx := findOpenedViewer(v, "other.txt"); got != nil || idx != -1 {
		t.Fatalf("findOpenedViewer local miss = (%v, %d), want (nil, -1)", got, idx)
	}
}

func TestFileListedAsOnlyMatchesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/target.txt", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir+"/target-dir", 0700); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(dir)

	if !fileListedAs(context.Background(), v, dir, "target.txt") {
		t.Fatal("fileListedAs did not find the regular file")
	}
	if fileListedAs(context.Background(), v, dir, "target-dir") {
		t.Fatal("fileListedAs treated a directory as a file")
	}
	if fileListedAs(context.Background(), v, dir, "missing.txt") {
		t.Fatal("fileListedAs found a missing file")
	}
}

// The check mark is missing from many console fonts; the sort menu marks the
// mode in force with its direction and an option with the square-root sign
// (f4#1769).
func TestActionSortMenuMarkersNeedNoCheckMarkGlyph(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	if fsp == nil {
		t.Fatal("panels frame has no active panel")
	}
	fsp.UseSortGroups = true
	fsp.SetSortMode(panel.SortName)

	actionSortMenuForPanel(pf, fsp)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	defer menu.Close()
	for i, item := range menu.Items {
		if strings.Contains(item.Text, "✓") {
			t.Errorf("row %d still uses the check mark: %q", i, item.Text)
		}
	}
	if want := panel.SortModeMarker(fsp.SortIsAscending()) + " "; !strings.HasPrefix(menu.Items[0].Text, want) {
		t.Errorf("sort by name row = %q, want the marker %q", menu.Items[0].Text, want)
	}
	if !strings.HasPrefix(menu.Items[6].Text, panel.MenuCheckMark+" ") {
		t.Errorf("sort groups row = %q, want the %q marker", menu.Items[6].Text, panel.MenuCheckMark)
	}
	if panel.SortModeMarker(true) == panel.SortModeMarker(false) {
		t.Error("ascending and descending use the same marker")
	}
}
