package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/keymap"
)

func TestHotkeyRemovalPreservesDefaultsAndConcurrentChanges(t *testing.T) {
	for _, mode := range []string{"default", "custom", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			previous := keymap.GlobalHotkeysMgr
			hm := keymap.NewHotkeyManager(filepath.Join(t.TempDir(), "keys.ini"))
			hm.Defaults = map[string]map[string]string{"Shell": {"F8": "File.Delete"}}
			hm.Bindings = map[string]map[string]string{}
			if mode == "custom" {
				hm.Defaults = map[string]map[string]string{}
				hm.Bind("Shell", "F8", "File.Delete")
			}
			keymap.GlobalHotkeysMgr = hm
			t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
			d, err := (hotkeySettingsProvider{}).Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			rows := d.Records["bindings"]
			found := false
			for i, r := range rows {
				if r.Values["binding.Key"] == "F8" {
					d.Records["bindings"] = append(rows[:i], rows[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				t.Fatal("missing baseline binding")
			}
			if mode == "concurrent" {
				hm.Bind("Shell", "F8", "File.Copy")
			}
			result := d.Commit(context.Background())
			if mode == "concurrent" {
				if len(result.Errors) == 0 || !d.Dirty("bindings") || hm.Bindings["Shell"]["F8"] != "File.Copy" {
					t.Fatal("concurrent binding was overwritten or draft lost")
				}
				return
			}
			if len(result.Errors) > 0 {
				t.Fatal(result.Errors)
			}
			if mode == "default" {
				if hm.Bindings["Shell"]["F8"] != "None" {
					t.Fatal("removed default was restored")
				}
				next, err := (hotkeySettingsProvider{}).Begin(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer next.Close()
				for _, r := range next.Records["bindings"] {
					if r.Values["binding.Key"] == "F8" && r.Values["binding.Action"] == "None" {
						return
					}
				}
				t.Fatal("disabled default missing from next editing session")
			} else if _, ok := hm.Bindings["Shell"]["F8"]; ok {
				t.Fatal("removed custom binding remains")
			}
		})
	}
}
