//go:build !windows

package vfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

// rawSyscallCreate creates an empty file at path using the raw open(2)
// syscall directly, bypassing os.Create/os.OpenFile. Go's os package does
// not itself reject invalid UTF-8 on Unix, but the point of going around it
// here is to pin the test to the actual boundary this feature is about: the
// bytes the kernel's directory-entry syscalls hand back, with nothing of
// f4's or the os package's own making in between.
func rawSyscallCreate(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("raw syscall.Open(%q) failed: %v", path, err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("closing raw fd for %q: %v", path, err)
	}
}

// TestPUA_DecodeEncodeRoundTrip pins decodeUTF8OrMap/encodeMappedString
// against the same contract github.com/unxed/zip and github.com/unxed/tar
// already implement (see their pua.go / sqlite.go): a non-UTF-8 byte string
// maps to a MappedStringMark-prefixed, valid-UTF-8 string using one
// private-use rune (0xE000-0xE0FF) per byte, and decodes back to the exact
// original bytes.
func TestPUA_DecodeEncodeRoundTrip(t *testing.T) {
	raw := []byte("bad_\xff\xfe\x80_name.txt")
	if utf8.Valid(raw) {
		t.Fatal("test fixture must not itself be valid UTF-8")
	}

	mapped := decodeUTF8OrMap(raw)
	if !utf8.ValidString(mapped) {
		t.Fatalf("mapped string is not valid UTF-8: %q", mapped)
	}
	if !strings.HasPrefix(mapped, MappedStringMarkStr) {
		t.Fatalf("mapped string missing MappedStringMark prefix: %q", mapped)
	}
	if got := encodeMappedString(mapped); string(got) != string(raw) {
		t.Fatalf("encodeMappedString round trip = %q, want %q", got, raw)
	}

	// A name that is already valid UTF-8 must be left alone byte for byte.
	plain := []byte("обычное_имя.txt")
	if !utf8.Valid(plain) {
		t.Fatal("regression fixture must itself be valid UTF-8")
	}
	if got := decodeUTF8OrMap(plain); got != string(plain) {
		t.Fatalf("decodeUTF8OrMap altered a valid UTF-8 name: got %q, want %q", got, plain)
	}
	if got := encodeMappedString(string(plain)); string(got) != string(plain) {
		t.Fatalf("encodeMappedString altered an unmapped name: got %q, want %q", got, plain)
	}
}

// TestPUA_DecodeMappedPathSegments checks that only the path component that
// actually carries the mark gets decoded, and every other component
// (including an ordinary one that happens to look like it starts with a
// private-use rune) survives untouched.
func TestPUA_DecodeMappedPathSegments(t *testing.T) {
	rawLeaf := []byte("leaf_\xfe_name")
	mappedLeaf := decodeUTF8OrMap(rawLeaf)

	p := "/home/user/projects/" + mappedLeaf
	want := "/home/user/projects/" + string(rawLeaf)
	if got := decodeMappedPathSegments(p); got != want {
		t.Fatalf("decodeMappedPathSegments = %q, want %q", got, want)
	}

	// No mark anywhere: unchanged, including a path that is entirely
	// ordinary text.
	plain := "/home/user/projects/notes.txt"
	if got := decodeMappedPathSegments(plain); got != plain {
		t.Fatalf("decodeMappedPathSegments altered a plain path: got %q, want %q", got, plain)
	}
}

// TestPUA_DisplayName checks that DisplayName hands the screen the exact
// original bytes back (so vtui's own existing "?"-for-unrenderable-byte
// rendering applies, unchanged, instead of a row of private-use tofu), and
// that a name which was never mapped is returned unchanged.
func TestPUA_DisplayName(t *testing.T) {
	raw := "bad_\xff\xfe_name.txt"
	mapped := decodeUTF8OrMap([]byte(raw))
	if got := DisplayName(mapped); got != raw {
		t.Fatalf("DisplayName(%q) = %q, want the original bytes %q", mapped, got, raw)
	}
	if got := DisplayName("plain_name.txt"); got != "plain_name.txt" {
		t.Fatalf("DisplayName altered a plain name: %q", got)
	}
}

