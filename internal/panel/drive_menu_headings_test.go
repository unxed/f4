package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// The Tools and Links captions of the drive menu are written into the rules
// that divide the sections, not on rows of their own: the menu is two rows
// shorter and the captions cannot be selected (f4#1148).
func TestDriveMenuCaptionsLiveInTheSeparators(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	defer sysinfo.SnapshotDrives()()
	sysinfo.SetDrives([]sysinfo.DriveEntry{
		{Name: "Alpha", Factory: func() vfs.VFS { return nil }},
		{Name: "Beta", Factory: func() vfs.VFS { return nil }},
	})

	pf.ShowDriveMenu(0)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}

	captions := map[string]int{}
	for i, item := range menu.Items {
		if item.Separator && item.Text != "" {
			captions[item.Text] = i
			if menu.IsSelectable != nil && menu.IsSelectable(i) {
				t.Errorf("the %q rule can be selected", item.Text)
			}
			continue
		}
		if text := strings.ReplaceAll(item.Text, "&", ""); text == i18n.Msg("Drive.Tools") || text == i18n.Msg("Drive.Links") {
			t.Errorf("caption %q still takes a row of its own at %d", text, i)
		}
	}
	tools, ok := captions[i18n.Msg("Drive.Tools")]
	if !ok {
		t.Fatalf("no Tools caption in the rules: %v", captions)
	}
	// The built-in tools come first (Other panel, Temporary panel, and the
	// registry on Windows), then the plugin tools (f4#1148).
	builtin := map[string]bool{
		strings.TrimSpace(strings.ReplaceAll(i18n.Msg("Panel.Other"), "&", "")):     true,
		strings.TrimSpace(strings.ReplaceAll(i18n.Msg("TempPanel.Drive"), "&", "")): true,
		"Windows Registry": true,
	}
	first := tools + 1
	for first < len(menu.Items) && builtin[strings.TrimSpace(strings.ReplaceAll(menu.Items[first].Text, "&", ""))] {
		first++
	}
	if first == tools+1 {
		t.Fatalf("the built-in tools are not first under the Tools rule: %q", menu.Items[tools+1].Text)
	}
	if next := strings.TrimSpace(strings.ReplaceAll(menu.Items[first].Text, "&", "")); next != "Alpha" {
		t.Fatalf("the row after the built-in tools is %q, want the first plugin tool", next)
	}
	for _, item := range menu.Items {
		clean := strings.ReplaceAll(item.Text, "&", "")
		if clean == "Alpha" || clean == "Beta" {
			if strings.Contains(item.Text, "&") {
				t.Errorf("tool row %q unexpectedly has an automatic hotkey: %q", clean, item.Text)
			}
		}
	}
	if _, ok := captions[i18n.Msg("Drive.Links")]; !ok {
		t.Fatalf("no Links caption in the rules: %v", captions)
	}
}

// f4#1148 (mitek1977-coder): the bookmark slots get their caption in the rule
// above them, like Tools and Links; the slot digit is one space from the path,
// as on the Tools and Links rows; and the bottom line lists Ctrl+Up/Down
// rather than the second key for Ins.
func TestDriveMenuBookmarksCaptionAndSpacing(t *testing.T) {
	cfg := t.TempDir()
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}
	place := t.TempDir()
	if err := os.WriteFile(BookmarksFilePath(),
		[]byte("[3]\nPath="+place+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowDriveMenu(0)
	top := vtui.FrameManager.GetTopFrame()
	menu, ok := driveMenuFromFrame(top)
	if !ok {
		t.Fatal("no drive menu on top")
	}

	caption := strings.TrimSpace(i18n.Msg("Bookmarks.Title"))
	row := -1
	for i, item := range menu.Items {
		if strings.HasPrefix(item.Text, "&3") {
			row = i
		}
	}
	if row < 1 {
		t.Fatalf("no row for bookmark slot 3")
	}
	if rule := menu.Items[row-1]; !rule.Separator || rule.Text != caption {
		t.Errorf("the rule above the bookmarks is %+v, want a separator captioned %q", rule, caption)
	}
	if want := "&3 " + dialog.EscapeAmpersand(dialog.TruncPathLeft(place, 64)); menu.Items[row].Text != want {
		t.Errorf("bookmark row %q, want %q", menu.Items[row].Text, want)
	}

	frame, ok := top.(*DriveMenuFrame)
	if !ok {
		t.Fatalf("top frame is %T", top)
	}
	if strings.Contains(frame.bottomHint, "Ctrl+N") || !strings.HasSuffix(strings.TrimSpace(frame.bottomHint), "Ctrl+Up/Down") {
		t.Errorf("bottom line %q: want no Ctrl+N and Ctrl+Up/Down at the end", frame.bottomHint)
	}
}
