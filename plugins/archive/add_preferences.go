package archive

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/unxed/f4/internal/config"
)

type addArchivePreferences struct {
	KeepFileExtension *bool `json:"keep_file_extension"`
}

var addArchivePreferencesPath = func() string {
	return filepath.Join(config.GetF4ConfigDir(), "archive_preferences.json")
}

func loadKeepArchiveFileExtension() bool {
	data, err := os.ReadFile(addArchivePreferencesPath())
	if err != nil {
		return true
	}
	var preferences addArchivePreferences
	if json.Unmarshal(data, &preferences) != nil || preferences.KeepFileExtension == nil {
		return true
	}
	return *preferences.KeepFileExtension
}

func saveKeepArchiveFileExtension(keep bool) error {
	data, err := json.Marshal(addArchivePreferences{KeepFileExtension: &keep})
	if err != nil {
		return err
	}
	path := addArchivePreferencesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil { // #nosec G703 -- path is in f4's private config directory.
		return err
	}
	return os.Rename(tmp, path)
}
