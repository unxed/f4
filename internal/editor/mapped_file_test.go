package editor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/vfs"
)

func mapTestFile(t *testing.T, content string) (*MappedFile, string, vfs.VFS, vfs.ReadAtCloser) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "mapped.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(dir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	m, err := MapEditorFile(v, f)
	if err != nil {
		t.Fatalf("MapEditorFile: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, path, v, f
}

func TestCanMemoryMapFileSkipsMultiplyLinkedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "linked.txt")
	ordinary := filepath.Join(dir, "ordinary.txt")
	// #nosec G703 -- both paths are controlled files inside t.TempDir.
	if err := os.WriteFile(path, []byte("linked"), 0600); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- both paths are controlled files inside t.TempDir.
	if err := os.WriteFile(ordinary, []byte("ordinary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(dir, "linked-alias.txt")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	v := vfs.NewOSVFS(dir)
	if CanMemoryMapFile(context.Background(), v, path) {
		t.Fatal("multiply-linked file must not be memory mapped")
	}
	if !CanMemoryMapFile(context.Background(), v, ordinary) {
		t.Fatal("ordinary file should remain eligible for memory mapping")
	}
}

func TestMapEditorFile_BacksThePieceTableWithoutCopying(t *testing.T) {
	content := "hello world\nsecond line\n"
	m, _, _, _ := mapTestFile(t, content)

	if got := string(m.Bytes()); got != content {
		t.Fatalf("mapped contents = %q, want %q", got, content)
	}

	// The point of mapping: the piece table's whole-buffer window is the
	// mapping itself, so a search scans the file rather than a copy of it.
	Pt := piecetable.New(m.Bytes())
	view, ok := Pt.View(0, Pt.Size())
	if !ok {
		t.Fatal("View over a mapped buffer must succeed")
	}
	if &view[0] != &m.Bytes()[0] {
		t.Error("View copied the mapping instead of pointing into it")
	}
}

func TestMapEditorFileWithOffsetKeepsLogicalTextAndReleasesMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("text")...), 0600); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(dir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close source file: %v", err)
		}
	}()

	m, err := MapEditorFileWithOffset(v, f, vfs.UTF8BOMSize)
	if err != nil {
		t.Fatalf("MapEditorFileWithOffset: %v", err)
	}
	if got := string(m.Bytes()); got != "text" {
		t.Fatalf("mapped logical text = %q, want %q", got, "text")
	}
	if got := m.Size(); got != len("text") {
		t.Fatalf("mapped logical size = %d, want %d", got, len("text"))
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMapEditorFile_DeclinesWhatItShouldNotMap(t *testing.T) {
	dir := t.TempDir()

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		v := vfs.NewOSVFS(dir)
		f, err := v.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		if _, err := MapEditorFile(v, f); err != ErrNotMappable {
			t.Errorf("err = %v, want errNotMappable", err)
		}
	})

	t.Run("no backing file", func(t *testing.T) {
		v := vfs.NewOSVFS(dir)
		if _, err := MapEditorFile(v, nil); err != ErrNotMappable {
			t.Errorf("err = %v, want errNotMappable", err)
		}
	})

	t.Run("backing without a descriptor", func(t *testing.T) {
		// This is what a remote file looks like from here: it can answer
		// reads, but there is no local descriptor to map. It is the check
		// that keeps FISH+ and the cloud backends off this path, since
		// isLocalOSVFS deliberately sees through wrappers around a local
		// file system and cannot be the only gate.
		if _, err := MapEditorFile(vfs.NewOSVFS(dir), descriptorlessFile{size: 1024}); err != ErrNotMappable {
			t.Errorf("err = %v, want errNotMappable", err)
		}
	})
	t.Run("osvfs file exposes valid descriptor", func(t *testing.T) {
		path := filepath.Join(dir, "desc_check.txt")
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		v := vfs.NewOSVFS(dir)
		f, err := v.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		fd, ok := f.(fileDescriptor)
		if !ok || fd.Fd() == 0 || fd.Fd() == ^uintptr(0) {
			t.Errorf("OSVFS.Open returned file without valid Fd(): ok=%v, fd=%v", ok, fd)
		}
	})
}

// descriptorlessFile is a vfs.ReadAtCloser with no Fd, like every remote one.
type descriptorlessFile struct {
	size int64
}

func (d descriptorlessFile) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return 0, io.EOF
}
func (d descriptorlessFile) Read(ctx context.Context, p []byte) (int, error) { return 0, io.EOF }
func (d descriptorlessFile) Close() error                                    { return nil }
func (d descriptorlessFile) Size() int64                                     { return d.size }

// TestGuardMappedFaults_SurvivesTruncation is the reason the guard exists. A
// file truncated after it was mapped leaves its last pages backed by nothing,
// and touching one raises SIGBUS — a signal that takes the whole process down
// unless the goroutine that touches it asked for a panic instead.
func TestGuardMappedFaults_SurvivesTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps a mapped file from being truncated at all")
	}

	// Several pages, so there is a page to lose that is not the first.
	content := strings.Repeat("0123456789abcdef", 4096) // 64 KB
	m, path, _, _ := mapTestFile(t, content)

	if err := os.Truncate(path, 4096); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	faulted := false
	touch := func() {
		defer guardMappedFaults("reading a truncated mapping", func() { faulted = true })()
		// Read every page: the ones past the new end have nothing behind them.
		sum := 0
		for i := 0; i < len(m.Bytes()); i += 4096 {
			sum += int(m.Bytes()[i])
		}
		_ = sum
	}
	touch()

	if !faulted {
		// Some kernels keep the pages readable; the guard is still what makes
		// the difference on the ones that do not, and the test has proven it
		// does not crash either way.
		t.Log("no fault raised for the truncated pages on this kernel")
	}
}

// TestGuardMappedFaults_LetsOrdinaryPanicsThrough keeps the guard from turning
// into a blanket recover: a bug in the code it wraps must still surface.
func TestGuardMappedFaults_LetsOrdinaryPanicsThrough(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("the guard swallowed a panic that was not a fault")
		}
		if got, ok := r.(string); !ok || got != "not a fault" {
			t.Fatalf("recovered %v, want the original panic", r)
		}
	}()

	func() {
		defer guardMappedFaults("testing", func() { t.Error("fault handler ran for an ordinary panic") })()
		panic("not a fault")
	}()
}
