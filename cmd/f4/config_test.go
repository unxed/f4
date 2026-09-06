package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

func TestConfig_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := getUserConfigIniPath
	getUserConfigIniPath = func() string { return userIniPath }
	origPathsFunc := getConfigIniPaths
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	// Save original config to restore after test
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()

	// 1. Set some non-default values
	AppConfig.ShowHiddenFiles = false
	AppConfig.ColorStyle = "Classic"
	AppConfig.ShowDirPrefix = true
	AppConfig.SavePanelPaths = false
	AppConfig.EditorCrosshair = true
	AppConfig.EditorColorerBackground = false
	AppConfig.CommandLineAutoComplete = false
	AppConfig.SeparateFileExtensions = true
	AppConfig.PanelScrollbarMode = PanelScrollbarMinimal
	AppConfig.ShowPanelFileInfo = true
	AppConfig.MacroRecordFormat = 1
	AppConfig.UseTrash = true
	AppConfig.TerminalCtrlNWorkspace = false
	AppConfig.ConsoleMode = "host"
	AppConfig.ConsoleOverlayUI = true
	AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsNever)
	AppConfig.WorkspaceTabsOverlay = false
	AppConfig.CtrlTabShowsMenu = true
	AppConfig.AltNumberSwitchesTabs = false
	AppConfig.RestoreWorkspaceTabs = false
	AppConfig.WorkspaceTabNumbering = WorkspaceTabNumbersOrder
	AppConfig.ApplyCommandParallelism = 0
	AppConfig.AutoSaveSettings = false
	AppConfig.DisplayFullPathInTitle = true
	AppConfig.EditorAutodetectCodePage = false
	AppConfig.EditorDefaultCodePage = 1251
	AppConfig.ViewerAutodetectCodePage = true
	AppConfig.ViewerDefaultCodePage = 866

	// 2. Save
	SaveConfig()

	// 3. Reset to defaults
	AppConfig.ShowHiddenFiles = true
	AppConfig.ColorStyle = "Modern"
	AppConfig.ShowDirPrefix = false
	AppConfig.EditorCrosshair = false
	AppConfig.EditorColorerBackground = true
	AppConfig.SeparateFileExtensions = false
	AppConfig.PanelScrollbarMode = PanelScrollbarOff
	AppConfig.ShowPanelFileInfo = false
	AppConfig.MacroRecordFormat = 0
	AppConfig.UseTrash = false
	AppConfig.TerminalCtrlNWorkspace = true
	AppConfig.ConsoleMode = "own"
	AppConfig.ConsoleOverlayUI = false
	AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsAlways)
	AppConfig.WorkspaceTabsOverlay = true
	AppConfig.CtrlTabShowsMenu = false
	AppConfig.AltNumberSwitchesTabs = true
	AppConfig.RestoreWorkspaceTabs = true
	AppConfig.WorkspaceTabNumbering = WorkspaceTabNumbersAlways
	AppConfig.ApplyCommandParallelism = 1
	AppConfig.AutoSaveSettings = true
	AppConfig.DisplayFullPathInTitle = false
	AppConfig.EditorAutodetectCodePage = true
	AppConfig.EditorDefaultCodePage = 65001
	AppConfig.ViewerAutodetectCodePage = false
	AppConfig.ViewerDefaultCodePage = 65001

	// 4. Load
	LoadConfig()
	if AppConfig.ColorStyle != "Classic" {
		t.Errorf("LoadConfig failed to restore color style: %q", AppConfig.ColorStyle)
	}

	// 5. Verify
	if AppConfig.ShowHiddenFiles {
		t.Error("LoadConfig failed to restore ShowHiddenFiles")
	}
	if AppConfig.WorkspaceTabMode != int(vtui.WorkspaceTabsNever) {
		t.Errorf("LoadConfig failed to restore workspace tab mode: %d", AppConfig.WorkspaceTabMode)
	}
	if AppConfig.WorkspaceTabsOverlay {
		t.Error("LoadConfig failed to restore disabled workspace tab overlay")
	}
	if !AppConfig.CtrlTabShowsMenu {
		t.Error("LoadConfig failed to restore Ctrl+Tab menu mode")
	}
	if AppConfig.AltNumberSwitchesTabs {
		t.Error("LoadConfig failed to restore disabled Alt+number tab switching")
	}
	if AppConfig.RestoreWorkspaceTabs {
		t.Error("LoadConfig failed to restore disabled workspace tab restoration")
	}
	if AppConfig.WorkspaceTabNumbering != WorkspaceTabNumbersOrder {
		t.Errorf("LoadConfig restored workspace tab numbering %v, want order", AppConfig.WorkspaceTabNumbering)
	}
	if !AppConfig.ShowDirPrefix {
		t.Error("LoadConfig failed to restore ShowDirPrefix")
	}
	if AppConfig.SavePanelPaths {
		t.Error("LoadConfig failed to restore SavePanelPaths")
	}
	if AppConfig.AutoSaveSettings {
		t.Error("LoadConfig failed to restore disabled AutoSaveSettings")
	}
	if !AppConfig.DisplayFullPathInTitle {
		t.Error("LoadConfig failed to restore DisplayFullPathInTitle")
	}
	if !AppConfig.EditorCrosshair {
		t.Error("LoadConfig failed to restore EditorCrosshair")
	}
	if AppConfig.EditorColorerBackground {
		t.Error("LoadConfig failed to restore EditorColorerBackground")
	}
	if AppConfig.CommandLineAutoComplete {
		t.Error("LoadConfig failed to restore CommandLineAutoComplete")
	}
	if !AppConfig.SeparateFileExtensions {
		t.Error("LoadConfig failed to restore SeparateFileExtensions")
	}
	if AppConfig.PanelScrollbarMode != PanelScrollbarMinimal {
		t.Errorf("LoadConfig restored PanelScrollbarMode %v, want minimal", AppConfig.PanelScrollbarMode)
	}
	if !AppConfig.ShowPanelFileInfo {
		t.Error("LoadConfig failed to restore ShowPanelFileInfo")
	}
	if AppConfig.MacroRecordFormat != 1 {
		t.Error("LoadConfig failed to restore MacroRecordFormat")
	}
	if !AppConfig.UseTrash {
		t.Error("LoadConfig failed to restore UseTrash")
	}
	if AppConfig.TerminalCtrlNWorkspace {
		t.Error("LoadConfig failed to restore TerminalCtrlNWorkspace")
	}
	if AppConfig.ConsoleMode != "host" {
		t.Errorf("LoadConfig failed to restore ConsoleMode: got %q, want %q", AppConfig.ConsoleMode, "host")
	}
	if !AppConfig.ConsoleOverlayUI {
		t.Error("LoadConfig failed to restore ConsoleOverlayUI")
	}
	if AppConfig.ApplyCommandParallelism != 0 {
		t.Errorf("ApplyCommandParallelism = %d, want Unlimited (0)", AppConfig.ApplyCommandParallelism)
	}
	if AppConfig.EditorAutodetectCodePage {
		t.Error("LoadConfig failed to restore disabled Editor autodetection")
	}
	if AppConfig.EditorDefaultCodePage != 1251 {
		t.Errorf("EditorDefaultCodePage = %d, want 1251", AppConfig.EditorDefaultCodePage)
	}
	if !AppConfig.ViewerAutodetectCodePage {
		t.Error("LoadConfig failed to restore enabled Viewer autodetection")
	}
	if AppConfig.ViewerDefaultCodePage != 866 {
		t.Errorf("ViewerDefaultCodePage = %d, want 866", AppConfig.ViewerDefaultCodePage)
	}
}

