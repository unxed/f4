package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
)

// These settings have no dialog. SaveConfig rewrites settings.ini whole, so a
// value it does not write does not survive the first save.
func TestSaveConfigKeepsTheSettingsThatHaveNoDialog(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ImageOverlay = false
	cfg.ImageX11OffsetX = 3
	cfg.ImageX11OffsetY = -2
	cfg.VideoPauseOnFocusLoss = true
	cfg.TTYXKeys = false
	cfg.TTYXKeyList = "Ctrl+Enter, Ctrl+Tab"
	cfg.HistoryDisabledBuckets = "cmdline,folders"

	back := DefaultConfig()
	parseConfigInto(&back, ini.Parse(bytes.NewReader(SerializeSettingsConfig(cfg))))
	if back.ImageOverlay || back.ImageX11OffsetX != 3 || back.ImageX11OffsetY != -2 ||
		!back.VideoPauseOnFocusLoss || back.TTYXKeys || back.TTYXKeyList != "Ctrl+Enter, Ctrl+Tab" || back.HistoryDisabledBuckets != "cmdline,folders" {
		t.Fatalf("after a save and a load: overlay %v, offsets %d,%d, pause %v, ttyx %v %q",
			back.ImageOverlay, back.ImageX11OffsetX, back.ImageX11OffsetY,
			back.VideoPauseOnFocusLoss, back.TTYXKeys, back.TTYXKeyList)
	}
}

func TestSaveConfigLeavesTheDefaultTTYXKeyListOut(t *testing.T) {
	if data := string(SerializeSettingsConfig(DefaultConfig())); strings.Contains(data, "KeyList") {
		t.Fatalf("the default TTYXi KeyList was written, freezing it in the profile:\n%s", data)
	}
}
