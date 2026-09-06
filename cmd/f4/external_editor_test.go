package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredExternalEditorCommand(t *testing.T) {
	oldConfig := AppConfig
	oldProbe := probeGUIBackend
	t.Cleanup(func() {
		AppConfig = oldConfig
		probeGUIBackend = oldProbe
	})

	AppConfig.ExternalEditorCommand = "legacy-editor"
	AppConfig.ExternalEditorConsole = "micro"
	AppConfig.ExternalEditorGUI = "gedit"

	probeGUIBackend = func() string { return "" }
	if got := configuredExternalEditorCommand(); got != "micro" {
		t.Fatalf("console editor = %q, want micro", got)
	}

	probeGUIBackend = func() string { return "wayland" }
	if got := configuredExternalEditorCommand(); got != "gedit" {
		t.Fatalf("GUI editor = %q, want gedit", got)
	}

	AppConfig.ExternalEditorGUI = ""
	if got := configuredExternalEditorCommand(); got != "legacy-editor" {
		t.Fatalf("GUI legacy fallback = %q, want legacy-editor", got)
	}

	AppConfig.ExternalEditorCommand = ""
	AppConfig.ExternalEditorConsole = ""
	probeGUIBackend = func() string { return "" }
	if got := configuredExternalEditorCommand(); got != "" {
		t.Fatalf("empty editor configuration = %q, want empty", got)
	}
}

func TestConfig_ExternalEditorCommandsRoundTripAndLegacyFallback(t *testing.T) {
	tmpDir := t.TempDir()
	iniPath := filepath.Join(tmpDir, "settings.ini")
	oldConfig := AppConfig
	oldUserPath := getUserConfigIniPath
	oldConfigPaths := getConfigIniPaths
	t.Cleanup(func() {
		AppConfig = oldConfig
		getUserConfigIniPath = oldUserPath
		getConfigIniPaths = oldConfigPaths
	})
	getUserConfigIniPath = func() string { return iniPath }
	getConfigIniPaths = func() []string { return []string{iniPath} }

	AppConfig.ExternalEditorCommand = "legacy-editor"
	AppConfig.ExternalEditorConsole = "micro"
	AppConfig.ExternalEditorGUI = "gedit"
	SaveConfig()

	AppConfig.ExternalEditorCommand = ""
	AppConfig.ExternalEditorConsole = ""
	AppConfig.ExternalEditorGUI = ""
	LoadConfig()
	if AppConfig.ExternalEditorConsole != "micro" || AppConfig.ExternalEditorGUI != "gedit" {
		t.Fatalf("split editor commands after round trip: console=%q GUI=%q", AppConfig.ExternalEditorConsole, AppConfig.ExternalEditorGUI)
	}

	if err := os.WriteFile(iniPath, []byte("[Editor]\nExternalEditorCommand = old-editor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	LoadConfig()
	if AppConfig.ExternalEditorConsole != "old-editor" || AppConfig.ExternalEditorGUI != "old-editor" {
		t.Fatalf("legacy editor command migration: console=%q GUI=%q", AppConfig.ExternalEditorConsole, AppConfig.ExternalEditorGUI)
	}
}
