package settings

import (
	"os"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/sdk/f4settings"
)

// settingsRecordStoreByID finds the core record store for a collection ID,
// exactly like coreRecordSettingsProvider.Catalog does when it walks
// p.stores. It fails the test rather than the caller having to nil-check.
func settingsRecordStoreByID(t *testing.T, p coreRecordSettingsProvider, id string) *settingsRecordStore {
	t.Helper()
	for i := range p.stores {
		if p.stores[i].collection.ID == id {
			return &p.stores[i]
		}
	}
	t.Fatalf("no record store with id %q", id)
	return nil
}

// settingsFileRevision hashes an existing file and reports "missing" for one
// that does not exist. A directory is neither: os.ReadFile refuses it with
// an error that is not os.ErrNotExist, and that generic failure has to reach
// the caller instead of being folded into "missing" (#1148-style silent data
// loss would otherwise look like a fresh profile).
func TestSettingsFileRevisionReadError(t *testing.T) {
	dir := t.TempDir()
	if rev, err := settingsFileRevision(dir); err == nil {
		t.Fatalf("expected an error reading a directory as a file, got revision %q", rev)
	}
}

// The associations store's load/save closures translate between the flat
// f4settings.Record shape and panel.FileAssoc's fixed six-slot arrays. This
// exercises both directions through the real store the provider builds,
// including every AssocKeyName slot.
func TestSettingsCoreRecordProviderAssociationsRoundTrip(t *testing.T) {
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "associations")
	t.Cleanup(func() { _ = os.Remove(store.path) })

	empty, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("fresh profile already has %d associations", len(empty))
	}

	values := map[string]string{"assoc.Mask": "*.go", "assoc.Description": "Go files"}
	for _, name := range panel.AssocKeyName {
		values["assoc."+name] = "cmd-" + name
		values["assoc."+name+"Enabled"] = "true"
	}
	if err := store.save([]f4settings.Record{{ID: "association:0", Values: values}}); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d associations, want 1", len(loaded))
	}
	row := loaded[0]
	if row.Values["assoc.Mask"] != "*.go" || row.Values["assoc.Description"] != "Go files" {
		t.Fatalf("mask/description lost: %#v", row.Values)
	}
	for _, name := range panel.AssocKeyName {
		if row.Values["assoc."+name] != "cmd-"+name {
			t.Fatalf("command for slot %s lost: %#v", name, row.Values)
		}
		if row.Values["assoc."+name+"Enabled"] != "true" {
			t.Fatalf("enabled flag for slot %s lost: %#v", name, row.Values)
		}
	}
}

// The bookmarks store enforces exactly ten slots on save (it backs a fixed
// BookmarkSet array) and otherwise round-trips each slot's path.
func TestSettingsCoreRecordProviderBookmarksRoundTrip(t *testing.T) {
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "bookmarks")
	t.Cleanup(func() { _ = os.Remove(store.path) })

	if err := store.save([]f4settings.Record{{ID: "bookmark:0", Values: map[string]string{"bookmark.Path": "/tmp/only-one"}}}); err == nil {
		t.Fatal("expected save to reject a set that is not exactly ten slots")
	}

	var rows []f4settings.Record
	for i := 0; i < 10; i++ {
		rows = append(rows, f4settings.Record{ID: "bookmark:seed", Values: map[string]string{
			"bookmark.Path": "/tmp/slot" + string(rune('0'+i)),
		}})
	}
	if err := store.save(rows); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 10 {
		t.Fatalf("loaded %d bookmark slots, want 10", len(loaded))
	}
	for i, row := range loaded {
		want := "/tmp/slot" + string(rune('0'+i))
		if row.Values["bookmark.Path"] != want {
			t.Fatalf("slot %d path = %q, want %q", i, row.Values["bookmark.Path"], want)
		}
	}
}

