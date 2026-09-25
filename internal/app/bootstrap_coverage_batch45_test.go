package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtui"
)

func prepareSessionSaveCoverageBatch45(t *testing.T) {
	oldFrameManager := vtui.FrameManager
	vtui.FrameManager = nil
	t.Cleanup(func() { vtui.FrameManager = oldFrameManager })

	oldSearch, oldReplace := editor.LastEditorSearch, editor.LastEditorReplace
	oldCase := editor.LastEditorSearchCase
	oldReverse := editor.LastEditorSearchReverse
	oldRegexp := editor.LastEditorSearchRegexp
	oldWholeWord := editor.LastEditorSearchWholeWord
	oldMask, oldText := LastFindFileMask, LastFindFileText
	oldFindCase := LastFindFileCaseSensitive
	oldFindWhole := LastFindFileWholeWords
	oldFindRegexp := LastFindFileRegexp
	oldNotContaining := LastFindFileNotContaining
	oldFolders, oldSymlinks := LastFindFileFolders, LastFindFileSymlinks
	t.Cleanup(func() {
		editor.LastEditorSearch, editor.LastEditorReplace = oldSearch, oldReplace
		editor.LastEditorSearchCase = oldCase
		editor.LastEditorSearchReverse = oldReverse
		editor.LastEditorSearchRegexp = oldRegexp
		editor.LastEditorSearchWholeWord = oldWholeWord
		LastFindFileMask, LastFindFileText = oldMask, oldText
		LastFindFileCaseSensitive = oldFindCase
		LastFindFileWholeWords = oldFindWhole
		LastFindFileRegexp = oldFindRegexp
		LastFindFileNotContaining = oldNotContaining
		LastFindFileFolders, LastFindFileSymlinks = oldFolders, oldSymlinks
	})

	editor.LastEditorSearch = "needle"
	editor.LastEditorReplace = "replacement"
	editor.LastEditorSearchCase = true
	editor.LastEditorSearchReverse = true
	editor.LastEditorSearchRegexp = true
	editor.LastEditorSearchWholeWord = true
	LastFindFileMask = "*.go"
	LastFindFileText = "needle"
	LastFindFileCaseSensitive = true
	LastFindFileWholeWords = true
	LastFindFileRegexp = true
	LastFindFileNotContaining = true
	LastFindFileFolders = true
	LastFindFileSymlinks = true
}

func readSessionSaveCoverageBatch45(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.ini")
	if err := saveSessionFileError(path, true, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSaveSessionFileWritesEditorSearchCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	data := readSessionSaveCoverageBatch45(t)
	for _, want := range []string{"Pattern = needle", "Replace = replacement", "CaseSensitive = 1", "Reverse = 1", "Regexp = 1", "WholeWord = 1"} {
		if !strings.Contains(data, want) {
			t.Errorf("saved session lacks %q", want)
		}
	}
}

func TestSaveSessionFileWritesFindOptionsCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	data := readSessionSaveCoverageBatch45(t)
	for _, want := range []string{"Mask = *.go", "Text = needle", "CaseSensitive = 1", "WholeWords = 1", "NotContaining = 1", "Folders = 1", "Symlinks = 1"} {
		if !strings.Contains(data, want) {
			t.Errorf("saved session lacks %q", want)
		}
	}
}

func TestSaveSessionFileWritesPanelStateCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	oldActive, oldWide := panel.LastActivePanel, panel.LastWidePanel
	oldPanels, oldLeft, oldRight := panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight
	t.Cleanup(func() {
		panel.LastActivePanel, panel.LastWidePanel = oldActive, oldWide
		panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight = oldPanels, oldLeft, oldRight
	})
	panel.LastActivePanel, panel.LastWidePanel = 1, 1
	panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight = true, false, true
	data := readSessionSaveCoverageBatch45(t)
	for _, want := range []string{"ActivePanel = 1", "WidePanel = 1", "ShowPanels = 1", "ShowLeft = 0", "ShowRight = 1"} {
		if !strings.Contains(data, want) {
			t.Errorf("saved session lacks %q", want)
		}
	}
}

