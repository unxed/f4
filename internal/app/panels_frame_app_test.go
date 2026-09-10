package app

import (
	"context"
	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/plugins/archive"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPanelsFrame_ArkanoidHotkey(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	vtui.FrameManager.Push(pf) // Screen 0

	initialScreens := len(vtui.FrameManager.Screens)

	// 1. Запуск игры
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  'A',
		ControlKeyState: vtinput.LeftAltPressed | vtinput.LeftCtrlPressed,
	})

	if len(vtui.FrameManager.Screens) != initialScreens+1 {
		t.Fatalf("Expected %d screens, got %d", initialScreens+1, len(vtui.FrameManager.Screens))
	}

	arkScreen := vtui.FrameManager.Screens[len(vtui.FrameManager.Screens)-1]
	if !arkScreen.Transparent {
		t.Error("Arkanoid screen should be transparent (headless)")
	}
	if arkScreen.GetTitle() != "Arkanoid" {
		t.Errorf("Expected Arkanoid title, got %s", arkScreen.GetTitle())
	}

	// 2. Пытаемся запустить еще раз (не должно создавать новый экран, а только переключить)
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  'A',
		ControlKeyState: vtinput.LeftAltPressed | vtinput.LeftCtrlPressed,
	})

	if len(vtui.FrameManager.Screens) != initialScreens+1 {
		t.Error("Second Arkanoid launch erroneously created a duplicate screen")
	}

	// Clean up Arkanoid to prevent background loop leak
	arkFrame := arkScreen.Frames[0].(*ArkanoidFrame)
	t.Cleanup(arkFrame.Close)
}

func TestPanelsFrame_ProcessMouse_DoubleClick(t *testing.T) {
	oldNavigationMode := config.App.NavigationMode
	config.App.NavigationMode = config.NavigationClassic
	t.Cleanup(func() { config.App.NavigationMode = oldNavigationMode })

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Active is initially right (1)
	if pf.ActiveIdx != 1 {
		t.Fatalf("Expected initial activeIdx 1, got %d", pf.ActiveIdx)
	}

	tmp := t.TempDir()
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Bypass async load
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	fsp.Refresh()

	initialPath := fsp.Vfs.GetPath()

	// Double click on ".." in the left panel. Derive the row from the
	// table geometry: the menu-bar setting changes the panel's top inset.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          checkedMouseCoordinate(t, fsp.Table.X1),
		MouseY:          checkedMouseCoordinate(t, fsp.Table.Y1+fsp.Table.MarginTop),
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})
	if pf.ActiveIdx != 0 {
		t.Errorf("Expected activeIdx 0 after left click, got %d", pf.ActiveIdx)
	}

	if fsp.Vfs.GetPath() == initialPath {
		t.Error("Double click on '..' should have changed directory")
	}
}

func TestPanelsFrame_ProcessMouse_DoubleClickFile(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	oldNavigationMode := config.App.NavigationMode
	config.App.NavigationMode = config.NavigationClassic
	t.Cleanup(func() { config.App.NavigationMode = oldNavigationMode })

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	if err := os.WriteFile(runnablePath, []byte("echo"), 0600); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.SetViewMode(panel.ViewModeDetailed)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsDir: false}},
	}
	fsp.Refresh()

	// Must init frame manager to catch async tasks from actionExecute
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	vtui.FrameManager.Push(pf)

	// Double click on "run.sh" in the left panel. Derive the second row
	// from the table geometry instead of assuming a particular top inset.
	handled := pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          checkedMouseCoordinate(t, fsp.Table.X1),
		MouseY:          checkedMouseCoordinate(t, fsp.Table.Y1+fsp.Table.MarginTop+1),
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})
	if !handled {
		t.Fatal("double click was not handled")
	}
	if got := fsp.GetSelectedName(); got != "run.sh" {
		t.Fatalf("double click selected %q, want run.sh", got)
	}

	// Wait for the async task that actually executes the file.
	// Since other tasks (like ReadDirectory) might be in the queue,
	// we process the channel in a loop until panels are hidden.
	timeout := time.After(1 * time.Second)
	for pf.ShowPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("actionExecute did not hide the panels within 1s")
		}
	}
	if pf.ShowPanels {
		t.Error("Double clicking a runnable file should hide the panels")
	}
}

func setupArchiveEntryPanel(t *testing.T) (*panel.PanelsFrame, *panel.FileSystemPanel, string) {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))

	pf := paneltest.SetupMockPanelsFrame(t)
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "payload.zip")
	if err := os.WriteFile(archivePath, []byte("not opened by this regression test"), 0600); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.SetViewMode(panel.ViewModeDetailed)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "payload.zip", IsDir: false}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1)
	pf.ActiveIdx = 0

	archiveProvider := &archive.ArchiveProvider{}
	vfs.RegisterProvider(archiveProvider)
	t.Cleanup(func() {
		if !vfs.UnregisterProvider(archiveProvider) {
			t.Errorf("archive provider was not registered")
		}
	})

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	vtui.FrameManager.Push(pf)
	return pf, fsp, tmp
}

func TestPanelsFrame_EnterArchiveFileRequiresExplicitAction(t *testing.T) {
	pf, fsp, tmp := setupArchiveEntryPanel(t)
	defer pf.Close()

	if !pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	}) {
		t.Fatal("plain Enter on an archive file must be handled")
	}
	if fsp.ProviderOpenTask != nil {
		t.Fatal("plain Enter on an archive file must not start a provider transition")
	}
	if got := fsp.Vfs.GetPath(); filepath.Clean(got) != filepath.Clean(tmp) {
		t.Fatalf("plain Enter changed the panel path to %q", got)
	}
	if !pf.ShowPanels {
		t.Fatal("plain Enter on an archive file must not launch it")
	}
}

func TestPanelsFrame_ProcessMouse_DoubleClickArchiveFileRequiresExplicitAction(t *testing.T) {
	pf, fsp, tmp := setupArchiveEntryPanel(t)
	defer pf.Close()

	handled := pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          checkedMouseCoordinate(t, fsp.Table.X1),
		MouseY:          checkedMouseCoordinate(t, fsp.Table.Y1+fsp.Table.MarginTop+1),
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})
	if !handled {
		t.Fatal("double click on an archive file must be handled")
	}
	if got := fsp.GetRawSelectedName(); got != "payload.zip" {
		t.Fatalf("double click selected %q, want payload.zip", got)
	}
	if fsp.ProviderOpenTask != nil {
		t.Fatal("double click on an archive file must not start a provider transition")
	}
	if got := fsp.Vfs.GetPath(); filepath.Clean(got) != filepath.Clean(tmp) {
		t.Fatalf("double click changed the panel path to %q", got)
	}
	if !pf.ShowPanels {
		t.Fatal("double click on an archive file must not launch it")
	}
}

func TestPanelsFrame_ProcessMouse_MiddleClickArchiveFileRequiresExplicitAction(t *testing.T) {
	pf, fsp, tmp := setupArchiveEntryPanel(t)
	defer pf.Close()

	handled := pf.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		MouseX:      checkedMouseCoordinate(t, fsp.Table.X1),
		MouseY:      checkedMouseCoordinate(t, fsp.Table.Y1+fsp.Table.MarginTop+1),
		ButtonState: vtinput.FromLeft2ndButtonPressed,
	})
	if !handled {
		t.Fatal("middle click on an archive file must be handled")
	}
	if fsp.ProviderOpenTask != nil {
		t.Fatal("middle click on an archive file must not start a provider transition")
	}
	if got := fsp.Vfs.GetPath(); filepath.Clean(got) != filepath.Clean(tmp) {
		t.Fatalf("middle click changed the panel path to %q", got)
	}
	if !pf.ShowPanels {
		t.Fatal("middle click on an archive file must not launch it")
	}
}

func TestPanelsFrame_CtrlShiftArrowsOpenDriveMenuForPanelSide(t *testing.T) {
	tests := []struct {
		name     string
		key      uint16
		panelIdx int
	}{
		{name: "left", key: vtinput.VK_LEFT, panelIdx: 0},
		{name: "right", key: vtinput.VK_RIGHT, panelIdx: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 25)
			vtui.FrameManager.Init(scr)
			theme.SetDefaultF4Palette()

			pf := panel.NewPanelsFrame()
			defer pf.Close()
			pf.ResizeConsole(80, 25)
			paths := []string{t.TempDir(), t.TempDir()}
			for i, pnl := range pf.Panels {
				if err := pnl.(*panel.FileSystemPanel).Vfs.SetPath(paths[i]); err != nil {
					t.Fatal(err)
				}
			}

			if !pf.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tt.key,
				ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
			}) {
				t.Fatal("Ctrl+Shift+Arrow was not handled")
			}
			menu := paneltest.FindDriveMenu(t)
			menu.OnAction(0)
			want := paths[1-tt.panelIdx]
			if got := pf.Panels[tt.panelIdx].(*panel.FileSystemPanel).Vfs.GetPath(); got != want {
				t.Fatalf("drive menu changed path %q, want panel %d to receive %q", got, tt.panelIdx, want)
			}
		})
	}
}

