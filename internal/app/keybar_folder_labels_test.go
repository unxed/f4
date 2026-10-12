package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/vfs"
)

// F3 on a folder sizes it and F4 on one opens its attributes, so the key bar
// says Size and Attr there instead of View and Edit (f4#1794, f4#1795).
func TestKeyBarNamesWhatF3AndF4DoOnAFolder(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"file.txt"})
	fsp := pf.GetActivePanel()
	fsp.Entries = append(fsp.Entries, &panel.FileEntry{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}})
	fsp.Refresh()

	cases := []struct {
		name           string
		wantF3, wantF4 string
	}{
		// F3 sizes the folder the panel shows from "..", F4 has nothing there
		// (f4#1795).
		{"..", i18n.Msg("KeyBar.F3Size"), ""},
		{"file.txt", "", ""},
		{"sub", i18n.Msg("KeyBar.F3Size"), i18n.Msg("KeyBar.F4Attr")},
	}
	for _, tc := range cases {
		idx := -1
		for i, e := range fsp.Entries {
			if e.Name == tc.name {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("no %q in the panel", tc.name)
		}
		fsp.SetCursorIndex(idx)
		if got := viewKeyBarLabel(); got != tc.wantF3 {
			t.Errorf("F3 label on %q = %q, want %q", tc.name, got, tc.wantF3)
		}
		if got := editKeyBarLabel(); got != tc.wantF4 {
			t.Errorf("F4 label on %q = %q, want %q", tc.name, got, tc.wantF4)
		}
	}
}

// F3 on a folder ends with a line saying what was counted, so the number that
// appears in the size column is not the only sign anything happened (f4#1795).
func TestDirSizeResultNamesTheFolderItsSizeAndCounts(t *testing.T) {
	got := dirSizeResult("photos", vfs.OpStats{Bytes: 3 * 1024 * 1024, Files: 12, Dirs: 2})
	for _, want := range []string{"photos", "3.0 MB", "12", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("dirSizeResult = %q, lacks %q", got, want)
		}
	}
}

// The F6 slot of the key bar carries the short caption of the slot, not the
// long name the menu gives the action ("Rename or move"), which the slot cut off
// (f4#891).
func TestKeyBarF6UsesTheShortCaption(t *testing.T) {
	act, ok := action.Lookup("File.Move")
	if !ok {
		t.Fatal("File.Move is not registered")
	}
	if act.KeyBarLabel == nil {
		t.Fatal("File.Move has no key-bar caption of its own")
	}
	if got, want := act.KeyBarLabel(), i18n.Msg("KeyBar.F6"); got != want || got == "" {
		t.Fatalf("F6 caption = %q, want %q", got, want)
	}
	if got := act.KeyBarLabel(); len([]rune(got)) > 8 {
		t.Fatalf("F6 caption %q is longer than a key-bar slot", got)
	}
}