func TestSaveSessionFileWritesLeftPanelStateCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	oldPath, oldCursor := panel.LastLeftPath, panel.LastLeftCursor
	t.Cleanup(func() { panel.LastLeftPath, panel.LastLeftCursor = oldPath, oldCursor })
	panel.LastLeftPath, panel.LastLeftCursor = "/left", "left.txt"
	data := readSessionSaveCoverageBatch45(t)
	if !strings.Contains(data, "Folder = /left") || !strings.Contains(data, "CurFile = left.txt") {
		t.Fatalf("left panel state missing from session:\n%s", data)
	}
}

func TestSaveSessionFileWritesRightPanelStateCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	oldPath, oldCursor := panel.LastRightPath, panel.LastRightCursor
	t.Cleanup(func() { panel.LastRightPath, panel.LastRightCursor = oldPath, oldCursor })
	panel.LastRightPath, panel.LastRightCursor = "/right", "right.txt"
	data := readSessionSaveCoverageBatch45(t)
	if !strings.Contains(data, "Folder = /right") || !strings.Contains(data, "CurFile = right.txt") {
		t.Fatalf("right panel state missing from session:\n%s", data)
	}
}

func TestSaveSessionFileWritesFalseEditorFlagsCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	editor.LastEditorSearchCase = false
	editor.LastEditorSearchReverse = false
	editor.LastEditorSearchRegexp = false
	editor.LastEditorSearchWholeWord = false
	data := readSessionSaveCoverageBatch45(t)
	for _, want := range []string{"CaseSensitive = 0", "Reverse = 0", "Regexp = 0", "WholeWord = 0"} {
		if !strings.Contains(data, want) {
			t.Errorf("saved session lacks %q", want)
		}
	}
}

func TestSaveSessionFileWritesFalseFindFlagsCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	LastFindFileCaseSensitive = false
	LastFindFileWholeWords = false
	LastFindFileRegexp = false
	LastFindFileNotContaining = false
	LastFindFileFolders = false
	LastFindFileSymlinks = false
	data := readSessionSaveCoverageBatch45(t)
	for _, want := range []string{"CaseSensitive = 0", "WholeWords = 0", "NotContaining = 0", "Folders = 0", "Symlinks = 0"} {
		if !strings.Contains(data, want) {
			t.Errorf("saved session lacks %q", want)
		}
	}
}

func TestSaveSessionFileReturnsWriteErrorCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	if err := saveSessionFileError(t.TempDir(), true, true); err == nil {
		t.Fatal("saveSessionFileError(directory) returned nil")
	}
}

func TestSaveSessionFileWritesCurrentPanelFieldsCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	oldMode, oldSort := panel.LastLeftViewMode, panel.LastLeftSortMode
	t.Cleanup(func() { panel.LastLeftViewMode, panel.LastLeftSortMode = oldMode, oldSort })
	panel.LastLeftViewMode, panel.LastLeftSortMode = 4, 3
	data := readSessionSaveCoverageBatch45(t)
	if !strings.Contains(data, "ViewMode = 4") || !strings.Contains(data, "SortMode = 3") {
		t.Fatalf("left panel mode/sort missing from session:\n%s", data)
	}
}

func TestSaveSessionFileWritesCurrentRightPanelFieldsCoverageBatch45(t *testing.T) {
	prepareSessionSaveCoverageBatch45(t)
	oldMode, oldSort := panel.LastRightViewMode, panel.LastRightSortMode
	t.Cleanup(func() { panel.LastRightViewMode, panel.LastRightSortMode = oldMode, oldSort })
	panel.LastRightViewMode, panel.LastRightSortMode = 4, 3
	data := readSessionSaveCoverageBatch45(t)
	if !strings.Contains(data, "ViewMode = 4") || !strings.Contains(data, "SortMode = 3") {
		t.Fatalf("right panel mode/sort missing from session:\n%s", data)
	}
}
