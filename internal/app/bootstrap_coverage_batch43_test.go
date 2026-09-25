package app

import (
	"os"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/panel"
)

func installSessionFixtureCoverageBatch43(t *testing.T, contents string) {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "session.ini"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	previous := getSessionIniPath
	getSessionIniPath = func() string { return path }
	t.Cleanup(func() { getSessionIniPath = previous })
}

func TestLoadSessionMissingFileCoverageBatch43(t *testing.T) {
	previous := getSessionIniPath
	getSessionIniPath = func() string { return t.TempDir() + string(os.PathSeparator) + "missing.ini" }
	t.Cleanup(func() { getSessionIniPath = previous })
	sessionLoaded = false
	LoadSession()
	if !sessionLoaded {
		t.Fatal("LoadSession should mark the session as loaded even when the file is absent")
	}
}

func TestLoadSessionReadsEditorSearchCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[EditorSearch]\nPattern = needle\nReplace = replacement\nCaseSensitive = 1\nReverse = 1\nRegexp = 1\nWholeWord = 1\n")
	LoadSession()
	if editor.LastEditorSearch != "needle" || editor.LastEditorReplace != "replacement" ||
		!editor.LastEditorSearchCase || !editor.LastEditorSearchReverse ||
		!editor.LastEditorSearchRegexp || !editor.LastEditorSearchWholeWord {
		t.Fatalf("editor search state was not restored: %#v %#v", editor.LastEditorSearch, editor.LastEditorReplace)
	}
}

func TestLoadSessionReadsFindFileOptionsCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[FindFile]\nMask = *.go\nText = TODO\nCaseSensitive = 1\nWholeWords = 1\nRegexp = 1\nNotContaining = 1\nFolders = 1\nSymlinks = 1\n")
	LoadSession()
	if LastFindFileMask != "*.go" || LastFindFileText != "TODO" ||
		!LastFindFileCaseSensitive || !LastFindFileWholeWords || !LastFindFileRegexp ||
		!LastFindFileNotContaining || !LastFindFileFolders || !LastFindFileSymlinks {
		t.Fatalf("find-file state was not restored: mask=%q text=%q", LastFindFileMask, LastFindFileText)
	}
}

func TestLoadSessionReadsLeftPanelStateCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Panel/Left]\nFolder = /left\nCurFile = item.txt\nViewMode = 2\nSortMode = 3\nSortReverse = 1\nUseSortGroups = 1\nGroupBy = 1\nGroupReverse = 1\nGroupFoldersSeparately = 0\n")
	LoadSession()
	if panel.LastLeftPath != "/left" || panel.LastLeftCursor != "item.txt" ||
		panel.LastLeftViewMode != 2 || panel.LastLeftSortMode != 3 ||
		!panel.LastLeftSortRev || !panel.LastLeftSortGroups ||
		panel.LastLeftGroupBy != 1 || !panel.LastLeftGroupReverse || panel.LastLeftGroupFoldersSeparately {
		t.Fatalf("left panel state was not restored: path=%q cursor=%q", panel.LastLeftPath, panel.LastLeftCursor)
	}
}

func TestLoadSessionInvalidLeftGroupDefaultsCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Panel/Left]\nGroupBy = not-a-number\n")
	LoadSession()
	if panel.LastLeftGroupBy != panel.GroupNone {
		t.Fatalf("invalid left GroupBy = %v, want GroupNone", panel.LastLeftGroupBy)
	}
}

func TestLoadSessionReadsRightPanelStateCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Panel/Right]\nFolder = /right\nCurFile = other.txt\nViewMode = 4\nSortMode = 5\nSortReverse = 1\nUseSortGroups = 1\nGroupBy = 1\nGroupReverse = 1\nGroupFoldersSeparately = 0\n")
	LoadSession()
	if panel.LastRightPath != "/right" || panel.LastRightCursor != "other.txt" ||
		panel.LastRightViewMode != 4 || panel.LastRightSortMode != 5 ||
		!panel.LastRightSortRev || !panel.LastRightSortGroups ||
		panel.LastRightGroupBy != 1 || !panel.LastRightGroupReverse || panel.LastRightGroupFoldersSeparately {
		t.Fatalf("right panel state was not restored: path=%q cursor=%q", panel.LastRightPath, panel.LastRightCursor)
	}
}

func TestLoadSessionInvalidRightGroupDefaultsCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Panel/Right]\nGroupBy = not-a-number\n")
	LoadSession()
	if panel.LastRightGroupBy != panel.GroupNone {
		t.Fatalf("invalid right GroupBy = %v, want GroupNone", panel.LastRightGroupBy)
	}
}

func TestLoadSessionNormalizesWidePanelCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Session]\nWidePanel = 9\n")
	LoadSession()
	if panel.LastWidePanel != -1 {
		t.Fatalf("invalid WidePanel = %d, want -1", panel.LastWidePanel)
	}
}

func TestLoadSessionReadsSessionVisibilityCoverageBatch43(t *testing.T) {
	installSessionFixtureCoverageBatch43(t, "[Session]\nActivePanel = 2\nWidePanel = 1\nShowPanels = 0\nShowLeft = 0\nShowRight = 1\n")
	LoadSession()
	if panel.LastActivePanel != 2 || panel.LastWidePanel != 1 || panel.LastShowPanels || panel.LastShowLeft || !panel.LastShowRight {
		t.Fatalf("session visibility was not restored: active=%d wide=%d", panel.LastActivePanel, panel.LastWidePanel)
	}
}

func TestSaveSessionReturnsBeforeWritingWhenNotLoadedCoverageBatch43(t *testing.T) {
	previousLoaded := sessionLoaded
	previousPath := getSessionIniPath
	path := t.TempDir() + string(os.PathSeparator) + "not-written.ini"
	sessionLoaded = false
	getSessionIniPath = func() string { return path }
	t.Cleanup(func() {
		sessionLoaded = previousLoaded
		getSessionIniPath = previousPath
	})
	SaveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("SaveSession created %q while the session was not loaded", path)
	}
}
