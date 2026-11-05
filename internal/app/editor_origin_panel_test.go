package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The editor and viewer are screens of their own: panels can be switched
// with Tab while they stay open in the background. Closing one then shows
// the panel that is active by then (far2l, Far Manager), unless
// EditorReturnsToOriginPanel asks for the panel it was opened from (f4#2247).
func TestEditorViewerCloseReturnsToOriginPanel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restore bool
		want    int
	}{
		{"default keeps the last active panel", false, 1},
		{"option returns to the origin panel", true, 0},
	} {
		for _, kind := range []string{"editor", "viewer"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				oldCfg := config.App
				defer func() { config.App = oldCfg }()
				config.App.EditorReturnsToOriginPanel = tc.restore

				vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
				testutil.DrainPendingTasks()

				dir := t.TempDir()
				path := filepath.Join(dir, "a.txt")
				if err := os.WriteFile(path, []byte("first line\nsecond line\n"), 0600); err != nil {
					t.Fatal(err)
				}
				filesystem := vfs.NewOSVFS(dir)
				pf := panel.NewPanelsFrame()
				pf.Panels[0] = panel.NewFileSystemPanel(0, 0, 40, 20, filesystem)
				pf.Panels[1] = panel.NewFileSystemPanel(40, 0, 40, 20, filesystem.Clone())
				pf.ShowPanels, pf.ShowLeftPanel, pf.ShowRightPanel = true, true, true
				pf.ActiveIdx = 0
				pf.ResizeConsole(120, 60)
				vtui.FrameManager.Push(pf)

				var closeFrame func()
				if kind == "editor" {
					f, err := filesystem.Open(context.Background(), path)
					if err != nil {
						t.Fatal(err)
					}
					showEditor(pf, filesystem, path, f)
					ev, _ := findOpenedEditor(filesystem, path)
					if ev == nil {
						t.Fatal("editor was not opened")
					}
					ev.CancelIndexing()
					ev.WaitForIndexing()
					closeFrame = ev.Close
				} else {
					vv, err := viewer.NewViewerView(context.Background(), filesystem, path)
					if err != nil {
						t.Fatal(err)
					}
					showViewer(pf, vv, path)
					closeFrame = vv.Close
				}

				// Back on the panels, Tab makes the other one active.
				pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
				if pf.ActiveIdx != 1 {
					t.Fatalf("Tab left panel %d active, want 1", pf.ActiveIdx)
				}

				closeFrame()
				if pf.ActiveIdx != tc.want {
					t.Fatalf("active panel after closing the %s = %d, want %d", kind, pf.ActiveIdx, tc.want)
				}
			})
		}
	}
}
