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
	"github.com/unxed/vtui"
)

// screenIndexHolding is the index of the workspace that has frame, -1 if none.
func screenIndexHolding(frame vtui.Frame) int {
	for i, s := range vtui.FrameManager.Screens {
		for _, f := range s.Frames {
			if f == frame {
				return i
			}
		}
	}
	return -1
}

// The editor and viewer are workspaces of their own. The user may leave one
// for another workspace (Ctrl+Tab) and come back to it; closing it then goes
// back to the workspace that was in use before it, which is not the one it was
// opened from. EditorReturnsToOriginPanel asks for the latter (f4#2247).
func TestEditorViewerCloseReturnsToOriginWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name       string
		restore    bool
		wantOrigin bool
	}{
		{"default leaves the workspace to the frame manager", false, false},
		{"option returns to the origin workspace", true, true},
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
				origin := activeAppScreen()

				var frame vtui.Frame
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
					frame, closeFrame = ev, ev.Close
				} else {
					vv, err := viewer.NewViewerView(context.Background(), filesystem, path)
					if err != nil {
						t.Fatal(err)
					}
					showViewer(pf, vv, path)
					frame, closeFrame = vv, vv.Close
				}

				editorScreen := activeAppScreen()
				if editorScreen == nil || editorScreen == origin {
					t.Fatal("the frame did not get a workspace of its own")
				}

				// Visit another workspace and come back to the frame.
				other := panel.NewPanelsFrame()
				vtui.FrameManager.AddScreenBackground(other)
				otherIdx := screenIndexHolding(other)
				if otherIdx < 0 {
					t.Fatal("the other workspace was not added")
				}
				vtui.FrameManager.SwitchScreen(otherIdx)
				vtui.FrameManager.SwitchScreen(screenIndexHolding(frame))
				if activeAppScreen() != editorScreen {
					t.Fatal("did not come back to the frame's workspace")
				}

				closeFrame()
				testutil.DrainUITasks()
				if got := activeAppScreen() == origin; got != tc.wantOrigin {
					t.Fatalf("origin workspace active after closing the %s = %v, want %v", kind, got, tc.wantOrigin)
				}
			})
		}
	}
}
