package editor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// f4#1861: saving a file must not turn a hard or symbolic link to it into a
// separate copy.

func saveEditedThrough(t *testing.T, path, text string, prefix rune) {
	t.Helper()
	ev := NewEditorView(piecetable.New([]byte(text)), vfs.NewOSVFS(filepath.Dir(path)), path)
	defer ev.Close()
	f, err := ev.Vfs.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ev.File = f
	ev.CursorPos = 0
	// An insertion changes the length, so the save goes through the stage.
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: prefix})
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
}

func TestEditorSave_KeepsHardLinks(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.txt"), filepath.Join(dir, "second.txt")
	if err := os.WriteFile(first, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	saveEditedThrough(t, second, "text", 'X')
	if got, _ := os.ReadFile(first); string(got) != "Xtext" {
		t.Fatalf("the other name still reads %q: the link was broken", got)
	}
	a, errA := os.Stat(first)
	b, errB := os.Stat(second)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Fatal("the two names are no longer one file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("the stage was left behind: %d entries", len(entries))
	}
}

func TestEditorSave_WritesThroughSymlink(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target.txt"), filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links need a privilege here: %v", err)
		}
		t.Fatal(err)
	}
	saveEditedThrough(t, link, "text", 'Y')
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file: %v, %v", info.Mode(), err)
	}
	if got, _ := os.ReadFile(target); string(got) != "Ytext" {
		t.Fatalf("the target reads %q", got)
	}
}
