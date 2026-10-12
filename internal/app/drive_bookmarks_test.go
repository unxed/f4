package app

import (
	"github.com/unxed/f4/internal/panel"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
)

func TestDriveBookmarksRoundTripHasNoTenItemLimit(t *testing.T) {
	bookmarks := make([]panel.DriveBookmark, 12)
	for i := range bookmarks {
		bookmarks[i] = panel.DriveBookmark{
			Name:   "Folder " + string(rune('A'+i)),
			Path:   filepath.Join("$HOME", "folder", string(rune('a'+i))),
			Hotkey: "CtrlF" + string(rune('1'+i)),
		}
	}

	path := filepath.Join(t.TempDir(), "drive-bookmarks.ini")
	if err := panel.SaveDriveBookmarks(path, bookmarks); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := panel.LoadDriveBookmarks(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(bookmarks) {
		t.Fatalf("loaded %d bookmarks, want %d", len(got), len(bookmarks))
	}
	for i := range bookmarks {
		if got[i] != bookmarks[i] {
			t.Errorf("bookmark %d = %#v, want %#v", i, got[i], bookmarks[i])
		}
	}
}

func TestDriveBookmarksLoadSkipsInvalidAndCompactsSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drive-bookmarks.ini")
	content := `[4]
Name=Later
Path=/later
Hotkey=F8

[foreign]
Name=Ignored
Path=/ignored

[9]
Name=First
Path=/first
Hotkey=Ф

[12]
Path=/missing-name
`
	if err := config.WriteUserFileAtomically(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := panel.LoadDriveBookmarks(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []panel.DriveBookmark{{Name: "Later", Path: "/later", Hotkey: "F8"}, {Name: "First", Path: "/first", Hotkey: "Ф"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("loaded bookmarks = %#v, want %#v", got, want)
	}
}

func TestDriveBookmarkMenuTextUsesNameAndKeepsHotkeyLeft(t *testing.T) {
	if got := panel.DriveBookmarkMenuText(panel.DriveBookmark{Name: "Long folder", Path: "/tmp/x", Hotkey: "Q"}); got != "&Q Long folder" {
		t.Fatalf("menu text = %q, want left-side letter shortcut", got)
	}
	if got := panel.DriveBookmarkMenuText(panel.DriveBookmark{Name: "Русская папка", Path: "/tmp/x", Hotkey: "Ф"}); got != "&Ф Русская папка" {
		t.Fatalf("Cyrillic menu text = %q, want left-side letter shortcut", got)
	}
	if got := panel.DriveBookmarkMenuText(panel.DriveBookmark{Name: "Long folder", Path: "/tmp/x", Hotkey: "CtrlF5"}); got != "Ctrl+F5 Long folder" {
		t.Fatalf("chord menu text = %q, want formatted left-side shortcut", got)
	}
	if got := panel.DriveBookmarkMenuText(panel.DriveBookmark{Name: "A & B", Path: "/tmp/x"}); strings.Contains(got, "/tmp/x") || !strings.Contains(got, "A && B") {
		t.Fatalf("menu text = %q, want escaped name without path", got)
	}
}

func TestDriveBookmarkKeyMatchesFarEventSpelling(t *testing.T) {
	if !panel.DriveBookmarkKeyMatches(panel.DriveBookmark{Name: "Русский", Path: "/tmp", Hotkey: "Ф"}, keymap.EventToHotkeyString(keymap.ParseFarKey("Ф"))) {
		t.Fatal("Cyrillic drive bookmark key did not match")
	}
	if !panel.DriveBookmarkKeyMatches(panel.DriveBookmark{Name: "Folder", Path: "/tmp", Hotkey: "CtrlF5"}, keymap.EventToHotkeyString(keymap.ParseFarKey("CtrlF5"))) {
		t.Fatal("chord drive bookmark key did not match")
	}
}

func TestDriveBookmarkEditorCapturesAnyKeyAndCanClearIt(t *testing.T) {
	dialog := panel.NewDriveBookmarkEditDialog(panel.DriveBookmark{}, "/tmp/default", nil)
	dialog.SetFocusedItem(dialog.HotkeyEdit)
	fieldX1, _, fieldX2, _ := dialog.HotkeyEdit.GetPosition()
	if fieldX2-fieldX1+1 != 1 {
		t.Fatalf("hotkey field width = %d, want one character", fieldX2-fieldX1+1)
	}
	labelX1, _, labelX2, _ := dialog.GetChildren()[4].GetPosition()
	if labelX1 >= fieldX1 || labelX2 >= fieldX1 {
		t.Fatalf("hotkey label position = %d..%d, field starts at %d", labelX1, labelX2, fieldX1)
	}

	if !dialog.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'ф'}) {
		t.Fatal("editor did not consume a Cyrillic hotkey")
	}
	if got := dialog.HotkeyEdit.GetText(); got != "Ф" {
		t.Fatalf("captured hotkey = %q, want Ф", got)
	}
	if !dialog.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE}) {
		t.Fatal("editor did not consume Delete while clearing hotkey")
	}
	if got := dialog.HotkeyEdit.GetText(); got != "" {
		t.Fatalf("cleared hotkey = %q, want empty", got)
	}
}
