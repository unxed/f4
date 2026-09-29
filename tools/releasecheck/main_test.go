package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func targz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A release directory holding only the Linux and macOS amd64 archives fails
// on every other platform, and names the plugin archive older builds would
// install in f4's place. What it does hold is unpacked, and refused because
// the "f4" in it is not a Go build for its platform.
func TestRunReportsWhatInstalledBuildsWouldMisread(t *testing.T) {
	dir := t.TempDir()
	for name, entry := range map[string]string{
		"f4-linux-amd64.tar.gz":              "f4",
		"f4-darwin-amd64.tar.gz":             "f4",
		"android-plugin-darwin-amd64.tar.gz": "android-plugin",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), targz(t, entry, "x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if got := run([]string{dir}, &out); got != 1 {
		t.Fatalf("run() = %d, want 1\n%s", got, out.String())
	}
	for _, want := range []string{
		"::error::no archive of f4 for windows/amd64",
		"on darwin/amd64 would install android-plugin-darwin-amd64.tar.gz",
		"::error::f4-linux-amd64.tar.gz: f4 carries no Go build information",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRunRefusesAnArchiveWithoutTheExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f4-linux-amd64.tar.gz"), targz(t, "README", "no f4"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	run([]string{dir}, &out)
	if !strings.Contains(out.String(), "::error::f4-linux-amd64.tar.gz: the update archive did not replace") {
		t.Errorf("an archive without f4 passed:\n%s", out.String())
	}
}
