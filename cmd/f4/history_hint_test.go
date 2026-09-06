package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// waitForHistoryClipboard mirrors waitForMarkedClipboard: SetClipboard runs
// off the UI goroutine, so we poll for a short deadline.
func waitForHistoryClipboard(t *testing.T, want string) string {
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

// TestHistoryHint_MessagesResolved guards against a typo in the language
// bundle keys — if the message isn't loaded, vtui.Msg returns "{key}" and
// the bottom border of the Alt+F8/Alt+F12 dialogs would show a placeholder.
func TestHistoryHint_MessagesResolved(t *testing.T) {
	for _, key := range []string{"History.CommandsHint", "History.FoldersHint", "History.ViewEditHint"} {
		s := Msg(key)
		if s == "" || strings.HasPrefix(s, "{") {
			t.Errorf("Msg(%q) not resolved: %q", key, s)
			continue
		}
		// Each hint must at least reference Enter (paste/goto) and
		// Shift+Del (delete), the two shortcuts issue #290 called out as
		// undocumented.
		if !strings.Contains(s, "Enter") {
			t.Errorf("Msg(%q) missing Enter hint: %q", key, s)
		}
		if !strings.Contains(s, "Shift+Del") {
			t.Errorf("Msg(%q) missing Shift+Del hint: %q", key, s)
		}
		if !strings.Contains(s, "Ins") {
			t.Errorf("Msg(%q) missing Insert/pin hint: %q", key, s)
		}
	}
}

// TestActionCommandHistory_WiresHint verifies actionCommandHistory installs
// a historySearch with Msg("History.CommandsHint") — the string the F8
// dialog actually paints on its bottom border.
func TestActionCommandHistory_WiresHint(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	pf.cmdLine.Edit.History = []string{"ls -la", "grep -r foo ."}

	activeHistorySearch = nil
	t.Cleanup(func() {
		if activeHistorySearch != nil {
			activeHistorySearch.cleanup()
		}
	})

	actionCommandHistory(pf)

	if activeHistorySearch == nil {
		t.Fatal("actionCommandHistory did not install a historySearch")
	}
	want := Msg("History.CommandsHint")
	if got := activeHistorySearch.hint; got != want {
		t.Errorf("commands hint = %q, want %q", got, want)
	}
}

func TestActionCommandHistoryInsertPersistsLock(t *testing.T) {
	initHistoryTestScreen(t)
	hp := &F4HistoryProvider{
		path: filepath.Join(t.TempDir(), "history.json"),
		data: make(map[string][]string),
		rich: map[string][]HistoryRecord{"cmdline": {{Name: "echo pinned"}}},
	}
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = hp
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	pf.cmdLine.Edit.History = []string{"echo pinned"}

	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if activeHistorySearch == nil || !activeHistorySearch.supportsLocks || !activeHistorySearch.showDetails {
		t.Fatal("command history capabilities are not enabled")
	}
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_INSERT})
	records := hp.LoadRichHistory("cmdline")
	if len(records) != 1 || !records[0].Lock {
		t.Fatalf("Insert did not persist command lock: %#v", records)
	}
}

func TestActionFoldersHistory_WiresHint(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(120, 40)

	// actionFoldersHistory uses GlobalHistoryProvider — seed it with an in-memory
	// stub so the dialog has something to render.
	prev := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = &stubHistoryProvider{"folders": {"/tmp", "/home"}}
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	activeHistorySearch = nil
	t.Cleanup(func() {
		if activeHistorySearch != nil {
			activeHistorySearch.cleanup()
		}
	})

	actionFoldersHistory(pf)

	if activeHistorySearch == nil {
		t.Fatal("actionFoldersHistory did not install a historySearch")
	}
	if activeHistorySearch.supportsLocks {
		t.Fatal("non-persistent folder history exposed a fake lock column")
	}
	want := Msg("History.FoldersHint")
	if got := activeHistorySearch.hint; got != want {
		t.Errorf("folders hint = %q, want %q", got, want)
	}
}

