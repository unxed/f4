package visren

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/unxed/f4/vfs"
)

type config struct {
	WordDiv      string `json:"word_div"`
	EditorFormat string `json:"editor_format"`
}

const (
	editorFormatSourceTarget = "source_target"
	editorFormatTargetsOnly  = "targets_only"
)

func configPath() string {
	dir := vfs.CustomConfigDir
	if dir == "" {
		if userDir, err := os.UserConfigDir(); err == nil {
			dir = filepath.Join(userDir, "f4")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "visren.json")
}

func loadConfig() config {
	cfg := config{WordDiv: "-. _&", EditorFormat: editorFormatSourceTarget}
	data, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.WordDiv == "" {
		cfg.WordDiv = "-. _&"
	}
	if cfg.EditorFormat != editorFormatSourceTarget && cfg.EditorFormat != editorFormatTargetsOnly {
		cfg.EditorFormat = editorFormatSourceTarget
	}
	if runes := []rune(cfg.WordDiv); len(runes) > 18 {
		cfg.WordDiv = string(runes[:18])
	}
	return cfg
}

func saveConfig(cfg config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".visren-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	for len(data) > 0 {
		written, err := tmp.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
