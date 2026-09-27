package multiarc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
)

// fakeAdd is one archiveWriter.add call, with what was staged for each
// member at that moment ("<dir>" for a directory).
type fakeAdd struct {
	members  []string
	replaced []string
	staged   map[string]string
}

// fakeWriter is a backend whose listing is whatever its writes made it, so
// a test can follow a VFS write through to the relisting that follows.
type fakeWriter struct {
	entries  []entry
	checkErr error
	addErr   error
	checks   []writeOp
	adds     []fakeAdd
	removes  [][]string
}

func (*fakeWriter) id() string { return "fakew" }

func (w *fakeWriter) list(context.Context, string) ([]entry, error) {
	return append([]entry(nil), w.entries...), nil
}

func (*fakeWriter) extractAll(context.Context, string, string) error         { return nil }
func (*fakeWriter) extractOne(context.Context, string, string, string) error { return nil }

func (w *fakeWriter) checkWrite(_ context.Context, _ string, op writeOp, _ string) error {
	w.checks = append(w.checks, op)
	return w.checkErr
}

func (w *fakeWriter) add(_ context.Context, _ string, stageDir string, members, replaced []string) error {
	call := fakeAdd{members: members, replaced: replaced, staged: map[string]string{}}
	for _, m := range members {
		full := filepath.Join(stageDir, filepath.FromSlash(m))
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		if info.IsDir() {
			call.staged[m] = "<dir>"
			w.entries = append(w.entries, entry{Path: m, Raw: m + "/", IsDir: true})
			continue
		}
		content, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			return err
		}
		call.staged[m] = string(content)
		kept := w.entries[:0]
		for _, e := range w.entries {
			if e.Path != m {
				kept = append(kept, e)
			}
		}
		w.entries = append(kept, entry{Path: m, Raw: m, Size: int64(len(content)), SizeKnown: true})
	}
	w.adds = append(w.adds, call)
	return w.addErr
}

func (w *fakeWriter) remove(_ context.Context, _ string, raws []string) error {
	w.removes = append(w.removes, raws)
	gone := map[string]bool{}
	for _, r := range raws {
		gone[r] = true
	}
	kept := w.entries[:0]
	for _, e := range w.entries {
		raw := e.Raw
		if raw == "" {
			raw = e.Path
		}
		if !gone[raw] {
			kept = append(kept, e)
		}
	}
	w.entries = kept
	return nil
}

// newWritableTestVFS opens w as an archive that sits in its own temp
// directory: staging directories are made next to the archive.
func newWritableTestVFS(t *testing.T, w *fakeWriter) *MultiArcVFS {
	t.Helper()
	localPath := filepath.Join(t.TempDir(), "test.fake")
	if err := os.WriteFile(localPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewMultiArcVFS(nil, localPath, "test.fake", w, "fakew")
}

func statIsDir(t *testing.T, v *MultiArcVFS, p string) bool {
	t.Helper()
	item, err := v.Stat(context.Background(), p)
	if err != nil {
		t.Fatalf("Stat %s: %v", p, err)
	}
	return item.IsDir
}

func TestMultiArcVFSMkDirStagesAnEmptyDirectory(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "top.txt"}}}
	v := newWritableTestVFS(t, w)
	if err := v.MkDir(context.Background(), "/new/sub"); err != nil {
		t.Fatalf("MkDir: %v", err)
	}
	if len(w.adds) != 1 || !reflect.DeepEqual(w.adds[0].members, []string{"new/sub"}) || w.adds[0].staged["new/sub"] != "<dir>" {
		t.Fatalf("adds = %#v, want new/sub staged as a directory", w.adds)
	}
	if !reflect.DeepEqual(w.checks, []writeOp{writeMkDir}) {
		t.Errorf("checks = %v, want one writeMkDir", w.checks)
	}
	if !statIsDir(t, v, "/new/sub") {
		t.Error("the new directory should be listed after MkDir")
	}
	assertNoScratchLeft(t, v.localPath)
}

