package panel

import (
	"bufio"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/keymap"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DriveBookmark is a named link shown in the Alt+F1/Alt+F2 drive menu.
// Unlike folder bookmarks, drive-menu links are not limited to ten numbered
// slots. Hotkey contains the Far-style raw spelling (for example "Q" or
// "CtrlF5").
type DriveBookmark struct {
	Name   string
	Path   string
	Hotkey string
}

// DriveBookmarksFilePath returns the user-config location of the named drive
// menu links. It deliberately has its own file: bookmarks.ini is shared with
// far2l and remains the storage for the ten numbered folder shortcuts.
func DriveBookmarksFilePath() string {
	if config.IsPortableProfile() {
		return filepath.Join(config.GetF4ConfigDir(), "settings", "drive-bookmarks.ini")
	}
	configDir, _ := config.UserConfigDir()
	return filepath.Join(configDir, "f4", "settings", "drive-bookmarks.ini")
}

// LoadDriveBookmarks reads the ordered INI list. Numeric sections are
// accepted so the file stays human-editable; gaps are ignored when the list
// is returned. Missing files mean an empty list.
func LoadDriveBookmarks(path string) ([]DriveBookmark, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	byIndex := make(map[int]DriveBookmark)
	var current int
	validSection := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			index, parseErr := strconv.Atoi(strings.TrimSpace(line[1 : len(line)-1]))
			validSection = parseErr == nil && index >= 0
			if validSection {
				current = index
				if _, exists := byIndex[current]; !exists {
					byIndex[current] = DriveBookmark{}
				}
			}
			continue
		}
		if !validSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		bookmark := byIndex[current]
		switch key {
		case "Name":
			bookmark.Name = value
		case "Path":
			bookmark.Path = value
		case "Hotkey":
			bookmark.Hotkey = value
		}
		byIndex[current] = bookmark
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(byIndex))
	for index, bookmark := range byIndex {
		if strings.TrimSpace(bookmark.Name) == "" || strings.TrimSpace(bookmark.Path) == "" {
			continue
		}
		indices = append(indices, index)
	}
	sort.Ints(indices)
	bookmarks := make([]DriveBookmark, 0, len(indices))
	for _, index := range indices {
		bookmarks = append(bookmarks, byIndex[index])
	}
	return bookmarks, nil
}

// SaveDriveBookmarks writes the named links in a deterministic compact INI
// form and publishes the complete file atomically.
func SaveDriveBookmarks(path string, bookmarks []DriveBookmark) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	var buf strings.Builder
	first := true
	nextIndex := 0
	for _, bookmark := range bookmarks {
		name := strings.TrimSpace(bookmark.Name)
		pathValue := strings.TrimSpace(bookmark.Path)
		if name == "" || pathValue == "" {
			continue
		}
		if !first {
			buf.WriteByte('\n')
		}
		first = false
		buf.WriteByte('[')
		buf.WriteString(strconv.Itoa(nextIndex))
		nextIndex++
		buf.WriteString("]\nName=")
		buf.WriteString(name)
		buf.WriteString("\nPath=")
		buf.WriteString(pathValue)
		buf.WriteString("\nHotkey=")
		buf.WriteString(strings.TrimSpace(bookmark.Hotkey))
		buf.WriteByte('\n')
	}
	return config.WriteUserFileAtomically(path, []byte(buf.String()), 0o600)
}

func driveBookmarkIsValid(bookmark DriveBookmark) bool {
	return strings.TrimSpace(bookmark.Name) != "" && strings.TrimSpace(bookmark.Path) != ""
}

func DriveBookmarkKeyMatches(bookmark DriveBookmark, eKey string) bool {
	return driveBookmarkIsValid(bookmark) && strings.TrimSpace(bookmark.Hotkey) != "" &&
		strings.EqualFold(strings.TrimSpace(bookmark.Hotkey), strings.TrimSpace(eKey))
}

func DriveBookmarkMenuText(bookmark DriveBookmark) string {
	name := dialog.EscapeAmpersand(strings.TrimSpace(bookmark.Name))
	key := strings.TrimSpace(bookmark.Hotkey)
	// One space between the hotkey column and the name, as in the Tools (f4#1148).
	if key == "" {
		return "  " + name
	}
	if len([]rune(key)) == 1 && !strings.ContainsAny(key, "+") {
		return "&" + key + " " + name
	}
	return keymap.FormatKeyForUI(key) + " " + name
}
