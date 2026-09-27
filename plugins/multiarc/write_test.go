package multiarc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChunkNamesKeepsOrderAndLimits(t *testing.T) {
	if got := chunkNames(nil); len(got) != 0 {
		t.Fatalf("chunkNames(nil) = %v, want no chunks", got)
	}
	names := make([]string, maxChunkNames+3)
	for i := range names {
		names[i] = "n"
	}
	chunks := chunkNames(names)
	if len(chunks) != 2 || len(chunks[0]) != maxChunkNames || len(chunks[1]) != 3 {
		t.Fatalf("chunk sizes = %d chunks, want %d then 3", len(chunks), maxChunkNames)
	}

	long := strings.Repeat("x", maxChunkBytes/2)
	chunks = chunkNames([]string{long, long, "a"})
	if len(chunks) != 2 || !reflect.DeepEqual(chunks[1], []string{long, "a"}) {
		t.Fatalf("byte-limited chunks = %d, want the second long name to start a new chunk", len(chunks))
	}

	// A single name longer than the limit still goes out, alone: refusing it
	// here would only trade the tool's own error for a vaguer one.
	huge := strings.Repeat("y", maxChunkBytes+1)
	chunks = chunkNames([]string{huge, "b"})
	if len(chunks) != 2 || chunks[0][0] != huge {
		t.Fatalf("oversized name chunks = %v", len(chunks))
	}
}

func TestCoveringNames(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"directory entry covers its members", []string{"dir/", "dir/a", "dir/sub/", "dir/sub/b", "top"}, []string{"dir/", "top"}},
		{"no directory entry keeps each member", []string{"syn/a", "syn/b"}, []string{"syn/a", "syn/b"}},
		{"dot-slash names cover their own kind only", []string{"./dir/", "./dir/a", "dir/b"}, []string{"./dir/", "dir/b"}},
		{"a sibling with a longer name is not a child", []string{"a", "a-b", "a/x", "ab"}, []string{"a", "a-b", "ab"}},
		{"duplicates collapse", []string{"f", "f"}, []string{"f"}},
		{"unsorted input", []string{"z/1", "z/", "a"}, []string{"a", "z/"}},
	}
	for _, c := range cases {
		if got := coveringNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: coveringNames(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestHasWildcard(t *testing.T) {
	if hasWildcard([]string{"plain.txt", "dir/[x].txt"}) {
		t.Error("brackets are not a 7-Zip wildcard")
	}
	if !hasWildcard([]string{"plain.txt", "w*ld"}) || !hasWildcard([]string{"q?"}) {
		t.Error("* and ? are 7-Zip wildcards")
	}
}

func TestRewriteArchiveReplacesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.tar")
	if err := os.WriteFile(arc, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// What the file system made of 0600: Windows keeps only a read-only bit.
	before, err := os.Stat(arc)
	if err != nil {
		t.Fatal(err)
	}
	var workDir string
	err = rewriteArchive(arc, "copy.tar", func(wd, copyName string) (string, error) {
		workDir = wd
		if filepath.Dir(wd) != dir {
			t.Errorf("work dir %s is not next to the archive", wd)
		}
		got, err := os.ReadFile(filepath.Join(wd, copyName))
		if err != nil || string(got) != "old" {
			t.Errorf("copy = (%q, %v), want the original's content", got, err)
		}
		return "new.tar", os.WriteFile(filepath.Join(wd, "new.tar"), []byte("new"), 0o644) // #nosec G306 -- the mode rewriteArchive must reset.
	})
	if err != nil {
		t.Fatalf("rewriteArchive: %v", err)
	}
	got, err := os.ReadFile(arc)
	if err != nil || string(got) != "new" {
		t.Fatalf("archive = (%q, %v), want new", got, err)
	}
	if info, err := os.Stat(arc); err != nil || info.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("archive mode = %v (%v), want the original %v", info.Mode().Perm(), err, before.Mode().Perm())
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("work dir %s was left behind", workDir)
	}
}

func TestRewriteArchiveFailureLeavesOriginal(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.tar")
	if err := os.WriteFile(arc, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	editErr := errors.New("tool failed")
	err := rewriteArchive(arc, "copy.tar", func(wd, copyName string) (string, error) {
		// A tool that got half-way before failing.
		return "", errors.Join(editErr, os.WriteFile(filepath.Join(wd, copyName), []byte("half"), 0o600))
	})
	if !errors.Is(err, editErr) {
		t.Fatalf("rewriteArchive error = %v, want %v", err, editErr)
	}
	if got, _ := os.ReadFile(arc); string(got) != "old" {
		t.Fatalf("archive = %q after a failed edit, want it untouched", got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".f4-multiarc-*"))
	if len(leftovers) != 0 {
		t.Fatalf("work dirs left behind: %v", leftovers)
	}
}