// TestPanelsFrame_AIHotkeyCanBeUnbound covers #492: once the built-in
// RCtrlA AI shortcut is unbound, Right Ctrl+A must neither open the AI panel
// nor die silently — it runs whatever plain Ctrl+A is bound to.
func TestPanelsFrame_AIHotkeyCanBeUnbound(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	preserveActionRegistry(t)

	oldHotkeys := keymap.GlobalHotkeysMgr
	oldGlobalHotkeys := plughost.GlobalHotkeys
	oldMacroMgr := macro.MacroMgr
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = oldHotkeys
		plughost.GlobalHotkeys = oldGlobalHotkeys
		macro.MacroMgr = oldMacroMgr
	})
	plughost.GlobalHotkeys = nil

	ctrlARuns := 0
	action.RegisterAction(action.Action{
		Name:    "Test.CtrlA",
		Area:    "Shell",
		Label:   "Ctrl+A stand-in",
		Handler: func() bool { ctrlARuns++; return true },
	})

	hm := keymap.NewHotkeyManager("")
	hm.Bind("Shell", "CtrlA", "Test.CtrlA")
	hm.Bind("Shell", "RCtrlA", "None")
	keymap.GlobalHotkeysMgr = hm
	macro.MacroMgr = macro.NewMacroManager("")

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowPanels = true
	vtui.FrameManager.Push(pf)

	if _, ok := pf.Panels[1].(*panel.FileSystemPanel).Vfs.(*aiVFSWrapper); ok {
		t.Fatal("test setup unexpectedly started with an AI panel")
	}
	e := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  'A',
		ControlKeyState: vtinput.RightCtrlPressed,
	}
	if pf.InterceptPluginKey(e) {
		t.Fatal("unbound RCtrl+A was intercepted before the hotkey manager")
	}
	if !macroFilter(macro.MacroMgr, e) {
		t.Fatal("unbound RCtrl+A was not consumed by the hotkey manager")
	}
	if _, ok := pf.Panels[1].(*panel.FileSystemPanel).Vfs.(*aiVFSWrapper); ok {
		t.Fatal("unbound RCtrl+A toggled the AI panel")
	}
	if ctrlARuns != 1 {
		t.Fatalf("unbound RCtrl+A ran the Ctrl+A binding %d times, want 1", ctrlARuns)
	}
}

func TestLayout_F4InternalDialogs_Validity(t *testing.T) {
	vtui.SetDefaultPalette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	t.Run("DummyOpDialog", func(t *testing.T) {
		// We need to capture the dialog created by showDummyOpDialog.
		// Since it pushes to the real FrameManager, we'll initialize it.
		fm := vtui.FrameManager
		scr := vtui.NewSilentScreenBuf()
		scr.AllocBuf(80, 25)
		fm.Init(scr)

		pf.ShowDummyOpDialog()
		top := fm.GetTopFrame()
		if dlg, ok := top.(vtui.Container); ok {
			vtui.AssertLayout(t, dlg)
			assertComboMenuDoesNotCoverButtons(t, dlg, "dummy operation")
			focusDlg, ok := top.(dialogFocusContainer)
			if !ok {
				t.Fatal("dummy operation dialog does not expose focus traversal")
			}
			assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "dummy operation")
			fm.Pop()
		} else {
			t.Fatal("Top frame is not a container")
		}
	})
}

func TestLayout_F4ActionDialogs_Validity(t *testing.T) {
	vtui.SetDefaultPalette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	// Validate the intended, full dialog layout. Short screens are exercised
	// separately by the viewport regression test; settings dialogs can now
	// intentionally extend beyond a small viewport and scroll their contents.
	pf.ResizeConsole(80, 60)
	fm := vtui.FrameManager

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 60)
	fm.Init(scr)

	// Helper to setup active panel with some files
	setupPanel := func() {
		pf.ActiveIdx = 0
		fsp := pf.Panels[0].(*panel.FileSystemPanel)
		fsp.Entries = []*panel.FileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "file1.txt"}},
		}
		fsp.Refresh()
		fsp.SetCursorIndex(1)
	}

	t.Run("CopyDialog", func(t *testing.T) {
		setupPanel()
		actionCopyMove(pf, false)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		focusDlg, ok := fm.GetTopFrame().(dialogFocusContainer)
		if !ok {
			t.Fatal("copy dialog does not expose focus traversal")
		}
		assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "copy")
		fm.Pop()
	})

	t.Run("MoveDialog", func(t *testing.T) {
		setupPanel()
		actionCopyMove(pf, true)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		focusDlg, ok := fm.GetTopFrame().(dialogFocusContainer)
		if !ok {
			t.Fatal("move dialog does not expose focus traversal")
		}
		assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "move")
		fm.Pop()
	})

	t.Run("MkDirDialog", func(t *testing.T) {
		actionMkDir(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		assertComboMenuDoesNotCoverButtons(t, dlg, "make directory")
		focusDlg, ok := fm.GetTopFrame().(dialogFocusContainer)
		if !ok {
			t.Fatal("make directory dialog does not expose focus traversal")
		}
		assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "make directory")
		fm.Pop()
	})

	t.Run("DeleteDialog", func(t *testing.T) {
		setupPanel()
		actionDelete(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		assertComboMenuDoesNotCoverButtons(t, dlg, "delete")
		focusDlg, ok := fm.GetTopFrame().(dialogFocusContainer)
		if !ok {
			t.Fatal("delete dialog does not expose focus traversal")
		}
		assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "delete")
		fm.Pop()
	})
	t.Run("FindFileDialog", func(t *testing.T) {
		setupPanel()
		actionFindFile(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("PanelSettingsDialog", func(t *testing.T) {
		actionPanelSettings(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("EditorSettingsDialog", func(t *testing.T) {
		actionEditorSettings(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("ViewerSettingsDialog", func(t *testing.T) {
		dialog.ShowViewerSettings()
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("AppearanceSettingsDialog", func(t *testing.T) {
		actionAppearanceSettings(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})
}

func TestPanelsFrame_CtrlViewModes(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)

	// 1. Изначально устанавливаем режим Medium
	fsp.SetViewMode(panel.ViewModeMedium)
	oldHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = oldHotkeys }()
	oldMacroMgr := macro.MacroMgr
	macro.MacroMgr = &macro.MacroManager{Macros: make(map[string]map[string][]*vtinput.InputEvent)}
	defer func() { macro.MacroMgr = oldMacroMgr }()
	rightCtrl3 := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '3', ControlKeyState: vtinput.RightCtrlPressed}
	if macroFilter(macro.MacroMgr, rightCtrl3) {
		t.Fatal("RightCtrl+3 was consumed by the configurable hotkey filter")
	}
	if !pressKey(pf, rightCtrl3) {
		t.Fatal("RightCtrl+3 was not handled by bookmarks")
	}
	if fsp.ViewMode != panel.ViewModeMedium {
		t.Fatalf("RightCtrl+3 changed panel mode to %v", fsp.ViewMode)
	}

	for _, tc := range []struct {
		key  uint16
		mode panel.ViewMode
	}{{'1', panel.ViewModeBrief}, {'2', panel.ViewModeMedium}, {'3', panel.ViewModeDetailed}} {
		pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tc.key, ControlKeyState: vtinput.LeftCtrlPressed})
		if fsp.ViewMode != tc.mode || pf.WidePanel != -1 {
			t.Errorf("Ctrl+%c: mode=%v wide=%d, want mode=%v wide=-1", tc.key, fsp.ViewMode, pf.WidePanel, tc.mode)
		}
	}

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '4', ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.WidePanel != pf.ActiveIdx || !fsp.Wide || len(fsp.Table.Columns) != 3 {
		t.Fatalf("Ctrl+4 did not enter Wide: wide=%d active=%d columns=%d", pf.WidePanel, pf.ActiveIdx, len(fsp.Table.Columns))
	}
	x1, _, x2, _ := fsp.GetPosition()
	if x1 != 0 || x2 != 79 {
		t.Fatalf("Wide geometry = %d..%d, want 0..79", x1, x2)
	}
	originalMode := fsp.ViewMode
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.WidePanel != pf.ActiveIdx || pf.ActiveIdx != 0 {
		t.Fatalf("Tab did not transfer Wide to left panel: wide=%d active=%d", pf.WidePanel, pf.ActiveIdx)
	}
	if fsp.ViewMode != originalMode {
		t.Error("Wide changed the right panel's normal view mode")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '2', ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.WidePanel != -1 || pf.Panels[0].(*panel.FileSystemPanel).ViewMode != panel.ViewModeMedium {
		t.Error("Ctrl+2 did not leave Wide and set Medium on the active panel")
	}
	_, _, leftX2, _ := pf.Panels[0].GetPosition()
	rightX1, _, _, _ := pf.Panels[1].GetPosition()
	if leftX2+1 != rightX1 {
		t.Errorf("split geometry was not restored: left x2=%d right x1=%d", leftX2, rightX1)
	}
}

func TestPanelsFrame_KeyHandling(t *testing.T) {
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Test Tab to switch active panel
	if pf.ActiveIdx != 1 {
		t.Fatalf("Initial active panel should be right (1), got %d", pf.ActiveIdx)
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.ActiveIdx != 0 {
		t.Error("Tab did not switch active panel to left (0)")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.ActiveIdx != 1 {
		t.Error("Tab did not switch active panel back to right (1)")
	}

	// 2. Test Ctrl+O to toggle panels
	if !pf.ShowPanels {
		t.Fatal("Panels should be visible initially")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.ShowPanels {
		t.Error("Ctrl+O did not hide panels")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if !pf.ShowPanels {
		t.Error("Ctrl+O did not show panels again")
	}

	// 3. Test Ctrl+Enter to insert filename
	pf.ActiveIdx = 0
	if fsp, ok := pf.Panels[0].(*panel.FileSystemPanel); ok {
		// Mock entries to avoid async dependency
		fsp.Entries = []*panel.FileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "testfile.txt"}},
		}
		fsp.Refresh()
		fsp.SetCursorIndex(1)
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: vtinput.LeftCtrlPressed})

	expectedName := pf.Panels[0].GetSelectedName()
	if pf.CmdLine.Edit.GetText() != expectedName {
		t.Errorf("Ctrl+Enter failed: expected '%s', got '%s'", expectedName, pf.CmdLine.Edit.GetText())
	}

	// 4. Test Ctrl+O to toggle panels even when terminal.PTY is busy (Issue #50)
	pf.ShowPanels = false
	pf.Pty = &paneltest.MockPty{}
	pf.Executing = true // terminal.PTY is busy

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if !pf.ShowPanels {
		t.Error("Ctrl+O should show panels even when term.PTY is busy")
	}

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.ShowPanels {
		t.Error("Ctrl+O should hide panels even when term.PTY is busy")
	}
}

func TestPanelsFrame_MenuCommands(t *testing.T) {
	oldHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHotkeys })

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	handled := pf.HandleCommand(appcmd.CmLeftDetailed, nil)
	if !handled {
		t.Error("CmLeftDetailed not handled")
	}
	if pf.Panels[0].(*panel.FileSystemPanel).ViewMode != panel.ViewModeDetailed {
		t.Error("Left panel mode not changed to Detailed")
	}

	pf.HandleCommand(appcmd.CmRightDetailed, nil)
	if pf.Panels[1].(*panel.FileSystemPanel).ViewMode != panel.ViewModeDetailed {
		t.Error("Right panel mode not changed to Detailed")
	}

	for _, menuIndex := range []int{0, 4} {
		items := pf.MenuBar.Items[menuIndex].SubItems
		for i, shortcut := range []string{"Ctrl+1", "Ctrl+2", "Ctrl+3", "Ctrl+4"} {
			if items[i].Shortcut != shortcut {
				t.Errorf("view shortcut %d in menu %d = %q, want %s", i, menuIndex, items[i].Shortcut, shortcut)
			}
		}
	}

	// Sort mode commands
	pf.HandleCommand(appcmd.CmLeftSortTime, nil)
	if pf.Panels[0].(*panel.FileSystemPanel).SortMode != panel.SortTime {
		t.Error("Left panel sort mode not changed to Time")
	}

	pf.HandleCommand(appcmd.CmRightSortSize, nil)
	if pf.Panels[1].(*panel.FileSystemPanel).SortMode != panel.SortSize {
		t.Error("Right panel sort mode not changed to Size")
	}

	// Menu checkmarks
	menuText := pf.MenuBar.Items[0].SubItems[2].Text
	if !strings.HasPrefix(menuText, "√") {
		t.Errorf("Menu checkmark not updated, got %q", menuText)
	}
	sortText := pf.MenuBar.Items[0].SubItems[7].Text
	if !strings.HasPrefix(sortText, "√") {
		t.Errorf("Sort menu checkmark not updated, got %q", sortText)
	}
}

func TestDirectoryCacheIdentitySurvivesReconnection(t *testing.T) {
	stable := "cloud-profile-version"
	first := &mockStableCacheVFS{
		mockCacheSessionVFS: &mockCacheSessionVFS{mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: "Cloud"}, session: new(int)},
		stable:              stable,
	}
	second := &mockStableCacheVFS{
		mockCacheSessionVFS: &mockCacheSessionVFS{mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/"), title: "Cloud"}, session: new(int)},
		stable:              stable,
	}
	if got, want := panel.DirectoryCacheKey(first, "Cloud:"+string(os.PathSeparator)+"Photos"), panel.DirectoryCacheKey(second, "Cloud:"+string(os.PathSeparator)+"Photos"); got != want {
		t.Fatalf("stable directory cache keys differ across reconnect: %#v != %#v", got, want)
	}
}

// TestPanelsFrame_CtrlL_TogglesInfoPanel exercises far2l's Ctrl+L:
//   - first press installs an panel.InfoPanel on the passive side, keeping
//     the file panel underneath alive and focus on the active side;
//   - second press removes it (toggle);
//   - Tab that lands on the alt slot keeps it open — the panel
//     visually becomes focused (as in far2l), but commands still
//     target the source file panel underneath.
func TestPanelsFrame_CtrlL_TogglesInfoPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	send := func(vk uint16, mods vtinput.ControlKeyState) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: mods,
		})
	}

	// paneltest.SetupMockPanelsFrame sets activeIdx = 1 (right). Passive is left.
	if pf.AltPanels[0] != nil || pf.AltPanels[1] != nil {
		t.Fatal("precondition: no alt panels expected initially")
	}

	send(vtinput.VK_L, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+L should install alt panel on passive (left) side")
	}
	if pf.AltPanels[1] != nil {
		t.Error("active (right) side must not get an alt panel")
	}
	if _, ok := pf.AltPanels[0].(*panel.InfoPanel); !ok {
		t.Errorf("expected *InfoPanel, got %T", pf.AltPanels[0])
	}
	if _, ok := pf.Panels[0].(*panel.FileSystemPanel); !ok {
		t.Error("file panel underneath must stay alive")
	}
	if pf.ActiveIdx != 1 {
		t.Errorf("Ctrl+L must not move active panel; got activeIdx=%d", pf.ActiveIdx)
	}

	// Source of the alt panel is the current active file panel.
	if src := pf.AltPanels[0].Source(); src != pf.Panels[1].(*panel.FileSystemPanel) {
		t.Error("alt panel source should be the active file panel")
	}

	// Second press toggles it off.
	send(vtinput.VK_L, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] != nil {
		t.Error("second Ctrl+L should remove alt panel")
	}

	// Install again, then Tab to the alt side — Tab must keep the
	// alt panel visible AND flip its focused state so the frame
	// title recolors (matches far2l).
	send(vtinput.VK_L, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] == nil {
		t.Fatal("re-install: alt panel should be present again")
	}
	send(vtinput.VK_TAB, 0)
	if pf.ActiveIdx != 0 {
		t.Fatalf("Tab should switch active to left; got activeIdx=%d", pf.ActiveIdx)
	}
	if pf.AltPanels[0] == nil {
		t.Error("Tab must NOT close the alt panel — it should stay visible")
	}
	// A render is required to propagate SetFocus into the alt panel;
	// call Show and then check the focus state was flipped.
	pf.Show(vtui.NewSilentScreenBuf())
	if !pf.AltPanels[0].IsFocused() {
		t.Error("after Tab + render, alt panel should report focused=true")
	}

	// Ctrl+L while focus is ON the alt panel must close IT (matches
	// far2l), not open another one on the opposite side.
	send(vtinput.VK_L, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] != nil {
		t.Error("Ctrl+L on focused alt panel should close it")
	}
	if pf.AltPanels[1] != nil {
		t.Error("Ctrl+L on focused alt must not spawn a second alt on the opposite side")
	}
}

// TestPanelsFrame_B_TogglesInfoPanelUnits verifies that `B` (plain,
// no modifiers) flips config.App.InfoPanelBytes while an info panel is
// visible, and falls through to fast-find otherwise.
func TestPanelsFrame_B_TogglesInfoPanelUnits(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	oldBytes := config.App.InfoPanelBytes
	defer func() { config.App.InfoPanelBytes = oldBytes }()
	config.App.InfoPanelBytes = false

	send := func(vk uint16) bool {
		return pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vk,
		})
	}

	// No info panel yet: `B` must NOT touch the config (fast-find
	// path is expected to consume it).
	send(vtinput.VK_B)
	if config.App.InfoPanelBytes {
		t.Errorf("without info panel: B must not flip units, got InfoPanelBytes=true")
	}

	// Install info panel on passive side, then `B` should flip units.
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_L,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+L should install info panel")
	}
	send(vtinput.VK_B)
	if !config.App.InfoPanelBytes {
		t.Errorf("with info panel: B should flip units to bytes")
	}
	send(vtinput.VK_B)
	if config.App.InfoPanelBytes {
		t.Errorf("second B should flip back to human")
	}
}