func TestMultiArcVFSMkDirRefusals(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "file.txt"}, {Path: "dir/x"}}}
	v := newWritableTestVFS(t, w)
	ctx := context.Background()
	if err := v.MkDir(ctx, "/dir"); !errors.Is(err, os.ErrExist) {
		t.Errorf("MkDir on an implied directory = %v, want os.ErrExist", err)
	}
	if err := v.MkDir(ctx, "/file.txt"); !errors.Is(err, os.ErrExist) {
		t.Errorf("MkDir on a file = %v, want os.ErrExist", err)
	}
	if err := v.MkDir(ctx, "/file.txt/sub"); err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Errorf("MkDir under a file = %v, want a refusal", err)
	}
	if err := v.MkDir(ctx, "/"); err == nil {
		t.Error("MkDir on the archive root should be refused")
	}
	if len(w.adds) != 0 {
		t.Fatalf("a refused MkDir still ran add: %#v", w.adds)
	}
}

func TestMultiArcVFSCreateCommitsOnClose(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "dir", IsDir: true}}}
	v := newWritableTestVFS(t, w)
	ctx := context.Background()

	wc, err := v.Create(ctx, "/dir/new.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := wc.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(w.adds) != 0 {
		t.Fatal("nothing should reach the archive before Close")
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(w.adds) != 1 || w.adds[0].staged["dir/new.txt"] != "hello" || len(w.adds[0].replaced) != 0 {
		t.Fatalf("adds = %#v, want dir/new.txt staged with its content and nothing replaced", w.adds)
	}
	if !reflect.DeepEqual(w.checks, []writeOp{writeAdd}) {
		t.Errorf("checks = %v, want one writeAdd", w.checks)
	}
	item, err := v.Stat(ctx, "/dir/new.txt")
	if err != nil || item.Size != 5 {
		t.Fatalf("Stat after Close = (%#v, %v), want the new 5-byte member", item, err)
	}
	if err := wc.Close(); err != nil || len(w.adds) != 1 {
		t.Errorf("a second Close = %v with %d adds, want nil and no second commit", err, len(w.adds))
	}
	assertNoScratchLeft(t, v.localPath)
}

// Overwriting a member names every raw spelling of it as replaced, so a
// backend that appends (GNU tar) can delete the old copy first.
func TestMultiArcVFSCreateReplacesByRawName(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "f.txt", Raw: "./f.txt"}}}
	v := newWritableTestVFS(t, w)
	wc, err := v.Create(context.Background(), "/f.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !reflect.DeepEqual(w.checks, []writeOp{writeReplace}) {
		t.Errorf("checks = %v, want writeReplace", w.checks)
	}
	if len(w.adds) != 1 || !reflect.DeepEqual(w.adds[0].replaced, []string{"./f.txt"}) {
		t.Fatalf("adds = %#v, want ./f.txt replaced", w.adds)
	}
}

func TestMultiArcVFSCreateAbortDiscards(t *testing.T) {
	w := &fakeWriter{}
	v := newWritableTestVFS(t, w)
	wc, err := v.Create(context.Background(), "/partial.bin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := wc.Write([]byte("half")); err != nil {
		t.Fatal(err)
	}
	aborter, ok := wc.(vfs.AbortableWriter)
	if !ok {
		t.Fatal("Create's writer should be a vfs.AbortableWriter")
	}
	if err := aborter.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close after Abort: %v", err)
	}
	if len(w.adds) != 0 {
		t.Fatalf("an aborted member reached the archive: %#v", w.adds)
	}
	assertNoScratchLeft(t, v.localPath)
}

// A backend that cannot do the write says so at Create, before the copy
// engine has sent a byte, and nothing is staged.
func TestMultiArcVFSCreateRefusedUpFront(t *testing.T) {
	refusal := errors.New("multiarc test: tool missing")
	w := &fakeWriter{checkErr: refusal}
	v := newWritableTestVFS(t, w)
	if _, err := v.Create(context.Background(), "/x"); !errors.Is(err, refusal) {
		t.Fatalf("Create = %v, want %v", err, refusal)
	}
	assertNoScratchLeft(t, v.localPath)
}