func TestSaveSettingsGroupsKeepUnselectedValues(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.ini")
	sessionPath := filepath.Join(tmpDir, "session.ini")

	oldConfig := AppConfig
	oldUserPath := getUserConfigIniPath
	oldConfigPaths := getConfigIniPaths
	oldSessionPath := getSessionIniPath
	t.Cleanup(swapFrameManager(t))
	defer func() {
		AppConfig = oldConfig
		getUserConfigIniPath = oldUserPath
		getConfigIniPaths = oldConfigPaths
		getSessionIniPath = oldSessionPath
	}()

	getUserConfigIniPath = func() string { return settingsPath }
	getConfigIniPaths = func() []string { return []string{settingsPath} }
	getSessionIniPath = func() string { return sessionPath }

	AppConfig.ColorStyle = "Persisted"
	AppConfig.GuiCols = 80
	AppConfig.GuiRows = 25
	SaveConfig()

	AppConfig.ColorStyle = "Pending"
	AppConfig.GuiCols = 120
	AppConfig.GuiRows = 40
	saveSettingsGroups(true, false, false)
	ini := LoadIni(settingsPath)
	if got := ini.GetString("Interface", "ColorStyle", ""); got != "Pending" {
		t.Fatalf("general settings were not saved: ColorStyle = %q", got)
	}
	if got := ini.GetString("Appearance", "GuiCols", ""); got != "80" {
		t.Fatalf("unselected GUI width changed: %q", got)
	}
	if got := ini.GetString("Appearance", "GuiRows", ""); got != "25" {
		t.Fatalf("unselected GUI height changed: %q", got)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n[ThirdParty]\nKeep=1\n")...)
	if err := os.WriteFile(settingsPath, data, 0600); err != nil { // #nosec G703 -- settingsPath is a fixed fixture name beneath t.TempDir().
		t.Fatal(err)
	}
	AppConfig.ColorStyle = "Not persisted"
	AppConfig.GuiCols = 140
	AppConfig.GuiRows = 50
	saveSettingsGroups(false, false, true)
	ini = LoadIni(settingsPath)
	if got := ini.GetString("Interface", "ColorStyle", ""); got != "Pending" {
		t.Fatalf("window-only save changed general settings: %q", got)
	}
	if got := ini.GetString("Appearance", "GuiCols", ""); got != "140" {
		t.Fatalf("window-only save did not save width: %q", got)
	}
	if got := ini.GetString("Appearance", "GuiRows", ""); got != "50" {
		t.Fatalf("window-only save did not save height: %q", got)
	}
	if got := ini.GetString("ThirdParty", "Keep", ""); got != "1" {
		t.Fatalf("window-only save discarded unknown settings: %q", got)
	}

	freshSettingsPath := filepath.Join(tmpDir, "fresh-settings.ini")
	getUserConfigIniPath = func() string { return freshSettingsPath }
	getConfigIniPaths = func() []string { return []string{freshSettingsPath} }
	AppConfig.GuiCols = 90
	AppConfig.GuiRows = 30
	saveSettingsGroups(false, false, true)
	fresh, err := os.ReadFile(freshSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fresh), "[Interface]") {
		t.Fatalf("window-only save created unrelated settings on a fresh profile:\n%s", fresh)
	}
}

