package app

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtui"
)

// plainMenuText is what a menu item reads as: its text without the hotkey
// marker, which is not the same letter in every language and moves when a menu
// has two items that want the same one (#1258).
func plainMenuText(text string) string {
	plain, _, _ := vtui.ParseAmpersandString(text)
	return plain
}

// requireDistinctHotkeys fails for every item whose hotkey another item of the
// same menu already has (#1258).
func requireDistinctHotkeys(t *testing.T, where string, items []vtui.MenuItem) {
	t.Helper()
	seen := map[rune]string{}
	for _, item := range items {
		if len(item.SubItems) > 0 {
			requireDistinctHotkeys(t, where+" > "+item.Text, item.SubItems)
		}
		if item.Separator {
			continue
		}
		hk := vtui.ExtractHotkey(item.Text)
		if hk == 0 {
			continue
		}
		if other, ok := seen[hk]; ok {
			t.Errorf("%s: %q and %q both answer to %q", where, other, item.Text, string(hk))
		}
		seen[hk] = item.Text
	}
}

// requireHotkeys fails for every item that has letters to carry a hotkey and
// has none. Menus of these sizes always allow one for each.
func requireHotkeys(t *testing.T, where string, items []vtui.MenuItem) {
	t.Helper()
	for _, item := range items {
		if len(item.SubItems) > 0 {
			requireHotkeys(t, where+" > "+item.Text, item.SubItems)
		}
		if item.Separator || vtui.ExtractHotkey(item.Text) != 0 {
			continue
		}
		plain := plainMenuText(item.Text)
		for _, r := range plain {
			if unicode.IsLetter(r) {
				t.Errorf("%s: %q has no hotkey", where, plain)
				break
			}
		}
	}
}

func requireDistinctBarHotkeys(t *testing.T, where string, bar []vtui.MenuBarItem) {
	t.Helper()
	labels := make([]vtui.MenuItem, len(bar))
	for i, entry := range bar {
		labels[i] = vtui.MenuItem{Text: entry.Label}
		requireDistinctHotkeys(t, where+" > "+entry.Label, entry.SubItems)
	}
	requireDistinctHotkeys(t, where+" (menu names)", labels)
}

func TestMenuHotkeysAreDistinctInEveryLanguage(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHotkeys })
	t.Cleanup(func() { i18n.InitLang("en", "", "") })

	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	vtui.FrameManager.Push(pf)

	files, err := filepath.Glob("../i18n/lang/*.lng")
	if err != nil || len(files) < 2 {
		t.Fatalf("language files: %v, %v", files, err)
	}
	for _, file := range files {
		code := strings.TrimSuffix(filepath.Base(file), ".lng")
		t.Run(code, func(t *testing.T) {
			i18n.InitLang(code, "en", "")

			requireDistinctBarHotkeys(t, "panels", pf.GetMenuBar().Items)
			for _, area := range []string{"Shell", "Editor", "Viewer", "Terminal"} {
				requireDistinctBarHotkeys(t, area, BuildMenuBarItems(area))
			}
			// The two languages whose menus the reports were made in must have
			// a hotkey on every item (#1258).
			if code == "en" || code == "ru" {
				for _, entry := range pf.GetMenuBar().Items {
					requireHotkeys(t, "panels > "+entry.Label, entry.SubItems)
				}
				for _, area := range []string{"Shell", "Editor", "Viewer", "Terminal"} {
					for _, entry := range BuildMenuBarItems(area) {
						requireHotkeys(t, area+" > "+entry.Label, entry.SubItems)
					}
				}
			}
		})
	}
}
