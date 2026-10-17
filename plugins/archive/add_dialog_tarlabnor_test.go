package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// f4#2002 (tarlabnor, 17-10, point 6): an archive no bigger than one volume
// keeps its own name; it gets no .001.
func TestSplitIntoVolumesKeepsAnArchiveSmallerThanAVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 70), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := splitIntoVolumes(context.Background(), path, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the archive is gone: %v", err)
	}
	if vols, _ := filepath.Glob(path + ".*"); len(vols) != 0 {
		t.Errorf("volumes %v for an archive smaller than a volume", vols)
	}
}

// The same for an archiver that writes its volumes itself (7z): one volume
// is renamed to the archive's name, two or more stay as they are.
func TestCollapseSingleVolume(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.7z")
	if err := os.WriteFile(one+".001", []byte("7z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := collapseSingleVolume(one); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(one); err != nil {
		t.Errorf("the single volume was not renamed: %v", err)
	}
	two := filepath.Join(dir, "two.7z")
	for _, ext := range []string{".001", ".002"} {
		if err := os.WriteFile(two+ext, []byte("7z"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := collapseSingleVolume(two); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(two + ".001"); err != nil {
		t.Errorf("a volume of a real set was renamed: %v", err)
	}
}

func TestAddArchive7zSmallerThanAVolume(t *testing.T) {
	dir := addArchiveRandomFile(t, addArchiveSettings{Name: "out.7z", Format: addArchiveFormatIndex(t, ".7z"), Solid: true, VolumeSize: 1 << 20}, 2000)
	if _, err := os.Stat(filepath.Join(dir, "out.7z")); err != nil {
		t.Errorf("out.7z: %v", err)
	}
	if vols, _ := filepath.Glob(filepath.Join(dir, "out.7z.0*")); len(vols) != 0 {
		t.Errorf("volumes %v for an archive smaller than a volume", vols)
	}
}

// selectionClearApp is the progress mock with a host that unmarks entries.
type selectionClearApp struct {
	*mockAppForProgress
	cleared []string
}

type recordedToken struct {
	app  *selectionClearApp
	name string
}

func (t recordedToken) Clear() bool {
	t.app.cleared = append(t.app.cleared, t.name)
	return true
}

func (a *selectionClearApp) CaptureSelectionToken(name string) (vfs.SelectionToken, bool) {
	return recordedToken{app: a, name: name}, true
}

// Point 1: what was marked is unmarked once it is in the archive.
func TestAddArchiveUnmarksWhatItArchived(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	showAddArchiveDialog = func(_ vfs.App, _ string, done func(addArchiveSettings)) {
		done(addArchiveSettings{Name: "out.zip", Format: 0})
	}
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := &selectionClearApp{mockAppForProgress: &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"a.txt", "b.txt"}, done: make(chan struct{})}}
	actionAddArchive(app)
	<-app.done
	if len(app.cleared) != 2 {
		t.Fatalf("unmarked %v, want a.txt and b.txt", app.cleared)
	}
}
