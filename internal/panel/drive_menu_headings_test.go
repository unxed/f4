package panel

import (
	"strings"
	"testing"

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
	if next := strings.ReplaceAll(menu.Items[tools+1].Text, "&", ""); next != "Alpha" {
		t.Fatalf("the row after the Tools rule is %q, want the first tool", next)
	}
	if _, ok := captions[i18n.Msg("Drive.Links")]; !ok {
		t.Fatalf("no Links caption in the rules: %v", captions)
	}
}
