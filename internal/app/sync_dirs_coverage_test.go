package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestSyncPathAndNames(t *testing.T) {
	if got := syncPathLine("Left", "C:/a/very-long-folder", 4); !strings.HasPrefix(got, "Left ") || got == "Left C:/a/very-long-folder" {
		t.Fatalf("syncPathLine() = %q", got)
	}
	if syncPathLine("Right", "folder", 20) != "Right folder" {
		t.Fatal("wide path line changed the path")
	}
	fs := vfs.NewOSVFS(t.TempDir())
	root := fs.GetPath()
	if got := syncSidePath(fs, root, "one//two.txt"); got != filepath.Join(root, "one", "two.txt") {
		t.Fatalf("syncSidePath() = %q", got)
	}
}

func TestSyncSnapshotsTrackVFSPathAndEpoch(t *testing.T) {
	fs := vfs.NewOSVFS(t.TempDir())
	fsp := &panel.FileSystemPanel{Vfs: fs, DirectoryEpoch: 7}
	snapshot, ok := captureSyncPanel(fsp)
	if !ok || !snapshot.stillCurrent() {
		t.Fatal("valid sync snapshot was not current")
	}
	fsp.DirectoryEpoch++
	if snapshot.stillCurrent() {
		t.Fatal("changed directory epoch remained current")
	}
	fsp.DirectoryEpoch = 7
	fsp.Vfs = nil
	if snapshot.stillCurrent() {
		t.Fatal("detached panel remained current")
	}
	if _, ok := captureSyncPanel(nil); ok {
		t.Fatal("nil panel was captured")
	}
}

func TestSyncGroupsAndTokens(t *testing.T) {
	for _, tt := range []struct {
		pair  fileops.SyncPair
		group int
	}{
		{fileops.SyncPair{State: fileops.SyncLeftOnly}, syncGroupToRight},
		{fileops.SyncPair{State: fileops.SyncLeftNewer}, syncGroupToRight},
		{fileops.SyncPair{State: fileops.SyncRightOnly}, syncGroupToLeft},
		{fileops.SyncPair{State: fileops.SyncRightNewer}, syncGroupToLeft},
		{fileops.SyncPair{State: fileops.SyncEqual}, syncGroupEqual},
		{fileops.SyncPair{State: fileops.SyncDiffers}, syncGroupDiffers},
		{fileops.SyncPair{Locked: true, State: fileops.SyncEqual}, syncGroupDiffers},
	} {
		if got := syncGroupOf(tt.pair); got != tt.group {
			t.Errorf("syncGroupOf(%#v) = %d, want %d", tt.pair, got, tt.group)
		}
	}
	for _, tt := range []struct {
		a    fileops.SyncAction
		want string
	}{
		{fileops.SyncCopyToRight, syncTokenToRight}, {fileops.SyncCopyToLeft, syncTokenToLeft},
		{fileops.SyncDeleteRight, syncTokenDeleteRight}, {fileops.SyncDeleteLeft, syncTokenDeleteLeft},
		{fileops.SyncSkip, syncTokenSkip},
	} {
		if got := syncActionToken(fileops.SyncPair{Action: tt.a}); got != tt.want {
			t.Errorf("syncActionToken(%d) = %q, want %q", tt.a, got, tt.want)
		}
	}
	if syncActionToken(fileops.SyncPair{State: fileops.SyncEqual}) != syncTokenEqual || syncActionToken(fileops.SyncPair{State: fileops.SyncDiffers}) != syncTokenDiffers {
		t.Fatal("comparison tokens are wrong")
	}
}

func TestSyncRowsFormatBothSides(t *testing.T) {
	when := time.Date(2026, time.January, 2, 3, 4, 0, 0, time.UTC)
	pair := fileops.SyncPair{Rel: "dir/file.txt", HasLeft: true, HasRight: true, Left: vfs.VFSItem{Size: 12, MTime: when}, Right: vfs.VFSItem{Size: 34, MTime: when}, Action: fileops.SyncCopyToRight}
	row := syncRow{pair: &pair}
	if row.GetCellText(0) != "dir/file.txt" || row.GetCellText(1) == "" || row.GetCellText(2) != "02.01.26 03:04" || row.GetCellText(3) != syncTokenToRight || row.GetCellText(4) != "02.01.26 03:04" || row.GetCellText(5) == "" {
		t.Fatalf("formatted sync row = %#v", row)
	}
	if row.GetCellText(99) != "" {
		t.Fatal("unknown sync column was not empty")
	}
	if syncSizeCell(false, pair.Left) != "" || syncSizeCell(true, vfs.VFSItem{IsDir: true}) == "" || syncDateCell(true, vfs.VFSItem{}) != "" {
		t.Fatal("empty and directory cells are wrong")
	}
}