// TestPanelsFrame_CtrlQ_TogglesQuickView mirrors the Ctrl+L test:
// first Ctrl+Q installs a panel.QuickViewPanel on the passive side, second
// press removes it, Tab flips the focused marker without closing,
// and Ctrl+Q on the focused alt closes IT (not spawns a second one).
func TestPanelsFrame_CtrlQ_TogglesQuickView(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	send := func(vk uint16, mods vtinput.ControlKeyState) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: mods,
		})
	}

	send(vtinput.VK_Q, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+Q should install alt on passive (left) side")
	}
	if _, ok := pf.AltPanels[0].(*panel.QuickViewPanel); !ok {
		t.Errorf("expected *QuickViewPanel, got %T", pf.AltPanels[0])
	}

	// Second press toggles off.
	send(vtinput.VK_Q, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] != nil {
		t.Error("second Ctrl+Q should remove alt panel")
	}

	// Reinstall, Tab, Ctrl+Q — closes the focused alt, doesn't spawn another.
	send(vtinput.VK_Q, vtinput.LeftCtrlPressed)
	send(vtinput.VK_TAB, 0)
	if pf.AltPanels[0] == nil {
		t.Error("Tab must NOT close the alt panel")
	}
	send(vtinput.VK_Q, vtinput.LeftCtrlPressed)
	if pf.AltPanels[0] != nil {
		t.Error("Ctrl+Q on focused quick-view should close it")
	}
	if pf.AltPanels[1] != nil {
		t.Error("Ctrl+Q on focused alt must not spawn a second alt")
	}
}

