package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// startupEditorRig is a panels frame over a temporary folder, on a frame
// manager of its own, and a way to open a file in an editor the way the open
// action ends: through showEditor.
type startupEditorRig struct {
	t   *testing.T
	pf  *panel.PanelsFrame
	dir string
	v   *vfs.OSVFS
}

func newStartupEditorRig(t *testing.T) *startupEditorRig {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()

	oldSave := panel.SaveSession
	panel.SaveSession = func() {}
	t.Cleanup(func() { panel.SaveSession = oldSave })
	t.Cleanup(func() { armStartupEditor("", nil) })

	dir := t.TempDir()
	v := vfs.NewOSVFS(dir)
	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.Panels[0] = panel.NewFileSystemPanel(0, 0, 40, 20, v)
	pf.Panels[1] = panel.NewFileSystemPanel(40, 0, 40, 20, v.Clone())
	pf.ResizeConsole(120, 60)
	vtui.FrameManager.Push(pf)
	return &startupEditorRig{t: t, pf: pf, dir: dir, v: v}
}

// open writes a file and shows it in an editor; the editor is closed with the
// test.
func (r *startupEditorRig) open(name string) string {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.WriteFile(path, []byte("text\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	f, err := r.v.Open(context.Background(), path)
	if err != nil {
		r.t.Fatal(err)
	}
	showEditor(r.pf, r.v, path, f)
	ev, _ := findOpenedEditor(r.v, path)
	if ev == nil {
		r.t.Fatalf("%s was not opened in an editor", name)
	}
	r.t.Cleanup(ev.Close)
	return path
}

func TestTakeStartupEditorHook_MatchesThePathOnce(t *testing.T) {
	t.Cleanup(func() { armStartupEditor("", nil) })
	called := 0
	dir := t.TempDir()
	v := vfs.NewOSVFS(dir)
	path := filepath.Join(dir, "a.txt")
	armStartupEditor(path, func() { called++ })
	if takeStartupEditorHook(v, filepath.Join(dir, "other.txt")) != nil {
		t.Fatal("another file took the -e editor's hook")
	}
	hook := takeStartupEditorHook(v, path)
	if hook == nil {
		t.Fatal("the -e file did not get its hook")
	}
	hook()
	if called != 1 {
		t.Fatalf("hook ran %d times, want 1", called)
	}
	if takeStartupEditorHook(v, "/tmp/a.txt") != nil {
		t.Error("the hook outlived the editor it was for: opening the file again would end the session")
	}
}

// f4 -e in a session of its own: closing the editor ends the session, without
// the question a quit from the panels asks (issue #1974).
func TestStartupEditorClose_EndsASessionOfItsOwn(t *testing.T) {
	r := newStartupEditorRig(t)
	path := filepath.Join(r.dir, "edit.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	armStartupEditor(path, startupEditorClosed(r.pf, true))
	f, err := r.v.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	showEditor(r.pf, r.v, path, f)
	ev, _ := findOpenedEditor(r.v, path)
	if ev == nil {
		t.Fatal("editor was not opened")
	}

	ev.Close()
	testutil.DrainUITasks()

	if !vtui.FrameManager.IsShutdown() {
		t.Error("closing the -e editor did not end the session")
	}
}

// f4 -e that joined a session already running leaves it running and lets go of
// this terminal only.
func TestStartupEditorClose_LeavesASharedSessionRunning(t *testing.T) {
	r := newStartupEditorRig(t)
	left := 0
	old := leaveSharedSession
	leaveSharedSession = func() { left++ }
	t.Cleanup(func() { leaveSharedSession = old })

	hook := startupEditorClosed(r.pf, false)
	hook()
	testutil.DrainUITasks()

	if left != 1 {
		t.Errorf("the terminal was let go %d times, want 1", left)
	}
	if vtui.FrameManager.IsShutdown() {
		t.Error("a guest's editor ended the session it joined")
	}
}

// A session of its own stays while another editor in it holds unsaved work.
func TestStartupEditorClose_KeepsTheSessionForUnsavedWork(t *testing.T) {
	r := newStartupEditorRig(t)
	r.open("other.txt")
	other, _ := findOpenedEditor(r.v, filepath.Join(r.dir, "other.txt"))
	other.Modified = true

	startupEditorClosed(r.pf, true)()
	testutil.DrainUITasks()

	if vtui.FrameManager.IsShutdown() {
		t.Error("the session ended over an editor with unsaved changes")
	}

	other.Modified = false
	startupEditorClosed(r.pf, true)()
	testutil.DrainUITasks()
	if !vtui.FrameManager.IsShutdown() {
		t.Error("with nothing unsaved left the session did not end")
	}
}
