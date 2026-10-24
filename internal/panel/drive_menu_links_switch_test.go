package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// "Show bookmarks" hides the bookmark slots only; the Links stay (f4#1148,
// list of 16.10, point 4: the switch used to hide both).
func TestDriveMenuShowBookmarksOffKeepsTheLinks(t *testing.T) {
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
	if err := SaveDriveBookmarks(DriveBookmarksFilePath(), []DriveBookmark{{Name: "Projects", Path: place}}); err != nil {
		t.Fatal(err)
	}
	oldOptions := config.App.DriveMenuOptions
	t.Cleanup(func() { config.App.DriveMenuOptions = oldOptions })
	config.App.DriveMenuOptions = oldOptions &^ config.DriveMenuShowBookmarks

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowDriveMenu(0)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top")
	}
	defer menu.Close()

	bookmarksCaption := strings.TrimSpace(i18n.Msg("Bookmarks.Title"))
	links, link := false, false
	for _, item := range menu.Items {
		switch {
		case item.Separator && item.Text == bookmarksCaption:
			t.Error("the Bookmarks section is shown with Show bookmarks off")
		case item.Separator && item.Text == i18n.Msg("Drive.Links"):
			links = true
		case strings.HasPrefix(item.Text, "&3"):
			t.Errorf("bookmark slot 3 is shown with Show bookmarks off: %q", item.Text)
		case strings.Contains(item.Text, "Projects"):
			link = true
		}
	}
	if !links || !link {
		t.Fatalf("Show bookmarks off hid the Links too (caption %v, link %v)", links, link)
	}

	menu.Close()
	config.App.DriveMenuOptions |= config.DriveMenuHideLinks
	pf.ShowDriveMenu(0)
	menu, ok = driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("no drive menu on top after hiding Links")
	}
	defer menu.Close()
	for _, item := range menu.Items {
		if item.Separator && item.Text == i18n.Msg("Drive.Links") {
			t.Error("the Links section is shown with Show links off")
		}
		if strings.Contains(item.Text, "Projects") {
			t.Errorf("named link is shown with Show links off: %q", item.Text)
		}
	}
}
