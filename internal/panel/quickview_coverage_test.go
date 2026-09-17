package panel

import (
	"errors"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestQuickViewPreviewAndLayoutContracts(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	if got := quickViewTextLines([]byte("one\ntwo\n")); len(got) != 2 || got[1] != "two" {
		t.Fatalf("quickViewTextLines trailing newline = %#v", got)
	}
	if got := quickViewTextLines([]byte("one\n\n")); len(got) != 2 || got[1] != "" {
		t.Fatalf("quickViewTextLines empty final line = %#v", got)
	}
	if got := quickViewTextLines(nil); len(got) != 0 {
		t.Fatalf("quickViewTextLines(nil) = %#v, want empty", got)
	}

	q := &QuickViewPanel{cacheRaw: []byte("plain text\n"), cacheCodepage: 65001}
	if !q.applyPreviewCodepage(65001, true) || q.cacheAutoDetect != true || q.cacheBinary {
		t.Fatalf("text codepage application failed: %#v", q)
	}
	if q.applyPreviewCodepage(999999, false) || q.cacheReadErr == nil {
		t.Fatal("unknown codepage should be reported")
	}
	q.hexMode = true
	if !q.applyPreviewCodepage(65001, false) || len(q.cacheLines) != 1 || !strings.Contains(q.cacheLines[0], "70 6") {
		t.Fatalf("hex codepage application = %#v", q.cacheLines)
	}

	if (&QuickViewPanel{}).toggleHexMode() {
		t.Fatal("toggleHexMode without bytes should fail")
	}
	q.cacheReadErr = nil
	q.hexMode = true
	q.cacheCodepage = 999999
	if q.toggleHexMode() || q.hexMode {
		t.Fatal("toggleHexMode should expose an invalid decode and leave text mode selected")
	}
	if (&QuickViewPanel{}).switchToCodepage(65001) {
		t.Fatal("switchToCodepage without bytes should fail")
	}

	q.cacheLines = []string{"abcdef", "", "界x"}
	q.Wrap = true
	wrapped, mapping := q.buildDisplayLines(3)
	if got, want := strings.Join(wrapped, "|"), "abc|def||界x"; got != want {
		t.Fatalf("wrapped lines = %q, want %q (mapping %#v)", got, want, mapping)
	}
	if got, want := mapping, []int{0, 0, 1, 2}; len(got) != len(want) {
		t.Fatalf("wrapped mapping length = %#v, want %#v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("wrapped mapping = %#v, want %#v", got, want)
			}
		}
	}
	q.Wrap = false
	plain, plainMap := q.buildDisplayLines(3)
	if len(plain) != 3 || plain[0] != "abcdef" || plainMap[2] != 2 {
		t.Fatalf("unwrapped lines = %#v, mapping %#v", plain, plainMap)
	}
	if got := firstDisplayForSource(mapping, 2); got != 3 {
		t.Fatalf("firstDisplayForSource = %d, want 3", got)
	}
	if got := firstDisplayForSource(mapping, 99); got != 0 {
		t.Fatalf("missing source mapped to %d, want 0", got)
	}
	for _, tc := range []struct {
		s     string
		width int
		want  int
	}{
		{"", 3, 0}, {"abc", 0, 3}, {"界x", 1, 0}, {"界x", 2, len("界")}, {"abc", 9, 3},
	} {
		if got := cellCut(tc.s, tc.width); got != tc.want {
			t.Errorf("cellCut(%q, %d) = %d, want %d", tc.s, tc.width, got, tc.want)
		}
	}
	if got := trimLeftCells("界abc", 1); got != "界abc" {
		t.Fatalf("trimLeftCells split wide rune = %q", got)
	}
	if got := trimLeftCells("界abc", 2); got != "abc" {
		t.Fatalf("trimLeftCells wide rune = %q", got)
	}
	if got := trimLeftCells("abc", 9); got != "" {
		t.Fatalf("trimLeftCells past end = %q", got)
	}

	q.SetPosition(0, 0, 10, 10)
	q.Wrap = true
	q.cacheLines = []string{"a", "b", "c"}
	q.scrollToSource(2)
	if q.ScrollY != 2 || q.hasPin {
		t.Fatalf("scrollToSource = scroll %d pin %v", q.ScrollY, q.hasPin)
	}
	q.SetPosition(4, 4, 4, 4)
	q.scrollToSource(1)
	if q.ScrollY != 2 {
		t.Fatalf("narrow scrollToSource changed scroll to %d", q.ScrollY)
	}
	small := &QuickViewPanel{}
	small.SetPosition(0, 5, 0, 5)
	regular := &QuickViewPanel{}
	regular.SetPosition(0, 1, 0, 8)
	if small.pageHeight() != 1 || regular.pageHeight() != 6 {
		t.Fatal("pageHeight edge cases are wrong")
	}
}

func TestQuickViewSelectionAndDirectoryRenderingContracts(t *testing.T) {
	root := t.TempDir()
	v := vfs.NewOSVFS(root)
	q := &QuickViewPanel{}
	if _, _, ok := q.selectedFile(); ok {
		t.Fatal("nil quick view source selected a file")
	}
	fp := &FileSystemPanel{Vfs: v}
	q.src = fp
	if _, _, ok := q.selectedFile(); ok {
		t.Fatal("empty panel selected a file")
	}
	fp.Entries = []*FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "folder", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file.txt"}},
	}
	for idx := 0; idx < 2; idx++ {
		fp.CursorIdx = idx
		if _, _, ok := q.selectedFile(); ok {
			t.Fatalf("directory-like entry %d selected a file", idx)
		}
	}
	fp.Entries[2] = nil
	fp.CursorIdx = 2
	if _, _, ok := q.selectedFile(); ok {
		t.Fatal("nil entry selected a file")
	}
	fp.Entries[2] = &FileEntry{VFSItem: vfs.VFSItem{Name: "file.txt", Size: 4}}
	path, item, ok := q.selectedFile()
	if !ok || item.Name != "file.txt" || path != v.Join(v.GetPath(), "file.txt") {
		t.Fatalf("selected file = %q, %#v, %v", path, item, ok)
	}
	if (&QuickViewPanel{}).GetSelectedName() != "" || q.GetSelectedName() != "file.txt" {
		t.Fatal("GetSelectedName edge cases failed")
	}

	var lines []string
	q.scanStats = vfs.OpStats{Dirs: 3, Files: 4, Bytes: 100, DirBytes: 20, PhysicalBytes: 50}
	q.scanClusterSize = 4096
	q.scanDone = false
	q.renderDir(&FileEntry{VFSItem: vfs.VFSItem{Name: "demo", IsDir: true}}, func(line string) { lines = append(lines, line) })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "demo") || !strings.Contains(joined, "240%") || !strings.Contains(joined, "4.0 KiB") || !strings.Contains(strings.ToLower(joined), "scanning") {
		t.Fatalf("directory rendering omitted expected rows: %q", joined)
	}
	lines = nil
	q.scanDone = true
	q.scanErr = errors.New("walk failed")
	q.scanStats = vfs.OpStats{}
	q.scanClusterSize = 0
	q.renderDir(&FileEntry{VFSItem: vfs.VFSItem{Name: "broken", IsDir: true}}, func(line string) { lines = append(lines, line) })
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "broken") || !strings.Contains(joined, "walk failed") || strings.Contains(strings.ToLower(joined), "scanning") {
		t.Fatalf("directory error rendering = %q", joined)
	}
}
