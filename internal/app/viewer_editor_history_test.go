package app

import (
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestViewerEditorHistoryPersistsAndDeduplicatesByFile(t *testing.T) {
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	root := t.TempDir()
	fs := vfs.NewOSVFS(root)
	first := filepath.Join(root, "first.txt")
	second := filepath.Join(root, "second.txt")
	rememberViewerEditorHistory(fs, first, historyModeView)
	rememberViewerEditorHistory(fs, second, historyModeEdit)
	rememberViewerEditorHistory(fs, first, historyModeEdit)

	entries := loadViewerEditorHistory()
	if len(entries) != 2 {
		t.Fatalf("history length = %d, want 2: %#v", len(entries), entries)
	}
	if entries[0].Path != first || entries[0].Mode != historyModeEdit {
		t.Fatalf("most recent entry = %#v, want edited %q", entries[0], first)
	}
	if entries[1].Path != second || entries[1].Mode != historyModeEdit {
		t.Fatalf("second entry = %#v, want edited %q", entries[1], second)
	}
}

func TestViewerEditorHistoryActionAndMenuRegistration(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	// #408: Alt+F11 must also open the dialog from inside the editor and the
	// viewer, not just from a panel, so the action's DefaultAreas extend its
	// native "Shell" binding into "Editor" and "Viewer" as well.
	for _, area := range []string{"Shell", "Editor", "Viewer"} {
		if got := hm.GetAction(area, "AltF11"); got != "Panel.ViewerEditorHistory" {
			t.Fatalf("%s/AltF11 action = %q, want Panel.ViewerEditorHistory", area, got)
		}
	}
	action, ok := GetAction("Panel.ViewerEditorHistory")
	if !ok || action.MenuPath != "Commands" {
		t.Fatalf("viewer/editor history action is not in Commands: %#v", action)
	}
}

// TestAltF11OpensViewerEditorHistoryFromEditorAndViewer covers the last piece
// of #408: the reporter asked to open the viewer/editor history from inside
// the editor or viewer itself, using the same Alt+F11 as in panels. The fix
// only widens Panel.ViewerEditorHistory's DefaultAreas, so this exercises the
// real dispatch path (macroFilter, the same function vtui.FrameManager.
// EventFilter is wired to) with a frame of the editor's or the viewer's own
// GetType() on top, the way TestMacro_GetCurrentArea already stands in for
// them. The same dialog panels use is expected to come up — no second dialog,
// no duplicated logic.
func TestAltF11OpensViewerEditorHistoryFromEditorAndViewer(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  vtui.FrameType
	}{
		{"Editor", vtui.TypeUser + 2},
		{"Viewer", vtui.TypeUser + 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initHistoryTestScreen(t)
			previousHistory := vtui.GlobalHistoryProvider
			provider := stubHistoryProvider{}
			vtui.GlobalHistoryProvider = &provider
			t.Cleanup(func() { vtui.GlobalHistoryProvider = previousHistory })

			oldHM := keymap.GlobalHotkeysMgr
			keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
			t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHM })

			root := t.TempDir()
			rememberViewerEditorHistory(vfs.NewOSVFS(root), filepath.Join(root, "file.txt"), historyModeView)

			// pf is the panel the editor/viewer was opened from: it stays on
			// the frame stack underneath the editor/viewer frame, the way
			// withPF's FindPanelsFrameAnyScreen expects to find it.
			pf := panel.NewPanelsFrame()
			defer pf.Close()
			pf.ResizeConsole(120, 40)
			vtui.FrameManager.Push(pf)
			vtui.FrameManager.Push(&mockAreaFrame{typ: tc.typ})
			t.Cleanup(func() {
				if activeHistorySearch != nil {
					activeHistorySearch.cleanup()
				}
			})

			mgr := macro.NewMacroManager("")
			altF11 := keymap.ParseFarKey("AltF11")
			if !macroFilter(mgr, altF11) {
				t.Fatalf("Alt+F11 was not consumed with a %s frame on top", tc.name)
			}

			menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
			if !ok || menu.GetHelp() != "HistoryViewEdit" {
				t.Fatalf("top frame after Alt+F11 from %s = %T, want the viewer/editor history dialog", tc.name, vtui.FrameManager.GetTopFrame())
			}
			menu.SetExitCode(-1)
		})
	}
}

func TestViewerEditorHistoryDialogDeletesEntry(t *testing.T) {
	initHistoryTestScreen(t)
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	root := t.TempDir()
	rememberViewerEditorHistory(vfs.NewOSVFS(root), filepath.Join(root, "file.txt"), historyModeView)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	actionViewerEditorHistory(pf)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok || menu.GetHelp() != "HistoryViewEdit" {
		t.Fatalf("history dialog = %T help=%q", vtui.FrameManager.GetTopFrame(), menu.GetHelp())
	}
	if activeHistorySearch == nil || activeHistorySearch.selectedSecondary() != i18n.Msg("History.Mode.View") {
		t.Fatal("viewer/editor mode column is missing")
	}

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_DELETE,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if got := loadViewerEditorHistory(); len(got) != 0 {
		t.Fatalf("deleted viewer/editor history survived: %#v", got)
	}
	if !menu.IsDone() {
		t.Fatal("empty viewer/editor history menu stayed open")
	}
}

