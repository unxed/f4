package panel

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// TestPanelsFrame_F9OpensMenuBelowItsBar covers issues #1129, #1144 and #1149.
// The menu bar used to be parked two rows above the screen while inactive, and
// vtui anchors a dropdown to the bar's row at the moment ActivateSubMenu runs:
// F9 therefore opened the menu above the visible area, with its first entries
// hidden behind the workspace tabs, and in terminal mode the bar itself was
// never painted at all. F9 itself now only raises the bar, so the dropdown is
// opened here the way a user opens it.
func TestPanelsFrame_F9OpensMenuBelowItsBar(t *testing.T) {
	cases := []struct {
		name       string
		showPanels bool
		workspaces int
	}{
		{"panels, one workspace", true, 1},
		{"panels, two workspaces", true, 2},
		{"terminal, one workspace", false, 1},
		{"terminal, two workspaces", false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(swapFrameManager(t))
			vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
			theme.SetDefaultF4Palette()
			oldAlways := config.App.AlwaysShowMenuBar
			config.App.AlwaysShowMenuBar = false
			t.Cleanup(func() { config.App.AlwaysShowMenuBar = oldAlways })
			oldItems := BuildMenuBarItems
			BuildMenuBarItems = func(area string) []vtui.MenuBarItem {
				if area != "Shell" {
					return nil
				}
				return []vtui.MenuBarItem{{
					Label:    "&Files",
					SubItems: []vtui.MenuItem{{Text: "&View"}, {Text: "&Edit"}},
				}}
			}
			t.Cleanup(func() { BuildMenuBarItems = oldItems })

			pf := NewPanelsFrame()
			defer pf.Close()
			pf.ShowPanels = tc.showPanels
			pf.ResizeConsole(80, 25)
			vtui.FrameManager.Push(pf)
			if tc.workspaces > 1 {
				vtui.FrameManager.AddScreenBackground(vtui.NewDesktop())
			}
			wantInset := tc.workspaces - 1
			if inset := vtui.FrameManager.WorkspaceTopInset(); inset != wantInset {
				t.Fatalf("workspace top inset = %d, want %d", inset, wantInset)
			}
			// An ordinary idle render precedes the keypress in the real event
			// loop and is what used to park the bar off-screen.
			pf.Show(vtui.FrameManager.Screen())

			pf.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true,
				VirtualKeyCode: vtinput.VK_F9,
			})
			if !pf.MenuBar.Active {
				t.Fatal("F9 did not activate the menu bar")
			}
			// F9 raises the bar alone, as far2l's ShellOptions(0) does
			// (issue #1144).
			if top := vtui.FrameManager.GetTopFrame(); top != nil && top.GetType() == vtui.TypeMenu {
				t.Fatalf("F9 opened a dropdown (%T); it should raise the bar only", top)
			}

			pf.Show(vtui.FrameManager.Screen())
			assertMenuBarPainted(t, pf, wantInset, "menu bar raised by F9")

			// Down is the first of the paths that open a dropdown, and the
			// geometry #1129 and #1149 were about is anchored to the bar's
			// row at that moment.
			pf.MenuBar.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true,
				VirtualKeyCode: vtinput.VK_DOWN,
			})
			dropdown := vtui.FrameManager.GetTopFrame()
			if dropdown == nil || dropdown.GetType() != vtui.TypeMenu {
				t.Fatalf("Down left %T on top, want an open vtui menu", dropdown)
			}
			if _, y1, _, _ := dropdown.GetPosition(); y1 != wantInset+1 {
				t.Errorf("dropdown top row = %d, want %d (immediately below the menu bar)", y1, wantInset+1)
			}

			pf.Show(vtui.FrameManager.Screen())
			assertMenuBarPainted(t, pf, wantInset, "menu bar while its dropdown is open")
		})
	}
}

// TestPanelsFrame_F9WithoutMenuItemsDoesNotPanic guards the F9 handler against
// an empty menu: vtui's MenuBar.ActivateSubMenu indexes Items unconditionally.
// A terminal workspace builds its bar entirely from the action table, so an
// empty table leaves it with no items at all, unlike the panels bar which
// always carries its two fixed side menus.
func TestPanelsFrame_F9WithoutMenuItemsDoesNotPanic(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	oldItems := BuildMenuBarItems
	BuildMenuBarItems = func(string) []vtui.MenuBarItem { return nil }
	t.Cleanup(func() { BuildMenuBarItems = oldItems })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShowPanels = false
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	pf.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_F9,
	})
	if pf.MenuBar.Active {
		t.Error("F9 activated a menu bar that has no items")
	}
}
