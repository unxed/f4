package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/zipper/archive"
)

// f4 #1243: text packed into a zip came out barely smaller than it went in.
// unxed/zip switched to Huffman coding alone, without the search for repeats,
// for any file with few distinct byte values, which is every ordinary text and
// was meant for the fastest level only. Prose that repeats itself has to shrink
// to a fraction of its size; the bug left it at about 60% and more.
func TestIssue1243PackedTextIsCompressed(t *testing.T) {
	const paragraph = "The panels show the folders side by side, and the command line waits below them. " +
		"Press F5 to copy the selected files to the other panel, F6 to move them and F8 to delete them. " +
		"A search opened with Alt+F7 walks the folder tree and lists what it found, and the list can be " +
		"opened as a panel of its own. Archives are folders as well: Enter goes into one, F5 copies out of it.\n"
	var text strings.Builder
	for i := 0; text.Len() < 120*1024; i++ {
		fmt.Fprintf(&text, "Section %d.\n%s\n", i, paragraph)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "guide.txt")
	// #nosec G306 G703 -- the file is inside the private test temp directory.
	if err := os.WriteFile(src, []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "guide.zip")
	a, err := archive.NewArchiver(zipPath, dir, archive.Options{Xattrs: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(context.Background(), map[string]os.FileInfo{src: info}); err != nil {
		_ = a.Close()
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	packed, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if percent := packed.Size() * 100 / int64(text.Len()); percent > 30 {
		t.Fatalf("%d bytes of repeating text packed into %d bytes (%d%%): the archiver is not looking for repeats (#1243)",
			text.Len(), packed.Size(), percent)
	}
}
