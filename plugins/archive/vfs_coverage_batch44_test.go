package archive

// Batch 44 covers archive formatting, progress, CRC and seek helper branches.

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/unxed/sevenzip"
)

func TestArchiveFormatSizeNegativeCoverageBatch44(t *testing.T) {
	if got := formatSize(-1); got != "?" {
		t.Fatalf("formatSize(-1) = %q, want ?", got)
	}
}

func TestArchiveFormatSizeBytesCoverageBatch44(t *testing.T) {
	if got := formatSize(512); got != "512 B" {
		t.Fatalf("formatSize(512) = %q, want 512 B", got)
	}
}

func TestArchiveFormatSizeScaledCoverageBatch44(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{bytes: 1024, want: "1.0 KB"},
		{bytes: 1024 * 1024, want: "1.0 MB"},
		{bytes: 1024 * 1024 * 1024, want: "1.0 GB"},
	} {
		if got := formatSize(tc.bytes); got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestJoinArchiveCloseErrorReturnsCloseErrorWithoutPrimaryCoverageBatch44(t *testing.T) {
	closeErr := errors.New("close")
	if got := joinArchiveCloseError(nil, closeErr); got != closeErr {
		t.Fatalf("joinArchiveCloseError(nil, close) = %v, want close error", got)
	}
}

func TestJoinArchiveCloseErrorReturnsPrimaryWithoutCloseCoverageBatch44(t *testing.T) {
	primary := errors.New("primary")
	if got := joinArchiveCloseError(primary, nil); got != primary {
		t.Fatalf("joinArchiveCloseError(primary, nil) = %v, want primary error", got)
	}
}

func TestJoinArchiveCloseErrorJoinsBothErrorsCoverageBatch44(t *testing.T) {
	primary := errors.New("primary")
	closeErr := errors.New("close")
	got := joinArchiveCloseError(primary, closeErr)
	if !errors.Is(got, primary) || !errors.Is(got, closeErr) {
		t.Fatalf("joined error %v does not contain both causes", got)
	}
}

func TestArchiveProgressPercentBoundsCoverageBatch44(t *testing.T) {
	for _, tc := range []struct {
		copied, total int64
		want          int
	}{
		{copied: 0, total: 10, want: 0},
		{copied: 5, total: 10, want: 50},
		{copied: 20, total: 10, want: 100},
		{copied: 5, total: 0, want: 0},
	} {
		if got := archiveProgressPercent(tc.copied, tc.total); got != tc.want {
			t.Errorf("archiveProgressPercent(%d, %d) = %d, want %d", tc.copied, tc.total, got, tc.want)
		}
	}
}

type archiveCoverageFileInfo struct{ sys any }

func (i archiveCoverageFileInfo) Name() string       { return "member" }
func (i archiveCoverageFileInfo) Size() int64        { return 0 }
func (i archiveCoverageFileInfo) Mode() fs.FileMode  { return 0 }
func (i archiveCoverageFileInfo) ModTime() time.Time { return time.Time{} }
func (i archiveCoverageFileInfo) IsDir() bool        { return false }
func (i archiveCoverageFileInfo) Sys() any           { return i.sys }

func TestArchiveFileCRCNilAndUnknownCoverageBatch44(t *testing.T) {
	if got, ok := archiveFileCRC(nil); ok || got != 0 {
		t.Fatalf("archiveFileCRC(nil) = (%d, %v), want (0, false)", got, ok)
	}
	if got, ok := archiveFileCRC(archiveCoverageFileInfo{sys: "not a header"}); ok || got != 0 {
		t.Fatalf("archiveFileCRC(unknown) = (%d, %v), want (0, false)", got, ok)
	}
}

func TestArchiveFileCRCReadsSevenZipHeadersCoverageBatch44(t *testing.T) {
	const want = uint32(0x12345678)
	if got, ok := archiveFileCRC(archiveCoverageFileInfo{sys: &sevenzip.FileHeader{CRC32: want}}); !ok || got != want {
		t.Fatalf("archiveFileCRC(pointer header) = (%#x, %v), want (%#x, true)", got, ok, want)
	}
	if got, ok := archiveFileCRC(archiveCoverageFileInfo{sys: sevenzip.FileHeader{CRC32: want}}); !ok || got != want {
		t.Fatalf("archiveFileCRC(value header) = (%#x, %v), want (%#x, true)", got, ok, want)
	}
}

type archiveCoverageSeekFile struct{ *bytes.Reader }

func (f archiveCoverageSeekFile) Stat() (fs.FileInfo, error) { return nil, nil }
func (f archiveCoverageSeekFile) Close() error               { return nil }

type archiveCoverageReadFile struct {
	data []byte
	pos  int
}

func (f *archiveCoverageReadFile) Read(p []byte) (int, error) {
	if f.pos == len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *archiveCoverageReadFile) Stat() (fs.FileInfo, error) { return nil, nil }
func (f *archiveCoverageReadFile) Close() error               { return nil }

func TestSeekArchiveFileUsesSeekAndReadFallbackCoverageBatch44(t *testing.T) {
	seekFile := archiveCoverageSeekFile{bytes.NewReader([]byte("abcdef"))}
	if err := seekArchiveFile(seekFile, 3); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := seekFile.Read(buf); err != nil || string(buf) != "d" {
		t.Fatalf("seeker after offset read = %q, %v; want d", buf, err)
	}

	readFile := &archiveCoverageReadFile{data: []byte("abcdef")}
	if err := seekArchiveFile(readFile, 3); err != nil {
		t.Fatal(err)
	}
	if readFile.pos != 3 {
		t.Fatalf("reader fallback position = %d, want 3", readFile.pos)
	}
	if err := seekArchiveFile(readFile, 10); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader fallback past EOF = %v, want unexpected EOF", err)
	}
}
