package findfile

import (
	"context"
	"github.com/unxed/f4/vfs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitFindMasks(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantIncludes []string
		wantExcludes []string
		wantErr      bool
	}{
		{name: "ordinary masks", input: " *.go, *.txt ", wantIncludes: []string{"*.go", "*.txt"}},
		{name: "included and excluded", input: "*.txt | .git, skip.txt", wantIncludes: []string{"*.txt"}, wantExcludes: []string{".git", "skip.txt"}},
		{name: "empty include defaults to all", input: " | .git", wantIncludes: []string{"*"}, wantExcludes: []string{".git"}},
		{name: "far star dot star", input: "*.* | *.tmp", wantIncludes: []string{"*"}, wantExcludes: []string{"*.tmp"}},
		{name: "multiple separators", input: "*.txt | *.zip | *.zip", wantErr: true},
		{name: "empty exclusions", input: "*.txt | ", wantErr: true},
		{name: "separator without masks", input: "|", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			includes, excludes, err := splitFindMasks(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitFindMasks(%q) accepted invalid syntax", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitFindMasks(%q) error = %v", tc.input, err)
			}
			if strings.Join(includes, ",") != strings.Join(tc.wantIncludes, ",") {
				t.Fatalf("includes = %#v, want %#v", includes, tc.wantIncludes)
			}
			if strings.Join(excludes, ",") != strings.Join(tc.wantExcludes, ",") {
				t.Fatalf("excludes = %#v, want %#v", excludes, tc.wantExcludes)
			}
		})
	}
}
func TestFindFileMaskMatches(t *testing.T) {
	if !findFileMaskMatches(".git", []string{"*.git", ".git"}, false) {
		t.Fatal("an excluded directory mask did not match")
	}
	if findFileMaskMatches("keep.txt", []string{".git", "skip.txt"}, false) {
		t.Fatal("an unrelated name matched an excluded mask")
	}
	if !findFileMaskMatches("REPORT.TXT", []string{"*.txt"}, true) {
		t.Fatal("case-insensitive file mask did not match")
	}
	if findFileMaskMatches("REPORT.TXT", []string{"*.txt"}, false) {
		t.Fatal("case-sensitive file mask matched unexpectedly")
	}
}

func TestFileContainsText_ChunkOverlap(t *testing.T) {
	// The word "SECRETPASSWORD" is 14 bytes long.
	// If chunk boundary splits it "SECRET" | "PASSWORD", we must still find it.

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "overlap.txt")

	// We will create a file just large enough to force our internal 128KB buffer to loop,
	// or we can test the function directly by overriding its internal buffer size logic
	// if it was exposed. Since it's hardcoded to 128KB, we write 128KB of padding,
	// then the secret word crossing the boundary.

	padding := make([]byte, 128*1024-6) // Leaves 6 bytes at the end of the first chunk
	for i := range padding {
		padding[i] = 'A'
	}

	data := append(padding, []byte("SECRETPASSWORD")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)

	// Test 1: Should find it
	found := fileContainsText(context.Background(), v, path, "secretpassword")
	if !found {
		t.Error("fileContainsText failed to find string crossing chunk boundary")
	}

	// Test 2: Should not find non-existent string
	foundMissing := fileContainsText(context.Background(), v, path, "missingpassword")
	if foundMissing {
		t.Error("fileContainsText falsely reported finding a non-existent string")
	}
}

func TestFindTextMatcherOptions(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		pattern string
		options Options
		want    bool
	}{
		{name: "case insensitive", data: "Needle", pattern: "needle", want: true},
		{name: "case sensitive miss", data: "Needle", pattern: "needle", options: Options{CaseSensitive: true}, want: false},
		{name: "whole word miss", data: "needles", pattern: "needle", options: Options{WholeWords: true}, want: false},
		{name: "whole word hit", data: "a needle here", pattern: "needle", options: Options{WholeWords: true}, want: true},
		{name: "regexp", data: "file-42", pattern: `file-[0-9]+`, options: Options{Regex: true, CaseSensitive: true}, want: true},
		{name: "regexp folded", data: "FILE-42", pattern: `file-[0-9]+`, options: Options{Regex: true}, want: true},
		{name: "regexp case sensitive miss", data: "FILE-42", pattern: `file-[0-9]+`, options: Options{Regex: true, CaseSensitive: true}, want: false},
		{name: "regexp whole word hit", data: "a file-42 here", pattern: `file-[0-9]+`, options: Options{Regex: true, CaseSensitive: true, WholeWords: true}, want: true},
		{name: "regexp whole word miss", data: "prefile-42x", pattern: `file-[0-9]+`, options: Options{Regex: true, CaseSensitive: true, WholeWords: true}, want: false},
		// Whole-word boundaries are decided per rune, so a multibyte
		// neighbour must be read as one letter and not as its first byte.
		{name: "cyrillic whole word hit", data: "вот иголка тут", pattern: "ИГОЛКА", options: Options{WholeWords: true}, want: true},
		{name: "cyrillic whole word miss", data: "иголками", pattern: "иголка", options: Options{WholeWords: true}, want: false},
		{name: "cyrillic regexp whole word miss", data: "иголками", pattern: "игол[кс]а", options: Options{Regex: true, WholeWords: true}, want: false},
		{name: "not containing", data: "nothing here", pattern: "needle", options: Options{NotContaining: true}, want: true},
		{name: "not containing miss", data: "needle here", pattern: "needle", options: Options{NotContaining: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := newFindTextMatcher(tc.pattern, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if got := matcher.matches([]byte(tc.data)); got != tc.want {
				t.Fatalf("matches(%q, %q) = %v, want %v", tc.data, tc.pattern, got, tc.want)
			}
		})
	}
}

func TestFindTextMatcherRejectsInvalidRegexp(t *testing.T) {
	if _, err := newFindTextMatcher("[", Options{Regex: true}); err == nil {
		t.Fatal("invalid regexp was accepted")
	}
}
