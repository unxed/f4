package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type nestedFolderHistoryVFS struct {
	*vfs.NullVFS
	parent vfs.VFS
	path   string
}

func (v *nestedFolderHistoryVFS) GetPath() string    { return v.path }
func (v *nestedFolderHistoryVFS) ParentVFS() vfs.VFS { return v.parent }

func TestShouldRecordFolderHistorySkipsUnqualifiedNestedAbsolutePath(t *testing.T) {
	pnl := &panel.FileSystemPanel{Vfs: &nestedFolderHistoryVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "/home/user",
	}}

	if panel.ShouldRecordFolderHistory(pnl, pnl.Vfs.GetPath()) {
		t.Fatal("unqualified absolute path from a nested VFS must not enter local folder history")
	}
	if !panel.ShouldRecordFolderHistory(pnl, "remote-relative") {
		t.Fatal("relative nested paths should retain the existing history behavior")
	}
	if !panel.ShouldRecordFolderHistory(pnl, "netfox://site/home/user") {
		t.Fatal("persistent URI paths must remain eligible for folder history")
	}
}

// pluginHistoryVFS is a minimal fake VFS implementing vfs.HistoryPathProvider
// (f4#262), used to exercise the generic panel-plugin folder-history hook
// without depending on any real plugin package such as NetFox.
type pluginHistoryVFS struct {
	*vfs.NullVFS
	parent   vfs.VFS
	path     string
	display  string
	ref      string
	entryOK  bool
	navigate func(ref string) bool
}

func (v *pluginHistoryVFS) GetPath() string    { return v.path }
func (v *pluginHistoryVFS) ParentVFS() vfs.VFS { return v.parent }
func (v *pluginHistoryVFS) HistoryEntry() (display, ref string, ok bool) {
	return v.display, v.ref, v.entryOK
}
func (v *pluginHistoryVFS) NavigateHistoryEntry(ref string) bool {
	if v.navigate == nil {
		return false
	}
	return v.navigate(ref)
}

func withTestHistoryProvider(t *testing.T) *history.F4HistoryProvider {
	t.Helper()
	hp := history.NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = hp
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })
	return hp
}

// TestRecordFolderHistoryEntryUsesPluginHook checks that a panel-plugin VFS
// (e.g. a NetFox session) owns its own folder-history entry end to end
// (f4#262): the recorded entry is the plugin's display text and opaque
// reference, never the raw remote path treated as if it were local.
func TestRecordFolderHistoryEntryUsesPluginHook(t *testing.T) {
	hp := withTestHistoryProvider(t)

	fakeVFS := &pluginHistoryVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "/root/foo",
		display: "user@host:/root/foo",
		ref:     "opaque-ref",
		entryOK: true,
	}
	pnl := &panel.FileSystemPanel{Vfs: fakeVFS}

	panel.RecordFolderHistoryEntry(pnl, fakeVFS, fakeVFS.GetPath())

	records, _ := history.LoadFolderHistoryRecords(hp)
	if len(records) != 1 {
		t.Fatalf("expected exactly one recorded entry, got %#v", records)
	}
	if records[0].Name != "user@host:/root/foo" {
		t.Fatalf("recorded entry must be the plugin's display text, got %q", records[0].Name)
	}
	if !records[0].IsPluginEntry() || records[0].PluginRef != "opaque-ref" {
		t.Fatalf("recorded entry must be marked plugin-owned: %#v", records[0])
	}
}

// TestRecordFolderHistoryEntrySkipsPluginWithoutHook is the regression test
// for f4#262: a panel-plugin VFS that does not implement
// vfs.HistoryPathProvider must produce no folder-history entry at all —
// neither the raw remote path mistaken for a local one, nor any placeholder.
func TestRecordFolderHistoryEntrySkipsPluginWithoutHook(t *testing.T) {
	hp := withTestHistoryProvider(t)

	fakeVFS := &nestedFolderHistoryVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "/home/user",
	}
	pnl := &panel.FileSystemPanel{Vfs: fakeVFS}

	panel.RecordFolderHistoryEntry(pnl, fakeVFS, fakeVFS.GetPath())

	records, _ := history.LoadFolderHistoryRecords(hp)
	if len(records) != 0 {
		t.Fatalf("a plugin VFS without the history hook must produce no entry, got %#v", records)
	}
}

// TestNavigateOpenPluginHistoryEntryNoOpenSession is the "session closed"
// case from f4#262: when neither panel has a live session matching the
// entry, navigation must be refused, and must never attempt to reconnect or
// crash.
func TestNavigateOpenPluginHistoryEntryNoOpenSession(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()

	if pf.NavigateOpenPluginHistoryEntry("no.such.PluginVFS", "some-ref") {
		t.Fatal("navigation must fail when no open panel has a matching session")
	}
}

// TestNavigateOpenPluginHistoryEntrySwitchesToOpenSession checks the success
// path: an already-open session on one of the two panels accepts the ref and
// the frame switches focus to it, without opening anything new.
func TestNavigateOpenPluginHistoryEntrySwitchesToOpenSession(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()

	moved := false
	fakeVFS := &pluginHistoryVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "/elsewhere",
		navigate: func(ref string) bool {
			if ref != "target-ref" {
				return false
			}
			moved = true
			return true
		},
	}
	otherIdx := 1 - pf.ActiveIdx
	fsp := pf.Panels[otherIdx].(*panel.FileSystemPanel)
	fsp.Vfs = fakeVFS

	vfsType := fmt.Sprintf("%T", fakeVFS)
	if !pf.NavigateOpenPluginHistoryEntry(vfsType, "target-ref") {
		t.Fatal("navigation must succeed when a matching session is open on the other panel")
	}
	paneltest.WaitForLoad(t, fsp)
	if !moved {
		t.Fatal("NavigateHistoryEntry was never called on the open session")
	}
	if pf.ActiveIdx != otherIdx {
		t.Fatalf("frame must switch focus to the panel holding the open session, ActiveIdx=%d want %d", pf.ActiveIdx, otherIdx)
	}
}
