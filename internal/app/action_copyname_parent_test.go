package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func waitForCopyNameClipboard(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := vtui.GetClipboard(); got == want {
			// The worker sets the clipboard before it finishes reading the global
			// FrameManager; join it before the next test replaces the manager.
			terminal.WaitForAsyncClipboard()
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	terminal.WaitForAsyncClipboard()
	return vtui.GetClipboard()
}

// seedPanelForCopyName wires up a panel.PanelsFrame whose active panel points at
// `path` and shows a `..` entry plus a couple of files, pushing it onto
// FrameManager so withPF handlers find it.
func seedPanelForCopyName(t *testing.T, path string) *panel.PanelsFrame {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(func() { pf.Close() })
	pf.ResizeConsole(80, 25)

	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(path)
	if err := fsp.Vfs.SetPath(path); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a.txt"}},
	}
	fsp.Refresh()

	vtui.FrameManager.Push(pf)
	return pf
}

func TestAction_PanelCopyName_CursorOnFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyName") {
		t.Fatal("Panel.CopyName did not run")
	}
	if got := waitForCopyNameClipboard(t, "a.txt"); got != "a.txt" {
		t.Errorf("clipboard = %q, want %q", got, "a.txt")
	}
}

func TestAction_PanelCopyName_CopiesCommandLineWhenNotEmpty(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	pf.CmdLine.Edit.SetText("echo hello world")
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyName") {
		t.Fatal("Panel.CopyName did not run")
	}
	if got := waitForCopyNameClipboard(t, "echo hello world"); got != "echo hello world" {
		t.Errorf("clipboard = %q, want %q", got, "echo hello world")
	}
}

func TestAction_PanelCopyPath_CursorOnFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyPath") {
		t.Fatal("Panel.CopyPath did not run")
	}
	want := filepath.Join(tmp, "a.txt")
	if got := waitForCopyNameClipboard(t, want); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}

func TestAction_PanelCopyPath_CursorOnParentUsesCurrentFolderPath(t *testing.T) {
	tmp := t.TempDir()
	inner := filepath.Join(tmp, "some-folder")
	if err := os.Mkdir(inner, 0700); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, inner)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(0) // ".."
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyPath") {
		t.Fatal("Panel.CopyPath did not run")
	}
	if got := waitForCopyNameClipboard(t, inner); got != inner {
		t.Errorf("cursor-on-.. clipboard = %q, want %q", got, inner)
	}
}

func TestAction_PanelInsertPath_CursorOnFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	pf.CmdLine.Edit.SetText("echo")

	if !RunAction("Panel.InsertPath") {
		t.Fatal("Panel.InsertPath did not run")
	}
	path := filepath.Join(tmp, "a.txt")
	// A path without spaces or cmd metacharacters is inserted bare on every
	// platform; backslashes are Windows path separators, not a reason to quote.
	want := "echo " + path
	if got := pf.CmdLine.Edit.GetText(); got != want {
		t.Errorf("command line = %q, want %q", got, want)
	}
}

func TestAction_PanelInsertFileName_AppendsSpaceWithoutLeadingSeparator(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p := seedPanelForCopyName(t, tmp)
	fsp := p.GetActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"

	base := tmp + string(filepath.Separator)
	p.CmdLine.Edit.SetText(base)
	if !RunAction("Panel.InsertFileName") {
		t.Fatal("Panel.InsertFileName did not run")
	}

	want := base + "a.txt "
	if got := p.CmdLine.Edit.GetText(); got != want {
		t.Errorf("command line = %q, want %q", got, want)
	}
	if !RunAction("Panel.InsertFileName") {
		t.Fatal("second Panel.InsertFileName did not run")
	}
	want += "a.txt "
	if got := p.CmdLine.Edit.GetText(); got != want {
		t.Errorf("repeated insertion = %q, want %q", got, want)
	}
}

func TestAction_PanelCopyName_CursorOnParentUsesCurrentFolderName(t *testing.T) {
	// t.TempDir() returns a stable, existing dir; take its basename to know
	// what the far2l "cursor on .. = current folder name" rule should yield.
	tmp := t.TempDir()
	inner := filepath.Join(tmp, "some-folder")
	if err := os.Mkdir(inner, 0700); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, inner)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(0) // ".."
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyName") {
		t.Fatal("Panel.CopyName did not run")
	}
	want := "some-folder"
	if got := waitForCopyNameClipboard(t, want); got != want {
		t.Errorf("cursor-on-.. clipboard = %q, want %q", got, want)
	}
}

// f4 #1408: with the command line empty, marked files take priority over the
// cursor item, matching far2l/Far3 — the same names CtrlShiftIns already
// copies via Panel.CopySelectedNames.
func TestAction_PanelCopyName_MarkedFilesTakePriorityOverCursor(t *testing.T) {
	tmp := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(tmp, n), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pf := seedMarkedPanel(t, tmp, []string{"a.txt", "b.txt", "c.txt"}, 2)
	fsp := pf.GetActivePanel()
	fsp.SetCursorIndex(3) // "c.txt", unmarked — must not win over the marks
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyName") {
		t.Fatal("Panel.CopyName did not run")
	}
	want := "a.txt\nb.txt"
	if got := waitForMarkedClipboard(t, want); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}
