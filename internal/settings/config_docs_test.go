package settings

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// configEditorKeys is every key f4:config can list: what SaveConfig writes
// by default, plus the keys it writes only in some states.
func configEditorKeys() map[string]bool {
	cfg := config.DefaultConfig()
	cfg.GuiPositionSaved = true
	cfg.TTYXKeyList = config.DefaultTTYXKeyList + ", Ctrl+Shift+Down"
	keys := make(map[string]bool)
	for _, option := range config.Options(cfg) {
		keys[option.Section+"."+option.Key] = true
	}
	return keys
}

func TestConfigOptionDocExplainsEveryKey(t *testing.T) {
	keys := configEditorKeys()
	for _, name := range []string{"Appearance.GuiPosX", "Appearance.GuiPosY", "TTYXi.KeyList"} {
		if !keys[name] {
			t.Fatalf("test setup: %s is not written, the conditional keys changed", name)
		}
	}
	for name := range keys {
		section, key, _ := strings.Cut(name, ".")
		if doc := ConfigOptionDoc(section, key); strings.TrimSpace(doc.Description) == "" {
			t.Errorf("%s has no description: give its Settings Center field one, or add it to configOptionExtraDocs", name)
		}
	}
	// A far2l [Layout] key f4 only keeps is explained too.
	if doc := ConfigOptionDoc("Layout", "FullscreenHelp"); doc.Description != configOptionLayoutExtraDoc {
		t.Errorf("unknown [Layout] key description = %q", doc.Description)
	}
}

func TestConfigOptionExtraDocsNameExistingKeys(t *testing.T) {
	keys := configEditorKeys()
	for name := range configOptionExtraDocs {
		if !keys[name] {
			t.Errorf("configOptionExtraDocs explains %s, which settings.ini no longer has", name)
		}
	}
}

func TestEveryCoreSettingIsStoredInAKey(t *testing.T) {
	stored := make(map[string]bool)
	for _, fields := range configOptionFields() {
		for _, field := range fields {
			stored[field.ID] = true
		}
	}
	for _, field := range coreSettingsStaticFields() {
		if !stored[field.ID] && field.ID != terminalThemeID { // derived from the colour keys, stores nothing itself
			t.Errorf("Settings Center field %s changes no settings.ini key", field.ID)
		}
	}
}

func TestConfigOptionDocFollowsKeysWhoseNamesDiffer(t *testing.T) {
	for _, tc := range []struct{ section, key, topic string }{
		{"Proxy", "Password", "Setting.ProxyPass"},
		{"VMenu", "MenuStopWrapOnEdge", "Setting.MenuLoopScroll"},
		{"Mouse", "PanelUp", "Setting.WheelPanelUp"},
		{"PathHints", "Timeout", "Setting.PathHintTimeout"},
		{"Startup", "Mode", "Setting.StartupMode"},
		{"Compare", "MaxDepth", "Setting.Compare.MaxDepth"},
		{"History", "ShowTimes", "Setting.HistoryShowTimes.0"},
		{"Panel", "VimHotkeys", "Setting.NavigationMode"},
	} {
		if got := ConfigOptionDoc(tc.section, tc.key).HelpTopic; got != tc.topic {
			t.Errorf("%s.%s help topic = %q, want %q", tc.section, tc.key, got, tc.topic)
		}
	}

	tab := ConfigOptionDoc("Editor", "TabSize")
	if tab.Label == "" || !strings.Contains(tab.Description, "tab-stop") {
		t.Errorf("Editor.TabSize = %+v, want Settings Center's label and description", tab)
	}
	drives := ConfigOptionDoc("Panel", "DriveMenuOptions")
	if tab := strings.Count(drives.Description, "\n"); tab < 14 || !strings.Contains(drives.Description, "\n13: ") {
		t.Errorf("DriveMenuOptions description = %q, want the text and one line per bit", drives.Description)
	}
	if doc := ConfigOptionDoc("Images", "SlideShowDelay"); doc.HelpTopic != "" || doc.Label != "" {
		t.Errorf("Images.SlideShowDelay = %+v, want a description alone", doc)
	}
}

func TestConfigOptionDocHelpTopicsExist(t *testing.T) {
	old := vtui.GlobalHelpEngine
	vtui.GlobalHelpEngine = vtui.NewHelpEngine(nil)
	t.Cleanup(func() { vtui.GlobalHelpEngine = old })
	InstallHelp("en")
	for name := range configEditorKeys() {
		section, key, _ := strings.Cut(name, ".")
		if topic := ConfigOptionDoc(section, key).HelpTopic; topic != "" && vtui.GlobalHelpEngine.GetTopic(topic) == nil {
			t.Errorf("%s points Shift+F1 at %q, which InstallHelp does not generate", name, topic)
		}
	}
}
