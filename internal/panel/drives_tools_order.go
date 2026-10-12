package panel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/sysinfo"
)

// The plugin rows of the drive menu (AI, Android, Network and the rest) are
// the "tools" of f4#1148. Their order is the order they registered in, which
// the user cannot influence; this file keeps the order the user chose with
// Ctrl+Up and Ctrl+Down in the menu, one tool name per line.

// Keys of the built-in tool rows in the order file. A plugin's name is its
// key; these cannot be one, since a plugin name never starts with "@".
const (
	driveToolKeyOtherPanel     = "@other-panel"
	driveToolKeyTemporaryPanel = "@temporary-panel"
	driveToolKeyRegistry       = "@windows-registry"
)

// DriveToolsOrderFilePath is where the chosen order of the drive menu's tool
// rows is stored.
func DriveToolsOrderFilePath() string {
	if config.IsPortableProfile() {
		return filepath.Join(config.GetF4ConfigDir(), "settings", "drive-tools-order.txt")
	}
	configDir, _ := config.UserConfigDir()
	return filepath.Join(configDir, "f4", "settings", "drive-tools-order.txt")
}

// LoadDriveToolsOrder reads the stored order. A missing file is an empty
// order, not an error: nobody has rearranged anything yet.
func LoadDriveToolsOrder(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// SaveDriveToolsOrder writes the order, one name per line.
func SaveDriveToolsOrder(path string, names []string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	var buf strings.Builder
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			buf.WriteString(name)
			buf.WriteByte('\n')
		}
	}
	return config.WriteUserFileAtomically(path, []byte(buf.String()), 0o600)
}

// orderDriveTools arranges entries the way the user left them: the names in
// order come first, in that order, and every other entry follows in the
// order it came in. A name in order that no entry carries (a plugin that is
// not installed any more) costs nothing, and brings back its place when the
// plugin returns.
func orderDriveTools(entries []sysinfo.DriveEntry, order []string) []sysinfo.DriveEntry {
	if len(order) == 0 {
		return entries
	}
	ordered := make([]sysinfo.DriveEntry, 0, len(entries))
	taken := make([]bool, len(entries))
	for _, name := range order {
		for i, entry := range entries {
			if !taken[i] && entry.Name == name {
				taken[i] = true
				ordered = append(ordered, entry)
				break
			}
		}
	}
	for i, entry := range entries {
		if !taken[i] {
			ordered = append(ordered, entry)
		}
	}
	return ordered
}

// moveDriveTool moves name one place by delta (-1 up, +1 down) within names,
// and reports false when it is not there or is already at that end.
func moveDriveTool(names []string, name string, delta int) ([]string, bool) {
	for i, candidate := range names {
		if candidate != name {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(names) {
			return names, false
		}
		moved := append([]string(nil), names...)
		moved[i], moved[j] = moved[j], moved[i]
		return moved, true
	}
	return names, false
}

// moveDriveToolInFile moves one tool of the menu, whose rows currently read
// shown, by delta and stores the whole new order.
func moveDriveToolInFile(shown []string, name string, delta int) (bool, error) {
	moved, ok := moveDriveTool(shown, name, delta)
	if !ok {
		return false, nil
	}
	return true, SaveDriveToolsOrder(DriveToolsOrderFilePath(), moved)
}