func TestSaveSessionDisabled(t *testing.T) {
	oldConfig := AppConfig
	oldSessionPath := getSessionIniPath
	defer func() {
		AppConfig = oldConfig
		getSessionIniPath = oldSessionPath
	}()

	path := filepath.Join(t.TempDir(), "session.ini")
	getSessionIniPath = func() string { return path }
	AppConfig.AutoSaveSettings = false
	SaveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("disabled automatic session save created %s, err=%v", path, err)
	}
}

func TestConfig_ConsoleModeDefaultsWhenAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowHiddenFiles = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.ConsoleMode = "host"
	AppConfig.ConsoleOverlayUI = true
	LoadConfig()
	if AppConfig.ConsoleMode != "own" {
		t.Fatalf("ConsoleMode must default to 'own' when setting is absent, got %q", AppConfig.ConsoleMode)
	}
	if AppConfig.ConsoleOverlayUI {
		t.Fatal("ConsoleOverlayUI must default to false when setting is absent")
	}
}

func TestConfig_TrashDefaultsOffWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[System]\nConfirmDelete = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.UseTrash = true
	LoadConfig()
	if AppConfig.UseTrash {
		t.Fatal("UseTrash must default to false when the setting is absent")
	}
}

func TestConfig_TerminalCtrlNWorkspaceDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowHiddenFiles = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.TerminalCtrlNWorkspace = false
	LoadConfig()
	if !AppConfig.TerminalCtrlNWorkspace {
		t.Fatal("TerminalCtrlNWorkspace must default to true when the setting is absent")
	}
}

func TestConfig_AltNumberSwitchesTabsDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\nWorkspaceTabMode = multiple\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.AltNumberSwitchesTabs = false
	LoadConfig()
	if !AppConfig.AltNumberSwitchesTabs {
		t.Fatal("AltNumberSwitchesTabs must default to true when the setting is absent")
	}
}

func TestConfig_WorkspaceTabModeDefaultsToAlwaysWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsMultiple)
	AppConfig.WorkspaceTabsOverlay = false
	LoadConfig()
	if AppConfig.WorkspaceTabMode != int(vtui.WorkspaceTabsAlways) {
		t.Fatalf("WorkspaceTabMode without a saved key = %d, want always-visible mode %d",
			AppConfig.WorkspaceTabMode, vtui.WorkspaceTabsAlways)
	}
	if !AppConfig.WorkspaceTabsOverlay {
		t.Fatal("WorkspaceTabsOverlay must default to true when the setting is absent")
	}
}

func TestConfig_RestoreWorkspaceTabsDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.RestoreWorkspaceTabs = false
	LoadConfig()
	if !AppConfig.RestoreWorkspaceTabs {
		t.Fatal("RestoreWorkspaceTabs must default to true when the setting is absent")
	}
}

func TestConfig_WorkspaceTabNumberingDefaultsToAlwaysWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.WorkspaceTabNumbering = WorkspaceTabNumbersOrder
	LoadConfig()
	if AppConfig.WorkspaceTabNumbering != WorkspaceTabNumbersAlways {
		t.Fatalf("WorkspaceTabNumbering must default to always, got %v", AppConfig.WorkspaceTabNumbering)
	}
}

func TestConfig_PanelFileInfoDefaultsHiddenWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.ShowPanelFileInfo = true
	LoadConfig()
	if AppConfig.ShowPanelFileInfo {
		t.Fatal("ShowPanelFileInfo must default to false when the setting is absent")
	}
}

func TestCreateDefaultHighlightIniDocumentsColorOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "highlight.ini")
	createDefaultHighlightIni(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, key := range []string{
		"# [Highlight_100]",
		"# NormalColor =",
		"# SelectedColor =",
		"# CursorColor =",
		"# SelectedCursorColor =",
		"NormalColorUnderCursor",
		"SelectedColorUnderCursor",
	} {
		if !strings.Contains(content, key) {
			t.Errorf("generated highlight.ini is missing documented %q", key)
		}
	}
}

func TestConfig_ApplyCommandParallelismDefaultsToLogicalCPUs(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() { getConfigIniPaths = origPathsFunc; AppConfig = oldCfg }()
	getConfigIniPaths = func() []string { return []string{userIniPath} }
	AppConfig.ApplyCommandParallelism = 0
	LoadConfig()
	if AppConfig.ApplyCommandParallelism != runtime.NumCPU() {
		t.Fatalf("ApplyCommandParallelism = %d, want %d", AppConfig.ApplyCommandParallelism, runtime.NumCPU())
	}
}

func TestConfig_MinimalPanelScrollbarsByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	AppConfig.PanelScrollbarMode = PanelScrollbarFull
	LoadConfig()
	if AppConfig.PanelScrollbarMode != PanelScrollbarMinimal {
		t.Fatal("panel scrollbars must use minimal mode when the setting is absent")
	}
}

func TestConfig_PanelScrollbarBooleanMigration(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowPanelScrollbars = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	LoadConfig()
	if AppConfig.PanelScrollbarMode != PanelScrollbarFull {
		t.Fatalf("boolean scrollbar setting migrated to %v, want full", AppConfig.PanelScrollbarMode)
	}
}

func TestConfig_DisabledPanelScrollbarBooleanMigration(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowPanelScrollbars = 0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	LoadConfig()
	if AppConfig.PanelScrollbarMode != PanelScrollbarOff {
		t.Fatalf("disabled boolean scrollbar setting migrated to %v, want off", AppConfig.PanelScrollbarMode)
	}
}