func TestActionFoldersHistoryInsertPersistsLock(t *testing.T) {
	initHistoryTestScreen(t)
	// Ins now also claims a folder bookmark slot (#407), so the bookmark
	// table has to resolve inside a temp profile, not the developer's own.
	setupPortableIni(t, "0")
	hp := &F4HistoryProvider{
		path: filepath.Join(t.TempDir(), "history.json"),
		data: map[string][]string{"folders": {"C:\\newest", "C:\\older"}},
		rich: make(map[string][]HistoryRecord),
	}
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = hp
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(120, 40)

	actionFoldersHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if activeHistorySearch == nil || !activeHistorySearch.supportsLocks {
		t.Fatal("folder history did not enable persistent locking")
	}
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_INSERT})
	records, _ := loadFolderHistoryRecords(hp)
	if len(records) != 2 || !records[0].Lock {
		t.Fatalf("Insert did not persist selected folder lock: %#v", records)
	}
	menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE,
		ControlKeyState: vtinput.ShiftPressed,
	})
	records, _ = loadFolderHistoryRecords(hp)
	if len(records) != 2 || !records[0].Lock {
		t.Fatalf("Shift+Del removed locked folder: %#v", records)
	}
}

type stubHistoryProvider map[string][]string

func (s stubHistoryProvider) LoadHistory(name string) []string { return s[name] }
func (s stubHistoryProvider) SaveHistory(name string, h []string) {
	dup := append([]string(nil), h...)
	s[name] = dup
}

// TestActionCommandHistory_CtrlIns_CopiesToClipboard exercises the far2l
// Ctrl+Ins shortcut we added: copy the highlighted entry to the system
// clipboard. Same VMenu key path also handles Ctrl+C.
func TestActionCommandHistory_CtrlIns_CopiesToClipboard(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	pf.cmdLine.Edit.History = []string{"echo one", "echo two", "echo three"}
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	// applyFilter puts newest at bottom → select "echo two" (middle).
	menu.SetSelectPos(1)
	vtui.SetClipboard("")

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_INSERT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if got := waitForHistoryClipboard(t, "echo two"); got != "echo two" {
		t.Errorf("clipboard = %q, want %q", got, "echo two")
	}

	menu.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

// TestActionCommandHistory_Del_ClearsAllAfterConfirm covers the far2l Del
// shortcut: prompt, then wipe the whole history and close the menu.
func TestActionCommandHistory_Del_ClearsAllAfterConfirm(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	prev := vtui.GlobalHistoryProvider
	saved := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &saved
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	pf.cmdLine.Edit.History = []string{"cmd1", "cmd2"}
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	menu.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_DELETE,
	})
	// The confirm dialog is now on top; click Ok (button index 0).
	confirm, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok || confirm.OnResult == nil {
		t.Fatalf("expected confirmation dialog, got %T", vtui.FrameManager.GetTopFrame())
	}
	confirm.OnResult(0)

	if len(pf.cmdLine.Edit.History) != 0 {
		t.Errorf("history not cleared: %v", pf.cmdLine.Edit.History)
	}
	if len(saved["cmdline"]) != 0 {
		t.Errorf("provider not wiped: %v", saved["cmdline"])
	}
}

// TestActionCommandHistory_Del_CancelKeepsHistory guards against the same
// path silently wiping the history when the user says Cancel.
func TestActionCommandHistory_Del_CancelKeepsHistory(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	prev := vtui.GlobalHistoryProvider
	saved := stubHistoryProvider{"cmdline": {"cmd1", "cmd2"}}
	vtui.GlobalHistoryProvider = &saved
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	pf.cmdLine.Edit.History = []string{"cmd1", "cmd2"}
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	menu.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_DELETE,
	})
	confirm := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	confirm.OnResult(1) // Cancel

	if len(pf.cmdLine.Edit.History) != 2 {
		t.Errorf("history unexpectedly changed: %v", pf.cmdLine.Edit.History)
	}
}

// initHistoryTestScreen sets up a fully-sized SilentScreenBuf for a test —
// historySearch.resize() bails out early when the FrameManager screen has
// zero width, which leaves the underlying VMenu without geometry and
// makes mouse-click coordinates land nowhere.
func initHistoryTestScreen(_ *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 40)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()
}