func TestSyncSidePathSkipsEmptyComponents(t *testing.T) {
	fs := vfs.NewOSVFS(t.TempDir())
	root := fs.GetPath()
	for _, rel := range []string{"/a/b", "a//b/", "a///b"} {
		if got := syncSidePath(fs, root, rel); got != filepath.Join(root, "a", "b") {
			t.Errorf("syncSidePath(%q) = %q", rel, got)
		}
	}
}

func TestSyncSelectedPlanFiltersDirections(t *testing.T) {
	pairs := []fileops.SyncPair{
		{Rel: "right", Action: fileops.SyncCopyToRight},
		{Rel: "left", Action: fileops.SyncCopyToLeft},
		{Rel: "delete-right", Action: fileops.SyncDeleteRight},
		{Rel: "delete-left", Action: fileops.SyncDeleteLeft},
		{Rel: "skip", Action: fileops.SyncSkip},
	}
	got := syncSelectedPlan(pairs, true, false, true)
	if len(got) != 3 || got[0].Rel != "right" || got[1].Rel != "delete-right" || got[2].Rel != "delete-left" {
		t.Fatalf("selected plan = %#v", got)
	}
	if got := syncSelectedPlan(pairs, false, false, false); len(got) != 0 {
		t.Fatalf("disabled plan = %#v", got)
	}
}

func TestSyncPanelCapabilityNeedsTwoPanels(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	if panelCanSyncDirs() {
		t.Fatal("sync capability reported without panels")
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	if panelCanSyncDirs() {
		t.Fatal("sync capability reported with an empty screen")
	}
}

func TestShowSyncResultsBuildsFilteredTable(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(100, 30)
	vtui.FrameManager.Init(screen)
	pairs := []fileops.SyncPair{{Rel: "copy", HasLeft: true, State: fileops.SyncLeftOnly, Action: fileops.SyncCopyToRight, Left: vfs.VFSItem{Size: 10}}, {Rel: "equal", HasLeft: true, HasRight: true, State: fileops.SyncEqual}, {Rel: "delete", HasRight: true, State: fileops.SyncRightOnly, Action: fileops.SyncDeleteRight}}
	ShowSyncResults(nil, fileops.SyncSides{}, config.SyncOptions{}, pairs)
	w, ok := vtui.FrameManager.GetTopFrame().(*SyncResultsWindow)
	if !ok || len(w.view) != len(pairs) || w.table == nil || w.lblStats == nil {
		t.Fatalf("sync result window = %#v", w)
	}
	w.filters[syncGroupEqual].State = 0
	w.rebuild()
	if len(w.view) != 2 {
		t.Fatalf("filtered rows = %d, want 2", len(w.view))
	}
	w.Close()
}

func TestSyncWindowProcessKeyChangesAction(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pair := fileops.SyncPair{Rel: "file", HasLeft: true, HasRight: true, State: fileops.SyncDiffers}
	ShowSyncResults(nil, fileops.SyncSides{}, config.SyncOptions{}, []fileops.SyncPair{pair})
	w := vtui.FrameManager.GetTopFrame().(*SyncResultsWindow)
	if !w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT}) || w.pairs[0].Action != fileops.SyncCopyToRight {
		t.Fatalf("right key action = %d", w.pairs[0].Action)
	}
	if !w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT}) || w.pairs[0].Action != fileops.SyncCopyToLeft {
		t.Fatalf("left key action = %d", w.pairs[0].Action)
	}
	if !w.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_BACK}) || w.pairs[0].Action != fileops.SyncSkip {
		t.Fatalf("back key action = %d", w.pairs[0].Action)
	}
	w.Close()
}

func TestSyncTotalsTextIncludesCopyAndDeleteCounts(t *testing.T) {
	text := syncTotalsText(fileops.SyncTotals{ToRight: 2, BytesToRight: 2048, ToLeft: 1, BytesToLeft: 1024, DeleteRight: 1, DeleteLeft: 2})
	if text == "" || text == syncTotalsText(fileops.SyncTotals{}) {
		t.Fatalf("sync totals text = %q", text)
	}
	if syncGroupToken(syncGroupToRight) != syncTokenToRight || syncGroupToken(syncGroupEqual) != syncTokenEqual || syncGroupToken(syncGroupDiffers) != syncTokenDiffers || syncGroupToken(syncGroupToLeft) != syncTokenToLeft {
		t.Fatal("sync group tokens are wrong")
	}
}
