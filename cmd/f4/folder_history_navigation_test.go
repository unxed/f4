package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestFolderHistoryStepMovesPositionally(t *testing.T) {
	history := []string{"newest", "middle", "oldest"}

	pos, path, ok := folderHistoryStep(history, "newest", -1, -1)
	if !ok || pos != 1 || path != "middle" {
		t.Fatalf("Back from newest = (%d, %q, %v), want (1, middle, true)", pos, path, ok)
	}
	pos, path, ok = folderHistoryStep(history, "middle", pos, -1)
	if !ok || pos != 2 || path != "oldest" {
		t.Fatalf("second Back = (%d, %q, %v), want (2, oldest, true)", pos, path, ok)
	}
	pos, path, ok = folderHistoryStep(history, "oldest", pos, 1)
	if !ok || pos != 1 || path != "middle" {
		t.Fatalf("Forward = (%d, %q, %v), want (1, middle, true)", pos, path, ok)
	}
	if _, _, ok = folderHistoryStep(history, "newest", 0, 1); ok {
		t.Fatal("Forward moved past the newest history entry")
	}
	pos, path, ok = folderHistoryStep(history, "not-recorded", -1, -1)
	if !ok || pos != 0 || path != "newest" {
		t.Fatalf("Back from unrecorded path = (%d, %q, %v), want newest entry", pos, path, ok)
	}
}

func TestFolderHistorySuppressionIsBoundToItsAsyncNavigation(t *testing.T) {
	panel := &FileSystemPanel{}
	olderTarget := `cloud.example:\older`
	newerTarget := `cloud.example:\newer`

	panel.suppressNextFolderHistory(olderTarget)
	olderToken, ok := panel.folderHistorySuppression(olderTarget)
	if !ok {
		t.Fatal("older history navigation did not acquire suppression")
	}

	// A cross-provider history jump can spend time opening the next provider
	// while a refresh from the old provider is still completing. The old
	// completion must not consume the newer jump's one-shot suppression.
	panel.suppressNextFolderHistory(newerTarget)
	if panel.consumeFolderHistorySuppression(olderTarget, olderToken) {
		t.Fatal("stale directory completion consumed newer history suppression")
	}

	newerToken, ok := panel.folderHistorySuppression(newerTarget)
	if !ok {
		t.Fatal("newer history navigation lost its suppression")
	}
	if !panel.consumeFolderHistorySuppression(newerTarget, newerToken) {
		t.Fatal("matching directory completion did not consume its suppression")
	}
	if _, ok := panel.folderHistorySuppression(newerTarget); ok {
		t.Fatal("history suppression was not one-shot")
	}
}

func TestFolderHistoryNavigationAndMenuDoNotReorderHistory(t *testing.T) {
	// The folder history dialog folds the bookmark table in (#407); keep it
	// pointed at a temp profile so the test cannot see or touch a real one.
	setupPortableIni(t, "0")
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	root := t.TempDir()
	newest := filepath.Join(root, "newest")
	missing := filepath.Join(root, "missing")
	middle := filepath.Join(root, "middle")
	oldest := filepath.Join(root, "oldest")
	for _, dir := range []string{newest, middle, oldest} {
		if err := ensureDir(dir); err != nil {
			t.Fatal(err)
		}
	}

	panel := NewFileSystemPanel(0, 0, 80, 20, vfs.NewOSVFS(newest))
	pf := &PanelsFrame{
		activeIdx:        0,
		showPanels:       true,
		folderHistoryPos: [2]int{-1, -1},
	}
	pf.panels[0] = panel
	defer pf.Close()
	waitForLoad(t, panel)

	provider := &F4HistoryProvider{
		path: filepath.Join(root, "history.json"),
		data: make(map[string][]string),
	}
	original := []string{newest, missing, middle, oldest}
	provider.SaveHistory("folders", original)
	oldProvider := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = provider
	defer func() { vtui.GlobalHistoryProvider = oldProvider }()

	panel.fastFindMode = true
	panel.fastFindStr = "new"
	if !pf.moveFolderHistory(panel, -1) {
		t.Fatal("Alt+Left history move was not performed")
	}
	if panel.fastFindMode || panel.fastFindStr != "" {
		t.Fatalf("folder history navigation left fast find open: mode=%v query=%q", panel.fastFindMode, panel.fastFindStr)
	}
	waitForLoad(t, panel)
	if !sameFolderHistoryPath(panel.vfs.GetPath(), middle) {
		t.Fatalf("Alt+Left path = %q, want %q", panel.vfs.GetPath(), middle)
	}
	if got := provider.LoadHistory("folders"); !reflect.DeepEqual(got, original) {
		t.Fatalf("Alt+Left reordered history: %#v", got)
	}

	if !pf.moveFolderHistory(panel, 1) {
		t.Fatal("Alt+Right history move was not performed")
	}
	waitForLoad(t, panel)
	if !sameFolderHistoryPath(panel.vfs.GetPath(), newest) {
		t.Fatalf("Alt+Right path = %q, want %q", panel.vfs.GetPath(), newest)
	}
	if got := provider.LoadHistory("folders"); !reflect.DeepEqual(got, original) {
		t.Fatalf("Alt+Right reordered history: %#v", got)
	}

	if !pf.moveFolderHistory(panel, -1) {
		t.Fatal("second Alt+Left history move was not performed")
	}
	waitForLoad(t, panel)

	actionFoldersHistory(pf)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("folder history frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	if menu.SelectPos < 0 || menu.SelectPos >= len(menu.Items) {
		t.Fatalf("initial menu selection = %d, item count %d", menu.SelectPos, len(menu.Items))
	}
	if activeHistorySearch == nil {
		t.Fatal("folder history did not install a history search")
	}
	_, selected, ok := activeHistorySearch.selected()
	if !ok || !sameFolderHistoryPath(selected.Name, middle) {
		t.Fatalf("initial menu selection = %q, want current folder %q", selected.Name, middle)
	}
	// Display order is oldest -> newest. Select the missing entry; activation
	// must skip it and continue downwards to the newer, accessible entry.
	menu.SetSelectPos(2)
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	vtui.FrameManager.Pop()
	waitForLoad(t, panel)
	if !sameFolderHistoryPath(panel.vfs.GetPath(), newest) {
		t.Fatalf("menu path = %q, want next accessible %q", panel.vfs.GetPath(), newest)
	}
	if got := provider.LoadHistory("folders"); !reflect.DeepEqual(got, original) {
		t.Fatalf("folder history menu reordered history: %#v", got)
	}
	if pf.folderHistoryPos[0] != 0 {
		t.Fatalf("menu history position = %d, want 0", pf.folderHistoryPos[0])
	}
}

func ensureDir(path string) error {
	return os.Mkdir(path, 0o700)
}
