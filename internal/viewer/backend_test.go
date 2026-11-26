package viewer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type failingViewerFile struct {
	err error
}

func (f *failingViewerFile) Size() int64 { return 16 }
func (f *failingViewerFile) ReadAt(context.Context, []byte, int64) (int, error) {
	return 0, f.err
}
func (f *failingViewerFile) Read(context.Context, []byte) (int, error) { return 0, f.err }
func (f *failingViewerFile) Close() error                              { return nil }

func TestViewerBackendPreservesBackgroundReadError(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	want := errors.New("remote range failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &ViewerBackend{File: &failingViewerFile{err: want}, size: 16, ctx: ctx, cancelCtx: cancel}

	if _, err := backend.ReadAt(0, 4); err != piecetable.ErrLoading {
		t.Fatalf("first ReadAt error = %v, want ErrLoading", err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("background read did not finish")
		}
		if _, err := backend.ReadAt(0, 4); errors.Is(err, want) {
			break
		} else if err != piecetable.ErrLoading {
			t.Fatalf("second ReadAt error = %v, want %v", err, want)
		}
	}
}

func TestReplaceFirstCacheKeepsWindowAdjacentToNextRead(t *testing.T) {
	const window = int64(256 * 1024)
	// The viewer has already moved beyond its initial window. A read at the end
	// of the second window requests the next overlapping window; retain the
	// second cache so the current page remains readable during the redraw.
	if !replaceFirstCache(0, int(window), 192*1024, int(window), 384*1024, int(window)) {
		t.Fatal("next window should replace the stale first cache")
	}
	// Moving backwards should retain the earlier window instead.
	if replaceFirstCache(0, int(window), 192*1024, int(window), -64*1024, int(window)) {
		t.Fatal("backward read should replace the farther second cache")
	}
}

var _ vfs.ReadAtCloser = (*failingViewerFile)(nil)

func TestViewerBackend_ReadAndFindLineStart(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "test.txt")
	content := "line1\nline2\nline3"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(t.TempDir())
	vb, err := NewViewerBackend(context.Background(), v, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()

	if vb.Size() != int64(len(content)) {
		t.Fatalf("Expected size %d, got %d", len(content), vb.Size())
	}

	// ReadAt Test
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var data []byte
	var errLoop error
	for i := 0; i < 100; i++ {
		data, errLoop = vb.ReadAt(6, 5)
		if errLoop == piecetable.ErrLoading {
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		break
	}

	if string(data) != "line2" {
		t.Errorf("ReadAt failed: expected 'line2', got '%s', err: %v", string(data), errLoop)
	}

	// FindLineStart Test (offset inside "line2")
	start := vb.FindLineStart(8)
	if start != 6 {
		t.Errorf("FindLineStart failed: expected 6, got %d", start)
	}

	// FindLineStart Test (offset inside "line1" / start of file)
	startZero := vb.FindLineStart(3)
	if startZero != 0 {
		t.Errorf("FindLineStart at file beginning should return 0, got %d", startZero)
	}
}

func TestViewerView_NonUTF8OffsetsUseDecodedStream(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	root := t.TempDir()
	path := filepath.Join(root, "cp866.txt")
	raw, err := vfs.EncodeBytes([]byte("Привет\nМир\n"), 866)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	oldAuto, oldDefault := config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage
	config.App.ViewerAutodetectCodePage = false
	config.App.ViewerDefaultCodePage = 866
	t.Cleanup(func() {
		config.App.ViewerAutodetectCodePage = oldAuto
		config.App.ViewerDefaultCodePage = oldDefault
	})

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()

	if vv.Codepage != 866 {
		t.Fatalf("codepage = %d, want 866", vv.Codepage)
	}
	if vv.Backend.Size() <= int64(len(raw)) {
		t.Fatalf("backend size = %d, want decoded UTF-8 stream larger than raw %d", vv.Backend.Size(), len(raw))
	}
	data := make([]byte, int(vv.Backend.Size()))
	n, err := vv.Backend.File.ReadAt(context.Background(), data, 0)
	if err != nil {
		t.Fatalf("ReadAt decoded stream: %v", err)
	}
	if string(data[:n]) != "Привет\nМир\n" {
		t.Fatalf("decoded stream = %q", data[:n])
	}

	// Switching to raw UTF-8 must not leave the viewport beyond the new,
	// shorter stream when the user was at the end of the decoded view.
	vv.TopOffset = vv.Backend.Size() - 1
	vv.ReloadWithCodepage(65001)
	if vv.TopOffset < 0 || vv.TopOffset >= vv.Backend.Size() {
		t.Fatalf("TopOffset = %d after reload, backend size = %d", vv.TopOffset, vv.Backend.Size())
	}

	// Codepage labels are still allowed in hex mode, but the displayed bytes
	// must remain the raw file bytes.
	vv.HexMode = true
	vv.ReloadWithCodepage(866)
	data = make([]byte, len(raw))
	n, err = vv.Backend.File.ReadAt(context.Background(), data, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt raw hex stream: %v", err)
	}
	if !bytes.Equal(data[:n], raw[:n]) {
		t.Fatalf("hex stream was decoded: got %x, want %x", data[:n], raw[:n])
	}
}

// indexingVFS answers line index questions the way a remote file system
// does, and counts the questions so the caching can be checked.
type indexingVFS struct {
	vfs.VFS
	offsets []int64
	total   int64
	calls   int
	err     error
}

func (v *indexingVFS) LineIndex(ctx context.Context, path string, first, count int64) (vfs.LineIndexResult, error) {
	v.calls++
	if v.err != nil {
		return vfs.LineIndexResult{}, v.err
	}
	res := vfs.LineIndexResult{First: first, Total: v.total}
	for i := first; i < first+count && i >= 1 && i <= int64(len(v.offsets)); i++ {
		res.Offsets = append(res.Offsets, v.offsets[i-1])
	}
	return res, nil
}

func TestViewerBackendLineStartFromEnd(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	v := &indexingVFS{VFS: vfs.NewOSVFS(dir), offsets: []int64{0, 6, 12}, total: 3}
	vb, err := NewViewerBackend(context.Background(), v, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()

	off, ok := vb.LineStartFromEnd(context.Background(), 2)
	if !ok {
		t.Fatal("the indexer was not used")
	}
	if off != 6 {
		t.Errorf("last two lines start at %d, want 6", off)
	}
	if v.calls != 2 {
		t.Errorf("%d index calls, want 2 (total, then offset)", v.calls)
	}

	// The total is already known for this size, so only the offset is asked
	// for the second time round.
	v.calls = 0
	if off, ok = vb.LineStartFromEnd(context.Background(), 1); !ok || off != 12 {
		t.Errorf("second call returned %d, %v", off, ok)
	}
	if v.calls != 1 {
		t.Errorf("%d index calls after caching the total, want 1", v.calls)
	}

	// Asking for more lines than the file has starts at its beginning.
	if off, ok = vb.LineStartFromEnd(context.Background(), 100); !ok || off != 0 {
		t.Errorf("whole file request returned %d, %v", off, ok)
	}
}

func TestViewerBackendLineStartFromEndFallsBack(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(tmp, []byte("a\nb\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// A plain local file system does not implement the interface at all.
	plain, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(dir), tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	if _, ok := plain.LineStartFromEnd(context.Background(), 1); ok {
		t.Error("a file system without the interface claimed to have an index")
	}

	// One that does, but fails, must not be trusted either.
	failing := &indexingVFS{VFS: vfs.NewOSVFS(dir), err: fmt.Errorf("no awk on remote host")}
	vb, err := NewViewerBackend(context.Background(), failing, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()
	if _, ok := vb.LineStartFromEnd(context.Background(), 1); ok {
		t.Error("a failing index was treated as an answer")
	}
}

// searchingVFS answers a search the way a remote host does: with the offsets
// of every match, in file order, worked out on its own side.
type searchingVFS struct {
	vfs.VFS
	offsets []int64
	can     bool
	err     error
	calls   int
}

func (v *searchingVFS) GetCapabilities() vfs.VFSCapabilities {
	caps := v.VFS.GetCapabilities()
	caps.HasSearch = v.can
	return caps
}

func (v *searchingVFS) Search(ctx context.Context, path, pattern string) (chan int64, error) {
	v.calls++
	if v.err != nil {
		return nil, v.err
	}
	out := make(chan int64, len(v.offsets))
	for _, off := range v.offsets {
		out <- off
	}
	close(out)
	return out, nil
}

func TestViewerBackendSearchFrom(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(tmp, []byte("needle here and needle there\n"), 0600); err != nil {
		t.Fatal(err)
	}

	remote := &searchingVFS{VFS: vfs.NewOSVFS(dir), offsets: []int64{0, 16}, can: true}
	vb, err := NewViewerBackend(context.Background(), remote, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()

	if at, searched := vb.SearchFrom(context.Background(), "needle", 0); !searched || at != 0 {
		t.Errorf("SearchFrom(0) = %d, %v; want 0, true", at, searched)
	}
	// Searching on from where the last hit was is what pressing the key
	// again means, and it must not find the same one over and over.
	if at, searched := vb.SearchFrom(context.Background(), "needle", 1); !searched || at != 16 {
		t.Errorf("SearchFrom(1) = %d, %v; want 16, true", at, searched)
	}
	// Searched and not there is a different answer from cannot search: the
	// caller must not fall back to scanning a file that has been searched.
	if at, searched := vb.SearchFrom(context.Background(), "needle", 100); !searched || at != -1 {
		t.Errorf("SearchFrom past the last hit = %d, %v; want -1, true", at, searched)
	}
	if remote.calls != 3 {
		t.Errorf("the file system was asked %d times, want 3", remote.calls)
	}
	if at, searched := vb.SearchBefore(context.Background(), "needle", 16); !searched || at != 0 {
		t.Errorf("SearchBefore(16) = %d, %v; want 0, true", at, searched)
	}
	if at, searched := vb.SearchBefore(context.Background(), "needle", 100); !searched || at != 16 {
		t.Errorf("SearchBefore(100) = %d, %v; want 16, true", at, searched)
	}

	// A file system that cannot search says so, and the viewer scans.
	local, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(dir), tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	if _, searched := local.SearchFrom(context.Background(), "needle", 0); searched {
		t.Error("a file system without HasSearch claimed to have searched")
	}

	// One that says it can and then fails is not to be trusted either.
	broken := &searchingVFS{VFS: vfs.NewOSVFS(dir), can: true, err: fmt.Errorf("no grep on remote host")}
	vb2, err := NewViewerBackend(context.Background(), broken, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb2.Close() }()
	if _, searched := vb2.SearchFrom(context.Background(), "needle", 0); searched {
		t.Error("a failed search was taken for an answer")
	}
}

func TestViewerSearchOffsetBothDirections(t *testing.T) {
	data := []byte("zero needle middle needle end")
	vb := &ViewerBackend{
		File:         &vfs.MemoryReadAtCloser{Data: data},
		size:         int64(len(data)),
		cacheOff:     0,
		cacheData:    data,
		totalLines:   -1,
		totalForSize: -1,
	}

	if got := SearchOffset(context.Background(), vb, "needle", 0, false, nil); got != 5 {
		t.Fatalf("forward first offset = %d, want 5", got)
	}
	if got := SearchOffset(context.Background(), vb, "needle", 6, false, nil); got != 19 {
		t.Fatalf("forward next offset = %d, want 19", got)
	}
	if got := SearchOffset(context.Background(), vb, "needle", int64(len(data)), true, nil); got != 19 {
		t.Fatalf("backward last offset = %d, want 19", got)
	}
	if got := SearchOffset(context.Background(), vb, "needle", 19, true, nil); got != 5 {
		t.Fatalf("backward previous offset = %d, want 5", got)
	}
}

func TestViewerBackend_UTF8BOMUsesLogicalOffsets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	text := "first\nsecond needle\n"
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...), 0600); err != nil {
		t.Fatal(err)
	}

	indexed := &indexingVFS{
		VFS:     vfs.NewOSVFS(dir),
		offsets: []int64{3, 9},
		total:   2,
	}
	vb, err := NewViewerBackend(context.Background(), indexed, path)
	if err != nil {
		t.Fatal(err)
	}
	indexedBackend := vb
	defer func() {
		if err := indexedBackend.Close(); err != nil {
			t.Errorf("close viewer backend: %v", err)
		}
	}()
	vb.DataOffset = vfs.UTF8BOMSize
	vb.size = int64(len(text))

	if got := vb.Size(); got != int64(len(text)) {
		t.Fatalf("logical size = %d, want %d", got, len(text))
	}
	if got, ok := vb.LineStart(context.Background(), 2); !ok || got != int64(len("first\n")) {
		t.Fatalf("LineStart(2) = %d, %v; want %d, true", got, ok, len("first\n"))
	}
	if got, ok := vb.LineStartFromEnd(context.Background(), 1); !ok || got != int64(len("first\n")) {
		t.Fatalf("LineStartFromEnd(1) = %d, %v; want %d, true", got, ok, len("first\n"))
	}

	searching := &searchingVFS{
		VFS:     vfs.NewOSVFS(dir),
		offsets: []int64{16}, // raw offset: BOM (3) + len("first\nsecond ") (13)
		can:     true,
	}
	searchBackend, err := NewViewerBackend(context.Background(), searching, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := searchBackend.Close(); err != nil {
			t.Errorf("close viewer backend: %v", err)
		}
	}()
	searchBackend.DataOffset = vfs.UTF8BOMSize
	searchBackend.size = int64(len(text))
	if got, ok := searchBackend.SearchFrom(context.Background(), "needle", 0); !ok || got != 13 {
		t.Fatalf("SearchFrom = %d, %v; want 13, true", got, ok)
	}
}

func TestViewerSearchMatchOptions(t *testing.T) {
	data := []byte("Needles needle NEEDLE42 needle")
	vb := &ViewerBackend{
		File:      &vfs.MemoryReadAtCloser{Data: data},
		size:      int64(len(data)),
		cacheOff:  0,
		cacheData: data,
	}
	defer func() { _ = vb.Close() }()

	cases := []struct {
		name       string
		pattern    string
		start      int64
		options    SearchOptions
		wantOffset int64
		wantLength int
	}{
		{name: "case insensitive", pattern: "needle", wantOffset: 0, wantLength: len("needle")},
		{name: "case sensitive", pattern: "needle", options: SearchOptions{CaseSensitive: true}, wantOffset: 8, wantLength: len("needle")},
		{name: "whole word", pattern: "needle", options: SearchOptions{WholeWord: true}, wantOffset: 8, wantLength: len("needle")},
		{name: "regex length", pattern: `needle\d+`, options: SearchOptions{Regexp: true}, wantOffset: 15, wantLength: len("NEEDLE42")},
		{name: "reverse", pattern: "needle", start: int64(len(data)), options: SearchOptions{Reverse: true, CaseSensitive: true}, wantOffset: 24, wantLength: len("needle")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOffset, gotLength, err := SearchMatch(context.Background(), vb, tc.pattern, tc.start, tc.options, nil)
			if err != nil {
				t.Fatalf("SearchMatch: %v", err)
			}
			if gotOffset != tc.wantOffset || gotLength != tc.wantLength {
				t.Fatalf("SearchMatch = (%d, %d), want (%d, %d)", gotOffset, gotLength, tc.wantOffset, tc.wantLength)
			}
		})
	}
}

func TestViewerSearchMatchRejectsInvalidRegexp(t *testing.T) {
	data := []byte("text")
	vb := &ViewerBackend{
		File:      &vfs.MemoryReadAtCloser{Data: data},
		size:      int64(len(data)),
		cacheOff:  0,
		cacheData: data,
	}
	defer func() { _ = vb.Close() }()

	if _, _, err := SearchMatch(context.Background(), vb, "[", 0, SearchOptions{Regexp: true}, nil); err == nil {
		t.Fatal("invalid regular expression was accepted")
	}
}

func TestViewerBackendLineStart(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		line int64
		want int64
		ok   bool
	}{
		{0, 0, true}, {1, 0, true}, {2, 6, true}, {3, 12, true}, {4, 0, false}, {99, 0, false},
	}

	// Once through the index the file system provides, once through the scan
	// that a file system without one falls back to. Both have to agree, or
	// the same keystroke would land somewhere else depending on the backend.
	indexed := &indexingVFS{VFS: vfs.NewOSVFS(dir), offsets: []int64{0, 6, 12}, total: 3}
	for name, v := range map[string]vfs.VFS{"indexed": indexed, "scanned": vfs.NewOSVFS(dir)} {
		vb, err := NewViewerBackend(context.Background(), v, tmp)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			got, ok := vb.LineStart(context.Background(), tc.line)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("%s: LineStart(%d) = %d, %v; want %d, %v",
					name, tc.line, got, ok, tc.want, tc.ok)
			}
		}
		if err := vb.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if indexed.calls == 0 {
		t.Error("the index was never asked")
	}
}

func TestViewerBackendLineStartWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(tmp, []byte("alpha\nomega"), 0600); err != nil {
		t.Fatal(err)
	}
	vb, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(dir), tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()

	if got, ok := vb.LineStart(context.Background(), 2); !ok || got != 6 {
		t.Errorf("LineStart(2) = %d, %v; want 6, true", got, ok)
	}
	if _, ok := vb.LineStart(context.Background(), 3); ok {
		t.Error("a line past the last one was found")
	}
}
