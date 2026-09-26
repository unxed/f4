package archive

import (
	stdzip "archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSelectedTestPathsResolvesAgainstTheBrowsedDirectory checks that marked
// names are joined against the archive directory currently being browsed
// (v.innerPath), not against the archive root, and that a marked directory
// selects its whole subtree via archiveExtractionPathSelected -- the same
// rule copyBulkFrom applies for "extract marked only" (f4#1250).
func TestSelectedTestPathsResolvesAgainstTheBrowsedDirectory(t *testing.T) {
	v := &ArchiveVFS{innerPath: "sub"}

	selected := v.selectedTestPaths([]string{"dir", "file.txt"})
	if selected == nil {
		t.Fatal("selectedTestPaths(marks) = nil, want a non-nil restriction")
	}
	for _, want := range []string{"sub/dir", "sub/file.txt"} {
		if !selected[want] {
			t.Fatalf("selected = %v, missing %q", selected, want)
		}
	}
	if !archiveExtractionPathSelected("sub/dir/nested/file.bin", selected) {
		t.Fatal("a marked directory must select everything under it")
	}
	if archiveExtractionPathSelected("sub/other.txt", selected) {
		t.Fatal("an unmarked sibling must not be selected")
	}
	if archiveExtractionPathSelected("outside/file.txt", selected) {
		t.Fatal("a path outside the browsed directory must not be selected")
	}
}

// TestSelectedTestPathsWithNothingMarkedMeansTestEverything is the regression
// half of f4#1250: with nothing marked, actionTestArchive must fall back to
// testing the whole archive exactly as it did before this change.
func TestSelectedTestPathsWithNothingMarkedMeansTestEverything(t *testing.T) {
	v := &ArchiveVFS{}
	if selected := v.selectedTestPaths(nil); selected != nil {
		t.Fatalf("selectedTestPaths(nil) = %v, want nil (test the whole archive)", selected)
	}
	if ctx := withArchiveTestSelection(context.Background(), nil); archiveTestSelectionFromContext(ctx) != nil {
		t.Fatal("withArchiveTestSelection(ctx, nil) must not attach a restriction")
	}
}

// issue1250BuildMarkableZip writes a two-member, uncompressed (Store) zip --
// "keep.txt" and "bad.txt" -- and then corrupts only "bad.txt"'s payload
// bytes in place. Store keeps each member's raw bytes contiguous and
// unambiguous inside the file, so the corruption cannot bleed into
// "keep.txt".
func issue1250BuildMarkableZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := stdzip.NewWriter(&buf)
	for _, entry := range []struct{ name, payload string }{
		{"keep.txt", "kept payload, never touched by the corruption below"},
		{"bad.txt", "payload that gets corrupted a few lines down"},
	} {
		fw, err := w.CreateHeader(&stdzip.FileHeader{Name: entry.name, Method: stdzip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(entry.payload)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()

	marker := []byte("payload that gets corrupted a few lines down")
	offset := bytes.Index(data, marker)
	if offset < 0 {
		t.Fatal("bad.txt payload not found in the archive bytes")
	}
	corrupted := append([]byte(nil), data...)
	for i := range marker {
		corrupted[offset+i] ^= 0xFF
	}
	return corrupted
}

// TestActionTestArchiveOnlyTestsMarkedMembers is the f4#1250 coverage:
// marking specific members before testing restricts Shift-F3 to only those
// members, and marking nothing tests the whole archive, as it always has.
func TestActionTestArchiveOnlyTestsMarkedMembers(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "marked.zip")
	if err := os.WriteFile(archivePath, issue1250BuildMarkableZip(t), 0o600); err != nil {
		t.Fatal(err)
	}

	// (a) mark only the healthy member: the corrupted one is never read, so
	// the test reports no error even though the archive as a whole is
	// damaged.
	v := &ArchiveVFS{}
	selected := v.selectedTestPaths([]string{"keep.txt"})
	ctx := withArchiveTestSelection(context.Background(), selected)
	if err := testArchiveOnce(ctx, archivePath, archivePath, "", &dummyReporter{}); err != nil {
		t.Fatalf("testing only the marked, healthy member failed: %v", err)
	}

	// (b) nothing marked: the whole archive is tested and the corrupted
	// member is caught, exactly as before this change.
	if err := testArchiveOnce(context.Background(), archivePath, archivePath, "", &dummyReporter{}); err == nil {
		t.Fatal("testing the whole archive did not catch the corrupted member")
	}
}
