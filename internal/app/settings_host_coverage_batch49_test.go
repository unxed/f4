package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
)

func TestSettingsHostSaveGeometryCreatesConfigCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	config.App.GuiCols, config.App.GuiRows = 120, 40
	if err := (settingsHost{}).SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "GuiCols = 120") || !strings.Contains(string(data), "GuiRows = 40") {
		t.Fatalf("geometry config = %q, err=%v", data, err)
	}
}

func TestSettingsHostSaveGeometryUpdatesExistingConfigCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	if err := os.WriteFile(configPath, []byte("[Other]\nKeep = yes\n\n[Appearance]\nGuiCols = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config.App.GuiCols, config.App.GuiRows = 121, 41
	if err := (settingsHost{}).SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "Keep = yes") || !strings.Contains(string(data), "GuiCols = 121") {
		t.Fatalf("updated geometry config = %q, err=%v", data, err)
	}
}

func TestSettingsHostSaveGeometryPreservesPositionDisabledCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	config.App.GuiPositionSaved = false
	config.App.GuiPosX, config.App.GuiPosY = 7, 9
	if err := (settingsHost{}).SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "GuiPosX") || strings.Contains(string(data), "GuiPosY") {
		t.Fatalf("disabled GUI position was serialized: %s", data)
	}
}

func TestSettingsHostSaveGeometryWritesPositionWhenSavedCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	config.App.GuiPositionSaved = true
	config.App.GuiPosX, config.App.GuiPosY = 17, 29
	if err := (settingsHost{}).SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), "GuiPosX = 17") || !strings.Contains(string(data), "GuiPosY = 29") {
		t.Fatalf("saved GUI position was not serialized: %s", data)
	}
}

func TestSettingsHostSaveGeometryCanBeRepeatedCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	host := settingsHost{}
	if err := host.SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	config.App.GuiCols = 140
	if err := host.SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), "GuiCols = 140") {
		t.Fatalf("second geometry save did not update file: %s", data)
	}
}

func TestSettingsHostSaveSessionWritesConfiguredPathCoverageBatch49(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	if err := (settingsHost{}).SaveSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session path was not written: %v", err)
	}
}

func TestSettingsHostSessionPathReturnsConfiguredPathCoverageBatch49(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	if got := (settingsHost{}).SessionPath(); got != sessionPath {
		t.Fatalf("SessionPath = %q, want %q", got, sessionPath)
	}
}

func TestSettingsHostGuiBackendsReturnsConfiguredChoicesCoverageBatch49(t *testing.T) {
	choices := (settingsHost{}).GuiBackends()
	if len(choices) != len(startupGuiBackends) {
		t.Fatalf("GuiBackends length = %d, want %d", len(choices), len(startupGuiBackends))
	}
	for i := range startupGuiBackends {
		if choices[i] != startupGuiBackends[i] {
			t.Fatalf("GuiBackends[%d] = %q, want %q", i, choices[i], startupGuiBackends[i])
		}
	}
}

func TestSettingsHostSaveGeometryUsesNestedConfigPathCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	nested := filepath.Join(filepath.Dir(configPath), "nested", "settings.ini")
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return nested }
	t.Cleanup(func() { config.GetUserConfigIniPath = oldPath })
	if err := (settingsHost{}).SaveGeometry(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("nested geometry path was not created: %v", err)
	}
}

func TestSettingsHostSaveSessionReportsInvalidPathCoverageBatch49(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	badPath := filepath.Join(filepath.Dir(configPath), "session-dir")
	if err := os.Mkdir(badPath, 0700); err != nil {
		t.Fatal(err)
	}
	oldSessionPath := getSessionIniPath
	getSessionIniPath = func() string { return badPath }
	t.Cleanup(func() { getSessionIniPath = oldSessionPath })
	if err := (settingsHost{}).SaveSession(); err == nil {
		t.Fatal("SaveSession succeeded for a directory path")
	}
}