// Drive links go through settingsValidateShortcut before saving, and their
// load/save closures translate to/from panel.DriveBookmark.
func TestSettingsCoreRecordProviderDriveLinksValidateAndRoundTrip(t *testing.T) {
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "drive-links")
	t.Cleanup(func() { _ = os.Remove(store.path) })

	bad := []f4settings.Record{{ID: "drive-link:0", Values: map[string]string{"link.Name": "Bad", "link.Path": "/tmp", "link.Hotkey": "anything you like"}}}
	if err := store.validate(bad); err == nil {
		t.Fatal("expected validate to reject a shortcut that is not a single key")
	}

	rows := []f4settings.Record{{ID: "drive-link:0", Values: map[string]string{"link.Name": "Home", "link.Path": "/home/user", "link.Hotkey": "Q"}}}
	if err := store.validate(rows); err != nil {
		t.Fatal(err)
	}
	if err := store.save(rows); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d drive links, want 1", len(loaded))
	}
	row := loaded[0]
	if row.Values["link.Name"] != "Home" || row.Values["link.Path"] != "/home/user" || row.Values["link.Hotkey"] != "Q" {
		t.Fatalf("drive link round trip mismatch: %#v", row.Values)
	}
}

func TestSettingsCoreRecordProviderDriveToolVisibilityRoundTrip(t *testing.T) {
	restore := sysinfo.SnapshotDrives()
	t.Cleanup(restore)
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "AI"}, {Name: "Network"}})
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "drive-tools")
	t.Cleanup(func() { _ = os.Remove(store.path) })

	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Values["tool.Enabled"] != "true" || rows[1].Values["tool.Enabled"] != "true" {
		t.Fatalf("fresh tool visibility = %#v, want all enabled", rows)
	}
	rows[1].Values["tool.Enabled"] = "false"
	if err := store.save(rows); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Values["tool.Enabled"] != "true" || loaded[1].Values["tool.Enabled"] != "false" {
		t.Fatalf("saved tool visibility = %#v", loaded)
	}
}

// The main user menu store's save rejects rows settingsMenuTree cannot
// resolve (a parent that does not exist) without touching disk, and a
// successful save/load round trip preserves submenu nesting, hotkeys and
// commands through the real far2l-compatible INI on disk.
func TestSettingsCoreRecordProviderUserMenuRoundTrip(t *testing.T) {
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "usermenu.main")
	t.Cleanup(func() { _ = os.Remove(store.path) })

	prefix := "menu.main."
	orphan := []f4settings.Record{{ID: "orphan", Values: map[string]string{prefix + "Label": "Orphan", prefix + "Parent": "missing"}}}
	if err := store.save(orphan); err == nil {
		t.Fatal("expected save to reject a row whose parent does not exist")
	}

	rows := []f4settings.Record{
		{ID: "root", Values: map[string]string{prefix + "Label": "Tools", prefix + "HotKey": "T", prefix + "Submenu": "true", prefix + "Parent": ""}},
		{ID: "child", Values: map[string]string{prefix + "Label": "Run", prefix + "HotKey": "R", prefix + "Submenu": "false", prefix + "Parent": "root", prefix + "Commands": "echo hi"}},
	}
	if err := store.validate(rows); err != nil {
		t.Fatal(err)
	}
	if err := store.save(rows); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d menu rows, want 2: %#v", len(loaded), loaded)
	}
	var rootRow, childRow f4settings.Record
	for _, r := range loaded {
		switch r.Values[prefix+"Label"] {
		case "Tools":
			rootRow = r
		case "Run":
			childRow = r
		}
	}
	if rootRow.Values[prefix+"Submenu"] != "true" {
		t.Fatalf("submenu flag lost on reload: %#v", rootRow.Values)
	}
	if childRow.Values[prefix+"Parent"] != rootRow.ID {
		t.Fatalf("child parent id = %q, want %q", childRow.Values[prefix+"Parent"], rootRow.ID)
	}
	if childRow.Values[prefix+"Commands"] != "echo hi" {
		t.Fatalf("commands lost on reload: %#v", childRow.Values)
	}
}

