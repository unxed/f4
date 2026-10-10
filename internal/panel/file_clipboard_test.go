package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type recordedFileClipboardOp struct {
	source, target vfs.VFS
	base           string
	names          []string
	dest           string
	move           bool
}

// newFileClipboardFixture returns a frame whose active panel stands in a
// directory holding a.txt and b.txt, both marked, and a recorder that stands
// in for the transfer a paste would start.
func newFileClipboardFixture(t *testing.T) (*PanelsFrame, *FileSystemPanel, string, *[]recordedFileClipboardOp) {
	t.Helper()
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	srcDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pnl := pf.GetActivePanel()
	pnl.Vfs = vfs.NewOSVFS(srcDir)
	pnl.ReadDirectory()
	waitForDirectoryLoads(t)
	testutil.DrainUITasks()
	pnl.ReplaceMarkedNames([]string{"a.txt", "b.txt"})

	var ops []recordedFileClipboardOp
	old := runFileClipboardOp
	runFileClipboardOp = func(source, target vfs.VFS, base string, names []string, dest string, move bool, done func()) {
		ops = append(ops, recordedFileClipboardOp{source, target, base, names, dest, move})
	}
	t.Cleanup(func() { runFileClipboardOp = old })
	return pf, pnl, srcDir, &ops
}

func fileClipboardSeparator() string {
	if runtime.GOOS == "windows" {
		return "\\"
	}
	return "/"
}

func TestFileClipboardCopyThenPasteStartsCopyIntoActivePanel(t *testing.T) {
	pf, pnl, srcDir, ops := newFileClipboardFixture(t)

	if !ActionCopyFilesToClipboard(pf, false) {
		t.Fatal("copy to clipboard not handled")
	}
	want := filepath.Join(srcDir, "a.txt") + "\n" + filepath.Join(srcDir, "b.txt")
	if pf.fileClip == nil {
		t.Fatal("copy to clipboard remembered nothing")
	}
	if pf.fileClip.text != want {
		t.Fatalf("clipboard text = %q, want %q", pf.fileClip.text, want)
	}

	dstDir := t.TempDir()
	pnl.Vfs = vfs.NewOSVFS(dstDir)
	if !pf.pasteFileClipboard(pf.fileClip.text+"\n", nil) {
		t.Fatal("a paste of the text we left on the clipboard did not paste the files")
	}
	if len(*ops) != 1 {
		t.Fatalf("%d transfers started, want 1", len(*ops))
	}
	op := (*ops)[0]
	if op.move || op.base != srcDir || strings.Join(op.names, ",") != "a.txt,b.txt" || op.dest != dstDir+fileClipboardSeparator() {
		t.Fatalf("transfer = %+v, want a copy of a.txt,b.txt from %s into %s", op, srcDir, dstDir)
	}
	if pf.fileClip == nil {
		t.Fatal("a copy forgot the files after one paste")
	}
}

func TestFileClipboardCutPastesAsMoveOnce(t *testing.T) {
	pf, pnl, _, ops := newFileClipboardFixture(t)
	pnl.ReplaceMarkedNames(nil)
	pnl.SetCursorIndex(1)
	name := pnl.GetSelectedNames()[0]

	if !ActionCopyFilesToClipboard(pf, true) {
		t.Fatal("cut to clipboard not handled")
	}
	pnl.Vfs = vfs.NewOSVFS(t.TempDir())
	if !pf.pasteFileClipboard(pf.fileClip.text, nil) {
		t.Fatal("paste did not use the cut files")
	}
	if len(*ops) != 1 || !(*ops)[0].move || len((*ops)[0].names) != 1 || (*ops)[0].names[0] != name {
		t.Fatalf("transfers = %+v, want one move of %q", *ops, name)
	}
	if pf.fileClip != nil {
		t.Fatal("a move left the files on the clipboard")
	}
	if pf.pasteFileClipboard("anything", nil) {
		t.Fatal("a second paste moved the files again")
	}
}

func TestFileClipboardIsDroppedWhenClipboardHoldsSomethingElse(t *testing.T) {
	pf, _, _, ops := newFileClipboardFixture(t)
	ActionCopyFilesToClipboard(pf, false)
	if pf.pasteFileClipboard("text copied elsewhere", nil) {
		t.Fatal("pasted files over a newer clipboard")
	}
	if pf.fileClip != nil || len(*ops) != 0 {
		t.Fatalf("stale clipboard kept: clip=%v ops=%d", pf.fileClip, len(*ops))
	}
}

