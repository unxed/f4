package id3editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/id3-go"
	v1 "github.com/unxed/id3-go/v1"
	"github.com/unxed/vtui"
)

type nonLocalID3VFS struct{ vfs.VFS }

func newID3FrameManager(t *testing.T) *vtui.FrameManagerType {
	t.Helper()
	old := vtui.FrameManager
	fm := vtui.NewFrameManager()
	fm.Init(vtui.NewSilentScreenBuf())
	vtui.FrameManager = fm
	t.Cleanup(func() {
		for fm.GetTopFrame() != nil {
			fm.Pop()
		}
		fm.Shutdown()
		vtui.FrameManager = old
	})
	return fm
}

func TestHandleEditRejectsUnsupportedSelections(t *testing.T) {
	local := vfs.NewOSVFS(t.TempDir())
	tests := []struct {
		name      string
		activeVFS vfs.VFS
		selected  []string
		wantTitle string
	}{
		{name: "no active VFS", wantTitle: " Error "},
		{name: "remote VFS", activeVFS: nonLocalID3VFS{}, selected: []string{"song.mp3"}, wantTitle: " Error "},
		{name: "no selection", activeVFS: local, wantTitle: " ID3 Editor "},
		{name: "non MP3", activeVFS: local, selected: []string{"song.txt"}, wantTitle: " ID3 Editor "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fm := newID3FrameManager(t)
			app := &id3AppStub{activeVFS: test.activeVFS, selected: test.selected}
			(&ID3EditorPlugin{}).handleEdit(app)

			dlg, ok := fm.GetTopFrame().(*vtui.Window)
			if !ok {
				t.Fatalf("top frame = %T, want message dialog", fm.GetTopFrame())
			}
			if dlg.GetTitle() != test.wantTitle {
				t.Errorf("dialog title = %q, want %q", dlg.GetTitle(), test.wantTitle)
			}
		})
	}
}

// TestCanHandleEditMatchesHandleEditEligibility checks that the Enabled
// predicate wired into the plugin command (f4#1356) agrees with what
// handleEdit itself accepts: disabled exactly on the selections that would
// otherwise pop an error dialog, enabled on a real local MP3 file.
func TestCanHandleEditMatchesHandleEditEligibility(t *testing.T) {
	local := vfs.NewOSVFS(t.TempDir())
	tests := []struct {
		name      string
		activeVFS vfs.VFS
		selected  []string
		want      bool
	}{
		{name: "no active VFS", want: false},
		{name: "remote VFS", activeVFS: nonLocalID3VFS{}, selected: []string{"song.mp3"}, want: false},
		{name: "no selection", activeVFS: local, want: false},
		{name: "non MP3", activeVFS: local, selected: []string{"song.txt"}, want: false},
		{name: "MP3", activeVFS: local, selected: []string{"song.mp3"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := &id3AppStub{activeVFS: test.activeVFS, selected: test.selected}
			if got := canHandleEdit(app); got != test.want {
				t.Errorf("canHandleEdit() = %v, want %v", got, test.want)
			}
		})
	}
}

func writeID3TestFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "song.mp3")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	tag := &v1.Tag{}
	tag.SetTitle("Initial Title")
	tag.SetArtist("Initial Artist")
	tag.SetAlbum("Initial Album")
	tag.SetYear("2020")
	tag.SetGenre("Rock")
	tag.SetComment("Initial Comment")
	if _, err := file.Write(tag.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandleEditOpensAndSavesSelectedMP3(t *testing.T) {
	fm := newID3FrameManager(t)
	path := writeID3TestFile(t)
	app := &id3AppStub{
		activeVFS: vfs.NewOSVFS(filepath.Dir(path)),
		selected:  []string{filepath.Base(path)},
	}

	(&ID3EditorPlugin{}).handleEdit(app)
	dlg, ok := fm.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want editor dialog", fm.GetTopFrame())
	}
	if strings.Trim(strings.TrimSpace(dlg.GetTitle()), "{}") != "ID3Editor.Title" {
		t.Errorf("dialog title = %q, want ID3Editor.Title", dlg.GetTitle())
	}

	var edits []*vtui.Edit
	var save *vtui.Button
	for _, child := range dlg.GetChildren() {
		switch item := child.(type) {
		case *vtui.Edit:
			edits = append(edits, item)
		case *vtui.Button:
			if item.IsDefault {
				save = item
			}
		}
	}
	if len(edits) != 6 {
		t.Fatalf("editor fields = %d, want 6", len(edits))
	}
	if save == nil || save.OnClick == nil {
		t.Fatal("save button callback is not installed")
	}
	edits[0].SetText("Saved Title")
	edits[1].SetText("Saved Artist")
	save.OnClick()
	if app.refreshCalls != 1 {
		t.Fatalf("RefreshAll calls = %d, want 1", app.refreshCalls)
	}

	file, err := id3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(strings.TrimRight(file.Title(), "\x00")) != "Saved Title" {
		t.Errorf("saved title = %q, want Saved Title", file.Title())
	}
	if strings.TrimSpace(strings.TrimRight(file.Artist(), "\x00")) != "Saved Artist" {
		t.Errorf("saved artist = %q, want Saved Artist", file.Artist())
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleEditPreservesModificationTimeWhenConfigured(t *testing.T) {
	oldConfigDir := vfs.CustomConfigDir
	vfs.CustomConfigDir = t.TempDir()
	t.Cleanup(func() { vfs.CustomConfigDir = oldConfigDir })
	if err := saveID3EditorSettings(id3EditorSettings{PreserveModTime: true}); err != nil {
		t.Fatal(err)
	}

	path := writeID3TestFile(t)
	want := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.Local)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}
	newID3FrameManager(t)
	app := &id3AppStub{activeVFS: vfs.NewOSVFS(filepath.Dir(path)), selected: []string{filepath.Base(path)}}
	(&ID3EditorPlugin{}).handleEdit(app)
	dlg := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	var save *vtui.Button
	for _, child := range dlg.GetChildren() {
		if button, ok := child.(*vtui.Button); ok && button.IsDefault {
			save = button
		}
	}
	if save == nil || save.OnClick == nil {
		t.Fatal("save button callback is not installed")
	}
	save.OnClick()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Errorf("modification time = %v, want %v", info.ModTime(), want)
	}
}

