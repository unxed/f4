package app

import (
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// seedPanelForRestore wires up a panel.PanelsFrame whose active panel shows the
// given entries and pushes it onto FrameManager so withPF handlers find it.
func seedPanelForRestore(t *testing.T, names []string) *panel.PanelsFrame {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(func() { pf.Close() })
	pf.ResizeConsole(80, 25)

	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(".")
	// panel.NewFileSystemPanel starts an asynchronous directory read. Let it finish
	// before returning, otherwise its completion task leaks into the shared
	// FrameManager queue and corrupts the next test that drains it (and, if
	// this test swaps the VFS, the late callback panics against the new VFS).
	paneltest.WaitForLoad(t, fsp)

	entries := []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	for _, n := range names {
		entries = append(entries, &panel.FileEntry{VFSItem: vfs.VFSItem{Name: n}})
	}
	fsp.Entries = entries
	fsp.Refresh()

	vtui.FrameManager.Push(pf)
	return pf
}

func collectSelected(fsp *panel.FileSystemPanel) []string {
	var out []string
	for _, e := range fsp.Entries {
		if e.Selected {
			out = append(out, e.Name)
		}
	}
	return out
}

func TestFileSystemPanel_SaveRestoreSelection_Swaps(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"a.txt", "b.txt", "c.txt"})
	fsp := pf.GetActivePanel()

	// Initial state: mark a.txt.
	fsp.SetItemSelected(1, true)

	// Take a snapshot and mutate: mark c.txt, unmark a.txt.
	fsp.SaveSelection()
	fsp.SetItemSelected(1, false)
	fsp.SetItemSelected(3, true)

	// First RestoreSelection: back to the snapshot state (a.txt marked, c.txt not).
	fsp.RestoreSelection()
	if got := collectSelected(fsp); len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("after 1st restore: selected = %v, want [a.txt]", got)
	}

	// Second RestoreSelection: back to the state before the first restore
	// (c.txt marked, a.txt not). Ctrl+M is a two-way swap.
	fsp.RestoreSelection()
	if got := collectSelected(fsp); len(got) != 1 || got[0] != "c.txt" {
		t.Fatalf("after 2nd restore: selected = %v, want [c.txt]", got)
	}
}

func TestFileSystemPanel_RestoreSelection_LeavesParentAlone(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"a.txt"})
	fsp := pf.GetActivePanel()

	// Force ".." into an impossible "PrevSelected=true" state — RestoreSelection
	// must not resurrect it into Selected. Guards against a regression where
	// far2l's Select() skip for parent-dir entries would not be honoured.
	fsp.Entries[0].PrevSelected = true

	fsp.RestoreSelection()
	if fsp.Entries[0].Selected {
		t.Error("RestoreSelection propagated selection to the '..' entry")
	}
}

func TestFileSystemPanel_PreviousSelectionIsDirectoryScoped(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "same.txt"), []byte(dir), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pf := seedPanelForRestore(t, nil)
	fsp := pf.GetActivePanel()
	fsp.Vfs = vfs.NewOSVFS(first)
	fsp.ReadDirectory()
	paneltest.WaitForLoad(t, fsp)
	if !fsp.SetSelectedByName("same.txt", true) {
		t.Fatal("first directory entry missing")
	}
	fsp.SaveSelection()
	fsp.SetSelectedByName("same.txt", false)

	if err := fsp.Vfs.SetPath(second); err != nil {
		t.Fatal(err)
	}
	fsp.ReadDirectory()
	paneltest.WaitForLoad(t, fsp)
	fsp.RestoreSelection()
	if fsp.IsNameSelected("same.txt") {
		t.Fatal("Ctrl+M applied the prior directory's same-named selection")
	}
}

func TestAction_PanelRestoreSelection_InvertRoundtrip(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"a.txt", "b.txt", "c.txt"})
	fsp := pf.GetActivePanel()

	// Mark just a.txt.
	fsp.SetItemSelected(1, true)

	// Invert: expect {b, c} marked. InvertSelection must snapshot first.
	if !RunAction("Panel.InvertSelection") {
		t.Fatal("Panel.InvertSelection did not run")
	}
	got := collectSelected(fsp)
	if len(got) != 2 || got[0] != "b.txt" || got[1] != "c.txt" {
		t.Fatalf("after invert: selected = %v, want [b.txt c.txt]", got)
	}

	// Ctrl+M restores the pre-invert state: only a.txt marked.
	if !RunAction("Panel.RestoreSelection") {
		t.Fatal("Panel.RestoreSelection did not run")
	}
	got = collectSelected(fsp)
	if len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("after restore: selected = %v, want [a.txt]", got)
	}
}

func TestFileSystemPanel_ApplyMaskSelection_SnapshotsBeforeMutating(t *testing.T) {
	pf := seedPanelForRestore(t, []string{"a.txt", "b.txt", "c.log"})
	fsp := pf.GetActivePanel()

	// Mark a.txt as the starting state.
	fsp.SetItemSelected(1, true)

	// Mass-select all *.log: only c.log gets marked. a.txt loses no marks.
	fsp.ApplyMaskSelection("*.log", true)
	got := collectSelected(fsp)
	if len(got) != 2 || got[0] != "a.txt" || got[1] != "c.log" {
		t.Fatalf("after mask: selected = %v, want [a.txt c.log]", got)
	}

	// Ctrl+M restores the pre-mask state: only a.txt marked.
	fsp.RestoreSelection()
	got = collectSelected(fsp)
	if len(got) != 1 || got[0] != "a.txt" {
		t.Fatalf("after restore: selected = %v, want [a.txt]", got)
	}
}

func TestHotkeyManager_RestoreSelectionDefault_Issue289(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()
	if got := hm.GetAction("Shell", "CtrlM"); got != "Panel.RestoreSelection" {
		t.Errorf("Shell/CtrlM: got %q, want %q", got, "Panel.RestoreSelection")
	}
}

func TestSelectFromClipboardActionIsDiscoverable(t *testing.T) {
	act, ok := GetAction("Panel.SelectFromClipboard")
	if !ok || act.Area != "Shell" || act.MenuPath != "Files" || act.Handler == nil {
		t.Fatal("clipboard selection is not registered for panels and the Files menu")
	}
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()
	if got := hm.Bindings["Shell"]["CtrlS"]; got != "Panel.SelectFromClipboard:NoTerminalApp" {
		t.Fatalf("Ctrl+S binding = %q", got)
	}
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = hm
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = old })
	for _, menu := range BuildMenuBarItems("Shell") {
		for _, item := range menu.SubItems {
			if plainMenuText(item.Text) == plainMenuText(act.DisplayLabel()) {
				if item.Shortcut != "Ctrl+S" {
					t.Fatalf("menu shortcut = %q", item.Shortcut)
				}
				return
			}
		}
	}
	t.Fatal("clipboard selection is missing from the menu")
}
