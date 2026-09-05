package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

func TestConvertLineEndings(t *testing.T) {
	cases := []struct {
		in, eol, want string
	}{
		{"a\nb\r\nc\rd", "\r\n", "a\r\nb\r\nc\r\nd"},
		{"a\nb\r\nc\rd", "\n", "a\nb\nc\nd"},
		{"a\nb\r\nc\rd", "\r", "a\rb\rc\rd"},
		{"a\r\nb\r\n", "\r\n", "a\r\nb\r\n"},
		{"", "\n", ""},
		{"no breaks", "\r\n", "no breaks"},
		{"trailing\r", "\n", "trailing\n"},
		{"x\n", "", "x\n"},
	}
	for _, c := range cases {
		got := convertLineEndings([]byte(c.in), []byte(c.eol))
		if string(got) != c.want {
			t.Errorf("convertLineEndings(%q, %q) = %q, want %q", c.in, c.eol, got, c.want)
		}
	}
	// Nothing to do: the input itself comes back, so the caller can skip
	// replacing the buffer.
	same := []byte("a\r\nb")
	if got := convertLineEndings(same, []byte("\r\n")); &got[0] != &same[0] {
		t.Error("unchanged input was copied")
	}
}

func TestStripEncodedBOM(t *testing.T) {
	if got := stripEncodedBOM([]byte{0xFF, 0xFE, 'a', 0}, 1200); string(got) != "a\x00" {
		t.Errorf("UTF-16LE: %x", got)
	}
	if got := stripEncodedBOM([]byte{0xFE, 0xFF, 0, 'a'}, 1201); string(got) != "\x00a" {
		t.Errorf("UTF-16BE: %x", got)
	}
	if got := stripEncodedBOM([]byte{0xFF, 0xFE, 0, 0, 'a', 0, 0, 0}, 12000); len(got) != 4 {
		t.Errorf("UTF-32LE: %x", got)
	}
	if got := stripEncodedBOM([]byte("abc"), 1251); string(got) != "abc" {
		t.Errorf("non-unicode codepage must be untouched: %q", got)
	}
}

// pumpEditorUntil runs UI tasks until cond holds.
func pumpEditorUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !cond() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline.C:
			t.Fatal("timeout waiting for the editor")
		}
	}
}

// TestEditorSaveAs_NewPathCodepageAndLineBreaks is the whole Shift+F2 round
// trip (#899): the buffer lands in a new file, in the chosen codepage, with
// the chosen line breaks, and the editor now edits that file.
func TestEditorSaveAs_NewPathCodepageAndLineBreaks(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("привет\nмир\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	target := filepath.Join(dir, "copy.txt")
	ev.saveAs(target, 1251, false, saveAsEOLDos)
	pumpEditorUntil(t, func() bool { return ev.filePath == target && !ev.saving && !ev.modified })
	drainPendingTasks()

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	want := []byte{0xEF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2, '\r', '\n', 0xEC, 0xE8, 0xF0, '\r', '\n'}
	if string(got) != string(want) {
		t.Errorf("target = %x, want %x", got, want)
	}
	if orig, err := os.ReadFile(path); err != nil || string(orig) != "привет\nмир\n" {
		t.Errorf("source file changed: %q, %v", orig, err)
	}
	if ev.Codepage != 1251 {
		t.Errorf("editor codepage = %d, want 1251", ev.Codepage)
	}
	if ev.li.LineCount() != 3 {
		t.Errorf("line count after conversion = %d, want 3", ev.li.LineCount())
	}
	// The bytes behind the text changed, so the old undo states are gone
	// rather than left to load garbage.
	if len(ev.undoStack) != 0 || len(ev.redoStack) != 0 {
		t.Errorf("undo history survived a byte-level rewrite: %d/%d", len(ev.undoStack), len(ev.redoStack))
	}
	if text, err := ev.pt.Bytes(); err != nil || string(text) != "привет\r\nмир\r\n" {
		t.Errorf("reopened text = %q, %v", text, err)
	}
	assertNoEditorTempSiblings(t, target)
}

// TestEditorSaveAs_UTF8BOMAndUTF16WithoutBOM covers the signature checkbox
// for the two codepage families that treat it differently.
func TestEditorSaveAs_UTF8BOMAndUTF16WithoutBOM(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("ab\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	withBOM := filepath.Join(dir, "bom.txt")
	ev.saveAs(withBOM, 65001, true, saveAsEOLKeep)
	pumpEditorUntil(t, func() bool { return ev.filePath == withBOM && !ev.saving && !ev.modified })
	if got, _ := os.ReadFile(withBOM); string(got) != "\xEF\xBB\xBFab\n" {
		t.Errorf("UTF-8 with BOM = %x", got)
	}
	if !ev.utf8BOM {
		t.Error("editor forgot the UTF-8 BOM it just wrote")
	}

	utf16 := filepath.Join(dir, "u16.txt")
	ev.saveAs(utf16, 1200, false, saveAsEOLKeep)
	pumpEditorUntil(t, func() bool { return ev.filePath == utf16 && !ev.saving && !ev.modified })
	if got, _ := os.ReadFile(utf16); string(got) != "a\x00b\x00\n\x00" {
		t.Errorf("UTF-16LE without BOM = %x", got)
	}
	if text, err := ev.pt.Bytes(); err != nil || string(text) != "ab\n" {
		t.Errorf("reopened text = %q, %v", text, err)
	}
	drainPendingTasks()
}

// TestEditorSaveAs_ExistingTargetAsksBeforeOverwriting: a name that is
// already taken is never replaced silently.
func TestEditorSaveAs_ExistingTargetAsksBeforeOverwriting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "taken.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev := openLocalEditor(t, dir, path)
	defer ev.Close()

	ev.saveAs(target, 65001, false, saveAsEOLKeep)
	var confirm *vtui.Window
	pumpEditorUntil(t, func() bool {
		if w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok && w != nil && w.OnResult != nil && strings.TrimSpace(w.GetTitle()) == strings.TrimSpace(Msg("SaveAs.Title")) {
			confirm = w
			return true
		}
		return false
	})
	if got, _ := os.ReadFile(target); string(got) != "old\n" {
		t.Fatalf("target replaced before the user answered: %q", got)
	}
	confirm.Close()
	confirm.OnResult(0)
	pumpEditorUntil(t, func() bool { return ev.filePath == target && !ev.saving && !ev.modified })
	if got, _ := os.ReadFile(target); string(got) != "new\n" {
		t.Errorf("target after confirmed overwrite = %q", got)
	}
	drainPendingTasks()
}

func TestEditorSaveAs_ResolvesRelativeNameNextToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	ev := openLocalEditor(t, dir, path)
	defer ev.Close()
	if got := ev.resolveSaveAsPath("  b.txt "); got != filepath.Join(dir, "b.txt") {
		t.Errorf("relative name resolved to %q", got)
	}
	if got := ev.resolveSaveAsPath(""); got != "" {
		t.Errorf("empty name resolved to %q", got)
	}
	if got := ev.resolveSaveAsPath(path); got != path {
		t.Errorf("absolute name changed to %q", got)
	}
}
