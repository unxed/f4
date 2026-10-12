package panel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/sysinfo"
)

// DriveToolsVisibilityFilePath is where the per-tool visibility choices of
// the drive menu are stored. A missing file means that every registered tool
// is visible.
func DriveToolsVisibilityFilePath() string {
	if config.IsPortableProfile() {
		return filepath.Join(config.GetF4ConfigDir(), "settings", "drive-tools-visibility.txt")
	}
	configDir, _ := config.UserConfigDir()
	return filepath.Join(configDir, "f4", "settings", "drive-tools-visibility.txt")
}

// LoadDisabledDriveTools reads the names hidden from the drive menu. Unknown
// names are retained so a temporarily unavailable plugin can stay hidden when
// it is loaded again later.
func LoadDisabledDriveTools(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names, nil
}

// SaveDisabledDriveTools writes one hidden tool name per line.
func SaveDisabledDriveTools(path string, names []string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	var buf strings.Builder
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		buf.WriteString(name)
		buf.WriteByte('\n')
	}
	return config.WriteUserFileAtomically(path, []byte(buf.String()), 0o600)
}

// FilterVisibleDriveTools removes only the explicitly disabled registered
// tools. It leaves the input order and entry metadata unchanged.
func FilterVisibleDriveTools(entries []sysinfo.DriveEntry, disabled []string) []sysinfo.DriveEntry {
	if len(disabled) == 0 {
		return entries
	}
	hidden := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		hidden[name] = true
	}
	visible := make([]sysinfo.DriveEntry, 0, len(entries))
	for _, entry := range entries {
		if !hidden[entry.Name] {
			visible = append(visible, entry)
		}
	}
	return visible
}

// HiddenDriveToolsAfter returns the names the visibility file should hold once
// the tools of drives are shown or hidden as shown says (shown[i] is for
// drives[i]). It keeps the file's order and the names of tools that are not
// registered right now, and puts the newly hidden ones at the end (f4#918).
func HiddenDriveToolsAfter(disabled []string, drives []sysinfo.DriveEntry, shown []bool) []string {
	stillHidden := map[string]bool{}
	for i, drv := range drives {
		if i < len(shown) && !shown[i] {
			stillHidden[drv.Name] = true
		}
	}
	var names []string
	for _, n := range disabled {
		known := false
		for _, drv := range drives {
			if drv.Name == n {
				known = true
				break
			}
		}
		if !known || stillHidden[n] {
			names = append(names, n)
			delete(stillHidden, n)
		}
	}
	for _, drv := range drives {
		if stillHidden[drv.Name] {
			names = append(names, drv.Name)
			delete(stillHidden, drv.Name)
		}
	}
	return names
}