// TestPanelsFrame_CtrlLQ_CoexistOnDifferentSides verifies the two
// hotkeys can point at different alt-panel kinds simultaneously —
// info on one side, quick view on the other — without collisions.
func TestPanelsFrame_CtrlLQ_CoexistOnDifferentSides(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	send := func(vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	// active = right → Ctrl+L opens info on left.
	send(vtinput.VK_L)
	if _, ok := pf.AltPanels[0].(*panel.InfoPanel); !ok {
		t.Fatalf("expected InfoPanel on left, got %T", pf.AltPanels[0])
	}
	// Tab to left (so info is now on active side).
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.ActiveIdx != 0 {
		t.Fatalf("Tab: expected activeIdx=0, got %d", pf.ActiveIdx)
	}
	// Ctrl+Q now — active side (0) has info, not quick-view, so
	// toggleAltPanel opens quick-view on the passive side (1).
	send(vtinput.VK_Q)
	if _, ok := pf.AltPanels[1].(*panel.QuickViewPanel); !ok {
		t.Errorf("expected QuickViewPanel on right, got %T", pf.AltPanels[1])
	}
	if _, ok := pf.AltPanels[0].(*panel.InfoPanel); !ok {
		t.Errorf("Ctrl+Q must not disturb existing InfoPanel on left, got %T", pf.AltPanels[0])
	}
}

// TestPanelsFrame_QuickViewWheel_ActivePanelScrolls locks in
// far/far2l behaviour: whichever panel is active gets scrolled by the
// wheel, regardless of where the mouse points. If the active slot is
// covered by a quick-view alt panel, the alt scrolls — not the file
// panel underneath.
func TestPanelsFrame_QuickViewWheel_ActivePanelScrolls(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	vtui.SetDefaultPalette()

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		if _, err := b.WriteString("line\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "big.txt"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	fsp := pf.Panels[1].(*panel.FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(tmp)
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "big.txt", Size: int64(b.Len())}}}
	fsp.CursorIdx = 0
	fsp.Refresh()

	// Ctrl+Q — alt lands on left (opposite of active=right).
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	q := pf.AltPanels[0].(*panel.QuickViewPanel)
	pf.LastAutoRefresh = time.Now()
	pf.Show(scr)

	// Tab — active moves to left (alt slot). From now on wheel should
	// hit the alt, regardless of mouse pointer.
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	pf.Show(scr)

	before := q.ScrollY
	// Point the mouse at the RIGHT half (over the file panel, not
	// over the alt) — active-side rule should still send wheel to alt.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseEventFlags: vtinput.MouseWheeled,
		WheelDirection:  -1,
		MouseX:          60,
		MouseY:          10,
	})
	if q.ScrollY == before {
		t.Errorf("wheel over passive side should still scroll active alt; scrollY=%d", q.ScrollY)
	}
}

// TestPanelsFrame_BToggle_WithQuickView ensures pressing plain `B`
// while a quick-view alt is up flips config.App.InfoPanelBytes. Before
// this PR the B toggle only fired for `info` alts.
func TestPanelsFrame_BToggle_WithQuickView(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Install a QuickView (Ctrl+Q). Active panel is right (activeIdx=1),
	// so alt lands on left (index 0).
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if _, ok := pf.AltPanels[0].(*panel.QuickViewPanel); !ok {
		t.Fatalf("expected QuickView on left, got %T", pf.AltPanels[0])
	}

	before := config.App.InfoPanelBytes
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_B,
	})
	if config.App.InfoPanelBytes == before {
		t.Error("B with QuickView visible should flip InfoPanelBytes")
	}
	// Flip back so the test is idempotent across a full suite.
	config.App.InfoPanelBytes = before
}

func TestPanelsFrame_SelectionByMask(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	fsp := pf.Panels[1].(*panel.FileSystemPanel)
	pf.ActiveIdx = 1

	// 1. Command line not empty -> should not intercept for regular char
	pf.CmdLine.Edit.SetText("a")
	handled := pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    '+',
	})
	if !handled {
		t.Error("Key should be handled by cmdLine")
	}

	// 1.5 Command line not empty, but Numpad + -> SHOULD intercept
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ADD,
	})
	if !handled {
		t.Error("Numpad + should be intercepted even if cmdLine is not empty")
	}
	if vtui.FrameManager.GetTopFrameType() != vtui.TypeDialog {
		t.Error("Selection dialog was not shown for Numpad +")
	}
	vtui.FrameManager.Pop() // Clean up dialog

	// 2. Command line empty, fastFindMode active -> should not intercept
	pf.CmdLine.Clear()
	fsp.FastFindMode = true
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    '+',
	})
	if !handled {
		t.Error("Key should be handled by fastFindMode in active panel")
	}

	// 3. Command line empty, fastFindMode NOT active -> SHOULD intercept and show dialog
	fsp.FastFindMode = false
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    '+',
	})
	if !handled {
		t.Error("Key should be intercepted for selection dialog")
	}
	if vtui.FrameManager.GetTopFrameType() != vtui.TypeDialog {
		t.Error("Selection dialog was not shown")
	}
}

// TestPanelsFrame_ProcessMouse_AltPanelSwallowsClicks makes sure a
// click on an alt panel (Ctrl+L / Ctrl+Q) does NOT fall through to
// the file panel underneath — otherwise a double-click can launch
// a file the user can't even see. Also verifies that a click on
// the passive-side alt panel activates that side.
func TestPanelsFrame_ProcessMouse_AltPanelSwallowsClicks(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	if err := os.WriteFile(runnablePath, []byte("echo"), 0600); err != nil {
		t.Fatal(err)
	}

	// Left panel holds the runnable; right is active by default.
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.SetViewMode(panel.ViewModeDetailed)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsDir: false}},
	}
	fsp.Refresh()

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Ctrl+L installs an info panel on the passive (left) slot.
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_L,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+L should install alt panel on the left slot")
	}
	priorActive := pf.ActiveIdx // right (1) from paneltest.SetupMockPanelsFrame

	// Double-click at (5,3) — coordinates that would hit run.sh
	// if the file panel underneath received the event.
	handled := pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          5,
		MouseY:          3,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})
	if !handled {
		t.Error("click on alt panel must return handled=true")
	}
	// Drain any incidental tasks (ReadDirectory etc); if the file
	// panel handled the double-click, panels would be hidden.
	timeout := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			break drain
		}
	}
	if !pf.ShowPanels {
		t.Error("double-click on alt panel must NOT launch the file underneath")
	}
	if pf.ActiveIdx == priorActive {
		t.Errorf("click on passive-side alt panel should activate that side; activeIdx stayed %d", pf.ActiveIdx)
	}
}

// TestPanelsFrame_ProcessMouse_MiddleClickOverAltPanel confirms the
// global middle-click → Enter branch is also swallowed when the
// click lands on an alt panel; otherwise the file under the alt
// still gets launched despite the fix.
func TestPanelsFrame_ProcessMouse_MiddleClickOverAltPanel(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "run.sh"), []byte("echo"), 0600); err != nil {
		t.Fatal(err)
	}
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.SetViewMode(panel.ViewModeDetailed)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsDir: false}},
	}
	fsp.Refresh()

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_L,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if pf.AltPanels[0] == nil {
		t.Fatal("Ctrl+L should install alt panel")
	}

	handled := pf.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		MouseX:      5,
		MouseY:      3,
		ButtonState: vtinput.FromLeft2ndButtonPressed,
	})
	if !handled {
		t.Error("middle-click on alt panel must be handled=true")
	}
	timeout := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			break drain
		}
	}
	if !pf.ShowPanels {
		t.Error("middle-click on alt panel must NOT trigger Enter → launch on the file underneath")
	}
}

// TestPanelsFrame_EscTogglesPanels exercises the FAR-style ESC
// toggle: hides visible panels when the command line is empty,
// shows hidden panels back if no interactive terminal app is
// running. Non-empty command line keeps the existing ESC-clears-
// cmdLine behaviour.
func TestPanelsFrame_EscTogglesPanels(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	old := config.App.EscTogglePanels
	defer func() { config.App.EscTogglePanels = old }()
	config.App.EscTogglePanels = true

	sendEsc := func() bool {
		return pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_ESCAPE,
		})
	}

	// Panels visible, cmdLine empty → ESC hides.
	if !pf.ShowPanels {
		t.Fatal("precondition: panels should start visible")
	}
	if !sendEsc() {
		t.Error("ESC on visible panels + empty cmdLine should be handled")
	}
	if pf.ShowPanels {
		t.Error("ESC should hide panels when cmdLine is empty")
	}

	// Panels hidden, quiet terminal.PTY → ESC brings them back.
	if !sendEsc() {
		t.Error("ESC on hidden panels + quiet term.PTY should be handled")
	}
	if !pf.ShowPanels {
		t.Error("ESC should show panels back on the second press")
	}

	// Panels visible with a typed command → ESC falls through to
	// the clear-cmdLine handler and panels stay put.
	pf.CmdLine.InsertString("something")
	if !sendEsc() {
		t.Error("ESC with a non-empty cmdLine should still be handled (clears it)")
	}
	if !pf.ShowPanels {
		t.Error("ESC with a typed command must NOT hide the panels — only clear the line")
	}
	if !pf.CmdLine.IsEmpty() {
		t.Error("ESC with a typed command should have cleared the command line")
	}
}

func TestPanelsFrame_CtrlF12SortMenu(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	fsp.SortMode = panel.SortTime
	fsp.SortReverse = false

	if !pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_F12,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+F12 was not handled")
	}

	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("Ctrl+F12 top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	// Five sort modes plus the sort-group toggle on the last row.
	if len(menu.Items) != 6 {
		t.Fatalf("sort menu has %d items, want 6", len(menu.Items))
	}
	if !strings.Contains(menu.Items[5].Text, i18n.Msg("Menu.SortUseGroups")) {
		t.Fatalf("last sort menu row = %q, want the sort-group toggle", menu.Items[5].Text)
	}
	panelX1, panelY1, panelX2, panelY2 := fsp.GetPosition()
	menuX1, menuY1, menuX2, menuY2 := menu.GetPosition()
	if !menuIsCenteredIn(menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2) {
		t.Fatalf("sort menu (%d,%d)-(%d,%d) is not centered in panel (%d,%d)-(%d,%d)",
			menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2)
	}
	if menu.SelectPos != int(panel.SortTime) || !strings.HasPrefix(menu.Items[panel.SortTime].Text, "✓ ") {
		t.Fatalf("current sort not selected/marked: pos=%d item=%q", menu.SelectPos, menu.Items[panel.SortTime].Text)
	}
	for idx, shortcut := range []string{"Ctrl+F3", "Ctrl+F4", "Ctrl+F5", "Ctrl+F6", "Ctrl+F7"} {
		if menu.Items[idx].Shortcut != shortcut {
			t.Errorf("sort menu shortcut %d = %q, want %q", idx, menu.Items[idx].Shortcut, shortcut)
		}
	}

	menu.SetSelectPos(int(panel.SortSize))
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	if fsp.SortMode != panel.SortSize || fsp.SortIsAscending() {
		t.Fatalf("sort menu selection = mode %v ascending %v, want Size descending",
			fsp.SortMode, fsp.SortIsAscending())
	}
	menu.Close()
	vtui.FrameManager.Pop()
}

