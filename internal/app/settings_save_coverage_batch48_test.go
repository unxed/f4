package app

import (
	"os"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
)

func TestSaveSettingsGroupsSkipsWhenNothingSelectedCoverageBatch48(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	saveSettingsGroups(false, false, false)
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("empty save selection created config, stat error=%v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("empty save selection created session, stat error=%v", err)
	}
}

func TestSaveSettingsGroupsSavesGeneralOnlyCoverageBatch48(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	saveSettingsGroups(true, false, false)
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "[Interface]") {
		t.Fatalf("general settings were not serialized: err=%v data=%s", err, data)
	}
}

func TestSaveSettingsGroupsSavesPanelOnlyCoverageBatch48(t *testing.T) {
	_, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	saveSettingsGroups(false, true, false)
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not saved: %v", err)
	}
}

func TestSaveSettingsGroupsSavesWindowOnlyCoverageBatch48(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSettingsGroups(false, false, true)
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "[Appearance]") {
		t.Fatalf("window settings were not serialized: err=%v data=%s", err, data)
	}
}

func TestSaveSettingsGroupsSavesGeneralAndPanelCoverageBatch48(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, false)
	saveSettingsGroups(true, true, false)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("general settings were not saved: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not saved: %v", err)
	}
}

func TestSaveSettingsGroupsSavesWindowAndPanelCoverageBatch48(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSettingsGroups(false, true, true)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("window settings were not saved: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not saved: %v", err)
	}
}

func TestSaveSettingsGroupsSavesAllGroupsCoverageBatch48(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSettingsGroups(true, true, true)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("general/window settings were not saved: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not saved: %v", err)
	}
}

func TestSaveSettingsGroupsUsesFullConfigWhenGeneralAndWindowSelectedCoverageBatch48(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSettingsGroups(true, false, true)
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "[Interface]") {
		t.Fatalf("combined general/window save was not full config: err=%v data=%s", err, data)
	}
}

func TestSaveSettingsGroupsWritesWindowFileWithoutFrameManagerCoverageBatch48(t *testing.T) {
	configPath, _ := prepareSessionOptionsCoverageBatch47(t, false)
	saveSettingsGroups(false, false, true)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("window-only save without FrameManager was not written: %v", err)
	}
}

func TestSaveSettingsGroupsWritesPanelAfterWindowCoverageBatch48(t *testing.T) {
	configPath, sessionPath := prepareSessionOptionsCoverageBatch47(t, true)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	saveSettingsGroups(false, true, true)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("window settings were not saved before panel settings: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("panel settings were not saved after window settings: %v", err)
	}
}