// TestActionCommandHistory_HelpTopic pins the F1 help topic name so
// FrameManager routes to the History page instead of the generic
// Panels topic (issue #290 follow-up).
func TestActionCommandHistory_HelpTopic(t *testing.T) {
	initHistoryTestScreen(t)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	pf.cmdLine.Edit.History = []string{"a", "b"}
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if got := menu.GetHelp(); got != "History" {
		t.Errorf("commands help topic = %q, want %q", got, "History")
	}
	menu.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionFoldersHistory_HelpTopic(t *testing.T) {
	initHistoryTestScreen(t)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	prev := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = &stubHistoryProvider{"folders": {"/tmp"}}
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	actionFoldersHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if got := menu.GetHelp(); got != "HistoryFolders" {
		t.Errorf("folders help topic = %q, want %q", got, "HistoryFolders")
	}
	menu.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

// TestHistoryHelpTopics_LoadedInHelpEngine catches typos in the .hlf
// entries — if either topic is missing, F1 in the dialog would render
// an empty "topic not found" page.
func TestHistoryHelpTopics_LoadedInHelpEngine(t *testing.T) {
	InitHelpSystem()
	for _, name := range []string{"History", "HistoryFolders", "HistoryViewEdit"} {
		if topic := vtui.GlobalHelpEngine.GetTopic(name); topic == nil {
			t.Errorf("help topic %q not registered in engine", name)
		}
	}
}

// TestActionCommandHistory_MouseClickPastesEntry — clicking a menu row
// must accept it just like Enter (VMenu.ProcessMouse fires OnAction on
// left click, and the dialog needs to wire OnAction to the paste path).
func TestActionCommandHistory_MouseClickPastesEntry(t *testing.T) {
	initHistoryTestScreen(t)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	// History convention: [0] is newest. historySearch shows oldest at the
	// top and newest at the bottom, so bottom-row = "echo newest".
	pf.cmdLine.Edit.History = []string{"echo newest", "echo oldest"}
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	x1, y1, _, _ := menu.GetPosition()
	if menu.SelectPos != 1 {
		t.Fatalf("test invariant: SelectPos=%d, expected 1 (newest at bottom row)", menu.SelectPos)
	}
	rowY := y1 + 1 + (menu.SelectPos - menu.TopPos) // menu content starts 1 row below the top border

	menu.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		MouseX:      testInt16(x1 + 2),
		MouseY:      testInt16(rowY),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	if got := pf.cmdLine.Edit.GetText(); got != "echo newest" {
		t.Errorf("cmdline after click = %q, want %q", got, "echo newest")
	}
	if !menu.IsDone() {
		t.Error("menu should be closed after click accept")
	}
}

func TestActionCommandHistory_PathColumnAndInsertion(t *testing.T) {
	initHistoryTestScreen(t)
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{"cmdline": {"echo newest"}}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	path := filepath.Join(t.TempDir(), "folder with space")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	rememberCommandHistoryPath("echo newest", path, provider["cmdline"])

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	search := activeHistorySearch
	if search == nil || search.selectedSecondary() != path || !search.showSecond {
		t.Fatalf("path column was not initialized: search=%#v", search)
	}

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F2,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if search.showSecond {
		t.Fatal("Ctrl+F2 did not hide the path column")
	}
	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F2,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if !search.showSecond {
		t.Fatal("Ctrl+F2 did not restore the path column")
	}

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})
	want := `"` + path + `"`
	if runtime.GOOS != "windows" {
		want = "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	}
	if got := pf.cmdLine.Edit.GetText(); got != want {
		t.Fatalf("Ctrl+Shift+Enter inserted %q, want %q", got, want)
	}
}

func TestActionCommandHistory_CtrlPgDnNavigatesToStoredPath(t *testing.T) {
	initHistoryTestScreen(t)
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{"cmdline": {"echo newest"}}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	target := t.TempDir()
	rememberCommandHistoryPath("echo newest", target, provider["cmdline"])
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))
	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_NEXT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if got := pf.getActivePanel().vfs.GetPath(); got != target {
		t.Fatalf("Ctrl+PgDn navigated to %q, want %q", got, target)
	}
	if !menu.IsDone() {
		t.Fatal("history menu stayed open after Ctrl+PgDn")
	}
	waitForLoad(t, pf.getActivePanel())
}

