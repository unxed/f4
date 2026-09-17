package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/vfs"
)

func TestActionsBinaryHeaderAndSupportedViewerGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []byte
		want   bool
	}{
		{name: "empty", header: nil},
		{name: "text", header: []byte("plain text")},
		{name: "nul", header: []byte{'a', 0, 'b'}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := editorHeaderIsBinary(tc.header, 65001); got != tc.want {
				t.Fatalf("editorHeaderIsBinary(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
	if tryOpenVideoPlayer(nil, nil, "movie.mp4") || tryOpenImageViewer(nil, nil, "picture.png") {
		t.Fatal("nil panel was accepted by a supported-viewer guard")
	}
}

func TestImageSiblingPathsFollowTheActivePanel(t *testing.T) {
	dir := t.TempDir()
	fs := vfs.NewOSVFS(dir)
	fsp := &panel.FileSystemPanel{
		Vfs: fs,
		Entries: []*panel.FileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "first.png"}},
			{VFSItem: vfs.VFSItem{Name: "notes.txt"}},
			{VFSItem: vfs.VFSItem{Name: "second.jpg"}},
		},
	}
	fsp.SetCursorIndex(3)
	pf := &panel.PanelsFrame{Panels: [2]panel.Panel{fsp, nil}, ActiveIdx: 0}

	got, index := imageSiblingPaths(pf, fs, filepath.Join(dir, "second.jpg"))
	want := []string{filepath.Join(dir, "first.png"), filepath.Join(dir, "second.jpg")}
	if !reflect.DeepEqual(got, want) || index != 1 {
		t.Fatalf("imageSiblingPaths() = (%v, %d), want (%v, 1)", got, index, want)
	}
	if got, index := imageSiblingPaths(pf, fs, filepath.Join(t.TempDir(), "second.jpg")); got != nil || index != -1 {
		t.Fatalf("sibling paths from another directory = (%v, %d), want (nil, -1)", got, index)
	}
	if got, index := imageSiblingPaths(nil, fs, "picture.png"); got != nil || index != -1 {
		t.Fatalf("sibling paths without panels = (%v, %d), want (nil, -1)", got, index)
	}
}