func TestConfig_ImagesSectionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	getUserConfigIniPath = func() string { return userIniPath }
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
		SetImageDecoderPriorities(nil)
	}()

	AppConfig.SlideShowDelay = 9
	AppConfig.ImageExternalTimeout = 42
	AppConfig.ImageDecoderPriority = "external:-5|go-std:3"
	SaveConfig()

	AppConfig.SlideShowDelay = 5
	AppConfig.ImageExternalTimeout = 0
	AppConfig.ImageDecoderPriority = ""
	LoadConfig()

	if AppConfig.SlideShowDelay != 9 {
		t.Errorf("SlideShowDelay is %d, want 9", AppConfig.SlideShowDelay)
	}
	if AppConfig.ImageExternalTimeout != 42 {
		t.Errorf("ExternalTimeout is %d, want 42", AppConfig.ImageExternalTimeout)
	}
	if AppConfig.ImageDecoderPriority != "external:-5|go-std:3" {
		t.Errorf("DecoderPriority is %q", AppConfig.ImageDecoderPriority)
	}
	if got := imageDecoderPriorityOf("external", -10); got != -5 {
		t.Errorf("loading must apply the priorities, external is %d", got)
	}
	if got := imageDecoderPriorityOf("go-bmp", 10); got != 10 {
		t.Errorf("a decoder nobody overrode must keep its own priority, got %d", got)
	}
}

func TestConfig_Merge(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "global.ini")
	userPath := filepath.Join(tmpDir, "user.ini")

	globalContent := `
[Panel]
ShowHiddenFiles = 0
[Editor]
TabSize = 8
`
	userContent := `
[Panel]
ShowHiddenFiles = 1
[Editor]
Crosshair = 1
`
	if err := os.WriteFile(globalPath, []byte(globalContent), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte(userContent), 0600); err != nil {
		t.Fatal(err)
	}

	// Mock paths
	origPathsFunc := getConfigIniPaths
	getConfigIniPaths = func() []string { return []string{globalPath, userPath} }
	defer func() { getConfigIniPaths = origPathsFunc }()

	// Save original config to restore after test
	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()

	// Reset config to defaults before loading
	AppConfig = F4Config{
		ShowHiddenFiles:         true,
		EditorTabSize:           4,
		EditorCrosshair:         false,
		CommandLineAutoComplete: true, // A default that shouldn't be touched
	}

	LoadConfig()

	if !AppConfig.ShowHiddenFiles {
		t.Error("User config (ShowHiddenFiles=1) should override global (0)")
	}
	if AppConfig.EditorTabSize != 8 {
		t.Errorf("Global config (TabSize=8) was not loaded. Got %d", AppConfig.EditorTabSize)
	}
	if !AppConfig.EditorCrosshair {
		t.Error("User config (Crosshair=1) was not loaded.")
	}
	if !AppConfig.CommandLineAutoComplete {
		t.Error("Default value (CommandLineAutoComplete=true) was incorrectly overwritten.")
	}
}

func TestConfig_GuiFontPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Переопределяем путь к конфигурационному файлу для тестов
	oldPathFunc := getUserConfigIniPath
	getUserConfigIniPath = func() string {
		return filepath.Join(tmpDir, "settings.ini")
	}
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = oldPathFunc
		AppConfig = oldCfg
	}()

	// Задаем тестовые значения
	AppConfig.GuiFont = "UbuntuMono-Regular"
	AppConfig.GuiUseSystemMonospace = false
	AppConfig.GuiFontSize = 22
	SaveConfig()

	// Сбрасываем текущую конфигурацию в памяти
	AppConfig.GuiFont = ""
	AppConfig.GuiUseSystemMonospace = true
	AppConfig.GuiFontSize = 0

	// Читаем заново из временного файла
	LoadConfig()

	if AppConfig.GuiFont != "UbuntuMono-Regular" {
		t.Errorf("Expected GuiFont to be 'UbuntuMono-Regular', got %q", AppConfig.GuiFont)
	}
	if AppConfig.GuiFontSize != 22 {
		t.Errorf("Expected GuiFontSize to be 22, got %d", AppConfig.GuiFontSize)
	}
	if AppConfig.GuiUseSystemMonospace {
		t.Error("Expected GuiUseSystemMonospace to remain disabled")
	}
}