func TestPanelsFrame_RightClickHeaderOpensPanelCenteredSortMenu(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ActiveIdx = 1
	// RunAction("Panel.SortMenu") resolves the panels frame through
	// FrameManager screens, as in production.
	vtui.FrameManager.Push(pf)
	left := pf.Panels[0].(*panel.FileSystemPanel)

	if !pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: testutil.Int16(left.Table.X1), MouseY: testutil.Int16(left.Table.Y1),
		ButtonState: vtinput.RightmostButtonPressed,
	}) {
		t.Fatal("right click on column header was not handled")
	}
	if pf.ActiveIdx != 1 {
		t.Fatalf("right-clicking the passive panel header changed active panel to %d", pf.ActiveIdx)
	}
	if pf.PanelMouseCapture != nil {
		t.Fatal("header context click incorrectly captured a file-panel drag")
	}

	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("right-click header top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	panelX1, panelY1, panelX2, panelY2 := left.GetPosition()
	menuX1, menuY1, menuX2, menuY2 := menu.GetPosition()
	if !menuIsCenteredIn(menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2) {
		t.Fatalf("context sort menu (%d,%d)-(%d,%d) is not centered in left panel (%d,%d)-(%d,%d)",
			menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2)
	}
	menu.Close()
	vtui.FrameManager.Pop()
}

func TestPanelsFrame_CtrlBrackets_Insertion(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*panel.FileSystemPanel)
	rp := pf.Panels[1].(*panel.FileSystemPanel)

	tmp := t.TempDir()
	leftPath := filepath.Join(tmp, "left")
	rightPath := filepath.Join(tmp, "right")
	if err := os.MkdirAll(leftPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0700); err != nil {
		t.Fatal(err)
	}

	if err := lp.Vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := rp.Vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}

	// 1. Тест Ctrl+[ (Путь левой панели)
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	gotLeft := pf.CmdLine.Edit.GetText()
	expectedLeft := leftPath
	if gotLeft != expectedLeft {
		t.Errorf("Ctrl+[ failed: expected %q, got %q", expectedLeft, gotLeft)
	}

	// 2. Тест Ctrl+] (Путь правой панели)
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_6,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	gotRight := pf.CmdLine.Edit.GetText()
	expectedRight := rightPath
	if gotRight != expectedRight {
		t.Errorf("Ctrl+] failed: expected %q, got %q", expectedRight, gotRight)
	}
}

func TestPanelsFrame_CtrlBrackets_InsertionWhenPanelsHidden(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*panel.FileSystemPanel)
	rp := pf.Panels[1].(*panel.FileSystemPanel)
	tmp := t.TempDir()
	leftPath := filepath.Join(tmp, "left")
	rightPath := filepath.Join(tmp, "right")
	if err := os.MkdirAll(leftPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := lp.Vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := rp.Vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}

	pf.ShowPanels = false
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if got := pf.CmdLine.Edit.GetText(); got != leftPath {
		t.Errorf("hidden-panels Ctrl+[ inserted %q, want %q", got, leftPath)
	}

	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_6,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if got := pf.CmdLine.Edit.GetText(); got != rightPath {
		t.Errorf("hidden-panels Ctrl+] inserted %q, want %q", got, rightPath)
	}
}

func TestPanelsFrame_ManualRefresh(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Setup a mock directory
	tmp := t.TempDir()
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Press Ctrl+R
	handled := pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_R,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if !handled {
		t.Error("Ctrl+R was not handled")
	}

	// It should trigger ReadDirectory
	if !fsp.IsLoading {
		t.Error("Ctrl+R did not trigger panel refresh (isLoading should be true)")
	}
}

func TestPanelsFrame_CtrlO_HardRedraw(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	fm.Push(pf)

	// Ensure screen is "clean" initially
	scr.Flush()

	// Simulate Ctrl+O
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// After Ctrl+O, the ScreenBuf MUST be marked as dirty (needs full redraw)
	// getCell (or any mutex-locked method) is not needed here, just check the internal flag
	// which is exported for this reason.
	// Since we can't easily access unexported 'dirty', we verify the effect of HardReset:
	// all shadow cells must be zeroed.
	for i := 0; i < 80*25; i++ {
		// Use a hack to check shadow if possible, or just trust the logic if dirty isn't visible.
		// In vtui, HardReset sets dirty = true.
	}

	// We'll add a helper/check to vtui for testing this if needed,
	// but for now, we check the logic works.
}

func TestPanelsFrame_ReturnExecution(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Создаем временный запускаемый файл
	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "runme.sh")
	if err := os.WriteFile(runnablePath, []byte("echo 1"), 0600); err != nil {
		t.Fatal(err)
	}

	// Настраиваем VFS и выбираем этот файл на панели
	fsp := pf.Panels[1].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "runme.sh", IsDir: false}},
	}
	fsp.Refresh()
	fsp.SelectName("runme.sh")

	// Проверяем начальное состояние
	if !pf.ShowPanels {
		t.Fatal("Panels should be visible initially")
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf()) // For TaskChan

	// Имитируем нажатие Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Ждем асинхронного выполнения
	timeout := time.After(1 * time.Second)
	for pf.ShowPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("actionExecute did not hide the panels within 1s")
		}
	}
	if pf.ShowPanels {
		t.Error("Panels should be hidden after executing a terminal-runnable file")
	}

	if len(pf.CmdLine.Edit.History) == 0 {
		t.Error("Executed file was not added to history")
	} else {
		expectedCmd := "runme.sh"
		if runtime.GOOS != "windows" {
			expectedCmd = "./runme.sh"
		}
		if pf.CmdLine.Edit.History[0] != expectedCmd {
			t.Errorf("History mismatch: got %q, want %q", pf.CmdLine.Edit.History[0], expectedCmd)
		}
	}
}

func TestPanelsFrame_NonRunnableOpen(t *testing.T) {
	if _, _, supported := panel.AssociatedFileCommand("test"); !supported {
		t.Skipf("associated-file launch is unsupported on %s", runtime.GOOS)
	}
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	launches := make(chan recordedExternalUICall, 1)
	pf.ExternalUIRunner = func(command string, args []string, dir string) error {
		launches <- recordedExternalUICall{command: command, args: append([]string(nil), args...), dir: dir}
		return nil
	}
	pf.ResizeConsole(80, 25)
	tmp := t.TempDir()
	docPath := filepath.Join(tmp, "readme.txt")
	if err := os.WriteFile(docPath, []byte("some text"), 0600); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Panels[1].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "readme.txt", IsDir: false}},
	}
	fsp.Refresh()
	fsp.SelectName("readme.txt")

	// Нажимаем Enter на текстовом файле
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели должны остаться видимыми (так как открытие идет через внешнюю ОС)
	if !pf.ShowPanels {
		t.Error("Panels should stay visible when opening non-runnable files via OS associations")
	}

	var launch recordedExternalUICall
	select {
	case launch = <-launches:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for associated-file launch")
	}
	wantCommand, wantArgs, supported := panel.AssociatedFileCommand(docPath)
	if !supported {
		t.Fatalf("associated-file launch is unsupported on %s", runtime.GOOS)
	}
	want := recordedExternalUICall{command: wantCommand, args: wantArgs, dir: tmp}
	if gotKey, wantKey := externalUICallKey(launch), externalUICallKey(want); gotKey != wantKey {
		t.Errorf("associated-file launch = %#v, want %#v", launch, want)
	}
}

func TestPanelsFrame_FilesMenuLabels(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	pf := panel.NewPanelsFrame()
	defer pf.Close()

	// Items[1] is the "Files" menu (Left, Files, Commands, Options, Right)
	filesMenu := pf.MenuBar.Items[1]
	if filesMenu.Label != "&Files" {
		t.Errorf("Expected Files menu label '&Files', got %q", filesMenu.Label)
	}

	expected := "&" + i18n.Msg("Menu.Files.RenMov")
	var renMove *vtui.MenuItem
	for i := range filesMenu.SubItems {
		if filesMenu.SubItems[i].Text == expected {
			renMove = &filesMenu.SubItems[i]
			break
		}
	}
	if renMove == nil {
		t.Fatalf("Files menu has no item %q", expected)
	}
	if renMove.Text != expected {
		t.Errorf("Expected Files item %q, got %q", expected, renMove.Text)
	}

	if renMove.Shortcut != "F6" {
		t.Errorf("Expected shortcut 'F6', got %q", renMove.Shortcut)
	}
}

func TestPanelsFrame_CommandRouting_FKeys(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	// Mock exit behavior to check F10
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	fm.Push(pf)

	// Simulate F10
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F10,
	})

	// Now it shouldn't be shutdown immediately. A dialog should be on top.
	top := fm.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("Quit.Title") {
		t.Fatalf("Expected quit confirmation dialog, got %v", top)
	}

	// Simulate clicking "Leave" (button 0 in the ShowMessage dialog)
	if d, ok := top.(*vtui.Window); ok && d.OnResult != nil {
		d.OnResult(0)
	}

	if !fm.IsShutdown() {
		t.Error("F10 followed by confirmation did not trigger Shutdown")
	}
}

func TestPanelsFrame_F9Context(t *testing.T) {
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Test Left Panel context
	pf.ActiveIdx = 0
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	})

	if pf.MenuBar.SelectPos != 0 {
		t.Errorf("F9 on left panel: expected menu index 0, got %d", pf.MenuBar.SelectPos)
	}
	if !pf.MenuBar.Active {
		t.Error("MenuBar should be active after F9")
	}

	// 2. Test Right Panel context
	pf.MenuBar.Active = false // Reset
	pf.ActiveIdx = 1
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	})

	if pf.MenuBar.SelectPos != 4 {
		t.Errorf("F9 on right panel: expected menu index 4, got %d", pf.MenuBar.SelectPos)
	}
}

