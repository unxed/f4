package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

func TestConfig_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()

	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := GetUserConfigIniPath
	GetUserConfigIniPath = func() string { return userIniPath }
	origPathsFunc := GetConfigIniPaths
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	// Save original config to restore after test
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()

	// 1. Set some non-default values
	App.ShowHiddenFiles = false
	App.ColorStyle = "Classic"
	App.ShowDirPrefix = true
	App.SavePanelPaths = false
	App.EditorCrosshair = true
	App.EditorColorerBackground = false
	App.CommandLineAutoComplete = false
	App.SeparateFileExtensions = true
	App.ShowSymlinkArrow = true
	App.StartInCurrentFolder = true
	App.ArchiveTarIndexCache = false
	App.PanelScrollbarMode = PanelScrollbarMinimal
	App.ShowPanelFileInfo = true
	App.MacroRecordFormat = 1
	App.UseTrash = true
	App.TerminalCtrlNWorkspace = false
	App.ConsoleMode = "host"
	App.ConsoleOverlayUI = true
	App.WorkspaceTabMode = int(vtui.WorkspaceTabsNever)
	App.WorkspaceTabsOverlay = false
	App.CtrlTabShowsMenu = true
	App.AltNumberSwitchesTabs = false
	App.RestoreWorkspaceTabs = false
	App.WorkspaceTabNumbering = WorkspaceTabNumbersOrder
	App.ApplyCommandParallelism = 0
	App.AutoSaveSettings = false
	App.DisplayFullPathInTitle = true
	App.UseLocalLanguageFiles = true
	App.EditorAutodetectCodePage = false
	App.EditorDefaultCodePage = 1251
	App.ViewerAutodetectCodePage = true
	App.ViewerDefaultCodePage = 866
	App.ViewerOpenAsSupportedType = false

	// 2. Save
	SaveConfig()

	// 3. Reset to defaults
	App.ShowHiddenFiles = true
	App.ColorStyle = "Modern"
	App.ShowDirPrefix = false
	App.EditorCrosshair = false
	App.EditorColorerBackground = true
	App.SeparateFileExtensions = false
	App.ShowSymlinkArrow = false
	App.StartInCurrentFolder = false
	App.ArchiveTarIndexCache = true
	App.PanelScrollbarMode = PanelScrollbarOff
	App.ShowPanelFileInfo = false
	App.MacroRecordFormat = 0
	App.UseTrash = false
	App.TerminalCtrlNWorkspace = true
	App.ConsoleMode = "own"
	App.ConsoleOverlayUI = false
	App.WorkspaceTabMode = int(vtui.WorkspaceTabsAlways)
	App.WorkspaceTabsOverlay = true
	App.CtrlTabShowsMenu = false
	App.AltNumberSwitchesTabs = true
	App.RestoreWorkspaceTabs = true
	App.WorkspaceTabNumbering = WorkspaceTabNumbersAlways
	App.ApplyCommandParallelism = 1
	App.AutoSaveSettings = true
	App.DisplayFullPathInTitle = false
	App.UseLocalLanguageFiles = false
	App.EditorAutodetectCodePage = true
	App.EditorDefaultCodePage = 65001
	App.ViewerAutodetectCodePage = false
	App.ViewerDefaultCodePage = 65001
	App.ViewerOpenAsSupportedType = true

	// 4. Load
	LoadConfig()
	if App.ColorStyle != "Classic" {
		t.Errorf("LoadConfig failed to restore color style: %q", App.ColorStyle)
	}

	// 5. Verify
	if App.ShowHiddenFiles {
		t.Error("LoadConfig failed to restore ShowHiddenFiles")
	}
	if App.WorkspaceTabMode != int(vtui.WorkspaceTabsNever) {
		t.Errorf("LoadConfig failed to restore workspace tab mode: %d", App.WorkspaceTabMode)
	}
	if !App.UseLocalLanguageFiles {
		t.Error("LoadConfig failed to restore enabled local language files")
	}
	if App.WorkspaceTabsOverlay {
		t.Error("LoadConfig failed to restore disabled workspace tab overlay")
	}
	if !App.CtrlTabShowsMenu {
		t.Error("LoadConfig failed to restore Ctrl+Tab menu mode")
	}
	if App.AltNumberSwitchesTabs {
		t.Error("LoadConfig failed to restore disabled Alt+number tab switching")
	}
	if App.RestoreWorkspaceTabs {
		t.Error("LoadConfig failed to restore disabled workspace tab restoration")
	}
	if App.WorkspaceTabNumbering != WorkspaceTabNumbersOrder {
		t.Errorf("LoadConfig restored workspace tab numbering %v, want order", App.WorkspaceTabNumbering)
	}
	if !App.ShowDirPrefix {
		t.Error("LoadConfig failed to restore ShowDirPrefix")
	}
	if App.SavePanelPaths {
		t.Error("LoadConfig failed to restore SavePanelPaths")
	}
	if App.AutoSaveSettings {
		t.Error("LoadConfig failed to restore disabled AutoSaveSettings")
	}
	if !App.DisplayFullPathInTitle {
		t.Error("LoadConfig failed to restore DisplayFullPathInTitle")
	}
	if !App.EditorCrosshair {
		t.Error("LoadConfig failed to restore EditorCrosshair")
	}
	if App.EditorColorerBackground {
		t.Error("LoadConfig failed to restore EditorColorerBackground")
	}
	if App.CommandLineAutoComplete {
		t.Error("LoadConfig failed to restore CommandLineAutoComplete")
	}
	if !App.SeparateFileExtensions {
		t.Error("LoadConfig failed to restore SeparateFileExtensions")
	}
	if !App.ShowSymlinkArrow {
		t.Error("LoadConfig failed to restore an enabled ShowSymlinkArrow")
	}
	if !App.StartInCurrentFolder {
		t.Error("LoadConfig failed to restore an enabled StartInCurrentFolder")
	}
	if App.ArchiveTarIndexCache {
		t.Error("LoadConfig failed to restore a disabled ArchiveTarIndexCache")
	}
	if App.PanelScrollbarMode != PanelScrollbarMinimal {
		t.Errorf("LoadConfig restored PanelScrollbarMode %v, want minimal", App.PanelScrollbarMode)
	}
	if !App.ShowPanelFileInfo {
		t.Error("LoadConfig failed to restore ShowPanelFileInfo")
	}
	if App.MacroRecordFormat != 1 {
		t.Error("LoadConfig failed to restore MacroRecordFormat")
	}
	if !App.UseTrash {
		t.Error("LoadConfig failed to restore UseTrash")
	}
	if App.TerminalCtrlNWorkspace {
		t.Error("LoadConfig failed to restore TerminalCtrlNWorkspace")
	}
	if App.ConsoleMode != "host" {
		t.Errorf("LoadConfig failed to restore ConsoleMode: got %q, want %q", App.ConsoleMode, "host")
	}
	if !App.ConsoleOverlayUI {
		t.Error("LoadConfig failed to restore ConsoleOverlayUI")
	}
	if App.ApplyCommandParallelism != 0 {
		t.Errorf("ApplyCommandParallelism = %d, want Unlimited (0)", App.ApplyCommandParallelism)
	}
	if App.EditorAutodetectCodePage {
		t.Error("LoadConfig failed to restore disabled Editor autodetection")
	}
	if App.EditorDefaultCodePage != 1251 {
		t.Errorf("EditorDefaultCodePage = %d, want 1251", App.EditorDefaultCodePage)
	}
	if !App.ViewerAutodetectCodePage {
		t.Error("LoadConfig failed to restore enabled Viewer autodetection")
	}
	if App.ViewerDefaultCodePage != 866 {
		t.Errorf("ViewerDefaultCodePage = %d, want 866", App.ViewerDefaultCodePage)
	}
	if App.ViewerOpenAsSupportedType {
		t.Error("LoadConfig failed to restore disabled Viewer OpenAsSupportedType")
	}
}

func TestConfig_ConsoleModeDefaultsWhenAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowHiddenFiles = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.ConsoleMode = "host"
	App.ConsoleOverlayUI = true
	LoadConfig()
	if App.ConsoleMode != "own" {
		t.Fatalf("ConsoleMode must default to 'own' when setting is absent, got %q", App.ConsoleMode)
	}
	if App.ConsoleOverlayUI {
		t.Fatal("ConsoleOverlayUI must default to false when setting is absent")
	}
}

func TestConfig_TrashDefaultsOffWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[System]\nConfirmDelete = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.UseTrash = true
	LoadConfig()
	if App.UseTrash {
		t.Fatal("UseTrash must default to false when the setting is absent")
	}
}

func TestConfig_TerminalCtrlNWorkspaceDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowHiddenFiles = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.TerminalCtrlNWorkspace = false
	LoadConfig()
	if !App.TerminalCtrlNWorkspace {
		t.Fatal("TerminalCtrlNWorkspace must default to true when the setting is absent")
	}
}

func TestConfig_AltNumberSwitchesTabsDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\nWorkspaceTabMode = multiple\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.AltNumberSwitchesTabs = false
	LoadConfig()
	if !App.AltNumberSwitchesTabs {
		t.Fatal("AltNumberSwitchesTabs must default to true when the setting is absent")
	}
}

