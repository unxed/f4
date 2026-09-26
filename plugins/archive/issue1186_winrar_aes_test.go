package archive

import (
	"context"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/zip"
)

// Issue #1186, item 4: a WinRAR 7.23 AES-256 self-extracting zip rejected its
// own, correct password ("zip: AES info missing"). issue1186_sfx_zip_test.go
// covers the structural fix -- a self-extracting archive is now handed to
// the zip reader whole rather than copied out from under its stub, which
// used to invalidate every offset the copy did not also shift -- but only
// against a plain, unencrypted zip this package's own writer built, with a
// synthetic stub and hand-shifted offsets. It never exercised WinRAR's own
// AES layout, which is what actually broke for the reporter.
//
// testdata/issue1186_winrar_aes_sfx.exe carries that layout for real: it is
// tarlabnor's attached archive (WinRAR 7.23, AES-256, password "123", 11
// members) with its ~487 KiB WinRAR stub replaced by a 32-byte placeholder
// and every offset the central directory and end record store shifted by the
// same constant to match. Every entry's local and central-directory headers,
// extra fields (WinZip AES 0x9901 as AE-1, and the NTFS timestamp field
// WinRAR also wrote), and AES-256 ciphertext plus authentication code are
// WinRAR's own bytes, untouched; 7-Zip extracts this fixture and the original
// archive to byte-identical output. If the offset fix ever regresses, or a
// future change mishandles WinRAR's specific AE-1/method-99 combination, this
// is the archive that catches it.
func TestIssue1186WinRARAesSFXPassword(t *testing.T) {
	fixture, err := os.ReadFile("testdata/issue1186_winrar_aes_sfx.exe")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	path := filepath.Join(root, "setup.exe")
	if err := os.WriteFile(path, fixture, 0o600); err != nil { // #nosec G703 -- path is inside the per-test directory created by testing.T.TempDir.
		t.Fatal(err)
	}
	ctx := context.Background()
	const password = "123"

	// The archive is handed over whole: same path, no temporary copy.
	embedded, backingPath, closer, err := materializeLocalSFX(path)
	if err != nil {
		t.Fatalf("materializeLocalSFX: %v", err)
	}
	if closer != nil {
		_ = closer.Close()
	}
	if embedded.format != "zip" || backingPath != path || closer != nil {
		t.Fatalf("materializeLocalSFX = %q, %s, closer %v; want the file itself, read as zip",
			embedded.format, filepath.Base(backingPath), closer != nil)
	}

	// The password verifies and every member's AES info parses -- the two
	// things "zip: AES info missing" and a rejected password stood for.
	zr, err := zip.OpenReaderWithPassword(path, password)
	if err != nil {
		t.Fatalf("open with password: %v", err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) != 11 {
		t.Fatalf("got %d entries, want 11", len(zr.File))
	}

	// The panel's own directory listing goes through the same materialization.
	// A zip's central directory names its members in the clear, so listing it
	// does not by itself need the password (see password_test.go for the case
	// where the archive backend does need it up front, e.g. .7z); the mock
	// below is only a safety net against this ever changing and the panel
	// prompting interactively in a non-interactive test run.
	previousPrompt := archivePasswordPrompt
	archivePasswordPrompt = func(context.Context, string) (string, error) { return password, nil }
	defer func() { archivePasswordPrompt = previousPrompt }()

	archiveVFS, err := NewArchiveVFS(vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	defer func() { _ = archiveVFS.Close() }()
	var items []vfs.VFSItem
	if err := archiveVFS.ReadDir(ctx, archiveVFS.GetPath(), func(chunk []vfs.VFSItem) { items = append(items, chunk...) }); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 11 {
		t.Fatalf("listed %d entries, want 11", len(items))
	}

	dest := t.TempDir()
	if err := extractArchiveOnce(ctx, path, dest, password, &issue915ProgressRecorder{}); err != nil {
		t.Fatalf("extract: %v", err)
	}

	extracted := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		got, err := os.ReadFile(filepath.Join(dest, f.Name)) // #nosec G703 -- f.Name comes from the committed fixture archive, not untrusted input, and dest is the per-test directory created by testing.T.TempDir.
		if err != nil {
			t.Fatalf("read extracted %s: %v", f.Name, err)
		}
		if got32 := crc32.ChecksumIEEE(got); got32 != f.CRC32 {
			t.Fatalf("%s: crc32 %08x, want %08x", f.Name, got32, f.CRC32)
		}
		extracted++
	}
	if extracted != 11 {
		t.Fatalf("verified %d extracted files, want 11", extracted)
	}

	if err := testArchiveOnce(ctx, path, path, password, &issue915ProgressRecorder{}); err != nil {
		t.Fatalf("test: %v", err)
	}
}
