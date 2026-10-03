package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dsnet/compress/bzip2"
	"github.com/unxed/f4/vfs"
)

func tarGzipBytes(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func openTarGzipArchive(t *testing.T, content []byte) (*ArchiveVFS, string) {
	t.Helper()
	root := t.TempDir()
	archivePath := filepath.Join(root, "outer.tar.gz")
	if err := os.WriteFile(archivePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := NewArchiveVFS(vfs.NewOSVFS(root), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := v.Close(); err != nil {
			t.Errorf("close tar.gz archive VFS: %v", err)
		}
	})
	return v, archivePath
}

func requireReaderBacked(t *testing.T, v *ArchiveVFS, label string) {
	t.Helper()
	if !v.readerBacked {
		t.Fatalf("%s is not reader-backed", label)
	}
	if v.backingPath != "" {
		t.Fatalf("%s has a materialized backing path %q", label, v.backingPath)
	}
}

func readArchiveMember(t *testing.T, v *ArchiveVFS, path string) []byte {
	t.Helper()
	r, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(ctxReader{r: r, ctx: context.Background()})
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}

func bzip2Bytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := bzip2.NewWriter(&buf, &bzip2.WriterConfig{Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestArchiveVFSNestedBzipPreservesMemberName(t *testing.T) {
	ctx := context.Background()
	outer, outerPath := openOuterArchive(t, map[string][]byte{
		"sample1.bz2": bzip2Bytes(t, []byte("nested bzip content")),
	})

	innerPath := outer.Join(outerPath, "sample1.bz2")
	inner, err := NewArchiveVFSContext(ctx, outer, innerPath)
	if err != nil {
		t.Fatalf("open bzip2 inside ZIP: %v", err)
	}
	t.Cleanup(func() { _ = inner.Close() })

	var names []string
	if err := inner.ReadDir(ctx, inner.GetPath(), func(items []vfs.VFSItem) {
		for _, item := range items {
			names = append(names, item.Name)
		}
	}); err != nil {
		t.Fatalf("read nested bzip2: %v", err)
	}
	if len(names) != 1 || names[0] != "sample1" {
		t.Fatalf("nested bzip2 listing = %v, want [sample1]", names)
	}
}

// The same generic reader-backed path composes ZIP and compressed TAR in both
// provider transitions, and keeps every intermediary as a VFS stream. The
// final member is read normally; opening each archive is the important part
// of this test because that is where the old implementation downloaded the
// whole parent into a new temporary file.
func TestArchiveVFSReaderBackedNestedZipAndTarGzip(t *testing.T) {
	ctx := context.Background()
	leaf := []byte("reader-backed nested content")
	innerZip := zipBytes(t, "leaf.txt", leaf)
	middleTarGz := tarGzipBytes(t, "inner.bin", innerZip)
	outer, outerPath := openOuterArchive(t, map[string][]byte{"middle.tar.gz": middleTarGz})

	middlePath := outer.Join(outerPath, "middle.tar.gz")
	middle, err := NewArchiveVFSContext(ctx, outer, middlePath)
	if err != nil {
		t.Fatalf("open tar.gz inside ZIP: %v", err)
	}
	requireReaderBacked(t, middle, "tar.gz inside ZIP")
	t.Cleanup(func() { _ = middle.Close() })

	innerPath := middle.Join(middlePath, "inner.bin")
	provider := &ArchiveProvider{}
	if !provider.CanOpen(ctx, middle, innerPath) {
		t.Fatalf("provider rejected ZIP content with foreign member name %q", innerPath)
	}
	inner, err := provider.Open(ctx, middle, innerPath)
	if err != nil {
		t.Fatalf("open ZIP inside tar.gz through provider: %v", err)
	}
	innerArchive, ok := inner.(*ArchiveVFS)
	if !ok {
		t.Fatalf("provider returned %T, want *ArchiveVFS", inner)
	}
	requireReaderBacked(t, innerArchive, "ZIP inside tar.gz")
	t.Cleanup(func() { _ = inner.Close() })

	got := readArchiveMember(t, innerArchive, inner.Join(innerPath, "leaf.txt"))
	if !bytes.Equal(got, leaf) {
		t.Fatalf("nested content = %q, want %q", got, leaf)
	}

	streamPath := outer.Join(outerPath, "middle.tar.gz")
	member, err := outer.Open(withStreamRead(ctx), streamPath)
	if err != nil {
		t.Fatalf("open source member through stream path: %v", err)
	}
	wrapper, ok := member.(*archiveReadWrapper)
	if !ok {
		t.Fatalf("stream source handle type = %T, want *archiveReadWrapper", member)
	}
	probe := make([]byte, 8)
	if _, err := member.ReadAt(ctx, probe, 0); err != nil {
		t.Fatalf("read source member through ReadAt: %v", err)
	}
	if got := wrapper.TempPath(); got != "" {
		t.Fatalf("reader-backed source allocated temporary path %q", got)
	}
	_ = member.Close()
}

// A compressed TAR can itself be the outer local archive. Its ZIP child must
// take the same reader-backed route, proving the implementation is not tied to
// one fixed outer/inner pair.
func TestArchiveVFSReaderBackedNestedTarGzipAndZip(t *testing.T) {
	ctx := context.Background()
	leaf := []byte("tar outer, zip inner")
	innerZip := zipBytes(t, "leaf.txt", leaf)
	outer, outerPath := openTarGzipArchive(t, tarGzipBytes(t, "inner.zip", innerZip))

	innerPath := outer.Join(outerPath, "inner.zip")
	inner, err := NewArchiveVFSContext(ctx, outer, innerPath)
	if err != nil {
		t.Fatalf("open ZIP inside tar.gz: %v", err)
	}
	requireReaderBacked(t, inner, "ZIP inside outer tar.gz")
	t.Cleanup(func() { _ = inner.Close() })

	// ZIP reads its central directory near the end first and then returns to
	// the local header at the beginning. A sequential parent member cannot
	// serve that backwards ReadAt efficiently, so the stream adapter switches
	// to one private materialization instead of replaying the TAR member for
	// every request (f4#1731).
	stream, ok := inner.fsys.(*streamArchiveFS)
	if !ok {
		t.Fatalf("nested ZIP fs = %T, want *streamArchiveFS", inner.fsys)
	}
	source, ok := stream.source.source.(*archiveReadWrapper)
	if !ok {
		t.Fatalf("nested ZIP source = %T, want *archiveReadWrapper", stream.source.source)
	}
	source.mu.Lock()
	materialized := source.extracted && source.tmpPath != ""
	source.mu.Unlock()
	if !materialized {
		t.Fatal("nested ZIP parent member was replayed instead of materialized after backward read")
	}

	got := readArchiveMember(t, inner, inner.Join(innerPath, "leaf.txt"))
	if !bytes.Equal(got, leaf) {
		t.Fatalf("nested content = %q, want %q", got, leaf)
	}
}
