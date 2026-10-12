package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func dirSizeFixture(t *testing.T) (*panel.PanelsFrame, *panel.FileSystemPanel, map[string]*panel.FileEntry) {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmp := t.TempDir()
	sizes := map[string]int{"a": 3, "b": 5, "c": 7}
	for name, n := range sizes {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, name, "f.bin"), make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	vtui.FrameManager.Push(pf) // FindPanelsFrame, which the Enabled predicates use, looks here
	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(tmp)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*panel.FileEntry{"..": {VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	list := []*panel.FileEntry{entries[".."]}
	for _, name := range []string{"a", "b", "c"} {
		entries[name] = &panel.FileEntry{VFSItem: vfs.VFSItem{Name: name, IsDir: true}}
		list = append(list, entries[name])
	}
	fsp.Entries = list
	return pf, fsp, entries
}

func pumpSizeTasksUntil(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the folder sizes were never calculated")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// f4#1795: with folders marked, F3 sizes the marked ones and leaves the one
// under the cursor alone.
func TestViewOnADirectorySizesTheMarkedOnes(t *testing.T) {
	pf, fsp, e := dirSizeFixture(t)
	e["a"].Selected, e["c"].Selected = true, true
	fsp.SetCursorIndex(2) // b: not marked
	actionViewFile(pf)
	pumpSizeTasksUntil(t, func() bool { return e["a"].SizeCalculated && e["c"].SizeCalculated })
	if e["a"].Size != 3 || e["c"].Size != 7 {
		t.Errorf("marked sizes = %d and %d, want 3 and 7", e["a"].Size, e["c"].Size)
	}
	if e["b"].SizeCalculated {
		t.Error("the unmarked folder under the cursor was sized as well")
	}
}

// Nothing marked: F3 sizes the folder under the cursor, as before.
func TestViewOnADirectorySizesTheCursorOneWhenNothingIsMarked(t *testing.T) {
	pf, fsp, e := dirSizeFixture(t)
	fsp.SetCursorIndex(2)
	actionViewFile(pf)
	pumpSizeTasksUntil(t, func() bool { return e["b"].SizeCalculated })
	if e["b"].Size != 5 || e["a"].SizeCalculated || e["c"].SizeCalculated {
		t.Errorf("sizes: b=%d, a calculated=%v, c calculated=%v", e["b"].Size, e["a"].SizeCalculated, e["c"].SizeCalculated)
	}
}

// F3 is live on "..": it sizes the folder the panel shows.
func TestViewIsEnabledOnTheParentRow(t *testing.T) {
	_, fsp, _ := dirSizeFixture(t)
	fsp.SetCursorIndex(0)
	if !viewEnabled() {
		t.Error("F3 is dimmed on \"..\"")
	}
	if cursorEntryEnabled() {
		t.Error("F4 must stay unavailable on \"..\"")
	}
	if viewKeyBarLabel() == "" {
		t.Error("the F3 label on \"..\" is not Size")
	}
}
