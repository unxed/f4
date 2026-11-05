package id3editor

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/f4/vfs"
)

type id3EditorSettings struct {
	PreserveModTime bool `json:"preserve_mod_time"`
}

func id3EditorSettingsPath() string {
	dir := vfs.CustomConfigDir
	if dir == "" {
		if userDir, err := os.UserConfigDir(); err == nil {
			dir = filepath.Join(userDir, "f4")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "id3editor.json")
}

func loadID3EditorSettings() id3EditorSettings {
	var settings id3EditorSettings
	data, err := os.ReadFile(id3EditorSettingsPath())
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	return settings
}

func saveID3EditorSettings(settings id3EditorSettings) error {
	path := id3EditorSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// #nosec G703 -- configuration path is derived from the user's config directory.
	return os.WriteFile(path, data, 0o600)
}

func id3SettingsProvider() *f4settings.StructProvider[id3EditorSettings] {
	return &f4settings.StructProvider[id3EditorSettings]{
		Definition: f4settings.Catalog{
			ID:         "id3editor",
			Categories: []f4settings.Category{{ID: "metadata", Label: f4settings.Text{English: "Metadata & reports"}}},
			Fields:     []f4settings.Field{f4settings.Scalar("id3editor.PreserveModTime", "metadata", "ID3 Tag Editor", "Preserve file modification time", "Keep the MP3 file's modification time unchanged when saving tags.", f4settings.Boolean)},
		},
		Read: func() (id3EditorSettings, error) { return loadID3EditorSettings(), nil },
		Save: func(_, next id3EditorSettings) error { return saveID3EditorSettings(next) },
	}
}
