package archive

import (
	stdzip "archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/vtui"
)

func TestParseVolumeSize(t *testing.T) {
	for in, want := range map[string]int64{"700M": 700 << 20, "4g": 4 << 30, "1500K": 1500 << 10, "123": 123, "10MB": 10 << 20, " 2 G ": 2 << 30} {
		if got, err := parseVolumeSize(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-5M", "abc", "M"} {
		if _, err := parseVolumeSize(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

// Zip and tar write their volumes themselves; 7z is cut afterwards
// (f4#2002, part 3).
func TestVolumeSizeReachesTheRightPlace(t *testing.T) {
	z := addArchiveSettings{Format: addArchiveFormatIndex(t, ".zip"), VolumeSize: 1 << 20}
	if z.options().SplitSize != 1<<20 || z.postSplit() {
		t.Error("zip writes its volumes itself")
	}
	s := addArchiveSettings{Format: addArchiveFormatIndex(t, ".7z"), VolumeSize: 1 << 20}
	if s.options().SplitSize != 0 || !s.postSplit() {
		t.Error("7z is cut into volumes after it is written")
	}
	if got := s.archiveFile("x.7z"); got != "x.7z.001" {
		t.Errorf("first volume %q", got)
	}
}

func addArchiveRandomFile(t *testing.T, settings addArchiveSettings, size int) (dir string) {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	showAddArchiveDialog = func(_ vfs.App, _ string, _ bool, done func(addArchiveSettings)) { done(settings) }

	dir = t.TempDir()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file1.bin"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	app := &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"file1.bin"}, done: make(chan struct{})}
	actionAddArchive(app)
	<-app.done
	return dir
}

// joinVolumes checks that the archive is in volumes no bigger than size,
// with no single file beside them, and returns them joined.
func joinVolumes(t *testing.T, dir, name string, size int64) []byte {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Errorf("%s is there as one file beside its volumes", name)
	}
	vols, _ := filepath.Glob(filepath.Join(dir, name+".0*"))
	if len(vols) < 2 {
		t.Fatalf("volumes: %v", vols)
	}
	var all bytes.Buffer
	for _, v := range vols {
		data, err := os.ReadFile(v)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(data)) > size {
			t.Errorf("%s is %d bytes, more than %d", filepath.Base(v), len(data), size)
		}
		all.Write(data)
	}
	return all.Bytes()
}

func TestAddArchiveZipInVolumes(t *testing.T) {
	zipIdx := addArchiveFormatIndex(t, ".zip")
	store := len(addArchiveFormats[zipIdx].Methods) - 1
	dir := addArchiveRandomFile(t, addArchiveSettings{Name: "out.zip", Format: zipIdx, Method: store, VolumeSize: 4096}, 20000)
	joined := joinVolumes(t, dir, "out.zip", 4096)
	r, err := stdzip.NewReader(bytes.NewReader(joined), int64(len(joined)))
	if err != nil {
		t.Fatalf("the joined volumes are not a zip: %v", err)
	}
	if len(r.File) != 1 || r.File[0].UncompressedSize64 != 20000 {
		t.Fatalf("files in the joined volumes: %v", r.File)
	}
}

func TestAddArchive7zInVolumes(t *testing.T) {
	dir := addArchiveRandomFile(t, addArchiveSettings{Name: "out.7z", Format: addArchiveFormatIndex(t, ".7z"), Solid: true, VolumeSize: 4096}, 20000)
	joinVolumes(t, dir, "out.7z", 4096)
	r, err := sevenzip.OpenReader(filepath.Join(dir, "out.7z.001"))
	if err != nil {
		t.Fatalf("the volumes do not open as 7z: %v", err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 1 {
		t.Fatalf("files: %d", len(r.File))
	}
	rc, err := r.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if n, err := io.Copy(io.Discard, rc); err != nil || n != 20000 {
		t.Fatalf("read %d bytes, %v", n, err)
	}
}

// An archive that ends exactly on a volume boundary gets no empty volume.
func TestSplitIntoVolumesOnABoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.7z")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 300), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := splitIntoVolumes(context.Background(), path, 100); err != nil {
		t.Fatal(err)
	}
	vols, _ := filepath.Glob(path + ".*")
	if len(vols) != 3 {
		t.Fatalf("volumes %v, want 3", vols)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the whole archive is still there")
	}
}

// "Split into volumes" enables the size; a size that cannot be read keeps
// the window open.
func TestAddArchiveDialogVolumeSize(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var got *addArchiveSettings
	d := newAddArchiveDialog("docs.zip", func(s addArchiveSettings) { got = &s })
	if !d.volumeSize.IsDisabled() {
		t.Fatal("the volume size is chosen only when splitting")
	}
	d.split.State = 1
	d.split.OnChange(1)
	if d.volumeSize.IsDisabled() {
		t.Fatal("splitting enables the volume size")
	}
	d.volumeSize.Edit.SetText("lots")
	d.create.OnClick()
	if got != nil {
		t.Fatal("an unreadable volume size created the archive")
	}
	d.volumeSize.Edit.SetText("100M")
	d.create.OnClick()
	if got == nil || got.VolumeSize != 100<<20 {
		t.Fatalf("Create handed over %+v", got)
	}
}

// Volumes are offered only where f4 opens them again from the first one:
// zip and 7z; a tar format is written as one file.
func TestVolumesOpenFromTheFirstOne(t *testing.T) {
	tgz := addArchiveSettings{Format: addArchiveFormatIndex(t, ".tar.gz"), VolumeSize: 4096}
	if tgz.options().SplitSize != 0 || tgz.postSplit() || tgz.archiveFile("x.tar.gz") != "x.tar.gz" {
		t.Error("a tar archive must not be cut into volumes")
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	d := newAddArchiveDialog("docs.tar.gz", func(addArchiveSettings) {})
	if !d.split.IsDisabled() {
		t.Error("tar: splitting is not offered")
	}

	zipIdx := addArchiveFormatIndex(t, ".zip")
	for _, s := range []addArchiveSettings{
		{Name: "out.zip", Format: zipIdx, Method: len(addArchiveFormats[zipIdx].Methods) - 1, VolumeSize: 4096},
		{Name: "out.7z", Format: addArchiveFormatIndex(t, ".7z"), Solid: true, VolumeSize: 4096},
	} {
		dir := addArchiveRandomFile(t, s, 20000)
		parent := vfs.NewOSVFS(dir)
		first := filepath.Join(dir, s.Name+".001")
		p := &ArchiveProvider{}
		if !p.CanOpen(context.Background(), parent, first) {
			t.Errorf("%s: the first volume is not taken for an archive", s.Name)
			continue
		}
		av, err := p.Open(context.Background(), parent, first)
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		n := 0
		err = av.ReadDir(context.Background(), av.GetPath(), func(items []vfs.VFSItem) { n += len(items) })
		// Closed before the temporary directory is removed: Windows does not
		// delete a volume that is still open.
		if cerr := av.Close(); cerr != nil {
			t.Errorf("%s: close: %v", s.Name, cerr)
		}
		if err != nil || n != 1 {
			t.Errorf("%s: %d entries, %v", s.Name, n, err)
		}
	}
}