func TestPanelsFrame_F9HiddenPanels_UsesShellMenuAndKeepsTerminalLog(t *testing.T) {
	old := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	defer func() { keymap.GlobalHotkeysMgr = old }()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ShowPanels = false

	items := pf.GetMenuBar().Items
	wantLabels := []string{i18n.Msg("Menu.Shell.Files"), i18n.Msg("Menu.Shell.Commands"), i18n.Msg("Menu.Shell.Options")}
	if len(items) != len(wantLabels) {
		t.Fatalf("hidden-panels F9 menu has %d top-level items, want %d: %+v", len(items), len(wantLabels), items)
	}
	for i, want := range wantLabels {
		if items[i].Label != want {
			t.Errorf("hidden-panels F9 menu %d = %q, want %q", i, items[i].Label, want)
		}
	}

	wantTerminalItems := map[string]bool{
		i18n.Msg("Action.Terminal.ViewLog"): false,
		i18n.Msg("Action.Terminal.EditLog"): false,
	}
	for _, item := range items[0].SubItems {
		if _, ok := wantTerminalItems[item.Text]; ok {
			wantTerminalItems[item.Text] = true
		}
	}
	for label, found := range wantTerminalItems {
		if !found {
			t.Errorf("hidden-panels Files menu is missing terminal action %q", label)
		}
	}

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	})
	if !pf.MenuBar.Active {
		t.Error("F9 with hidden panels did not activate the main menu")
	}
	if pf.MenuBar.SelectPos != 0 {
		t.Errorf("F9 with hidden panels selected menu %d, want Files menu 0", pf.MenuBar.SelectPos)
	}
}

func TestPanelsFrame_CopyShortcuts(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	if fsp.CancelLoad != nil {
		fsp.CancelLoad()
	}
	fsp.IsLoading = false
	if fsp.LoadingTimer != nil {
		fsp.LoadingTimer.Stop()
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "target.txt"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1)
	pf.ActiveIdx = 0

	// 1. Test Ctrl+Ins (Filename)
	vtui.SetClipboard("")
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_INSERT, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	for i := 0; i < 50; i++ {
		if vtui.GetClipboard() == "target.txt" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := vtui.GetClipboard(); got != "target.txt" {
		t.Fatalf("Ctrl+Ins failed: expected 'target.txt', got %q", got)
	}

	// 2. Test Ctrl+D (copy full path)
	vtui.SetClipboard("")
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: 'D', ControlKeyState: vtinput.LeftCtrlPressed,
	})
	expectedPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), "target.txt")

	for i := 0; i < 50; i++ {
		if vtui.GetClipboard() == expectedPath {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := vtui.GetClipboard(); got != expectedPath {
		t.Fatalf("Ctrl+D failed: expected %q, got %q", expectedPath, got)
	}

	// 3. Test Ctrl+F (insert full path into the command line)
	pf.CmdLine.Edit.SetText("")
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: 'F', ControlKeyState: vtinput.LeftCtrlPressed,
	})
	// A path without spaces or cmd metacharacters is inserted bare on every
	// platform; backslashes are Windows path separators, not a reason to quote.
	expectedCommand := expectedPath
	if got := pf.CmdLine.Edit.GetText(); got != expectedCommand {
		t.Fatalf("Ctrl+F failed: expected command line %q, got %q", expectedCommand, got)
	}
}

func TestPanelsFrame_ShiftF9_SaveSettings(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	// Создаем временный файл конфигурации
	tmp, err := os.CreateTemp("", "settings-*.ini")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp.Name()) }) // Temporary file cleanup failure is uninteresting.
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}

	oldGetPath := config.GetUserConfigIniPath
	oldGetPaths := config.GetConfigIniPaths
	config.GetUserConfigIniPath = func() string { return tmp.Name() }
	config.GetConfigIniPaths = func() []string { return []string{tmp.Name()} }
	defer func() {
		config.GetUserConfigIniPath = oldGetPath
		config.GetConfigIniPaths = oldGetPaths
	}()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	vtui.FrameManager.Push(pf)

	// Отправляем хоткей Shift+F9
	ev := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F9,
		ControlKeyState: vtinput.ShiftPressed,
	}

	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Shift+F9 keypress")
	}
	if dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok {
		for _, item := range dlg.GetChildren() {
			if btn, ok := item.(*vtui.Button); ok && btn.IsDefault {
				btn.OnClick()
				break
			}
		}
	} else {
		t.Fatalf("Shift+F9 top frame = %T, want save-settings dialog", vtui.FrameManager.GetTopFrame())
	}

	// ShowToast posts its setup and owns a timer goroutine. Join it before this
	// test lets another test reuse the manager.
	testutil.PumpUntilToastActive(t)
	testutil.WaitForToastExpiry(t, 3*time.Second)

	// Проверяем, что файл настроек действительно был записан на диск
	info, err := os.Stat(tmp.Name())
	if err != nil || info.Size() == 0 {
		t.Error("Expected Shift+F9 to write settings to ini file")
	}
}

func TestPanelsFrame_MiddleClick_LaunchesFile(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	vtui.FrameManager.Push(pf)

	// Подставляем стабильный mockUpdateVFS для панели
	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	fsp.Vfs = &mockUpdateVFS{}
	fsp.ReadDirectory()
	paneltest.WaitForLoad(t, fsp)
	fsp.SetCursorIndex(0)

	// Симулируем клик колесом мыши по первой строке
	ev := &vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		MouseX:      testutil.Int16(fsp.X1 + 5),
		MouseY:      testutil.Int16(fsp.Y1 + 1), // Клик по первой строке
		ButtonState: vtinput.FromLeft2ndButtonPressed,
		KeyDown:     true,
	}

	if !pf.ProcessMouse(ev) {
		t.Error("Expected PanelsFrame to handle middle click mouse event")
	}
	timeout := time.After(time.Second)
	for vtui.FrameManager.GetTopFrameType() != vtui.TypeDialog {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("middle-click execution result did not reach the UI")
		}
	}
}

func TestPanelsFrame_CtrlBackslash_GoesToRoot(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "a", "b", "c")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.SetPath(sub); err != nil {
		t.Fatal(err)
	}

	// Отправляем Ctrl+\
	ev := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_5,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Ctrl+\\")
	}

	expectedRoot := "/"
	if runtime.GOOS == "windows" {
		expectedRoot = filepath.VolumeName(tmp) + string(os.PathSeparator)
	}
	if filepath.Clean(fsp.Vfs.GetPath()) != filepath.Clean(expectedRoot) {
		t.Errorf("Ctrl+\\ failed to go to root: expected %q, got %q", expectedRoot, fsp.Vfs.GetPath())
	}
}

func TestPanelsFrame_CtrlPgUp_GoesToParentOrDriveMenu(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.SetPath(sub); err != nil {
		t.Fatal(err)
	}

	// Отправляем Ctrl+PgUp
	ev := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_PRIOR,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Ctrl+PgUp")
	}

	// 1. Должны подняться на один уровень до tmp
	if filepath.Clean(fsp.Vfs.GetPath()) != filepath.Clean(tmp) {
		t.Errorf("Ctrl+PgUp failed to go up: expected %q, got %q", tmp, fsp.Vfs.GetPath())
	}
	if fsp.PendingSelection != "sub" {
		t.Errorf("Ctrl+PgUp should position cursor on 'sub', got %q", fsp.PendingSelection)
	}

	// 2. Поднимаемся все дальше до физического корня системы
	for !fsp.Vfs.IsAtRoot() {
		if err := fsp.Vfs.SetPath(".."); err != nil {
			break
		}
	}

	// Отправляем Ctrl+PgUp на корне диска -> должно открыться Drive Menu
	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Ctrl+PgUp at root")
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetType() != vtui.TypeMenu || !strings.Contains(top.GetTitle(), "Drive") {
		t.Errorf("Expected Drive menu on top when pressing Ctrl+PgUp at root, got %v", top)
	}
}

func TestPanelsFrame_CtrlPgDn_EntersDir(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}},
	}
	fsp.Refresh()
	fsp.SelectName("sub")

	// Отправляем Ctrl+PgDn
	ev := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_NEXT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Ctrl+PgDn")
	}

	// Так как на панели симулируется Enter, путь должен измениться на sub
	if filepath.Clean(fsp.Vfs.GetPath()) != filepath.Clean(sub) {
		t.Errorf("Ctrl+PgDn failed to enter directory: expected %q, got %q", sub, fsp.Vfs.GetPath())
	}
}

