package editor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// openLocalEditor opens path the way showEditor does for a local UTF-8 file:
// lazily, through an AsyncBuffer, so the save path sees the piece table it
// sees in production.
func openLocalEditor(t *testing.T, dir, path string) *EditorView {
	t.Helper()

	v := vfs.NewOSVFS(dir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	buf := NewAsyncBuffer(context.Background(), f)
	buf.Prewarm()

	ev := NewEditorView(piecetable.NewWithBuffer(buf), v, path)
	ev.File = f
	ev.AsyncBuf = buf
	ev.Codepage = 65001
	return ev
}

// TestEditorSave_InPlacePatchDoesNotRenameAMissingStage covers the save that
// OSVFS.PatchInPlace accepts: it writes through to the file itself, so there is
// no staged sibling to rename into place. The finalize step used to try anyway
// and reported "Failed to finalize save (rename failed): ... no such file or
// directory" — with the new content already on disk, so the file was correct
// and the editor still believed the save had failed.
//
// PatchInPlace accepts exactly the edits that leave every unchanged piece at
// its original offset, which is why this needs a same-length replacement: an
// insertion shifts them and falls back to the staged path.
func TestEditorSave_InPlacePatchDoesNotRenameAMissingStage(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "aa.txt")
	if err := os.WriteFile(path, []byte("hello world\nsecond line\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	ev.replaceRange(0, 5, []byte("HELLO"))
	ev.Modified = true

	ev.SaveToFile(nil)
	waitEditorSave(t, ev)
	testutil.DrainPendingTasks()

	// The content alone proves nothing here: PatchInPlace writes it before the
	// finalize step runs, so it is correct either way. Going clean is what says
	// the save actually completed.
	if ev.Modified {
		t.Error("editor still marked modified: the save reported failure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "HELLO world\nsecond line\n" {
		t.Errorf("content = %q, want %q", got, "HELLO world\nsecond line\n")
	}
	assertNoEditorTempSiblings(t, path)
}

// TestEditorSave_UnmodifiedBufferCompletes is the same trap reached the
// shortest way: an untouched buffer is one original piece at offset zero, which
// PatchInPlace accepts trivially.
func TestEditorSave_UnmodifiedBufferCompletes(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "untouched.txt")
	content := strings.Repeat("line of text\n", 100)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	ev.Modified = true // F2 on a buffer whose bytes happen to be unchanged
	ev.SaveToFile(nil)
	waitEditorSave(t, ev)
	testutil.DrainPendingTasks()

	if ev.Modified {
		t.Error("editor still marked modified: the save reported failure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != content {
		t.Errorf("content changed: got %d bytes, want %d", len(got), len(content))
	}
	assertNoEditorTempSiblings(t, path)
}

// TestEditorSave_InsertionStillStages keeps the other half honest: an edit that
// shifts offsets is refused by PatchInPlace, so the save must still go through
// a staged sibling and rename it into place.
func TestEditorSave_InsertionStillStages(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "grown.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	ev.InsertTextAtCursor([]byte("XYZ"))
	ev.Modified = true

	ev.SaveToFile(nil)
	waitEditorSave(t, ev)
	testutil.DrainPendingTasks()

	if ev.Modified {
		t.Error("editor still marked modified: the save reported failure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "XYZhello world\n" {
		t.Errorf("content = %q, want %q", got, "XYZhello world\n")
	}
	assertNoEditorTempSiblings(t, path)
}

func TestEditorSave_ShrinkingEditStillTruncates(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "shrunk.txt")
	if err := os.WriteFile(path, []byte("aGVsbG8gd29ybGQ=\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	selectEditorBytes(ev, len("aGVsbG8gd29ybGQ=\n"))
	if err := ev.TransformBase64Selection(false); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ev.SaveToFile(nil)
	waitEditorSave(t, ev)
	testutil.DrainPendingTasks()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Errorf("content = %q, want %q", got, "hello world")
	}
}

func TestEditorSave_PreservesTabSizeForCursorLayout(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "tabbed.txt")
	if err := os.WriteFile(path, []byte("\tvalue\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	ev.TabSize = 4
	ev.Engine.SetTabSize(ev.TabSize)
	ev.CursorLine = 0
	ev.CursorPos = 1
	_, before := ev.Engine.LogicalToVisual(ev.CursorPos)
	if before != 4 {
		t.Fatalf("cursor visual column before save = %d, want 4", before)
	}

	ev.Modified = true
	ev.SaveToFile(nil)
	waitEditorSave(t, ev)
	testutil.DrainPendingTasks()

	if ev.Modified {
		t.Fatal("editor still marked modified after save")
	}
	_, after := ev.Engine.LogicalToVisual(ev.CursorPos)
	if after != before {
		t.Fatalf("cursor visual column after save = %d, want %d", after, before)
	}
}

// A save is a write: the file's modification time must move to the time of the
// save and not go back to what it was when the file was opened (f4#1817).
func TestEditorSave_UpdatesModificationTime(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	ev := NewEditorView(piecetable.New([]byte("text")), vfs.NewOSVFS(filepath.Dir(path)), path)
	defer ev.Close()
	f, err := ev.Vfs.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ev.File = f

	ev.CursorPos = len("text")
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	ev.SaveToFile(nil)

	timeout := time.After(5 * time.Second)
	for ev.Saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("save did not finish")
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "text!" {
		t.Fatalf("saved content = %q, want %q", got, "text!")
	}
	if age := time.Since(info.ModTime()); age > time.Minute || age < -time.Minute {
		t.Errorf("modification time after save = %v (%v ago), want about now", info.ModTime(), age)
	}
}
