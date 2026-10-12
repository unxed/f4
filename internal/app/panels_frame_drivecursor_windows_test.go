//go:build windows

package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// TestPanelsFrame_DriveMenu_CursorOnCurrentDrive verifies that opening the
// drive menu (Alt+F1/F2) for a panel sitting on a real filesystem drive lands
// the cursor on that drive.
func TestPanelsFrame_DriveMenu_CursorOnCurrentDrive(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Put the left panel on the current working directory's drive.
	cur, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(cur)
	if vol == "" {
		t.Skip("current directory has no drive letter")
	}
	left := pf.Panels[0].(*panel.FileSystemPanel)
	left.Vfs = vfs.NewOSVFS(cur)
	if err := left.Vfs.SetPath(cur); err != nil {
		t.Fatal(err)
	}

	// Open Alt+F1 (left panel drive menu).
	pf.ShowDriveMenu(0)

	top := vtui.FrameManager.GetTopFrame()
	menu, ok := paneltest.DriveMenuFromFrame(top)
	if !ok {
		t.Fatal("Drive menu not opened")
	}

	row := menu.Items[menu.SelectPos].Text
	if !strings.Contains(strings.ToUpper(row), strings.ToUpper(vol)) {
		t.Errorf("cursor on row %q, expected it to match drive %s", row, vol)
	}
}