func TestViewerEditorHistoryInsertPersistsLockAndProtectsEntry(t *testing.T) {
	initHistoryTestScreen(t)
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	root := t.TempDir()
	path := filepath.Join(root, "pinned.txt")
	fs := vfs.NewOSVFS(root)
	rememberViewerEditorHistory(fs, path, historyModeView)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	actionViewerEditorHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_INSERT})
	entries := loadViewerEditorHistory()
	if len(entries) != 1 || !entries[0].Lock {
		t.Fatalf("Insert did not persist viewer/editor lock: %#v", entries)
	}
	if activeHistorySearch.processKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3}) {
		t.Fatal("viewer/editor history F3 was intercepted by command details")
	}
	menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if entries = loadViewerEditorHistory(); len(entries) != 1 || !entries[0].Lock {
		t.Fatalf("Shift+Del removed locked viewer/editor entry: %#v", entries)
	}
	rememberViewerEditorHistory(fs, path, historyModeEdit)
	if entries = loadViewerEditorHistory(); len(entries) != 1 || !entries[0].Lock || entries[0].Mode != historyModeEdit {
		t.Fatalf("revisited viewer/editor entry lost its lock: %#v", entries)
	}
}

// TestViewerEditorHistoryStampsEntriesAndFiltersByDate covers the request in
// issue #408: every remembered file carries the moment it was opened, the
// dialog prints it, and the incremental filter therefore answers the "I forgot
// the name but I remember the day" lookup.
func TestViewerEditorHistoryStampsEntriesAndFiltersByDate(t *testing.T) {
	initHistoryTestScreen(t)
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	previousModes := config.App.HistoryShowTimes
	config.App.HistoryShowTimes[config.HistoryTypeViewEdit] = config.HistoryShowDateTime
	t.Cleanup(func() { config.App.HistoryShowTimes = previousModes })

	root := t.TempDir()
	opened := time.Now().Add(-time.Second)
	rememberViewerEditorHistory(vfs.NewOSVFS(root), filepath.Join(root, "forgotten.txt"), historyModeView)

	entries := loadViewerEditorHistory()
	if len(entries) != 1 || entries[0].Timestamp.Before(opened) {
		t.Fatalf("viewer/editor entry was not stamped: %#v", entries)
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	actionViewerEditorHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	search := activeHistorySearch
	t.Cleanup(func() {
		if activeHistorySearch != nil {
			activeHistorySearch.cleanup()
		}
		menu.SetExitCode(-1)
	})
	if search == nil || !search.showTimes {
		t.Fatal("viewer/editor history dialog has no timestamp column")
	}

	day := entries[0].Timestamp.Format("2006-01-02")
	if row := search.displayText(search.all[0]); !strings.HasPrefix(row, day) {
		t.Fatalf("history row = %q, want it to start with %q", row, day)
	}

	for _, r := range day {
		if !search.processKey(historyKey(r)) {
			t.Fatalf("filter character %q was not handled", r)
		}
	}
	if len(menu.Items) != 1 {
		t.Fatalf("filtering by %q left %d rows, want 1", day, len(menu.Items))
	}
	for range day {
		search.processKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	}
	for _, r := range "1999-01-01" {
		search.processKey(historyKey(r))
	}
	if len(menu.Items) != 0 {
		t.Fatalf("filtering by another day left %d rows, want 0", len(menu.Items))
	}
}

// TestViewerEditorHistoryCtrlTCyclesTimeMode pins the far2l behaviour of
// Ctrl+T in this dialog: cycle date+time → date → off, with the choice stored
// in the view/edit slot of HistoryShowTimes rather than shared with the
// command and folder dialogs.
func TestViewerEditorHistoryCtrlTCyclesTimeMode(t *testing.T) {
	initHistoryTestScreen(t)
	// onTimesChanged calls config.SaveConfig; keep it inside a temporary profile.
	setupPortableIni(t, "0")
	previous := vtui.GlobalHistoryProvider
	provider := stubHistoryProvider{}
	vtui.GlobalHistoryProvider = &provider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	previousModes := config.App.HistoryShowTimes
	config.App.HistoryShowTimes = [config.HistoryTypeCount]int{config.HistoryShowDateTime, config.HistoryShowDateTime, config.HistoryShowDateTime}
	t.Cleanup(func() { config.App.HistoryShowTimes = previousModes })

	root := t.TempDir()
	rememberViewerEditorHistory(vfs.NewOSVFS(root), filepath.Join(root, "note.txt"), historyModeEdit)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 40)
	actionViewerEditorHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	search := activeHistorySearch
	t.Cleanup(func() {
		if activeHistorySearch != nil {
			activeHistorySearch.cleanup()
		}
		menu.SetExitCode(-1)
	})
	if search == nil || search.timeMode != config.HistoryShowDateTime {
		t.Fatalf("viewer/editor dialog did not pick up the configured time mode: %#v", search)
	}
	clock := loadViewerEditorHistory()[0].Timestamp.Format("15:04:05")

	ctrlT := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_T, ControlKeyState: vtinput.LeftCtrlPressed,
	}
	menu.ProcessKey(ctrlT)
	if search.timeMode != config.HistoryShowDate || config.App.HistoryShowTimes[config.HistoryTypeViewEdit] != config.HistoryShowDate {
		t.Fatalf("Ctrl+T mode = %d, config = %v", search.timeMode, config.App.HistoryShowTimes)
	}
	if row := search.displayText(search.all[0]); strings.Contains(row, clock) {
		t.Fatalf("date-only row still shows the clock: %q", row)
	}
	if config.App.HistoryShowTimes[config.HistoryTypeCommands] != config.HistoryShowDateTime {
		t.Fatalf("Ctrl+T leaked into the command history slot: %v", config.App.HistoryShowTimes)
	}

	menu.ProcessKey(ctrlT)
	if search.timeMode != config.HistoryShowNone {
		t.Fatalf("second Ctrl+T mode = %d, want %d", search.timeMode, config.HistoryShowNone)
	}
	menu.ProcessKey(ctrlT)
	if search.timeMode != config.HistoryShowDateTime {
		t.Fatalf("third Ctrl+T mode = %d, want %d", search.timeMode, config.HistoryShowDateTime)
	}
}