// TestPluginLegacyMenuHandlerDelegatesToHandleEdit checks Init's fallback
// branch (host has no ContributionHost support) end to end: the callback
// RegisterPluginMenuItem receives must actually forward to handleEdit, not
// just get registered and left unused. TestPluginUsesLegacyMenuForHostsWithoutRichContributions
// (plugin_contributions_test.go) only checks that a handler was installed;
// this test is the one that calls it and checks it behaves like handleEdit.
func TestPluginLegacyMenuHandlerDelegatesToHandleEdit(t *testing.T) {
	host := &id3HostMock{}
	plugin := &ID3EditorPlugin{}
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.legacyHandler == nil {
		t.Fatal("legacy host did not receive an ID3 editor menu item handler")
	}

	fm := newID3FrameManager(t)
	app := &id3AppStub{} // no active VFS: handleEdit's very first guard

	host.legacyHandler(app)

	dlg, ok := fm.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want message dialog", fm.GetTopFrame())
	}
	if dlg.GetTitle() != " Error " {
		t.Errorf("dialog title = %q, want %q", dlg.GetTitle(), " Error ")
	}
}

// TestHandleEditSaveErrorShowsMessage covers btnSave.OnClick's error branch:
// when file.Close() fails to flush the edited tag back to disk, the dialog
// must report the error and stay open instead of closing and refreshing as
// if the save had succeeded. To provoke a real Close() failure without
// touching the id3-go library, truncate the underlying file out from under
// the still-open *id3.File: the v1 writer seeks v1.TagSize bytes back from
// EOF, and on a file now shorter than that the seek lands before byte 0 and
// fails.
func TestHandleEditSaveErrorShowsMessage(t *testing.T) {
	fm := newID3FrameManager(t)
	path := writeID3TestFile(t)
	app := &id3AppStub{
		activeVFS: vfs.NewOSVFS(filepath.Dir(path)),
		selected:  []string{filepath.Base(path)},
	}

	(&ID3EditorPlugin{}).handleEdit(app)
	dlg, ok := fm.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want editor dialog", fm.GetTopFrame())
	}

	var save *vtui.Button
	for _, child := range dlg.GetChildren() {
		if button, ok := child.(*vtui.Button); ok && button.IsDefault {
			save = button
		}
	}
	if save == nil || save.OnClick == nil {
		t.Fatal("save button callback is not installed")
	}

	if err := os.Truncate(path, 4); err != nil {
		t.Fatal(err)
	}

	save.OnClick()

	if dlg.IsDone() {
		t.Fatal("save error unexpectedly closed the editor dialog")
	}
	if app.refreshCalls != 0 {
		t.Fatalf("RefreshAll calls = %d, want 0 on save error", app.refreshCalls)
	}
	if len(app.messages) != 1 {
		t.Fatalf("message count = %d, want 1 on save error", len(app.messages))
	}
}

func TestShowEditorDialogCancelClosesFile(t *testing.T) {
	fm := newID3FrameManager(t)
	path := writeID3TestFile(t)
	app := &id3AppStub{}
	(&ID3EditorPlugin{}).showEditorDialog(app, path)
	dlg, ok := fm.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want editor dialog", fm.GetTopFrame())
	}

	var cancel *vtui.Button
	for _, child := range dlg.GetChildren() {
		button, ok := child.(*vtui.Button)
		if ok && !button.IsDefault {
			cancel = button
		}
	}
	if cancel == nil || cancel.OnClick == nil {
		t.Fatal("cancel button callback is not installed")
	}
	cancel.OnClick()
	if !dlg.IsDone() {
		t.Fatal("cancel did not close the editor dialog")
	}
}

var _ vfs.App = (*id3AppStub)(nil)
var _ vfs.VFS = (*nonLocalID3VFS)(nil)
