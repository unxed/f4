package archive

import (
	stdzip "archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// addArchiveTree archives the folder "src" of a fresh directory holding
// a.txt, b.bin and sub/c.txt, and returns the directory and the names in
// the archive.
func addArchiveTree(t *testing.T, settings addArchiveSettings) (string, []string) {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	settings.Name = "out.zip"
	settings.Format = addArchiveFormatIndex(t, ".zip")
	showAddArchiveDialog = func(_ vfs.App, _ string, _ bool, done func(addArchiveSettings)) { done(settings) }

	dir := t.TempDir()
	for _, f := range []string{"src/a.txt", "src/b.bin", "src/sub/c.txt"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"src"}, done: make(chan struct{})}
	actionAddArchive(app)
	<-app.done

	r, err := stdzip.OpenReader(filepath.Join(dir, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var names []string
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			names = append(names, filepath.ToSlash(f.Name))
		}
	}
	sort.Strings(names)
	return dir, names
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The filter puts only the matching files into the archive, and "Delete
// files after archiving" deletes only what went in: a folder still holding
// something left out stays (f4#2002, part 4).
func TestAddArchiveFilterAndDeleteOnlyWhatWentIn(t *testing.T) {
	dir, names := addArchiveTree(t, addArchiveSettings{Filter: "*.txt", DeleteAfter: true})
	if len(names) != 2 || filepath.Base(names[0]) != "a.txt" || filepath.Base(names[1]) != "c.txt" {
		t.Fatalf("archived %v, want a.txt and sub/c.txt", names)
	}
	if exists(filepath.Join(dir, "src", "a.txt")) || exists(filepath.Join(dir, "src", "sub", "c.txt")) {
		t.Error("an archived file was not deleted")
	}
	if exists(filepath.Join(dir, "src", "sub")) {
		t.Error("a folder left empty was not deleted")
	}
	if !exists(filepath.Join(dir, "src", "b.bin")) {
		t.Fatal("a file the filter left out was deleted")
	}
}

// Without a filter the whole selection goes in and, with "Delete files
// after archiving", goes away.
func TestAddArchiveDeleteAfterRemovesTheSelection(t *testing.T) {
	dir, names := addArchiveTree(t, addArchiveSettings{DeleteAfter: true})
	if len(names) != 3 {
		t.Fatalf("archived %v", names)
	}
	if exists(filepath.Join(dir, "src")) {
		t.Error("the archived folder is still there")
	}
}

// A file that cannot be read fails the archive, or with "Skip files that
// cannot be read" is left out.
func TestAddArchiveSkipsUnreadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file its owner cannot read")
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	dir := t.TempDir()
	for _, n := range []string{"ok.txt", "locked.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked.txt"), 0o600) })
	showAddArchiveDialog = func(_ vfs.App, _ string, _ bool, done func(addArchiveSettings)) {
		done(addArchiveSettings{Name: "out.zip", Format: addArchiveFormatIndex(t, ".zip"), SkipUnreadable: true})
	}
	app := &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"ok.txt", "locked.txt"}, done: make(chan struct{})}
	actionAddArchive(app)
	<-app.done
	r, err := stdzip.OpenReader(filepath.Join(dir, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 1 || r.File[0].Name != "ok.txt" {
		t.Fatalf("archive holds %d files", len(r.File))
	}
}

func TestAddArchiveDialogHandsOverPart4(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var got *addArchiveSettings
	d := newAddArchiveDialog("docs.zip", func(s addArchiveSettings) { got = &s })
	d.deleteAfter.State = 1
	d.skipErrors.State = 1
	d.filter.SetText(" *.txt ")
	d.create.OnClick()
	if got == nil || !got.DeleteAfter || !got.SkipUnreadable || got.Filter != "*.txt" {
		t.Fatalf("Create handed over %+v", got)
	}
}
