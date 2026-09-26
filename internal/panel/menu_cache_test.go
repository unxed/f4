package panel

import (
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

// TestGetMenuBarCachesUnchangedState is part of the fix for #884: vtui's
// render loop asks the top frame for its menu bar on every single frame
// (stepWithSize -> GetActiveMenuBar -> GetMenuBar), and before this fix that
// meant PanelsFrame rebuilt the whole menu bar from scratch every frame —
// walking the action table and reassigning every menu's underlined hotkey
// letter (menuhotkeys.assign/candidates) — whether or not anything the menu
// depends on had actually changed. Under a key held down over a slow
// connection, frames arrive faster than that rebuild completes and the input
// backlog grows without bound.
//
// This test stands the expensive rebuild in for a counter via the
// BuildMenuBarItems seam (the same one menubar_dropdown_test.go and
// host_coverage_test.go already substitute), which is exactly the function
// the reporter's CPU profile named as the hot path into menuhotkeys.
func TestGetMenuBarCachesUnchangedState(t *testing.T) {
	oldItems := BuildMenuBarItems
	calls := 0
	BuildMenuBarItems = func(area string) []vtui.MenuBarItem {
		calls++
		if area != "Shell" {
			return nil
		}
		return []vtui.MenuBarItem{
			{Label: "&Files", SubItems: []vtui.MenuItem{{Text: "&Open"}, {Text: "&Save"}}},
			{Label: "&Commands", SubItems: []vtui.MenuItem{{Text: "&Run"}}},
			{Label: "&Options", SubItems: []vtui.MenuItem{{Text: "&Setup"}}},
		}
	}
	t.Cleanup(func() { BuildMenuBarItems = oldItems })

	pf := &PanelsFrame{
		ActiveIdx:  0,
		ShowPanels: true,
		Panels:     [2]Panel{&FileSystemPanel{}, &FileSystemPanel{}},
		MenuBar:    vtui.NewMenuBar(nil),
	}

	if pf.GetMenuBar() == nil {
		t.Fatal("GetMenuBar returned nil")
	}
	if calls != 1 {
		t.Fatalf("first GetMenuBar call ran BuildMenuBarItems %d times, want 1", calls)
	}

	// Nothing that the menu depends on changed: a second (and third) call
	// must reuse the cached items rather than rebuilding.
	if pf.GetMenuBar() == nil {
		t.Fatal("GetMenuBar returned nil")
	}
	if pf.GetMenuBar() == nil {
		t.Fatal("GetMenuBar returned nil")
	}
	if calls != 1 {
		t.Fatalf("GetMenuBar with nothing changed ran BuildMenuBarItems %d times total, want still 1 (cache hit)", calls)
	}

	// The narrowed hotkey re-assignment GetMenuBar still does on every call
	// (for the two side menus UpdateMenuCheckmarks touches) must not leave a
	// bare autoMarker in any label: that marker only exists between
	// menuhotkeys.Auto and menuhotkeys.assign/Unique, and every path here
	// runs both.
	for _, item := range pf.MenuBar.Items {
		if strings.Contains(item.Label, "") {
			t.Errorf("menu %q label still carries the raw hotkey marker: %q", item.Label, item.Label)
		}
		for _, sub := range item.SubItems {
			if strings.Contains(sub.Text, "") {
				t.Errorf("menu %q item %q still carries the raw hotkey marker", item.Label, sub.Text)
			}
		}
	}

	// Switching the active panel is exactly the kind of state change the
	// cache must not paper over: a cache that never invalidates would be a
	// worse bug than the one being fixed (#884's own text says so).
	pf.ActiveIdx = 1
	if pf.GetMenuBar() == nil {
		t.Fatal("GetMenuBar returned nil")
	}
	if calls != 2 {
		t.Fatalf("GetMenuBar after switching the active panel ran BuildMenuBarItems %d times total, want 2 (cache miss)", calls)
	}

	// And once more with the (now different) state unchanged: back to a hit.
	if pf.GetMenuBar() == nil {
		t.Fatal("GetMenuBar returned nil")
	}
	if calls != 2 {
		t.Fatalf("GetMenuBar with the new state unchanged ran BuildMenuBarItems %d times total, want still 2 (cache hit)", calls)
	}
}

// TestBuildMenuItemsCacheInvalidatesOnVfsChange covers the other half of the
// same risk: toggling to the AI panel or otherwise swapping a side's VFS
// mutates the existing *FileSystemPanel in place (SwitchToVFS), it does not
// replace pf.Panels[i] with a new pointer, so a cache keyed on panel identity
// alone would miss it. The cache instead keys on the VFS's own dynamic type.
func TestBuildMenuItemsCacheInvalidatesOnVfsChange(t *testing.T) {
	oldItems := BuildMenuBarItems
	calls := 0
	BuildMenuBarItems = func(area string) []vtui.MenuBarItem {
		calls++
		return nil
	}
	t.Cleanup(func() { BuildMenuBarItems = oldItems })

	left := &FileSystemPanel{}
	pf := &PanelsFrame{
		ActiveIdx:  0,
		ShowPanels: true,
		Panels:     [2]Panel{left, &FileSystemPanel{}},
	}

	_ = pf.BuildMenuItems()
	if calls != 1 {
		t.Fatalf("first BuildMenuItems call ran BuildMenuBarItems %d times, want 1", calls)
	}
	_ = pf.BuildMenuItems()
	if calls != 1 {
		t.Fatalf("BuildMenuItems with nothing changed ran BuildMenuBarItems %d times total, want still 1", calls)
	}

	// Same *FileSystemPanel pointer, different VFS: IsAIPanel and the other
	// Visible checks that key off the VFS's type would now answer
	// differently, so the cache must not reuse the old build.
	left.Vfs = &mockTitleVFS{title: "ai"}
	_ = pf.BuildMenuItems()
	if calls != 2 {
		t.Fatalf("BuildMenuItems after swapping the left panel's VFS ran BuildMenuBarItems %d times total, want 2", calls)
	}
}
