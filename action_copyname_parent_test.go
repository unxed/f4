package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func waitForCopyNameClipboard(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := vtui.GetClipboard(); got == want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return vtui.GetClipboard()
}

// seedPanelForCopyName wires up a PanelsFrame whose active panel points at
// `path` and shows a `..` entry plus a couple of files, pushing it onto
// FrameManager so withPF handlers find it.
func seedPanelForCopyName(t *testing.T, path string) *PanelsFrame {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	t.Cleanup(func() { pf.Close() })
	pf.ResizeConsole(80, 25)

	fsp := pf.getActivePanel()
	fsp.vfs = vfs.NewOSVFS(path)
	fsp.vfs.SetPath(path)

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "a.txt"}},
	}
	fsp.Refresh()

	vtui.FrameManager.Push(pf)
	return pf
}

func TestAction_PanelCopyName_CursorOnFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.getActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	vtui.SetClipboard("")

	if !RunAction("Panel.CopyName") {
		t.Fatal("Panel.CopyName did not run")
	}
	if got := waitForCopyNameClipboard(t, "a.txt"); got != "a.txt" {
		t.Errorf("clipboard = %q, want %q", got, "a.txt")
	}
}

func TestAction_PanelCopyPath_CursorOnFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.getActivePanel()
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
	if err := os.Mkdir(inner, 0755); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, inner)
	fsp := pf.getActivePanel()
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
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, tmp)
	fsp := pf.getActivePanel()
	fsp.SetCursorIndex(1) // "a.txt"
	pf.cmdLine.Edit.SetText("echo")

	if !RunAction("Panel.InsertPath") {
		t.Fatal("Panel.InsertPath did not run")
	}
	path := filepath.Join(tmp, "a.txt")
	want := "echo " + path
	if runtime.GOOS == "windows" {
		want = `echo "` + path + `"`
	}
	if got := pf.cmdLine.Edit.GetText(); got != want {
		t.Errorf("command line = %q, want %q", got, want)
	}
}

func TestAction_PanelCopyName_CursorOnParentUsesCurrentFolderName(t *testing.T) {
	// t.TempDir() returns a stable, existing dir; take its basename to know
	// what the far2l "cursor on .. = current folder name" rule should yield.
	tmp := t.TempDir()
	inner := filepath.Join(tmp, "some-folder")
	if err := os.Mkdir(inner, 0755); err != nil {
		t.Fatal(err)
	}
	pf := seedPanelForCopyName(t, inner)
	fsp := pf.getActivePanel()
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