func TestConfig_WorkspaceTabModeDefaultsToAlwaysWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.WorkspaceTabMode = int(vtui.WorkspaceTabsMultiple)
	App.WorkspaceTabsOverlay = false
	LoadConfig()
	if App.WorkspaceTabMode != int(vtui.WorkspaceTabsAlways) {
		t.Fatalf("WorkspaceTabMode without a saved key = %d, want always-visible mode %d",
			App.WorkspaceTabMode, vtui.WorkspaceTabsAlways)
	}
	if !App.WorkspaceTabsOverlay {
		t.Fatal("WorkspaceTabsOverlay must default to true when the setting is absent")
	}
}

func TestConfig_RestoreWorkspaceTabsDefaultsOnWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.RestoreWorkspaceTabs = false
	LoadConfig()
	if !App.RestoreWorkspaceTabs {
		t.Fatal("RestoreWorkspaceTabs must default to true when the setting is absent")
	}
}

func TestConfig_WorkspaceTabNumberingDefaultsToAlwaysWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Interface]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.WorkspaceTabNumbering = WorkspaceTabNumbersOrder
	LoadConfig()
	if App.WorkspaceTabNumbering != WorkspaceTabNumbersAlways {
		t.Fatalf("WorkspaceTabNumbering must default to always, got %v", App.WorkspaceTabNumbering)
	}
}

func TestConfig_PanelFileInfoDefaultsHiddenWhenKeyIsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.ShowPanelFileInfo = true
	LoadConfig()
	if App.ShowPanelFileInfo {
		t.Fatal("ShowPanelFileInfo must default to false when the setting is absent")
	}
}

func TestCreateDefaultHighlightIniDocumentsColorOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "highlight.ini")
	CreateDefaultHighlightIni(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, key := range []string{
		"# [Highlight_100]",
		"Appearance.HighlightPriority",
		"user rules first, the default",
		"ContinueProcessing = 1 when a later rule",
		"# NormalColor =",
		"# SelectedColor =",
		"# CursorColor =",
		"# SelectedCursorColor =",
		"NormalColorUnderCursor",
		"SelectedColorUnderCursor",
		// Far Manager spellings of the same four colors, and the folder
		// example that shows why a rule needs an attribute of its own.
		"# NormalFileName =",
		"# SelectedFileName =",
		"# FileNameUnderCursor =",
		"# FileNameSelectedUnderCursor =",
		"# IncludeAttributes = Directory",
	} {
		if !strings.Contains(content, key) {
			t.Errorf("generated highlight.ini is missing documented %q", key)
		}
	}

	// The ordinary colors take a background just like the cursor ones, and the
	// example is the only place a reader sees that (#912).
	for _, key := range []string{"# NormalColor = ", "# SelectedColor = "} {
		idx := strings.Index(content, key)
		if idx < 0 {
			continue
		}
		line := content[idx:]
		if end := strings.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		if !strings.Contains(line, "background:") {
			t.Errorf("example %q shows no background: %s", key, line)
		}
	}
}

func TestConfig_ApplyCommandParallelismDefaultsToLogicalCPUs(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() { GetConfigIniPaths = origPathsFunc; App = oldCfg }()
	GetConfigIniPaths = func() []string { return []string{userIniPath} }
	App.ApplyCommandParallelism = 0
	LoadConfig()
	if App.ApplyCommandParallelism != runtime.NumCPU() {
		t.Fatalf("ApplyCommandParallelism = %d, want %d", App.ApplyCommandParallelism, runtime.NumCPU())
	}
}

func TestConfig_MinimalPanelScrollbarsByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	App.PanelScrollbarMode = PanelScrollbarFull
	LoadConfig()
	if App.PanelScrollbarMode != PanelScrollbarMinimal {
		t.Fatal("panel scrollbars must use minimal mode when the setting is absent")
	}
}

func TestConfig_PanelScrollbarBooleanMigration(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowPanelScrollbars = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	LoadConfig()
	if App.PanelScrollbarMode != PanelScrollbarFull {
		t.Fatalf("boolean scrollbar setting migrated to %v, want full", App.PanelScrollbarMode)
	}
}