// TestActionFoldersHistory_MouseClickNavigates guards the folder-history
// counterpart: click on a row must cd to that path on the active panel.
func TestActionFoldersHistory_MouseClickNavigates(t *testing.T) {
	initHistoryTestScreen(t)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	// Provider convention: [0] is newest. The bottom row of the dialog
	// shows the newest entry, so clicking it should cd to `newest`.
	newest := t.TempDir()
	oldest := t.TempDir()
	prev := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = &stubHistoryProvider{"folders": {newest, oldest}}
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	actionFoldersHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	x1, y1, _, _ := menu.GetPosition()
	if menu.SelectPos != 1 {
		t.Fatalf("test invariant: SelectPos=%d, expected 1", menu.SelectPos)
	}
	rowY := y1 + 1 + (menu.SelectPos - menu.TopPos)

	menu.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		MouseX:      testInt16(x1 + 2),
		MouseY:      testInt16(rowY),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	if got := pf.getActivePanel().vfs.GetPath(); got != newest {
		t.Errorf("active panel path after click = %q, want %q", got, newest)
	}
	if !menu.IsDone() {
		t.Error("folder-history menu should be closed after click accept")
	}
	waitForLoad(t, pf.getActivePanel())
}

// TestHistorySearch_KeepsPainterAcrossModalOverlay guards issue #290
// regression: pushing another frame on top of the history menu (e.g.
// F1 help) must not tear down the bottom-border painter, otherwise the
// hint stays gone even after the overlay closes. installRenderer had
// been eagerly cleaning up on !IsTop; the fix is to cleanup only when
// the menu itself IsDone.
func TestHistorySearch_KeepsPainterAcrossModalOverlay(t *testing.T) {
	initHistoryTestScreen(t)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	pf.cmdLine.Edit.History = []string{"cmd1", "cmd2"}
	activeHistorySearch = nil
	t.Cleanup(func() {
		if activeHistorySearch != nil {
			activeHistorySearch.cleanup()
		}
	})
	actionCommandHistory(pf)
	if activeHistorySearch == nil {
		t.Fatal("actionCommandHistory did not install a historySearch")
	}
	installed := activeHistorySearch

	// Push a dummy overlay dialog on top of the menu — mimics F1 help.
	overlay := vtui.ShowMessage(" Help ", "help text", []string{"&Ok"})
	// A render pass with the overlay on top used to eagerly cleanup.
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	if vtui.FrameManager.OnRender != nil {
		vtui.FrameManager.OnRender(scr)
	}
	if activeHistorySearch != installed {
		t.Fatal("history painter was torn down while overlay was on top")
	}

	// Close the overlay; the menu is on top again → hint must reappear.
	overlay.SetExitCode(-1)
	vtui.FrameManager.Pop()
	if activeHistorySearch != installed {
		t.Fatal("history painter did not survive the overlay closing")
	}

	// Now close the menu itself; cleanup must fire.
	installed.menu.Close()
	if vtui.FrameManager.OnRender != nil {
		vtui.FrameManager.OnRender(scr)
	}
	if activeHistorySearch != nil {
		t.Error("history painter survived the menu being closed")
	}
}

// TestActionFoldersHistory_CtrlR_DropsMissingPaths exercises the far2l
// Ctrl+R shortcut on the folder-history dialog: prompt, then keep only
// paths that still exist on disk.
func TestActionFoldersHistory_CtrlR_DropsMissingPaths(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.panels[1].(*FileSystemPanel))

	real1 := t.TempDir()
	real2 := t.TempDir()
	missing := filepath.Join(real2, "no-such-dir")

	prev := vtui.GlobalHistoryProvider
	saved := stubHistoryProvider{"folders": {real1, missing, real2}}
	vtui.GlobalHistoryProvider = &saved
	t.Cleanup(func() { vtui.GlobalHistoryProvider = prev })

	// Sanity-check the "missing" path really is missing before we assert.
	if _, err := os.Stat(missing); err == nil {
		t.Fatalf("test setup: %q unexpectedly exists", missing)
	}

	actionFoldersHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_R,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	confirm := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	confirm.OnResult(0) // Ok

	got := saved["folders"]
	want := []string{real1, real2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("folders after Ctrl+R: %v, want %v", got, want)
	}
}