func TestFileClipboardSurvivesAnUnreadableSystemClipboard(t *testing.T) {
	pf, _, _, ops := newFileClipboardFixture(t)
	ActionCopyFilesToClipboard(pf, false)
	if !pf.pasteFileClipboard("", errors.New("no clipboard here")) || len(*ops) != 1 {
		t.Fatal("an unreadable clipboard lost the remembered files")
	}
}

func TestFileClipboardYieldsToTextOnTheCommandLine(t *testing.T) {
	pf, _, _, ops := newFileClipboardFixture(t)
	if !PanelCanCopyFilesToClipboard(pf) {
		t.Fatal("copy not available with marked files and an empty command line")
	}
	ActionCopyFilesToClipboard(pf, false)
	pf.CmdLine.Edit.SetText("git ")
	if PanelCanCopyFilesToClipboard(pf) {
		t.Fatal("Ctrl+C was taken from a command line that holds text")
	}
	if pf.pasteFileClipboard(pf.fileClip.text, nil) || len(*ops) != 0 {
		t.Fatal("paste went to the files while the command line holds text")
	}
	if pf.fileClip == nil {
		t.Fatal("typing on the command line made the clipboard forget the files")
	}
}

func TestPanelPasteTakesRememberedFilesBeforeText(t *testing.T) {
	pf, pnl, _, ops := newFileClipboardFixture(t)
	old := readPanelClipboard
	t.Cleanup(func() { terminal.WaitForAsyncClipboard(); readPanelClipboard = old })

	ActionCopyFilesToClipboard(pf, false)
	text := pf.fileClip.text
	readPanelClipboard = func(context.Context) (terminal.ClipboardContents, error) {
		return terminal.ClipboardContents{Text: text}, nil
	}
	pnl.Vfs = vfs.NewOSVFS(t.TempDir())
	if !ActionPasteClipboard(pf) {
		t.Fatal("paste not handled")
	}
	terminal.WaitForAsyncClipboard()
	testutil.DrainUITasks()
	if len(*ops) != 1 {
		t.Fatalf("%d transfers started by Ctrl+V, want 1", len(*ops))
	}
	if got := pf.CmdLine.Edit.GetText(); got != "" {
		t.Fatalf("the file paths were typed on the command line: %q", got)
	}
}

func TestPanelPasteExternalFileClipboardStartsCopy(t *testing.T) {
	pf, pnl, _, ops := newFileClipboardFixture(t)
	old := readPanelClipboard
	t.Cleanup(func() { terminal.WaitForAsyncClipboard(); readPanelClipboard = old })

	sourceDir := t.TempDir()
	for _, name := range []string{"from-a.txt", "from-b.txt"} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destDir := t.TempDir()
	pnl.Vfs = vfs.NewOSVFS(destDir)
	paths := []string{filepath.Join(sourceDir, "from-a.txt"), filepath.Join(sourceDir, "from-b.txt")}
	readPanelClipboard = func(context.Context) (terminal.ClipboardContents, error) {
		return terminal.ClipboardContents{Text: vtui.FormatURIList(paths), Files: paths}, nil
	}
	if !ActionPasteClipboard(pf) {
		t.Fatal("paste of external files not handled")
	}
	terminal.WaitForAsyncClipboard()
	testutil.DrainUITasks()
	if len(*ops) != 1 {
		t.Fatalf("%d transfers started by Ctrl+V, want 1", len(*ops))
	}
	op := (*ops)[0]
	if op.base != sourceDir || strings.Join(op.names, ",") != "from-a.txt,from-b.txt" || op.dest != destDir+fileClipboardSeparator() || op.move {
		t.Fatalf("transfer = %+v, want a copy from %s into %s", op, sourceDir, destDir)
	}
}

// f4#1767 (tarlabnor): files copied in another program after f4 copied its
// own are what Ctrl+V pastes, even when the clipboard text still reads as
// the paths f4 put there, or cannot be read at all.
func TestFileClipboardYieldsToOtherFilesOnTheSystemClipboard(t *testing.T) {
	for name, readErr := range map[string]error{"text still ours": nil, "text unreadable": errors.New("no text")} {
		t.Run(name, func(t *testing.T) {
			pf, _, srcDir, ops := newFileClipboardFixture(t)
			ActionCopyFilesToClipboard(pf, false)
			stale := pf.fileClip.text
			other := []string{filepath.Join(srcDir, "c.txt")}
			if !pf.pasteFileClipboardContents(terminal.ClipboardContents{Text: stale, Files: other}, readErr) {
				t.Fatal("the external files were not pasted")
			}
			if pf.fileClip != nil {
				t.Fatal("the files f4 remembered are still kept over newer ones")
			}
			if len(*ops) != 1 || strings.Join((*ops)[0].names, ",") != "c.txt" {
				t.Fatalf("transfers %+v, want c.txt", *ops)
			}
		})
	}
}