func TestConfig_DisabledPanelScrollbarBooleanMigration(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")
	if err := os.WriteFile(userIniPath, []byte("[Panel]\nShowPanelScrollbars = 0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	LoadConfig()
	if App.PanelScrollbarMode != PanelScrollbarOff {
		t.Fatalf("disabled boolean scrollbar setting migrated to %v, want off", App.PanelScrollbarMode)
	}
}

func TestConfig_ImagesSectionRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	GetUserConfigIniPath = func() string { return userIniPath }
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
		SetImageDecoderPriorities(nil)
	}()

	App.SlideShowDelay = 9
	App.ImageExternalTimeout = 42
	App.ImageDecoderPriority = "external:-5|go-std:3"
	SaveConfig()

	App.SlideShowDelay = 5
	App.ImageExternalTimeout = 0
	App.ImageDecoderPriority = ""
	LoadConfig()

	if App.SlideShowDelay != 9 {
		t.Errorf("SlideShowDelay is %d, want 9", App.SlideShowDelay)
	}
	if App.ImageExternalTimeout != 42 {
		t.Errorf("ExternalTimeout is %d, want 42", App.ImageExternalTimeout)
	}
	if App.ImageDecoderPriority != "external:-5|go-std:3" {
		t.Errorf("DecoderPriority is %q", App.ImageDecoderPriority)
	}
	if got := ImageDecoderPriorityOf("external", -10); got != -5 {
		t.Errorf("loading must apply the priorities, external is %d", got)
	}
	if got := ImageDecoderPriorityOf("go-bmp", 10); got != 10 {
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
	origPathsFunc := GetConfigIniPaths
	GetConfigIniPaths = func() []string { return []string{globalPath, userPath} }
	defer func() { GetConfigIniPaths = origPathsFunc }()

	// Save original config to restore after test
	oldCfg := App
	defer func() { App = oldCfg }()

	// Reset config to defaults before loading
	App = F4Config{
		ShowHiddenFiles:         true,
		EditorTabSize:           4,
		EditorCrosshair:         false,
		CommandLineAutoComplete: true, // A default that shouldn't be touched
	}

	LoadConfig()

	if !App.ShowHiddenFiles {
		t.Error("User config (ShowHiddenFiles=1) should override global (0)")
	}
	if App.EditorTabSize != 8 {
		t.Errorf("Global config (TabSize=8) was not loaded. Got %d", App.EditorTabSize)
	}
	if !App.EditorCrosshair {
		t.Error("User config (Crosshair=1) was not loaded.")
	}
	if !App.CommandLineAutoComplete {
		t.Error("Default value (CommandLineAutoComplete=true) was incorrectly overwritten.")
	}
}

func TestConfig_GuiFontPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Переопределяем путь к конфигурационному файлу для тестов
	oldPathFunc := GetUserConfigIniPath
	GetUserConfigIniPath = func() string {
		return filepath.Join(tmpDir, "settings.ini")
	}
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = oldPathFunc
		App = oldCfg
	}()

	// Задаем тестовые значения
	App.GuiFont = "UbuntuMono-Regular"
	App.GuiUseSystemMonospace = false
	App.GuiFontSize = 22
	SaveConfig()

	// Сбрасываем текущую конфигурацию в памяти
	App.GuiFont = ""
	App.GuiUseSystemMonospace = true
	App.GuiFontSize = 0

	// Читаем заново из временного файла
	LoadConfig()

	if App.GuiFont != "UbuntuMono-Regular" {
		t.Errorf("Expected GuiFont to be 'UbuntuMono-Regular', got %q", App.GuiFont)
	}
	if App.GuiFontSize != 22 {
		t.Errorf("Expected GuiFontSize to be 22, got %d", App.GuiFontSize)
	}
	if App.GuiUseSystemMonospace {
		t.Error("Expected GuiUseSystemMonospace to remain disabled")
	}
}

func TestConfig_GuiDimensionsPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	oldPathFunc := GetUserConfigIniPath
	GetUserConfigIniPath = func() string {
		return filepath.Join(tmpDir, "settings.ini")
	}
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = oldPathFunc
		App = oldCfg
	}()

	// 1. Задаем тестовые значения
	App.GuiCols = 120
	App.GuiRows = 45
	App.GuiPosX = -123
	App.GuiPosY = 456
	App.GuiPositionSaved = true
	App.ConfirmExit = false

	SaveConfig()

	// 2. Сбрасываем текущую конфигурацию в памяти
	App.GuiCols = 0
	App.GuiRows = 0
	App.GuiPosX = 0
	App.GuiPosY = 0
	App.GuiPositionSaved = false
	App.ConfirmExit = true

	// 3. Читаем заново из временного файла
	LoadConfig()

	// 4. Проверяем корректность восстановления
	if App.GuiCols != 120 {
		t.Errorf("Expected GuiCols to be 120, got %d", App.GuiCols)
	}
	if App.GuiRows != 45 {
		t.Errorf("Expected GuiRows to be 45, got %d", App.GuiRows)
	}
	if !App.GuiPositionSaved || App.GuiPosX != -123 || App.GuiPosY != 456 {
		t.Errorf("Expected GUI position to be -123,456, got saved=%v x=%d y=%d", App.GuiPositionSaved, App.GuiPosX, App.GuiPosY)
	}
	if App.ConfirmExit {
		t.Error("Expected ConfirmExit to be loaded as false, got true")
	}
}