func TestConfig_GuiDimensionsPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	oldPathFunc := getUserConfigIniPath
	getUserConfigIniPath = func() string {
		return filepath.Join(tmpDir, "settings.ini")
	}
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = oldPathFunc
		AppConfig = oldCfg
	}()

	// 1. Задаем тестовые значения
	AppConfig.GuiCols = 120
	AppConfig.GuiRows = 45
	AppConfig.GuiPosX = -123
	AppConfig.GuiPosY = 456
	AppConfig.GuiPositionSaved = true
	AppConfig.ConfirmExit = false

	SaveConfig()

	// 2. Сбрасываем текущую конфигурацию в памяти
	AppConfig.GuiCols = 0
	AppConfig.GuiRows = 0
	AppConfig.GuiPosX = 0
	AppConfig.GuiPosY = 0
	AppConfig.GuiPositionSaved = false
	AppConfig.ConfirmExit = true

	// 3. Читаем заново из временного файла
	LoadConfig()

	// 4. Проверяем корректность восстановления
	if AppConfig.GuiCols != 120 {
		t.Errorf("Expected GuiCols to be 120, got %d", AppConfig.GuiCols)
	}
	if AppConfig.GuiRows != 45 {
		t.Errorf("Expected GuiRows to be 45, got %d", AppConfig.GuiRows)
	}
	if !AppConfig.GuiPositionSaved || AppConfig.GuiPosX != -123 || AppConfig.GuiPosY != 456 {
		t.Errorf("Expected GUI position to be -123,456, got saved=%v x=%d y=%d", AppConfig.GuiPositionSaved, AppConfig.GuiPosX, AppConfig.GuiPosY)
	}
	if AppConfig.ConfirmExit {
		t.Error("Expected ConfirmExit to be loaded as false, got true")
	}
}

// TestConfig_LayoutRoundTrip verifies that the [Layout] section persists
// our three known keys and round-trips any unknown keys (e.g. far2l's
// FullscreenHelp / PanelsDisposition) untouched on save.
func TestConfig_LayoutRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := getUserConfigIniPath
	origPathsFunc := getConfigIniPaths
	getUserConfigIniPath = func() string { return iniPath }
	getConfigIniPaths = func() []string { return []string{iniPath} }
	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()

	// Seed a config file with our three keys AND two far2l-only keys.
	seed := "[Layout]\n" +
		"WidthDecrement=3\n" +
		"LeftHeightDecrement=5\n" +
		"RightHeightDecrement=7\n" +
		"FullscreenHelp=1\n" +
		"PanelsDisposition=2\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	LoadConfig()
	if AppConfig.WidthDecrement != 3 || AppConfig.LeftHeightDecrement != 5 || AppConfig.RightHeightDecrement != 7 {
		t.Errorf("LoadConfig [Layout] values: W=%d L=%d R=%d, want 3/5/7",
			AppConfig.WidthDecrement, AppConfig.LeftHeightDecrement, AppConfig.RightHeightDecrement)
	}
	if AppConfig.LayoutExtras["FullscreenHelp"] != "1" || AppConfig.LayoutExtras["PanelsDisposition"] != "2" {
		t.Errorf("LoadConfig extras: %v", AppConfig.LayoutExtras)
	}

	// Save and re-read the file — extras must survive verbatim.
	AppConfig.WidthDecrement = -2
	SaveConfig()
	out, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"[Layout]",
		"FullscreenHelp=1",
		"LeftHeightDecrement=5",
		"PanelsDisposition=2",
		"RightHeightDecrement=7",
		"WidthDecrement=-2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SaveConfig missing %q in output:\n%s", want, body)
		}
	}
}

func TestLoadWheelLines(t *testing.T) {
	ini := &IniFile{data: map[string]map[string]string{
		"Mouse": {"PanelUp": "5", "PanelDown": "-2"},
	}}
	if got := loadWheelLines(ini, "PanelUp"); got != 5 {
		t.Errorf("Expected 5, got %d", got)
	}
	if got := loadWheelLines(ini, "PanelDown"); got != 0 {
		t.Errorf("Expected negative value to clamp to 0, got %d", got)
	}
	if got := loadWheelLines(ini, "ViewerUp"); got != 0 {
		t.Errorf("Expected missing key to default to 0, got %d", got)
	}
}