func TestMultiArcVFSCreateOnDirectoryRefused(t *testing.T) {
	v := newWritableTestVFS(t, &fakeWriter{entries: []entry{{Path: "d/x"}}})
	if _, err := v.Create(context.Background(), "/d"); err == nil {
		t.Fatal("Create over a directory should be refused")
	}
}

// The delete engine calls Remove once per selected item and expects the
// whole subtree gone: every raw name under it goes to the backend at once.
func TestMultiArcVFSRemoveDeletesSubtree(t *testing.T) {
	w := &fakeWriter{entries: []entry{
		{Path: "dir", Raw: "dir/", IsDir: true},
		{Path: "dir/a", Raw: "dir/a"},
		{Path: "dir/sub/b", Raw: "./dir/sub/b"},
		{Path: "dirt", Raw: "dirt"},
	}}
	v := newWritableTestVFS(t, w)
	if err := v.Remove(context.Background(), "/dir"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	want := []string{"./dir/sub/b", "dir/", "dir/a"}
	if len(w.removes) != 1 || !reflect.DeepEqual(w.removes[0], want) {
		t.Fatalf("removes = %v, want %v", w.removes, want)
	}
	if _, err := v.Stat(context.Background(), "/dir"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat /dir after Remove = %v, want os.ErrNotExist", err)
	}
	if _, err := v.Stat(context.Background(), "/dirt"); err != nil {
		t.Errorf("a sibling sharing the prefix was touched: %v", err)
	}
}

func TestMultiArcVFSRemoveRefusals(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "a"}}}
	v := newWritableTestVFS(t, w)
	ctx := context.Background()
	if err := v.Remove(ctx, "/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Remove of a missing member = %v, want os.ErrNotExist", err)
	}
	if err := v.Remove(ctx, "/"); err == nil {
		t.Error("Remove of the archive root should be refused")
	}
	w.checkErr = errors.New("multiarc test: bsdtar cannot delete")
	if err := v.Remove(ctx, "/a"); !errors.Is(err, w.checkErr) {
		t.Errorf("Remove = %v, want the backend's refusal", err)
	}
	if len(w.removes) != 0 {
		t.Fatalf("a refused Remove still ran: %v", w.removes)
	}
}

// A failed write may have committed part of itself (a chunked command
// line), so the listing is read again either way.
func TestMultiArcVFSWriteFailureStillRelists(t *testing.T) {
	w := &fakeWriter{addErr: errors.New("multiarc test: second chunk failed")}
	v := newWritableTestVFS(t, w)
	if err := v.MkDir(context.Background(), "/half"); !errors.Is(err, w.addErr) {
		t.Fatalf("MkDir = %v, want %v", err, w.addErr)
	}
	if !statIsDir(t, v, "/half") {
		t.Error("what the failed write did commit should be listed")
	}
}

func TestMultiArcVFSCloneSeesWrites(t *testing.T) {
	w := &fakeWriter{entries: []entry{{Path: "a"}}}
	v := newWritableTestVFS(t, w)
	clone := v.Clone().(*MultiArcVFS)
	if err := v.MkDir(context.Background(), "/b"); err != nil {
		t.Fatalf("MkDir: %v", err)
	}
	if !statIsDir(t, clone, "/b") {
		t.Error("a clone should see the other clone's write")
	}
}

func TestMultiArcVFSRenameRefused(t *testing.T) {
	v := newWritableTestVFS(t, &fakeWriter{entries: []entry{{Path: "a"}}})
	if err := v.Rename(context.Background(), "/a", "/b"); !errors.Is(err, errNoRename) {
		t.Fatalf("Rename = %v, want errNoRename", err)
	}
}
