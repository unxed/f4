package archive

import (
	"path/filepath"
	"testing"

	"github.com/unxed/vtui"
)

func TestArchiveNameKeepsOrRemovesSingleFileExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive_preferences.json")
	oldPath := addArchivePreferencesPath
	addArchivePreferencesPath = func() string { return path }
	t.Cleanup(func() { addArchivePreferencesPath = oldPath })
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	if !loadKeepArchiveFileExtension() {
		t.Fatal("missing preference should preserve the source extension by default")
	}
	var created *addArchiveSettings
	d := newAddArchiveDialog("report.final.zip", func(s addArchiveSettings) { created = &s }, true)
	if got := d.name.GetText(); got != "report.final.zip" {
		t.Fatalf("default archive name = %q", got)
	}
	d.keepExtension.State = 0
	d.keepExtension.OnChange(0)
	if got := d.name.GetText(); got != "report.zip" {
		t.Fatalf("name without source extension = %q", got)
	}
	seven := addArchiveFormatIndex(t, ".7z")
	d.format.Menu.SetSelectPos(seven)
	d.format.Menu.OnAction(seven)
	if got := d.name.GetText(); got != "report.7z" {
		t.Fatalf("format change without source extension = %q", got)
	}
	d.create.OnClick()
	if created == nil || created.KeepFileExtension {
		t.Fatalf("Create handed over %+v", created)
	}
	if loadKeepArchiveFileExtension() {
		t.Fatal("unchecked preference was not persisted")
	}

	e := newAddArchiveDialog("report.final.zip", func(addArchiveSettings) {}, true)
	if got := e.name.GetText(); got != "report.zip" {
		t.Fatalf("saved preference did not affect next suggestion: %q", got)
	}
	e.keepExtension.State = 1
	e.keepExtension.OnChange(1)
	if got := e.name.GetText(); got != "report.final.zip" {
		t.Fatalf("re-enabled source extension = %q", got)
	}

	dir := newAddArchiveDialog("release.v1.zip", func(addArchiveSettings) {}, false)
	dir.keepExtension.State = 0
	dir.keepExtension.OnChange(0)
	if got := dir.name.GetText(); got != "release.v1.zip" {
		t.Fatalf("directory-derived name changed with file-extension option: %q", got)
	}
}