// TestOSVFS_NonUTF8Name_RoundTripsThroughListingAndOps is the end-to-end
// check f4#250 asks for: a file whose name is not valid UTF-8 (created via
// a raw syscall, see rawSyscallCreate) is listed through OSVFS.ReadDir,
// comes back as a valid-UTF-8, MappedStringMark-prefixed name, and every
// OSVFS operation that takes that name back (Stat, Open, Rename, Remove)
// resolves to the exact same on-disk file, proving the mapping is lossless
// in both directions rather than merely not crashing.
func TestOSVFS_NonUTF8Name_RoundTripsThroughListingAndOps(t *testing.T) {
	tmpDir := t.TempDir()
	rawName := "bad_utf8_\xff\xfe\x80_name.txt"
	if utf8.ValidString(rawName) {
		t.Fatal("test fixture must not itself be valid UTF-8")
	}
	rawSyscallCreate(t, filepath.Join(tmpDir, rawName))

	v := NewOSVFS(tmpDir)

	var listed []VFSItem
	if err := v.ReadDir(context.Background(), tmpDir, func(items []VFSItem) {
		listed = append(listed, items...)
	}); err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected exactly one entry, got %d: %+v", len(listed), listed)
	}
	mapped := listed[0].Name

	if !utf8.ValidString(mapped) {
		t.Fatalf("listed name is not valid UTF-8: %q", mapped)
	}
	if !strings.HasPrefix(mapped, MappedStringMarkStr) {
		t.Fatalf("listed name missing MappedStringMark prefix: %q", mapped)
	}
	if got := string(encodeMappedString(mapped)); got != rawName {
		t.Fatalf("mapped name does not decode back to the original bytes: got %q, want %q", got, rawName)
	}

	mappedPath := v.Join(tmpDir, mapped)

	// Stat must resolve the mapped path back to the real file.
	st, err := v.Stat(context.Background(), mappedPath)
	if err != nil {
		t.Fatalf("Stat(%q) failed: %v", mappedPath, err)
	}
	if st.Name != mapped {
		t.Fatalf("Stat name = %q, want %q", st.Name, mapped)
	}

	// Open must resolve the mapped path back to the real file.
	rc, err := v.Open(context.Background(), mappedPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", mappedPath, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("closing opened file: %v", err)
	}

	// Rename via the mapped path, then confirm on disk (bypassing the VFS)
	// that the raw-named file is actually gone and the new plain name
	// exists -- proving Rename decoded the mapped path back to the exact
	// original bytes rather than, say, creating a literal PUA-named file.
	renamedPath := v.Join(tmpDir, "renamed-ok.txt")
	if err := v.Rename(context.Background(), mappedPath, renamedPath); err != nil {
		t.Fatalf("Rename(%q) failed: %v", mappedPath, err)
	}
	if _, err := os.Lstat(filepath.Join(tmpDir, rawName)); err == nil {
		t.Fatalf("raw-named file still exists on disk after Rename")
	}
	if _, err := os.Lstat(filepath.Join(tmpDir, "renamed-ok.txt")); err != nil {
		t.Fatalf("renamed file missing on disk: %v", err)
	}

	// Recreate the raw-named file and confirm Remove also decodes correctly.
	rawSyscallCreate(t, filepath.Join(tmpDir, rawName))
	listed = nil
	if err := v.ReadDir(context.Background(), tmpDir, func(items []VFSItem) {
		for _, it := range items {
			if it.Name != "renamed-ok.txt" {
				listed = append(listed, it)
			}
		}
	}); err != nil {
		t.Fatalf("second ReadDir failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected exactly one re-created entry, got %d: %+v", len(listed), listed)
	}
	if err := v.Remove(context.Background(), v.Join(tmpDir, listed[0].Name)); err != nil {
		t.Fatalf("Remove(%q) failed: %v", listed[0].Name, err)
	}
	if _, err := os.Lstat(filepath.Join(tmpDir, rawName)); err == nil {
		t.Fatalf("raw-named file still exists on disk after Remove")
	}
}

// TestOSVFS_ValidUTF8Name_UnaffectedByMapping is the regression half of the
// contract: an ordinary, already-valid-UTF-8 name (including non-ASCII
// text) must come back from ReadDir completely unchanged and keep working
// through Open exactly as before this mapping existed.
func TestOSVFS_ValidUTF8Name_UnaffectedByMapping(t *testing.T) {
	tmpDir := t.TempDir()
	name := "обычное имя.txt"
	if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v := NewOSVFS(tmpDir)
	var listed []VFSItem
	if err := v.ReadDir(context.Background(), tmpDir, func(items []VFSItem) {
		listed = append(listed, items...)
	}); err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("valid UTF-8 name was altered: got %+v, want %q", listed, name)
	}

	rc, err := v.Open(context.Background(), v.Join(tmpDir, listed[0].Name))
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", listed[0].Name, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("closing opened file: %v", err)
	}
}
