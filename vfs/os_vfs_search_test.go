package vfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOSVFSFindFilesOptions(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"alpha.txt":        "Needle here\n",
		"needles.txt":      "needles only\n",
		"notes.log":        "another file\n",
		"nested/deep.txt":  "needle below\n",
		"nested/empty.dat": "",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	v := NewOSVFS(root)
	ctx := context.Background()

	hits, err := v.FindFiles(ctx, root, FindQuery{
		Masks:      []string{"*.txt"},
		Text:       "NEEDLE",
		IgnoreCase: true,
	})
	if err != nil {
		t.Fatalf("basic search: %v", err)
	}
	if got := len(hits); got != 3 {
		t.Fatalf("basic search found %d files, want 3: %+v", got, hits)
	}

	hits, err = v.FindFiles(ctx, root, FindQuery{
		Masks:      []string{"*.txt"},
		Text:       "needle",
		IgnoreCase: true,
		WholeWords: true,
	})
	if err != nil {
		t.Fatalf("whole-word search: %v", err)
	}
	if got := len(hits); got != 2 {
		t.Fatalf("whole-word search found %d files, want 2: %+v", got, hits)
	}

	hits, err = v.FindFiles(ctx, root, FindQuery{
		Masks:      []string{"*.txt"},
		Text:       `needle\s+(here|below)`,
		Regex:      true,
		IgnoreCase: true,
	})
	if err != nil {
		t.Fatalf("regexp search: %v", err)
	}
	if got := len(hits); got != 2 {
		t.Fatalf("regexp search found %d files, want 2: %+v", got, hits)
	}

	hits, err = v.FindFiles(ctx, root, FindQuery{
		Masks:         []string{"*.txt"},
		Text:          "needle",
		IgnoreCase:    true,
		NotContaining: true,
	})
	if err != nil {
		t.Fatalf("negative search: %v", err)
	}
	if got := len(hits); got != 0 {
		t.Fatalf("negative search found %d files, want 0: %+v", got, hits)
	}

	hits, err = v.FindFiles(ctx, root, FindQuery{Masks: []string{"nested"}, FindFolders: true})
	if err != nil {
		t.Fatalf("folder search: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Name != "nested" || !hits[0].Item.IsDir {
		t.Fatalf("folder search returned %+v, want nested directory", hits)
	}

	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join(root, "alpha.txt"), link); err == nil {
		hits, err = v.FindFiles(ctx, root, FindQuery{Masks: []string{"link*"}})
		if err != nil {
			t.Fatalf("default symlink search: %v", err)
		}
		if len(hits) != 0 {
			t.Fatalf("default search followed symlink: %+v", hits)
		}
		hits, err = v.FindFiles(ctx, root, FindQuery{Masks: []string{"link*"}, FindSymlinks: true})
		if err != nil {
			t.Fatalf("symlink search: %v", err)
		}
		if len(hits) != 1 || !hits[0].Item.IsSymlink {
			t.Fatalf("symlink search returned %+v, want one symlink", hits)
		}
	}
}
func TestFindQueryMatcherOptions(t *testing.T) {
	cases := []struct {
		name  string
		data  string
		query FindQuery
		want  bool
	}{
		{name: "folded literal", data: "Needle", query: FindQuery{Text: "needle", IgnoreCase: true}, want: true},
		{name: "case sensitive miss", data: "Needle", query: FindQuery{Text: "needle"}, want: false},
		{name: "regexp", data: "file-42", query: FindQuery{Text: `file-[0-9]+`, Regex: true}, want: true},
		{name: "regexp folded", data: "FILE-42", query: FindQuery{Text: `file-[0-9]+`, Regex: true, IgnoreCase: true}, want: true},
		{name: "regexp whole word miss", data: "prefile-42x", query: FindQuery{Text: `file-[0-9]+`, Regex: true, WholeWords: true}, want: false},
		// A multibyte neighbour has to be decoded as one rune; reading only
		// its first byte would make "иголками" look like a whole word.
		{name: "cyrillic whole word hit", data: "вот иголка тут", query: FindQuery{Text: "ИГОЛКА", IgnoreCase: true, WholeWords: true}, want: true},
		{name: "cyrillic whole word miss", data: "иголками", query: FindQuery{Text: "иголка", IgnoreCase: true, WholeWords: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := newFindQueryMatcher(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := matcher.hasMatch([]byte(tc.data)); got != tc.want {
				t.Fatalf("hasMatch(%q, %q) = %v, want %v", tc.data, tc.query.Text, got, tc.want)
			}
		})
	}
}

func TestFindQueryMatcherRejectsInvalidRegexp(t *testing.T) {
	if _, err := newFindQueryMatcher(FindQuery{Text: "[", Regex: true}); err == nil {
		t.Fatal("invalid regexp was accepted")
	}
}
