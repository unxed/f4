package archive

import (
	stdzip "archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func addArchiveFormatIndex(t *testing.T, ext string) int {
	t.Helper()
	for i, f := range addArchiveFormats {
		if f.Ext == ext {
			return i
		}
	}
	t.Fatalf("no format %s", ext)
	return -1
}

// What the window was told reaches the archiver, and only what the chosen
// format uses (f4#2002).
func TestAddArchiveSettingsBecomeArchiverOptions(t *testing.T) {
	zipIdx, sevenIdx, xzIdx := addArchiveFormatIndex(t, ".zip"), addArchiveFormatIndex(t, ".7z"), addArchiveFormatIndex(t, ".tar.xz")

	opts := addArchiveSettings{Format: zipIdx, Level: 4, Method: 1}.options()
	if opts.Method != "zstd" || opts.Level != 7 {
		t.Errorf("zip, Maximum, Zstandard: method %q level %d", opts.Method, opts.Level)
	}
	opts = addArchiveSettings{Format: sevenIdx, Level: 5, Solid: false}.options()
	if opts.Level != 0 || !opts.NonSolid || opts.Solid {
		t.Errorf("7z, non-solid: level %d solid %v nonsolid %v; the engine takes no level for 7z", opts.Level, opts.Solid, opts.NonSolid)
	}
	opts = addArchiveSettings{Format: xzIdx, Level: 1, Method: 3}.options()
	if opts.Method != "xz" || opts.Level != 1 || opts.Solid || opts.NonSolid {
		t.Errorf("tar.xz, Fastest: %+v", opts)
	}
}

func TestArchiveNameFollowsTheFormat(t *testing.T) {
	for _, tc := range []struct{ name, ext, want string }{
		{"docs.zip", ".7z", "docs.7z"},
		{"docs.tar.gz", ".tar.xz", "docs.tar.xz"},
		{"docs", ".zip", "docs.zip"},
		{"my.notes.txt", ".zip", "my.notes.txt.zip"},
	} {
		if got := archiveNameForFormat(tc.name, addArchiveFormatIndex(t, tc.ext)); got != tc.want {
			t.Errorf("%s as %s = %q, want %q", tc.name, tc.ext, got, tc.want)
		}
	}
}

// Choosing a format in the window renames the archive and offers only the
// controls that format uses; Create hands over what is shown.
func TestAddArchiveDialogFollowsTheFormat(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var got *addArchiveSettings
	d := newAddArchiveDialog("docs.zip", func(s addArchiveSettings) { got = &s })

	if d.level.IsDisabled() || d.method.IsDisabled() || !d.solid.IsDisabled() || d.solid.State != 0 {
		t.Fatal("zip: level and method are chosen, solid is not offered")
	}

	sevenIdx := addArchiveFormatIndex(t, ".7z")
	d.format.Menu.SetSelectPos(sevenIdx)
	d.format.Menu.OnAction(sevenIdx)
	if d.name.GetText() != "docs.7z" {
		t.Errorf("name after choosing 7z: %q", d.name.GetText())
	}
	if !d.level.IsDisabled() || !d.method.IsDisabled() || d.solid.IsDisabled() || d.solid.State != 1 {
		t.Error("7z: solid is offered and on, level and method are not chosen")
	}

	d.create.OnClick()
	if got == nil || got.Name != "docs.7z" || got.Format != sevenIdx || !got.Solid {
		t.Fatalf("Create handed over %+v", got)
	}
}

// The method chosen in the window is the one the archive is written with: a
// zip made with "Store" holds its files uncompressed.
func TestAddArchiveWritesWithTheChosenMethod(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	zipIdx := addArchiveFormatIndex(t, ".zip")
	store := -1
	for i, m := range addArchiveFormats[zipIdx].Methods {
		if m.ID == "store" {
			store = i
		}
	}
	showAddArchiveDialog = func(_ vfs.App, _ string, _ bool, done func(addArchiveSettings)) {
		done(addArchiveSettings{Name: "out.zip", Format: zipIdx, Method: store})
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), bytes.Repeat([]byte("data "), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"file1.txt"}, done: make(chan struct{})}
	actionAddArchive(app)
	<-app.done

	r, err := stdzip.OpenReader(filepath.Join(dir, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) == 0 {
		t.Fatal("the archive is empty")
	}
	for _, f := range r.File {
		if f.Method != stdzip.Store {
			t.Errorf("%s is stored with method %d, want Store", f.Name, f.Method)
		}
	}
}