func TestWheelScrollLines(t *testing.T) {
	if got := wheelScrollLines(7); got != 7 {
		t.Errorf("Expected configured 7, got %d", got)
	}
	if got := wheelScrollLines(0); got != vtui.WheelLinesPerNotch() {
		t.Errorf("Expected 0 to resolve to system %d, got %d", vtui.WheelLinesPerNotch(), got)
	}
	if got := wheelScrollLines(-3); got != vtui.WheelLinesPerNotch() {
		t.Errorf("Expected negative to resolve to system %d, got %d", vtui.WheelLinesPerNotch(), got)
	}
}

func TestConfig_MouseWheelRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := getUserConfigIniPath
	getUserConfigIniPath = func() string { return userIniPath }
	origPathsFunc := getConfigIniPaths
	getConfigIniPaths = func() []string { return []string{userIniPath} }

	oldCfg := AppConfig
	defer func() {
		getUserConfigIniPath = origUserPathFunc
		getConfigIniPaths = origPathsFunc
		AppConfig = oldCfg
	}()

	AppConfig.WheelPanelUp = 1
	AppConfig.WheelPanelDown = 2
	AppConfig.WheelEditorUp = 3
	AppConfig.WheelEditorDown = 4
	AppConfig.WheelViewerUp = 5
	AppConfig.WheelViewerDown = 6
	AppConfig.WheelMenuUp = 7
	AppConfig.WheelMenuDown = 8
	AppConfig.WheelTableUp = 9
	AppConfig.WheelTableDown = 10
	SaveConfig()

	AppConfig.WheelPanelUp = 0
	AppConfig.WheelPanelDown = 0
	AppConfig.WheelEditorUp = 0
	AppConfig.WheelEditorDown = 0
	AppConfig.WheelViewerUp = 0
	AppConfig.WheelViewerDown = 0
	AppConfig.WheelMenuUp = 0
	AppConfig.WheelMenuDown = 0
	AppConfig.WheelTableUp = 0
	AppConfig.WheelTableDown = 0
	LoadConfig()

	got := []int{
		AppConfig.WheelPanelUp, AppConfig.WheelPanelDown,
		AppConfig.WheelEditorUp, AppConfig.WheelEditorDown,
		AppConfig.WheelViewerUp, AppConfig.WheelViewerDown,
		AppConfig.WheelMenuUp, AppConfig.WheelMenuDown,
		AppConfig.WheelTableUp, AppConfig.WheelTableDown,
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wheel config field %d: expected %d, got %d", i, want[i], got[i])
		}
	}
}

// TestRequestSaveConfigPostsToTheFrameManagerItWasArmedWith is the regression
// for the second race of this shape the race job reported.
//
// The debounced save reads vtui.FrameManager half a second after it is armed,
// from the timer's own goroutine. Nothing keeps a test alive that long, so the
// read lands in whichever test is running by then, and the write it races is
// that test's swapFrameManager -- which is why the report names a test that has
// nothing to do with saving settings.
func TestRequestSaveConfigPostsToTheFrameManagerItWasArmedWith(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	arming := vtui.FrameManager

	oldConfig := AppConfig
	t.Cleanup(func() { AppConfig = oldConfig })
	AppConfig.AutoSaveSettings = true
	AppConfig.AutoSaveDialogSettings = true

	RequestSaveConfig()
	t.Cleanup(func() {
		saveConfigTimerMu.Lock()
		if saveConfigTimer != nil {
			saveConfigTimer.Stop()
		}
		saveConfigTimerMu.Unlock()
	})

	// What the next test does while the timer is still counting down.
	replacement := vtui.NewFrameManager()
	vtui.FrameManager = replacement
	t.Cleanup(func() {
		closeFrameManagerFrames(replacement)
		replacement.Shutdown()
		vtui.FrameManager = arming
	})

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case task := <-arming.TaskChan:
		// Not run: SaveConfig would write the real configuration file. That it
		// arrived here rather than on the replacement is the whole point.
		_ = task
	case <-replacement.TaskChan:
		t.Fatal("the debounced save posted to the frame manager that replaced it")
	case <-deadline.C:
		t.Fatal("the debounced save never reached the frame manager it was armed with")
	}
}
