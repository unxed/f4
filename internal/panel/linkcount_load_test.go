package panel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#1861: on Windows the directory listing carries no hard link count, so a
// panel showing the "LN" column asks the file system for it while loading.

// linkCountingVFS stands in for vfs.OSVFS on Windows: its listing has no
// link counts, FillLinkCounts adds them.
type linkCountingVFS struct {
	*vfs.OSVFS
	asked atomic.Int32
}

func (v *linkCountingVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	return v.OSVFS.ReadDir(ctx, path, func(items []vfs.VFSItem) {
		for i := range items {
			items[i].KnownMetadata &^= vfs.MetadataNlink
			items[i].Nlink = 0
		}
		onChunk(items)
	})
}

func (v *linkCountingVFS) FillLinkCounts(_ context.Context, _ string, items []vfs.VFSItem) {
	v.asked.Add(1)
	for i := range items {
		items[i].Nlink = 7
		items[i].KnownMetadata |= vfs.MetadataNlink
	}
}

func loadWithMode(t *testing.T, columns string) (*FileSystemPanel, *linkCountingVFS) {
	t.Helper()
	resetPanelViewModes(true)
	t.Cleanup(func() { resetPanelViewModes(true) })
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cols, err := TextToViewSettings(columns, strings.TrimSuffix(strings.Repeat("0,", strings.Count(columns, ",")+1), ","))
	if err != nil {
		t.Fatal(err)
	}
	panelViewModes.overrides[ViewModeDetailed] = &PanelViewSettings{Columns: cols}
	panelViewModes.generation++
	pnl := pf.GetActivePanel()
	pnl.SetViewMode(ViewModeDetailed)
	fs := &linkCountingVFS{OSVFS: vfs.NewOSVFS(dir)}
	pnl.Vfs = fs
	pnl.ReadDirectory()
	waitForDirectoryLoads(t)
	testutil.DrainUITasks()
	return pnl, fs
}

func TestLinkCountsAreFilledWhileTheColumnShows(t *testing.T) {
	pnl, fs := loadWithMode(t, "N,LN")
	if fs.asked.Load() == 0 {
		t.Fatal("the panel did not ask for the link counts")
	}
	for _, e := range pnl.AllEntries() {
		if e.Name == "a.txt" && (!e.HasMetadata(vfs.MetadataNlink) || e.Nlink != 7) {
			t.Fatalf("a.txt has no link count: %+v", e.VFSItem)
		}
	}
}

func TestLinkCountsAreNotAskedWithoutTheColumn(t *testing.T) {
	_, fs := loadWithMode(t, "N,S")
	if n := fs.asked.Load(); n != 0 {
		t.Fatalf("the panel asked for link counts %d times without the LN column", n)
	}
}

func TestSwitchingToTheColumnLoadsTheCounts(t *testing.T) {
	pnl, fs := loadWithMode(t, "N,S")
	columns, err := TextToViewSettings("N,LN", "0,0")
	if err != nil {
		t.Fatal(err)
	}
	panelViewModes.overrides[ViewModeBrief] = &PanelViewSettings{Columns: columns}
	panelViewModes.generation++
	pnl.SetViewMode(ViewModeBrief)
	waitForDirectoryLoads(t)
	testutil.DrainUITasks()
	if fs.asked.Load() == 0 {
		t.Fatal("switching to a mode with LN did not load the counts")
	}
}
