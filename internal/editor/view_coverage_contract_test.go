package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestEditorConfigAndBufferContracts(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := config.App
	t.Cleanup(func() { config.App = oldConfig })
	config.App.EditorUseEditorConfig = true
	config.App.EditorTabSize = 0
	config.App.EditorExpandTabs = 1
	config.App.EditorHighlighter = "None"
	config.App.EditorAutoComplete = true
	config.App.EditorAutoCompleteMask = " ; [bad; *.GO"

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte(`
# comments and blank lines are ignored
; this one too
[*.txt]
indent_style = space
indent_size = 3
[*.go]
indent_style = space
indent_size = 2
tab_width = 4
indent_style = tab
indent_size = invalid
not-a-setting
`), 0600); err != nil {
		t.Fatal(err)
	}

	if editorBufferHasNUL(nil) || editorBufferHasNUL(piecetable.New(nil)) {
		t.Fatal("nil and empty editor buffers should not be binary")
	}
	if !editorBufferHasNUL(piecetable.New([]byte("a\x00b"))) {
		t.Fatal("NUL-containing editor buffer should be binary")
	}

	ev := NewEditorViewWith(piecetable.New([]byte("first\n\nlast")), vfs.NewOSVFS(root), path, true, true)
	defer ev.Close()
	if ev.TabSize != 4 || ev.ExpandTabs != 0 {
		t.Fatalf("editorconfig settings = tab size %d, expand tabs %d", ev.TabSize, ev.ExpandTabs)
	}
	if !ev.acEnabled {
		t.Fatal("case-insensitive autocomplete mask did not match main.go")
	}
	if got, ok := ev.lineTextForHighlight(-1); ok || got != "" {
		t.Fatalf("invalid highlight line = %q, %v", got, ok)
	}
	if got, ok := ev.lineTextForHighlight(0); !ok || got != "first\n" {
		t.Fatalf("first highlight line = %q, %v", got, ok)
	}
	if got, ok := ev.lineTextForHighlight(2); !ok || got != "last" {
		t.Fatalf("last highlight line = %q, %v", got, ok)
	}
	if got, ok := ev.lineTextForHighlight(3); ok || got != "" {
		t.Fatalf("past highlight line = %q, %v", got, ok)
	}

	noConfig := &EditorView{UseEditorConfig: false}
	noConfig.ApplyEditorConfig()
	if got, ok := (extraCaret{off: 4}).selRange(); got != 4 || ok != 4 {
		t.Fatal("unselected extra caret range is incorrect")
	}
	if got, ok := (extraCaret{off: 2, anchor: 5, hasSel: true}).selRange(); got != 2 || ok != 5 {
		t.Fatal("forward extra caret range is incorrect")
	}
	if got, ok := (extraCaret{off: 5, anchor: 2, hasSel: true}).selRange(); got != 2 || ok != 5 {
		t.Fatal("reverse extra caret range is incorrect")
	}

	for _, tc := range []struct {
		ch   rune
		want byte
	}{
		{'0', 0}, {'9', 9}, {'a', 10}, {'f', 15}, {'A', 10}, {'F', 15}, {'x', 0},
	} {
		if got := hexCharToByte(tc.ch); got != tc.want {
			t.Errorf("hexCharToByte(%q) = %d, want %d", tc.ch, got, tc.want)
		}
	}
	if got, err := parseHexPatternToRegex("A ? 0f ??"); err != nil || got != "(?s)\\x0a.\\x0f." {
		t.Fatalf("hex pattern = %q, %v", got, err)
	}
	if got, err := parseHexReplacement("a 0F"); err != nil || string(got) != "\x0a\x0f" {
		t.Fatalf("hex replacement = %x, %v", got, err)
	}
	if _, err := parseHexReplacement("bad"); err == nil {
		t.Fatal("invalid hex replacement was accepted")
	}
}

func TestEditorSmallStateHelpers(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := config.App
	t.Cleanup(func() { config.App = oldConfig })
	config.App.EditorUseEditorConfig = false
	config.App.EditorHighlighter = "None"
	config.App.EditorAutoComplete = false

	if trailingLineTerminator(nil) != nil || trailingLineTerminator([]byte("x")) != nil {
		t.Fatal("unterminated data reported a line ending")
	}
	if got := string(trailingLineTerminator([]byte("x\n"))); got != "\n" {
		t.Fatalf("LF terminator = %q", got)
	}
	if got := string(trailingLineTerminator([]byte("x\r\n"))); got != "\r\n" {
		t.Fatalf("CRLF terminator = %q", got)
	}

	ev := NewEditorView(piecetable.New([]byte("one\r\ntwo\nthree")), nil, "notes.txt")
	defer ev.Close()
	if got := string(ev.lineTerminator(0)); got != "\r\n" {
		t.Fatalf("lineTerminator(0) = %q", got)
	}
	if got := string(ev.lineTerminator(1)); got != "\n" {
		t.Fatalf("lineTerminator(1) = %q", got)
	}
	if got := string(ev.preferredLineEnding(2)); got != "\n" {
		t.Fatalf("preferred line ending = %q", got)
	}
	if got := string(ev.preferredLineEnding(-1)); got != "\n" {
		t.Fatalf("fallback line ending = %q", got)
	}

	ev.CursorLine = 1
	ev.CursorPos = 1
	if first, last := ev.selectedLineSpan(); first != 1 || last != 1 {
		t.Fatalf("unselected line span = %d,%d", first, last)
	}
	ev.RectSelActive = true
	ev.rectSelStartLine = 2
	ev.CursorLine = 0
	if first, last := ev.selectedLineSpan(); first != 0 || last != 2 {
		t.Fatalf("rectangular line span = %d,%d", first, last)
	}
	ev.RectSelActive = false
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorLine = 1
	ev.CursorPos = 0
	if first, last := ev.selectedLineSpan(); first != 0 || last != 0 {
		t.Fatalf("selection ending at line start span = %d,%d", first, last)
	}

	if ev.GetTitle() != "Edit: notes.txt" || ev.GetWorkspaceTabTitle() != "notes.txt" || ev.GetWorkspaceTabMarker() != "E" {
		t.Fatal("editor titles are incorrect")
	}
	ev.DisplayTitle = "Scratch"
	if ev.GetTitle() != "Scratch" || ev.GetWorkspaceTabTitle() != "Scratch" {
		t.Fatal("display title did not override editor titles")
	}
	if ev.IsBusy() || ev.IsSaving() {
		t.Fatal("new editor unexpectedly reports busy")
	}
	if ev.GetType() != vtui.TypeUser+2 {
		t.Fatal("editor frame type is incorrect")
	}
}
