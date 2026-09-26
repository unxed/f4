package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

func prepareSessionOptionsCoverageBatch47(t *testing.T, withFrameManager bool) (string, string) {
	t.Helper()
	oldConfigPath := config.GetUserConfigIniPath
	oldConfigPaths := config.GetConfigIniPaths
	oldSessionPath := getSessionIniPath
	oldApp := config.App
	oldSessionLoaded := sessionLoaded
	oldFrameManager := vtui.FrameManager
	oldBackend := vtui.ActiveBackend()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.ini")
	sessionPath := filepath.Join(dir, "session.ini")
	config.GetUserConfigIniPath = func() string { return configPath }
	config.GetConfigIniPaths = func() []string { return []string{configPath} }
	getSessionIniPath = func() string { return sessionPath }
	config.App.AutoSaveDialogSettings = false
	vtui.FrameManager = nil
	if withFrameManager {
		vtui.FrameManager = vtui.NewFrameManager()
		vtui.SetActiveBackend("gui")
	}
	t.Cleanup(func() {
		config.GetUserConfigIniPath = oldConfigPath
		config.GetConfigIniPaths = oldConfigPaths
		getSessionIniPath = oldSessionPath
		config.App = oldApp
		sessionLoaded = oldSessionLoaded
		vtui.FrameManager = oldFrameManager
		vtui.SetActiveBackend(oldBackend)
	})
	return configPath, sessionPath
}

func TestSaveSessionWithOptionsSavesGUIWindowOnlyCoverageBatch47(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSessionWithOptions(false, false, true)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("GUI settings file was not written: %v", err)
	}
	if got := string(data); !strings.Contains(got, "GuiCols = 80") || !strings.Contains(got, "GuiRows = 25") {
		t.Fatalf("GUI settings do not contain captured size: %s", got)
	}
}

func TestSaveSessionWithOptionsUsesFullConfigForDialogSettingsCoverageBatch47(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.AutoSaveDialogSettings = true
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSessionWithOptions(false, false, true)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("full settings file was not written: %v", err)
	}
	if !strings.Contains(string(data), "[Interface]") {
		t.Fatalf("full settings serialization is missing: %s", data)
	}
}

func TestSaveSessionWithOptionsSkipsUnchangedGUIWindowCoverageBatch47(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 80, 25
	saveSessionWithOptions(false, false, true)
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("unchanged GUI geometry created a file, stat error=%v", err)
	}
}

func TestSaveSessionWithOptionsFlushesDialogSettingsWithoutGUICoverageBatch47(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	config.App.AutoSaveDialogSettings = true
	saveSessionWithOptions(false, false, false)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("dialog settings were not flushed: %v", err)
	}
}

func TestSaveSessionWithOptionsSavesPanelSettingsWithDialogFlushCoverageBatch47(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	config.App.AutoSaveDialogSettings = true
	saveSessionWithOptions(true, false, false)
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel session was not written: %v", err)
	}
}

func TestSaveSessionWithOptionsSavesCurrentPanelWithoutGUICoverageBatch47(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	saveSessionWithOptions(false, true, false)
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("current-panel session was not written: %v", err)
	}
}

func TestSaveSessionWithOptionsSavesGUIAndPanelSettingsTogetherCoverageBatch47(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSessionWithOptions(true, false, true)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("GUI settings were not written: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not written: %v", err)
	}
}

func TestSaveSessionWithOptionsSavesBothPanelGroupsCoverageBatch47(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	saveSessionWithOptions(true, true, false)
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("both panel groups were not written: %v", err)
	}
}

func TestSaveSessionSkipsWhenAutomaticSavingDisabledCoverageBatch47(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	sessionLoaded = true
	config.App.AutoSaveSettings = false
	config.App.AutoSavePanelSettings = true
	config.App.AutoSaveCurrentPanel = true
	SaveSession()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("disabled automatic saving created config, stat error=%v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("disabled automatic saving created session, stat error=%v", err)
	}
}

func TestSaveSessionFlushesDialogSettingsWhenLoadedCoverageBatch47(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	sessionLoaded = true
	config.App.AutoSaveSettings = true
	config.App.AutoSavePanelSettings = false
	config.App.AutoSaveCurrentPanel = false
	config.App.AutoSaveGUIWindow = false
	config.App.AutoSaveDialogSettings = true
	SaveSession()
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("loaded session did not flush dialog settings: %v", err)
	}
}
