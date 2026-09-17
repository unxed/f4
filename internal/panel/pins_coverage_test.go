package panel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/history"
)

func TestFolderPinsSlotHelpersAndSave(t *testing.T) {
	root := t.TempDir()
	t.Setenv("F4_PINS_ROOT", root)
	p := &FolderPins{File: filepath.Join(root, "settings", "bookmarks.ini")}
	p.Set[0].Path = "%F4_PINS_ROOT%/one"
	p.Set[2].Plugin = "far://plugin"

	if got := p.SlotAt(-1); got != "" {
		t.Fatalf("negative slot = %q", got)
	}
	if got := p.SlotAt(0); got != filepath.Join(root, "one") {
		t.Fatalf("expanded slot = %q", got)
	}
	if p.SlotOf(filepath.Join(root, "one")) != 0 || p.SlotOf("") != -1 || p.SlotOf(filepath.Join(root, "missing")) != -1 {
		t.Fatal("SlotOf result is incorrect")
	}
	if p.freeSlot() != 1 {
		t.Fatalf("freeSlot = %d, want 1", p.freeSlot())
	}

	p.Save()
	if _, err := os.Stat(p.File); err != nil {
		t.Fatalf("saved bookmarks: %v", err)
	}
	loaded, err := LoadBookmarks(p.File)
	if err != nil || loaded[0].Path != p.Set[0].Path || loaded[2].Plugin != p.Set[2].Plugin {
		t.Fatalf("loaded bookmarks = %#v, %v", loaded, err)
	}
}

func TestFolderPinsReconcileReleasesAndClaimsSlots(t *testing.T) {
	p := &FolderPins{}
	p.Set[0].Path = "/old"
	p.Set[2].Plugin = "preserved"
	records := []history.HistoryRecord{{Name: "/new", Lock: true}}
	if !p.Reconcile(records) {
		t.Fatal("Reconcile reported no change")
	}
	if p.Set[0].Path != "/new" || p.Set[2].Plugin != "preserved" {
		t.Fatalf("reconciled slots = %#v", p.Set)
	}
	if p.Reconcile(records) {
		t.Fatal("second Reconcile changed an already reconciled set")
	}
	if (*FolderPins)(nil).Reconcile(records) {
		t.Fatal("nil Reconcile reported a change")
	}
}

func TestMergeFolderPinsMarksExistingAndAppendsUnvisited(t *testing.T) {
	p := &FolderPins{}
	p.Set[0].Path = "/visited"
	p.Set[1].Path = "/unvisited"
	records := []history.HistoryRecord{{Name: "/visited"}, {Name: "/plain"}}
	merged := MergeFolderPins(records, p)
	if len(merged) != 3 || !merged[0].Lock || merged[1].Lock || merged[2].Name != "/unvisited" || !merged[2].Lock {
		t.Fatalf("merged records = %#v", merged)
	}
	if got := MergeFolderPins(records, nil); len(got) != len(records) {
		t.Fatalf("nil merge changed record count: %#v", got)
	}
}