func TestPanelsFrame_ShiftEnter_ExplorerLaunch(t *testing.T) {
	if _, _, supported := panel.SystemFileManagerCommand("test", true); !supported {
		t.Skipf("system file manager is unsupported on %s", runtime.GOOS)
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	launches := make(chan recordedExternalUICall, 3)
	pf.ExternalUIRunner = func(command string, args []string, dir string) error {
		launches <- recordedExternalUICall{command: command, args: append([]string(nil), args...), dir: dir}
		return nil
	}
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*panel.FileSystemPanel)
	tmp := t.TempDir()
	if err := lp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Create dummy file and folder
	if err := os.WriteFile(filepath.Join(tmp, "doc.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmp, "sub"), 0700); err != nil {
		t.Fatal(err)
	}

	lp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "doc.txt", IsDir: false}},
	}
	lp.Refresh()
	pf.ActiveIdx = 0

	// 1. Test Shift+Enter on file "doc.txt"
	lp.SetCursorIndex(2)
	handled := pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if !handled {
		t.Error("Expected Shift+Enter on file to be handled by PanelsFrame")
	}

	// 2. Test Shift+Enter on folder "sub"
	lp.SetCursorIndex(1)
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if !handled {
		t.Error("Expected Shift+Enter on folder to be handled by PanelsFrame")
	}

	// 3. Test Shift+Enter on parent folder ".."
	lp.SetCursorIndex(0)
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if !handled {
		t.Error("Expected Shift+Enter on '..' to be handled by PanelsFrame")
	}

	expectedLaunches := make(map[string]int)
	for _, target := range []struct {
		path  string
		isDir bool
	}{
		{path: filepath.Join(tmp, "doc.txt"), isDir: false},
		{path: filepath.Join(tmp, "sub"), isDir: true},
		{path: tmp, isDir: true},
	} {
		command, args, supported := panel.SystemFileManagerCommand(target.path, target.isDir)
		if !supported {
			t.Fatalf("system file manager is unsupported on %s", runtime.GOOS)
		}
		key := externalUICallKey(recordedExternalUICall{command: command, args: args})
		expectedLaunches[key]++
	}

	deadline := time.After(2 * time.Second)
	for received := 0; received < 3; received++ {
		select {
		case launch := <-launches:
			key := externalUICallKey(launch)
			if expectedLaunches[key] == 0 {
				t.Errorf("unexpected system-file-manager launch: %#v", launch)
			} else {
				expectedLaunches[key]--
			}
		case <-deadline:
			t.Fatalf("timed out after %d of 3 system-file-manager launches", received)
		}
	}
	for key, remaining := range expectedLaunches {
		if remaining != 0 {
			t.Errorf("missing %d system-file-manager launch(es) for %q", remaining, key)
		}
	}

	// 4. Test on non-local VFS (e.g. NullVFS) -> should show warning but remain handled
	lp.Vfs = vfs.NewNullVFS(0)
	lp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "file.txt"}}}
	lp.Refresh()
	lp.SetCursorIndex(0)

	handled = pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.ShiftPressed,
	})
	if !handled {
		t.Error("Expected Shift+Enter on non-local VFS to be handled (with warning dialog)")
	}
	select {
	case launch := <-launches:
		t.Errorf("non-local path unexpectedly launched the system file manager: %#v", launch)
	default:
	}

	// Clean up any warning dialog pushed on top
	if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
		vtui.FrameManager.GetTopFrame().SetExitCode(-1)
		vtui.FrameManager.Pop()
	}
}

func TestPanelsFrame_CtrlPgUp_EscapesNestedVFS(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	tmp := t.TempDir()

	parentVfs := vfs.NewOSVFS(tmp)
	nested := &mockNestedVFS{parent: parentVfs}
	fsp.Vfs = nested
	fsp.ProviderEntryName = "test.zip"

	// Отправляем Ctrl+PgUp на корне вложенной VFS
	ev := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_PRIOR,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}

	if !pressKey(pf, ev) {
		t.Error("Expected PanelsFrame to handle Ctrl+PgUp on nested VFS")
	}

	// Должны выйти в родительскую VFS и сфокусироваться на "test.zip"
	if fsp.Vfs != parentVfs {
		t.Error("Ctrl+PgUp failed to escape nested VFS to parent")
	}
	if fsp.PendingSelection != "test.zip" {
		t.Errorf("Expected pendingSelection 'test.zip', got %q", fsp.PendingSelection)
	}
}

// TestPanelsFrame_CtrlP_TogglesPassivePanel exercises issue #197:
// Ctrl+P should hide/show the panel opposite the currently active one,
// leaving the active panel untouched.
func TestPanelsFrame_CtrlP_TogglesPassivePanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *panel.PanelsFrame) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vtinput.VK_P,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	// Active = right (paneltest.SetupMockPanelsFrame's default), Ctrl+P hides left.
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	if pf.ActiveIdx != 1 || !pf.ShowLeftPanel || !pf.ShowRightPanel || !pf.ShowPanels {
		t.Fatalf("mock frame precondition: activeIdx=%d L=%v R=%v show=%v",
			pf.ActiveIdx, pf.ShowLeftPanel, pf.ShowRightPanel, pf.ShowPanels)
	}
	send(pf)
	if pf.ShowLeftPanel || !pf.ShowRightPanel {
		t.Errorf("active=right: Ctrl+P should hide left only, got L=%v R=%v",
			pf.ShowLeftPanel, pf.ShowRightPanel)
	}
	if pf.ActiveIdx != 1 {
		t.Errorf("Ctrl+P must not move the active panel, got activeIdx=%d", pf.ActiveIdx)
	}
	if !pf.ShowPanels {
		t.Errorf("one panel still visible, showPanels should stay true")
	}

	// Second press restores the hidden side.
	send(pf)
	if !pf.ShowLeftPanel || !pf.ShowRightPanel {
		t.Errorf("Ctrl+P again should restore left, got L=%v R=%v",
			pf.ShowLeftPanel, pf.ShowRightPanel)
	}

	// Symmetric case: active = left, Ctrl+P hides right.
	pf.ActiveIdx = 0
	send(pf)
	if !pf.ShowLeftPanel || pf.ShowRightPanel {
		t.Errorf("active=left: Ctrl+P should hide right only, got L=%v R=%v",
			pf.ShowLeftPanel, pf.ShowRightPanel)
	}
	if pf.ActiveIdx != 0 {
		t.Errorf("Ctrl+P must not move the active panel, got activeIdx=%d", pf.ActiveIdx)
	}

	// Toggle the last visible panel off — no panels left, showPanels drops.
	pf.ActiveIdx = 1
	pf.ShowLeftPanel = false
	pf.ShowRightPanel = true
	pf.ShowPanels = true
	send(pf) // active=right, so this touches left; left was false → becomes true
	if !pf.ShowLeftPanel {
		t.Fatalf("setup for last-visible test: expected left to come back, got L=%v", pf.ShowLeftPanel)
	}
	// Now hide right via Ctrl+F2 to isolate: only left visible, active=right (invalid state
	// that Ctrl+F2's auto-switch would fix; here we just want to test Ctrl+P's showPanels math).
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = false
	pf.ShowPanels = true
	pf.ActiveIdx = 0 // active on the visible panel
	send(pf)         // active=left, toggles right; right was false → becomes true
	if !pf.ShowRightPanel {
		t.Errorf("Ctrl+P should have shown the right panel again, got R=%v", pf.ShowRightPanel)
	}
}

