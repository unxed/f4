package unpack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A release file takes the time of its archive member and not the time of the
// update (f4#1817): the executable shows the day it was built.
func TestExtract_FilesTakeTheArchiveMemberTime(t *testing.T) {
	built := time.Date(2026, 10, 8, 16, 30, 0, 0, time.UTC)

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "f4/f4.exe", Method: zip.Deflate, Modified: built})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var tgzBuf bytes.Buffer
	gw := gzip.NewWriter(&tgzBuf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "f4/f4", Size: 6, Mode: 0o755, ModTime: built}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		kind, file string
		data       []byte
	}{
		{"zip", "f4.exe", zipBuf.Bytes()},
		{"targz", "f4", tgzBuf.Bytes()},
	}
	for _, c := range cases {
		dest := t.TempDir()
		if err := Extract(c.data, c.kind, dest, 2); err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		info, err := os.Stat(filepath.Join(dest, c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if got := info.ModTime().UTC().Truncate(time.Second); !got.Equal(built) {
			t.Errorf("%s: modification time = %v, want the archive's %v", c.kind, got, built)
		}
	}
}

func TestApplyArchiveTimes_ZeroTimeLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	applyArchiveTimes(path, time.Time{})
	info, _ := os.Stat(path)
	if !info.ModTime().Equal(old) {
		t.Errorf("a zero archive time changed the file's time to %v", info.ModTime())
	}
}
