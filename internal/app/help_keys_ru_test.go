package app

import (
	"github.com/unxed/f4/internal/keymap"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
)

func TestGenerateKeysHelpTopic_Russian(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	oldLang := config.App.Language
	defer func() {
		config.App.Language = oldLang
		initLang()
	}()
	config.App.Language = "ru"
	initLang()

	topic := generateKeysHelpTopic("PanelNav", "t", []string{"Shell"}, "")
	joined := strings.Join(topic.Lines, "\n")
	if !strings.Contains(joined, "Открыть файл в просмотрщике") {
		t.Errorf("Expected Russian description in generated topic\n---\n%s", joined)
	}

	topic2 := generateKeysHelpTopic("ViewerEditor", "t", []string{"Editor"}, "")
	if !strings.Contains(strings.Join(topic2.Lines, "\n"), "Сохранить файл") {
		t.Errorf("Expected Russian editor description in generated topic")
	}
}

// The generated help must follow the *help* language even when it
// differs from the UI language: a Russian .hlf gets Russian action
// descriptions with an English UI.
func TestGenerateKeysHelpTopic_HelpLanguageOverridesUI(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	oldLang := config.App.Language
	defer func() {
		config.App.Language = oldLang
		initLang()
	}()
	config.App.Language = "en"
	initLang()

	oldStrings := dialog.HelpActionStrings
	defer func() { dialog.HelpActionStrings = oldStrings }()
	dialog.HelpActionStrings = dialog.LoadHelpLangStrings("ru")
	if dialog.HelpActionStrings == nil {
		t.Fatal("Russian help strings not found")
	}

	topic := generateKeysHelpTopic("ViewerEditor", "t", []string{"Editor"}, "")
	joined := strings.Join(topic.Lines, "\n")
	if !strings.Contains(joined, "Сохранить файл") {
		t.Errorf("Expected Russian description with English UI\n---\n%s", joined)
	}
	if !strings.Contains(joined, "Редактор:") {
		t.Errorf("Expected Russian area header\n---\n%s", joined)
	}
}
