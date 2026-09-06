package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_AutoSaveCategoriesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.ini")
	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldConfig := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldConfig
	}()
	getUserConfigIniPath = func() string { return path }
	getConfigIniPaths = func() []string { return []string{path} }

	AppConfig.AutoSaveSettings = true
	AppConfig.AutoSaveDialogSettings = true
	AppConfig.AutoSavePanelSettings = false
	AppConfig.AutoSaveCurrentPanel = true
	AppConfig.AutoSaveGUIWindow = false
	SaveConfig()

	AppConfig.AutoSaveSettings = false
	AppConfig.AutoSaveDialogSettings = false
	AppConfig.AutoSavePanelSettings = true
	AppConfig.AutoSaveCurrentPanel = false
	AppConfig.AutoSaveGUIWindow = true
	LoadConfig()

	if !AppConfig.AutoSaveSettings || !AppConfig.AutoSaveDialogSettings || AppConfig.AutoSavePanelSettings ||
		!AppConfig.AutoSaveCurrentPanel || AppConfig.AutoSaveGUIWindow {
		t.Fatalf("autosave categories did not round-trip: master=%v dialog=%v panel=%v current=%v gui=%v",
			AppConfig.AutoSaveSettings, AppConfig.AutoSaveDialogSettings, AppConfig.AutoSavePanelSettings,
			AppConfig.AutoSaveCurrentPanel, AppConfig.AutoSaveGUIWindow)
	}
}

func TestConfig_AutoSaveCategoriesMigrateLegacyMaster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.ini")
	if err := os.WriteFile(path, []byte("[System]\nAutoSaveSettings = 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldConfig := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldConfig
	}()
	getUserConfigIniPath = func() string { return path }
	getConfigIniPaths = func() []string { return []string{path} }

	LoadConfig()
	if AppConfig.AutoSaveSettings || AppConfig.AutoSaveDialogSettings || AppConfig.AutoSavePanelSettings ||
		AppConfig.AutoSaveCurrentPanel || AppConfig.AutoSaveGUIWindow {
		t.Fatalf("legacy disabled autosave did not disable all categories: master=%v dialog=%v panel=%v current=%v gui=%v",
			AppConfig.AutoSaveSettings, AppConfig.AutoSaveDialogSettings, AppConfig.AutoSavePanelSettings,
			AppConfig.AutoSaveCurrentPanel, AppConfig.AutoSaveGUIWindow)
	}
}

func TestSaveSession_DisabledWhenAllCategoriesAreOff(t *testing.T) {
	oldConfig := AppConfig
	oldSessionPath := getSessionIniPath
	oldLoaded := sessionLoaded
	defer func() {
		AppConfig = oldConfig
		getSessionIniPath = oldSessionPath
		sessionLoaded = oldLoaded
	}()

	path := filepath.Join(t.TempDir(), "session.ini")
	getSessionIniPath = func() string { return path }
	// Otherwise the assertion passes for the wrong reason: unloaded state.
	sessionLoaded = true
	AppConfig.AutoSaveSettings = true
	AppConfig.AutoSaveDialogSettings = false
	AppConfig.AutoSavePanelSettings = false
	AppConfig.AutoSaveCurrentPanel = false
	AppConfig.AutoSaveGUIWindow = false
	SaveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("all-disabled automatic session save created %s, err=%v", path, err)
	}
}

// The session client, --version and --help reach SaveSession in main's defer
// without ever calling LoadSession: their defaults would cost the daemon's file
// its panel paths and wide mode.
func TestSaveSession_SkippedWhenStateWasNeverLoaded(t *testing.T) {
	oldConfig := AppConfig
	oldSessionPath := getSessionIniPath
	oldLoaded := sessionLoaded
	defer func() {
		AppConfig = oldConfig
		getSessionIniPath = oldSessionPath
		sessionLoaded = oldLoaded
	}()

	path := filepath.Join(t.TempDir(), "session.ini")
	getSessionIniPath = func() string { return path }
	sessionLoaded = false
	AppConfig.AutoSaveSettings = true
	AppConfig.AutoSaveDialogSettings = true
	AppConfig.AutoSavePanelSettings = true
	AppConfig.AutoSaveCurrentPanel = true
	AppConfig.AutoSaveGUIWindow = true
	SaveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session save without a loaded state created %s, err=%v", path, err)
	}
}

func TestMergeWorkspaceSessionSaveGranularPolicies(t *testing.T) {
	previous := []workspaceSessionState{{
		Number: 4, ActivePanel: 0, WidePanel: 1, ShowPanels: true,
		Left:  panelSessionState{Path: "old-left", Cursor: "old-left.txt", ViewMode: int(ViewModeBrief), SortMode: int(SortName)},
		Right: panelSessionState{Path: "old-right", Cursor: "old-right.txt", ViewMode: int(ViewModeDetailed), SortMode: int(SortTime)},
	}}
	current := []workspaceSessionState{{
		Number: 4, ActivePanel: 1, WidePanel: -1, ShowPanels: false,
		Left:  panelSessionState{Path: "new-left", Cursor: "new-left.txt", ViewMode: int(ViewModeMedium), SortMode: int(SortSize)},
		Right: panelSessionState{Path: "new-right", Cursor: "new-right.txt", ViewMode: int(ViewModeBrief), SortMode: int(SortExt)},
	}}

	pathsOnly, active := mergeWorkspaceSessionSave(previous, 0, current, 1, false, true)
	if active != 0 || pathsOnly[0].ShowPanels != previous[0].ShowPanels ||
		pathsOnly[0].Left.ViewMode != previous[0].Left.ViewMode || pathsOnly[0].Left.Path != current[0].Left.Path ||
		pathsOnly[0].Right.Cursor != current[0].Right.Cursor {
		t.Fatalf("current-panel-only merge changed the wrong fields: active=%d state=%#v", active, pathsOnly[0])
	}

	settingsOnly, active := mergeWorkspaceSessionSave(previous, 0, current, 1, true, false)
	if active != 1 || settingsOnly[0].ShowPanels != current[0].ShowPanels ||
		settingsOnly[0].Left.SortMode != current[0].Left.SortMode || settingsOnly[0].Left.Path != previous[0].Left.Path ||
		settingsOnly[0].Right.Cursor != previous[0].Right.Cursor {
		t.Fatalf("panel-settings-only merge changed the wrong fields: active=%d state=%#v", active, settingsOnly[0])
	}
}