// TestConfig_LayoutRoundTrip verifies that the [Layout] section persists
// our three known keys and round-trips any unknown keys (e.g. far2l's
// FullscreenHelp / PanelsDisposition) untouched on save.
func TestConfig_LayoutRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := GetUserConfigIniPath
	origPathsFunc := GetConfigIniPaths
	GetUserConfigIniPath = func() string { return iniPath }
	GetConfigIniPaths = func() []string { return []string{iniPath} }
	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
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
	if App.WidthDecrement != 3 || App.LeftHeightDecrement != 5 || App.RightHeightDecrement != 7 {
		t.Errorf("LoadConfig [Layout] values: W=%d L=%d R=%d, want 3/5/7",
			App.WidthDecrement, App.LeftHeightDecrement, App.RightHeightDecrement)
	}
	if App.LayoutExtras["FullscreenHelp"] != "1" || App.LayoutExtras["PanelsDisposition"] != "2" {
		t.Errorf("LoadConfig extras: %v", App.LayoutExtras)
	}

	// Save and re-read the file — extras must survive verbatim.
	App.WidthDecrement = -2
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
	ini := ini.Parse(strings.NewReader("[Mouse]\nPanelUp = 5\nPanelDown = -2\n"))
	if got := LoadWheelLines(ini, "PanelUp"); got != 5 {
		t.Errorf("Expected 5, got %d", got)
	}
	if got := LoadWheelLines(ini, "PanelDown"); got != 0 {
		t.Errorf("Expected negative value to clamp to 0, got %d", got)
	}
	if got := LoadWheelLines(ini, "ViewerUp"); got != 0 {
		t.Errorf("Expected missing key to default to 0, got %d", got)
	}
}

func TestWheelScrollLines(t *testing.T) {
	if got := WheelScrollLines(7); got != 7 {
		t.Errorf("Expected configured 7, got %d", got)
	}
	if got := WheelScrollLines(0); got != vtui.WheelLinesPerNotch() {
		t.Errorf("Expected 0 to resolve to system %d, got %d", vtui.WheelLinesPerNotch(), got)
	}
	if got := WheelScrollLines(-3); got != vtui.WheelLinesPerNotch() {
		t.Errorf("Expected negative to resolve to system %d, got %d", vtui.WheelLinesPerNotch(), got)
	}
}

func TestConfig_MouseWheelRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	userIniPath := filepath.Join(tmpDir, "settings.ini")

	origUserPathFunc := GetUserConfigIniPath
	GetUserConfigIniPath = func() string { return userIniPath }
	origPathsFunc := GetConfigIniPaths
	GetConfigIniPaths = func() []string { return []string{userIniPath} }

	oldCfg := App
	defer func() {
		GetUserConfigIniPath = origUserPathFunc
		GetConfigIniPaths = origPathsFunc
		App = oldCfg
	}()

	App.WheelPanelUp = 1
	App.WheelPanelDown = 2
	App.WheelEditorUp = 3
	App.WheelEditorDown = 4
	App.WheelViewerUp = 5
	App.WheelViewerDown = 6
	App.WheelMenuUp = 7
	App.WheelMenuDown = 8
	App.WheelTableUp = 9
	App.WheelTableDown = 10
	SaveConfig()

	App.WheelPanelUp = 0
	App.WheelPanelDown = 0
	App.WheelEditorUp = 0
	App.WheelEditorDown = 0
	App.WheelViewerUp = 0
	App.WheelViewerDown = 0
	App.WheelMenuUp = 0
	App.WheelMenuDown = 0
	App.WheelTableUp = 0
	App.WheelTableDown = 0
	LoadConfig()

	got := []int{
		App.WheelPanelUp, App.WheelPanelDown,
		App.WheelEditorUp, App.WheelEditorDown,
		App.WheelViewerUp, App.WheelViewerDown,
		App.WheelMenuUp, App.WheelMenuDown,
		App.WheelTableUp, App.WheelTableDown,
	}
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wheel config field %d: expected %d, got %d", i, want[i], got[i])
		}
	}
}