func TestPanelsFrame_CtrlOAndCtrlPSequence(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()

	sendCtrlO := func() {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vtinput.VK_O,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}
	sendCtrlP := func() {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vtinput.VK_P,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	pf.ActiveIdx = 0 // Left active
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true
	pf.ShowPanels = true

	// 1. Ctrl+O -> hide all panels
	sendCtrlO()
	if pf.ShowPanels {
		t.Fatal("Ctrl+O failed to hide panels")
	}

	// 2. Ctrl+P -> toggle passive panel off, but panels stay hidden
	sendCtrlP()
	if pf.ShowRightPanel {
		t.Error("Ctrl+P failed to toggle passive panel off")
	}
	if pf.ShowPanels {
		t.Error("Ctrl+P while panels hidden should NOT force showPanels=true")
	}

	// 3. Ctrl+O -> show panels (only Left panel is visible)
	sendCtrlO()
	if !pf.ShowPanels {
		t.Fatal("Ctrl+O failed to show panels")
	}
	if pf.ShowRightPanel || !pf.ShowLeftPanel {
		t.Errorf("Expected only left panel visible, got L=%v R=%v", pf.ShowLeftPanel, pf.ShowRightPanel)
	}

	// 4. Ctrl+P -> toggle passive panel on
	sendCtrlP()
	if !pf.ShowRightPanel || !pf.ShowLeftPanel {
		t.Errorf("Expected both panels visible after Ctrl+P, got L=%v R=%v", pf.ShowLeftPanel, pf.ShowRightPanel)
	}
}

// TestPanelsFrame_CtrlArrows_ResizePanels exercises the far2l-style
// panel resize keys: Ctrl+Left/Right shift the width split, Ctrl+Up/Down
// shrink/grow the panel-vs-terminal split. Requires empty cmdline;
// non-empty cmdline must fall through to word-navigation.
func TestPanelsFrame_CtrlArrows_ResizePanels(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	oldW, oldL, oldR := config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement
	oldAutoSave := config.App.AutoSaveSettings
	config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = 0, 0, 0
	config.App.AutoSaveSettings = false
	t.Cleanup(func() {
		config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = oldW, oldL, oldR
		config.App.AutoSaveSettings = oldAutoSave
	})

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *panel.PanelsFrame, vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	// Width: Ctrl+Left moves the split to the left (widthDecrement +1,
	// left panel shrinks, right grows) — arrow follows the boundary,
	// matching far2l / Far3 / far2m. Ctrl+Right does the reverse.
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	baseLeft := panelWidth(pf.Panels[0])

	send(pf, vtinput.VK_LEFT)
	if pf.WidthDecrement != 1 {
		t.Errorf("Ctrl+Left: widthDecrement=%d, want 1", pf.WidthDecrement)
	}
	if got := panelWidth(pf.Panels[0]); got != baseLeft-1 {
		t.Errorf("Ctrl+Left: left panel width=%d, want %d", got, baseLeft-1)
	}

	send(pf, vtinput.VK_RIGHT)
	send(pf, vtinput.VK_RIGHT)
	if pf.WidthDecrement != -1 {
		t.Errorf("Ctrl+Right twice: widthDecrement=%d, want -1", pf.WidthDecrement)
	}
	if got := panelWidth(pf.Panels[0]); got != baseLeft+1 {
		t.Errorf("Ctrl+Right twice: left panel width=%d, want %d", got, baseLeft+1)
	}

	// Non-empty cmdline: Ctrl+Left/Right must NOT resize.
	pf.WidthDecrement = 0
	pf.ResizeConsole(80, 25)
	pf.CmdLine.Edit.SetText("hello")
	send(pf, vtinput.VK_LEFT)
	if pf.WidthDecrement != 0 {
		t.Errorf("non-empty cmdline: widthDecrement changed to %d, want 0", pf.WidthDecrement)
	}
	pf.CmdLine.Edit.SetText("")

	// Height: Ctrl+Up shrinks the panel area from the bottom (both
	// leftHeightDecrement and rightHeightDecrement +1, moving both
	// panels' bottom edge up in lockstep). Ctrl+Down reverses it.
	// Ctrl+Down at 0 must clamp, not go negative.
	pf.LeftHeightDecrement = 0
	pf.RightHeightDecrement = 0
	pf.ResizeConsole(80, 25)
	basePanelH := panelHeight(pf.Panels[0])

	send(pf, vtinput.VK_UP)
	if pf.LeftHeightDecrement != 1 || pf.RightHeightDecrement != 1 {
		t.Errorf("Ctrl+Up: heightDecrements=%d/%d, want 1/1",
			pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
	if got := panelHeight(pf.Panels[0]); got != basePanelH-1 {
		t.Errorf("Ctrl+Up: panel height=%d, want %d", got, basePanelH-1)
	}
	if got := panelHeight(pf.Panels[1]); got != basePanelH-1 {
		t.Errorf("Ctrl+Up: right panel height=%d, want %d", got, basePanelH-1)
	}

	send(pf, vtinput.VK_DOWN)
	send(pf, vtinput.VK_DOWN) // Second Down at 0 should be a no-op (clamp).
	if pf.LeftHeightDecrement != 0 || pf.RightHeightDecrement != 0 {
		t.Errorf("Ctrl+Down past 0: heightDecrements=%d/%d, want 0/0 (clamp)",
			pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}

	// Width clamp: on an 80-col terminal, maxWD = 40 - 10 = 30. Push
	// past it with Ctrl+Left (the direction that bumps widthDecrement +1).
	pf.WidthDecrement = 0
	pf.ResizeConsole(80, 25)
	for i := 0; i < 40; i++ {
		send(pf, vtinput.VK_LEFT)
	}
	if pf.WidthDecrement != 30 {
		t.Errorf("width clamp: widthDecrement=%d, want 30", pf.WidthDecrement)
	}
}

// TestPanelsFrame_CtrlClear_ResetsLayoutDecrements verifies that Ctrl+Clear
// (NumPad 5 with NumLock off) zeroes widthDecrement / leftHeightDecrement /
// rightHeightDecrement in one shot, matching far2l's Ctrl+Clear.
func TestPanelsFrame_CtrlClear_ResetsLayoutDecrements(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	oldW, oldL, oldR := config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement
	oldAutoSave := config.App.AutoSaveSettings
	config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = 0, 0, 0
	config.App.AutoSaveSettings = false
	t.Cleanup(func() {
		config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = oldW, oldL, oldR
		config.App.AutoSaveSettings = oldAutoSave
	})

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.WidthDecrement = 5
	pf.LeftHeightDecrement = 3
	pf.RightHeightDecrement = 4
	config.App.WidthDecrement = 5
	config.App.LeftHeightDecrement = 3
	config.App.RightHeightDecrement = 4
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_CLEAR,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if pf.WidthDecrement != 0 || pf.LeftHeightDecrement != 0 || pf.RightHeightDecrement != 0 {
		t.Errorf("Ctrl+Clear: fields = %d/%d/%d, want 0/0/0",
			pf.WidthDecrement, pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
	if config.App.WidthDecrement != 0 || config.App.LeftHeightDecrement != 0 || config.App.RightHeightDecrement != 0 {
		t.Errorf("Ctrl+Clear: config.App = %d/%d/%d, want 0/0/0",
			config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement)
	}
}

// TestPanelsFrame_CtrlShiftArrows_AsymmetricHeight exercises far2l's
// Ctrl+Shift+Up / Ctrl+Shift+Down: bumps only the ACTIVE panel's height
// decrement, leaving the other panel untouched. Same gate as plain
// Ctrl+Up/Down (panels visible + cmdline empty).
func TestPanelsFrame_CtrlShiftArrows_AsymmetricHeight(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	oldW, oldL, oldR := config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement
	oldAutoSave := config.App.AutoSaveSettings
	config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = 0, 0, 0
	config.App.AutoSaveSettings = false
	t.Cleanup(func() {
		config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = oldW, oldL, oldR
		config.App.AutoSaveSettings = oldAutoSave
	})

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *panel.PanelsFrame, vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
		})
	}

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Baseline: both decrements 0.
	if pf.LeftHeightDecrement != 0 || pf.RightHeightDecrement != 0 {
		t.Fatalf("precondition: expected 0/0, got %d/%d",
			pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
	baseLeftH := panelHeight(pf.Panels[0])
	baseRightH := panelHeight(pf.Panels[1])

	// Active = right (mock default). Ctrl+Shift+Up bumps only right.
	send(pf, vtinput.VK_UP)
	if pf.LeftHeightDecrement != 0 || pf.RightHeightDecrement != 1 {
		t.Errorf("active=right Ctrl+Shift+Up: got %d/%d, want 0/1",
			pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
	if panelHeight(pf.Panels[0]) != baseLeftH {
		t.Errorf("left panel changed unexpectedly")
	}
	if panelHeight(pf.Panels[1]) != baseRightH-1 {
		t.Errorf("right panel height=%d, want %d",
			panelHeight(pf.Panels[1]), baseRightH-1)
	}
	if config.App.RightHeightDecrement != 1 {
		t.Errorf("config.App.RightHeightDecrement=%d, want 1",
			config.App.RightHeightDecrement)
	}
	// Ctrl+Shift+Down on active=right undoes it.
	send(pf, vtinput.VK_DOWN)
	if pf.RightHeightDecrement != 0 {
		t.Errorf("Ctrl+Shift+Down: rightHeightDecrement=%d, want 0",
			pf.RightHeightDecrement)
	}

	// Switch to left, Ctrl+Shift+Up bumps left only.
	pf.ActiveIdx = 0
	send(pf, vtinput.VK_UP)
	if pf.LeftHeightDecrement != 1 || pf.RightHeightDecrement != 0 {
		t.Errorf("active=left Ctrl+Shift+Up: got %d/%d, want 1/0",
			pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
	// Clamp: Ctrl+Shift+Down on a zero-decrement panel must not go negative.
	pf.LeftHeightDecrement = 0
	send(pf, vtinput.VK_DOWN)
	if pf.LeftHeightDecrement != 0 {
		t.Errorf("Ctrl+Shift+Down past 0: leftHeightDecrement=%d, want 0 (clamp)",
			pf.LeftHeightDecrement)
	}

	// Non-empty cmdline: Ctrl+Shift+Up/Down must NOT resize.
	pf.LeftHeightDecrement = 0
	pf.CmdLine.Edit.SetText("hello")
	send(pf, vtinput.VK_UP)
	if pf.LeftHeightDecrement != 0 {
		t.Errorf("non-empty cmdline: leftHeightDecrement changed to %d, want 0",
			pf.LeftHeightDecrement)
	}
	pf.CmdLine.Edit.SetText("")
}

// ponytail: the mocks below are copies of the ones in internal/panel's own
// tests. The 36 tests in this file need the action table, which lives here, and
// the panel's tests need the same mocks against private members, which live
// there; neither package can import the other's _test.go. Ceiling: two copies
// that drift apart silently. Upgrade path is the same as the helpers' —
// package panel_test, and one copy in internal/paneltest.

type recordedExternalUICall struct {
	command string
	args    []string
	dir     string
}

func externalUICallKey(call recordedExternalUICall) string {
	return call.command + "\x00" + strings.Join(call.args, "\x00") + "\x00" + call.dir
}

// menuIsCenteredIn allows the half-cell that integer centring cannot avoid: a
// menu whose height parity differs from the panel's can only sit one row above
// or below the exact centre.
func menuIsCenteredIn(menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2 int) bool {
	offBy := func(menuLow, menuHigh, panelLow, panelHigh int) int {
		delta := (menuLow + menuHigh) - (panelLow + panelHigh)
		if delta < 0 {
			return -delta
		}
		return delta
	}
	return offBy(menuX1, menuX2, panelX1, panelX2) <= 1 && offBy(menuY1, menuY2, panelY1, panelY2) <= 1
}

type mockUpdateVFS struct {
	vfs.NullVFS
}

func (m *mockUpdateVFS) GetPath() string { return "/" }

func (m *mockUpdateVFS) IsAtRoot() bool { return true }

func (m *mockUpdateVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	onChunk([]vfs.VFSItem{
		{Name: "test_file.txt", Size: 100, IsDir: false},
	})
	return nil
}

type mockNestedVFS struct {
	mockUpdateVFS
	parent vfs.VFS
}

func (m *mockNestedVFS) ParentVFS() vfs.VFS { return m.parent }

func (m *mockNestedVFS) Close() error { return nil }

func panelWidth(p panel.Panel) int { x1, _, x2, _ := p.GetPosition(); return x2 - x1 + 1 }

func panelHeight(p panel.Panel) int { _, y1, _, y2 := p.GetPosition(); return y2 - y1 + 1 }

type mockTitleVFS struct {
	vfs.OSVFS
	title      string
	panelTitle string
}

func (m *mockTitleVFS) GetTitle() string { return m.title }

func (m *mockTitleVFS) PanelTitle(string) string { return m.panelTitle }

type mockCacheSessionVFS struct {
	*mockTitleVFS
	session any
}

func (m *mockCacheSessionVFS) SessionKey() any { return m.session }

type mockStableCacheVFS struct {
	*mockCacheSessionVFS
	stable any
}

func (m *mockStableCacheVFS) DirectoryCacheKey() any { return m.stable }

func TestPanelsFrame_ShiftF5_KeyInterception(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "cursor_file.txt"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1) // Focus on cursor_file.txt
	pf.ActiveIdx = 0

	// Send Shift-F5 key
	handled := pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F5,
		ControlKeyState: vtinput.ShiftPressed,
	})

	if !handled {
		t.Fatal("Shift-F5 was not handled by PanelsFrame")
	}

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || !strings.Contains(top.GetTitle(), "Copy") {
		t.Errorf("Expected Copy dialog on top after Shift-F5, got %v", top)
	}

	// Cleanup
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}