// settingsMenuTree's parent-chain check must reject a parent id that is not
// in the row set at all, one that exists but is not itself a submenu, and a
// cycle formed entirely of declared submenus.
func TestSettingsMenuTreeRejectsInvalidParents(t *testing.T) {
	prefix := "menu.local."

	missing := []f4settings.Record{{ID: "a", Values: map[string]string{prefix + "Label": "Leaf", prefix + "Parent": "nowhere"}}}
	if _, err := settingsMenuTree(missing, prefix); err == nil {
		t.Fatal("expected error for a parent id that does not exist")
	}

	notSubmenu := []f4settings.Record{
		{ID: "a", Values: map[string]string{prefix + "Label": "A", prefix + "Submenu": "false", prefix + "Parent": ""}},
		{ID: "b", Values: map[string]string{prefix + "Label": "B", prefix + "Submenu": "false", prefix + "Parent": "a"}},
	}
	if _, err := settingsMenuTree(notSubmenu, prefix); err == nil {
		t.Fatal("expected error when the declared parent is not a submenu")
	}

	cycle := []f4settings.Record{
		{ID: "a", Values: map[string]string{prefix + "Label": "A", prefix + "Submenu": "true", prefix + "Parent": "b"}},
		{ID: "b", Values: map[string]string{prefix + "Label": "B", prefix + "Submenu": "true", prefix + "Parent": "a"}},
	}
	if _, err := settingsMenuTree(cycle, prefix); err == nil {
		t.Fatal("expected error for a submenu cycle")
	}
}

// A well-formed row set builds a tree where each row lands under its actual
// parent (root items and nested submenu children mixed together), commands
// are split on newlines only for leaves, and submenus never carry commands.
func TestSettingsMenuTreeBuildsNestedItems(t *testing.T) {
	prefix := "menu.local."
	rows := []f4settings.Record{
		{ID: "root", Values: map[string]string{prefix + "Label": "Tools", prefix + "HotKey": "T", prefix + "Submenu": "true", prefix + "Parent": ""}},
		{ID: "child", Values: map[string]string{prefix + "Label": "Run", prefix + "HotKey": "R", prefix + "Submenu": "false", prefix + "Parent": "root", prefix + "Commands": "echo one\necho two"}},
		{ID: "leaf", Values: map[string]string{prefix + "Label": "Top", prefix + "HotKey": "X", prefix + "Submenu": "false", prefix + "Parent": ""}},
	}
	items, err := settingsMenuTree(rows, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("root items = %d, want 2: %#v", len(items), items)
	}
	var tools, top *panel.UserMenuItem
	for i := range items {
		switch items[i].Label {
		case "Tools":
			tools = &items[i]
		case "Top":
			top = &items[i]
		}
	}
	if tools == nil || top == nil {
		t.Fatalf("missing expected root items: %#v", items)
	}
	if !tools.IsSubmenu() || len(tools.Submenu) != 1 || tools.Submenu[0].Label != "Run" {
		t.Fatalf("submenu was not built correctly: %#v", tools)
	}
	if len(tools.Submenu[0].Commands) != 2 || tools.Submenu[0].Commands[0] != "echo one" || tools.Submenu[0].Commands[1] != "echo two" {
		t.Fatalf("leaf commands not split on newline: %#v", tools.Submenu[0].Commands)
	}
	if top.IsSubmenu() {
		t.Fatalf("plain top-level item treated as a submenu: %#v", top)
	}
}

// The F11 menu page lists the registered entries and stores which of them are
// hidden by action name, keeping the names of plugins that are not loaded
// (f4#918).
func TestSettingsCoreRecordProviderPluginMenuVisibilityRoundTrip(t *testing.T) {
	saved := plughost.PluginMenuItems
	t.Cleanup(func() { plughost.PluginMenuItems = saved })
	plughost.PluginMenuItems = []plughost.PluginMenuItem{
		{ActionName: "test.menu.alpha", Label: "&Alpha"},
		{ActionName: "test.menu.beta", Label: "Beta"},
	}
	p := newCoreRecordSettingsProvider()
	store := settingsRecordStoreByID(t, p, "plugin-menu")
	t.Cleanup(func() { _ = os.Remove(store.path) })
	if err := panel.SavePluginMenuHidden([]string{"gone.plugin.entry"}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Values["entry.Name"] != "Alpha" || rows[0].Values["entry.Enabled"] != "true" {
		t.Fatalf("fresh F11 visibility = %#v, want both shown and the label without its ampersand", rows)
	}
	rows[1].Values["entry.Enabled"] = "false"
	if err := store.save(rows); err != nil {
		t.Fatal(err)
	}

	hidden, err := panel.LoadPluginMenuHidden()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(hidden, ",") != "gone.plugin.entry,test.menu.beta" {
		t.Fatalf("hidden names = %v, want the unloaded plugin's name kept and Beta added", hidden)
	}
	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Values["entry.Enabled"] != "true" || loaded[1].Values["entry.Enabled"] != "false" {
		t.Fatalf("saved F11 visibility = %#v", loaded)
	}
}
