package panel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestUserMenuSaveKeepsThePreviousFile(t *testing.T) {
	for _, mode := range []MenuMode{MenuModeLocal, MenuModeMain} {
		path := filepath.Join(t.TempDir(), "FarMenu.ini")
		first := []UserMenuItem{{HotKey: "g", Label: "Group", Submenu: []UserMenuItem{{HotKey: "1", Label: "One", Commands: []string{"echo 1"}}}}}
		if err := SaveRootForMode(mode, path, first); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path + menuBackupSuffix); !os.IsNotExist(err) {
			t.Fatalf("mode %v: a new menu file got a backup (%v)", mode, err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := SaveRootForMode(mode, path, []UserMenuItem{{HotKey: "g", Label: "Group", Submenu: []UserMenuItem{}}}); err != nil {
			t.Fatal(err)
		}
		kept, err := os.ReadFile(path + menuBackupSuffix)
		if err != nil {
			t.Fatalf("mode %v: no backup of the replaced menu: %v", mode, err)
		}
		if string(kept) != string(before) {
			t.Fatalf("mode %v: backup %q, want the replaced file %q", mode, kept, before)
		}
	}
}

func TestUserMenuSaveThroughVFSKeepsThePreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "FarMenu.ini")
	if err := os.WriteFile(path, []byte("old menu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.NewOSVFS(dir)
	if err := SaveRootForModeVFS(context.Background(), fs, MenuModeLocal, path, []UserMenuItem{{HotKey: "a", Label: "A", Commands: []string{"ls"}}}); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(path + menuBackupSuffix)
	if err != nil || string(kept) != "old menu\n" {
		t.Fatalf("backup %q (%v), want the replaced file", kept, err)
	}
}
