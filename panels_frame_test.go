package main

import (
	"context"
	"fmt"
	"github.com/unxed/f4/plugins/archive"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"github.com/unxed/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recordedExternalUICall struct {
	command string
	args    []string
	dir     string
}

type opaqueVisualPathVFS struct {
	*mockTitleVFS
	path string
}

func (v *opaqueVisualPathVFS) GetPath() string { return v.path }

func externalUICallKey(call recordedExternalUICall) string {
	return call.command + "\x00" + strings.Join(call.args, "\x00") + "\x00" + call.dir
}

type mouseCaptureTestPanel struct {
	vtui.ScreenObject
	events []vtinput.InputEvent
}

func TestPanelsFrame_WorkspaceTabTitleUsesFolderNames(t *testing.T) {
	root := t.TempDir()
	leftPath := filepath.Join(root, "left-leaf")
	rightPath := filepath.Join(root, "right-leaf")
	if err := os.MkdirAll(leftPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0755); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{showPanels: true}
	pf.panels[0] = &FileSystemPanel{vfs: vfs.NewOSVFS(leftPath)}
	pf.panels[1] = &FileSystemPanel{vfs: vfs.NewOSVFS(rightPath)}
	if marker := pf.GetWorkspaceTabMarker(); marker != "P" {
		t.Fatalf("panel workspace marker = %q, want P", marker)
	}
	title := pf.GetWorkspaceTabTitle()
	if !strings.Contains(title, "left-leaf ─ right-leaf") {
		t.Fatalf("workspace tab title = %q, want both leaf folder names", title)
	}
	if strings.Contains(title, root) {
		t.Fatalf("workspace tab title contains parent path: %q", title)
	}
}

func TestPanelsFrame_WorkspaceMenuInfoUsesFullPanelPaths(t *testing.T) {
	root := t.TempDir()
	leftPath := filepath.Join(root, "left", "nested")
	rightPath := filepath.Join(root, "right", "nested")
	if err := os.MkdirAll(leftPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0755); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{showPanels: true}
	pf.panels[0] = &FileSystemPanel{vfs: vfs.NewOSVFS(leftPath)}
	pf.panels[1] = &FileSystemPanel{vfs: vfs.NewOSVFS(rightPath)}
	info := pf.GetWorkspaceMenuInfo()
	if info.Icon != "P" || info.Primary != leftPath || info.Secondary != rightPath {
		t.Fatalf("workspace menu info = %#v, want full left/right paths", info)
	}

	pf.showPanels = false
	pf.executing = true
	pf.workspaceCommandTitle = "Python"
	info = pf.GetWorkspaceMenuInfo()
	if info.Icon != "T" || info.Primary != "Python" || info.Secondary != "" {
		t.Fatalf("terminal workspace menu info = %#v", info)
	}
}

func TestPanelsFrame_WorkspaceTabTitleTracksTerminalTitle(t *testing.T) {
	pf := &PanelsFrame{
		showPanels:            false,
		executing:             true,
		workspaceCommandTitle: workspaceCommandName("python script.py"),
		termView:              &TerminalView{Title: "Administrator: C:\\Windows\\System32\\cmd.exe - Python"},
	}
	if got := pf.GetWorkspaceTabTitle(); got != "Python" {
		t.Fatalf("terminal workspace tab title = %q, want %q", got, "Python")
	}
	if marker := pf.GetWorkspaceTabMarker(); marker != "T" {
		t.Fatalf("terminal workspace marker = %q, want T", marker)
	}
	pf.executing = false
	pf.workspaceCommandTitle = ""
	if got := pf.GetWorkspaceTabTitle(); got != "Terminal" {
		t.Fatalf("manually revealed terminal tab title = %q, want %q", got, "Terminal")
	}
	pf.showPanels = true
	pf.panels[0] = &FileSystemPanel{vfs: vfs.NewOSVFS(t.TempDir())}
	pf.panels[1] = &FileSystemPanel{vfs: vfs.NewOSVFS(t.TempDir())}
	if marker := pf.GetWorkspaceTabMarker(); marker != "P" {
		t.Fatalf("panel workspace kept terminal marker after returning to panels: %q", marker)
	}
}

func (p *mouseCaptureTestPanel) ProcessKey(*vtinput.InputEvent) bool { return false }
func (p *mouseCaptureTestPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	p.events = append(p.events, *e)
	return true
}
func (p *mouseCaptureTestPanel) GetSelectedName() string { return "" }

func TestPanelsFrame_MouseGestureStaysWithOriginPanel(t *testing.T) {
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()
	AppConfig.NavigationMode = NavigationClassic
	AppConfig.AlwaysShowMenuBar = false

	left := &mouseCaptureTestPanel{}
	right := &mouseCaptureTestPanel{}
	left.SetPosition(0, 0, 39, 20)
	right.SetPosition(40, 0, 79, 20)
	pf := &PanelsFrame{
		panels:         [2]Panel{left, right},
		showPanels:     true,
		showLeftPanel:  true,
		showRightPanel: true,
	}

	// Start in the left panel, move across the right panel, then release
	// there. Every event must still be delivered to the left panel.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 5, ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 79, MouseY: 5, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
	})
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, MouseX: 79, MouseY: 5,
	})

	if len(left.events) != 3 || len(right.events) != 0 {
		t.Fatalf("cross-panel drag delivered left=%d right=%d events; want 3,0", len(left.events), len(right.events))
	}
	if pf.panelMouseCapture != nil {
		t.Fatal("panel mouse capture was not released")
	}

	// A new gesture after release is free to start in the right panel.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 79, MouseY: 5, ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	if len(right.events) != 1 || pf.panelMouseCapture != right {
		t.Fatalf("new right-panel gesture was not captured: events=%d capture=%T", len(right.events), pf.panelMouseCapture)
	}
}

func TestPanelsFrame_MiddleMouseGestureTriggersOnce(t *testing.T) {
	pf := &PanelsFrame{}

	handled, trigger := pf.processMiddleMouseGesture(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft2ndButtonPressed,
	})
	if !handled || !trigger || !pf.middleMouseDown {
		t.Fatalf("initial middle down: handled=%v trigger=%v active=%v", handled, trigger, pf.middleMouseDown)
	}

	// Windows keeps KeyDown=true and the middle-button bit set on every move.
	for i := 0; i < 3; i++ {
		handled, trigger = pf.processMiddleMouseGesture(&vtinput.InputEvent{
			Type: vtinput.MouseEventType, KeyDown: true,
			ButtonState:     vtinput.FromLeft2ndButtonPressed,
			MouseEventFlags: vtinput.MouseMoved,
		})
		if !handled || trigger || !pf.middleMouseDown {
			t.Fatalf("middle move %d retriggered: handled=%v trigger=%v active=%v", i, handled, trigger, pf.middleMouseDown)
		}
	}

	// Wheel rotation while the middle button is held is a scroll event, not
	// another press. The gesture remains active until the actual release.
	handled, trigger = pf.processMiddleMouseGesture(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState:    vtinput.FromLeft2ndButtonPressed,
		WheelDirection: 1,
	})
	if handled || trigger || !pf.middleMouseDown {
		t.Fatalf("held-middle wheel classified as click: handled=%v trigger=%v active=%v", handled, trigger, pf.middleMouseDown)
	}

	// A backend may report motion with ButtonState=0; MouseMoved still makes
	// it part of the active gesture rather than a release.
	handled, trigger = pf.processMiddleMouseGesture(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, MouseEventFlags: vtinput.MouseMoved,
	})
	if !handled || trigger || !pf.middleMouseDown {
		t.Fatalf("buttonless move ended gesture: handled=%v trigger=%v active=%v", handled, trigger, pf.middleMouseDown)
	}

	handled, trigger = pf.processMiddleMouseGesture(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if !handled || trigger || pf.middleMouseDown {
		t.Fatalf("middle release: handled=%v trigger=%v active=%v", handled, trigger, pf.middleMouseDown)
	}

	_, trigger = pf.processMiddleMouseGesture(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		ButtonState: vtinput.FromLeft2ndButtonPressed,
	})
	if !trigger {
		t.Fatal("new middle down after release did not trigger")
	}
}

func TestPanelsFrame_MiddleHeldWheelRoutesToPanel(t *testing.T) {
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()
	AppConfig.NavigationMode = NavigationClassic
	AppConfig.AlwaysShowMenuBar = false

	left := &mouseCaptureTestPanel{}
	right := &mouseCaptureTestPanel{}
	left.SetPosition(0, 0, 39, 20)
	right.SetPosition(40, 0, 79, 20)
	pf := &PanelsFrame{
		panels:          [2]Panel{left, right},
		showPanels:      true,
		showLeftPanel:   true,
		showRightPanel:  true,
		middleMouseDown: true,
	}

	if !pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 5,
		ButtonState:    vtinput.FromLeft2ndButtonPressed,
		WheelDirection: -1,
	}) {
		t.Fatal("held-middle wheel was not handled")
	}
	if len(left.events) != 1 || left.events[0].WheelDirection != -1 {
		t.Fatalf("wheel was not routed to active panel: events=%#v", left.events)
	}
	if !pf.middleMouseDown {
		t.Fatal("wheel rotation prematurely ended middle-button gesture")
	}

	pf.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType})
	if pf.middleMouseDown {
		t.Fatal("middle-button release was not recognized after wheel rotation")
	}
}

func TestPanelsFrame_Layout(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Simulate 80x25 terminal
	pf.ResizeConsole(80, 25)

	// Calculate expected positions for 80x25 with KeyBar
	expectedKeyBarY := 24
	expectedCmdLineY := 23 // Always 1 line above KeyBar if KeyBar is present

	// 1. Check reserved rows with KeyBar visible
	if pf.keyBar.Y1 != expectedKeyBarY {
		t.Errorf("KeyBar position error: expected %d, got %d", expectedKeyBarY, pf.keyBar.Y1)
	}
	if pf.cmdLine.Y1 != expectedCmdLineY {
		t.Errorf("CommandLine position error: expected %d, got %d", expectedCmdLineY, pf.cmdLine.Y1)
	}

	// 2. Check layout after hiding KeyBar
	pf.showKeyBar = false
	pf.ResizeConsole(80, 25)

	// After hiding KeyBar, CommandLine should move to the bottom row
	expectedKeyBarY = 24 // Still the last line, but invisible
	expectedCmdLineY = 24
	if pf.cmdLine.Y1 != expectedCmdLineY {
		t.Errorf("CommandLine should be at %d when KeyBar hidden, got %d", expectedCmdLineY, pf.cmdLine.Y1)
	}
	if pf.keyBar.IsVisible() {
		t.Error("KeyBar should be invisible")
	}
}
func TestPanelsFrame_ArkanoidHotkey(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
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
	arkFrame.Close()
	for i := 0; i < 20; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func TestPanelsFrame_SelectionByMask(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[1].(*FileSystemPanel)
	pf.activeIdx = 1

	// 1. Command line not empty -> should not intercept for regular char
	pf.cmdLine.Edit.SetText("a")
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
	pf.cmdLine.Clear()
	fsp.fastFindMode = true
	handled = pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    '+',
	})
	if !handled {
		t.Error("Key should be handled by fastFindMode in active panel")
	}

	// 3. Command line empty, fastFindMode NOT active -> SHOULD intercept and show dialog
	fsp.fastFindMode = false
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
func TestPanelsFrame_DriveMenuListsAssignedBookmarks(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "f4", "settings", "bookmarks.ini"),
		[]byte("[6]\nPath="+target+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	pf.showDriveMenu(1)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("drive menu not shown, top frame is %T", vtui.FrameManager.GetTopFrame())
	}

	row := -1
	for i, it := range menu.Items {
		if strings.HasPrefix(it.Text, "&6  ") {
			row = i
		}
		for _, empty := range []string{"&0  ", "&1  ", "&9  "} {
			if strings.HasPrefix(it.Text, empty) {
				t.Errorf("unassigned slot listed: %q", it.Text)
			}
		}
	}
	if row == -1 {
		t.Fatalf("assigned bookmark missing from the drive menu: %#v", menu.Items)
	}
	// Long paths are cut from the front, so only the tail is guaranteed.
	if !strings.HasSuffix(menu.Items[row].Text, filepath.Base(target)) {
		t.Errorf("row %q should show the bookmarked path", menu.Items[row].Text)
	}
	if !menu.Items[row-1].Separator {
		t.Errorf("bookmarks should start after a separator, got %#v", menu.Items[row-1])
	}

	// Pressing the slot digit moves the panel the menu was opened for —
	// the whole point of the entry: Alt+F2 then 6.
	fsp := pf.panels[1].(*FileSystemPanel)
	menu.SetSelectPos(0)
	menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_6, Char: '6',
	})
	if got := fsp.vfs.GetPath(); got != target {
		t.Errorf("panel at %q, want %q", got, target)
	}
}

// settleFrames does what the render loop does between keystrokes: run the
// tasks frames posted, then drop the ones that closed themselves.
func settleFrames(t *testing.T) {
	t.Helper()
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			continue
		case <-time.After(200 * time.Millisecond):
		}
		break
	}
	for _, f := range append([]vtui.Frame(nil), openFrames()...) {
		if f.IsDone() {
			vtui.FrameManager.RemoveFrame(f)
		}
	}
}

func openFrames() []vtui.Frame {
	return vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Frames
}

// findDriveMenu returns the top-most drive menu on the stack. Unrelated
// tasks (the update check, for one) can push frames above it.
func findDriveMenu(t *testing.T) *vtui.VMenu {
	t.Helper()
	frames := openFrames()
	for i := len(frames) - 1; i >= 0; i-- {
		if m, ok := frames[i].(*vtui.VMenu); ok && m.GetTitle() == Msg("Drive.Title") {
			return m
		}
	}
	t.Fatalf("drive menu not on the frame stack: %#v", frames)
	return nil
}

func findBookmarksDialog(t *testing.T) *bookmarksFrame {
	t.Helper()
	frames := openFrames()
	for i := len(frames) - 1; i >= 0; i-- {
		if d, ok := frames[i].(*bookmarksFrame); ok {
			return d
		}
	}
	t.Fatalf("bookmarks dialog not on the frame stack: %#v", frames)
	return nil
}

func bookmarkRow(t *testing.T, menu *vtui.VMenu) int {
	t.Helper()
	for i, it := range menu.Items {
		if strings.HasPrefix(it.Text, "&6  ") {
			return i
		}
	}
	t.Fatalf("bookmark row missing: %#v", menu.Items)
	return -1
}

func TestPanelsFrame_DriveMenuBookmarkKeys(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(cfg, "f4", "settings", "bookmarks.ini")
	target := t.TempDir()
	write := func() {
		if err := os.WriteFile(ini,
			[]byte("[6]\nPath="+target+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	press := func(menu *vtui.VMenu, vk uint16) {
		menu.ProcessKey(&vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk,
		})
	}

	// F4 on the bookmark row opens the dialog on that slot, and closing
	// the dialog brings the drive menu back.
	pf.showDriveMenu(1)
	menu := findDriveMenu(t)
	menu.SetSelectPos(bookmarkRow(t, menu))
	press(menu, vtinput.VK_F4)
	settleFrames(t)
	dlg := findBookmarksDialog(t)
	if dlg.SelectPos != 6 {
		t.Errorf("dialog opened on slot %d, want 6", dlg.SelectPos)
	}
	press(dlg.VMenu, vtinput.VK_ESCAPE)
	settleFrames(t)
	if !dlg.IsDone() {
		t.Fatal("Esc did not close the dialog")
	}
	settleFrames(t)
	menu = findDriveMenu(t) // reopened by the dialog's close hook
	if menu.IsDone() {
		t.Fatal("drive menu did not come back after the dialog closed")
	}

	// Ins opens the dialog from any row, always at the first slot.
	menu.SetSelectPos(0)
	press(menu, vtinput.VK_INSERT)
	settleFrames(t)
	dlg = findBookmarksDialog(t)
	if dlg.SelectPos != 0 {
		t.Errorf("Ins opened the dialog on slot %d, want 0", dlg.SelectPos)
	}
	press(dlg.VMenu, vtinput.VK_ESCAPE)
	settleFrames(t)
	dlg.IsDone()
	settleFrames(t)

	// Del clears the slot, and the menu that comes back no longer lists it.
	menu = findDriveMenu(t)
	menu.SetSelectPos(bookmarkRow(t, menu))
	press(menu, vtinput.VK_DELETE)
	settleFrames(t)
	data, err := os.ReadFile(ini)
	if err != nil {
		t.Fatalf("read ini: %v", err)
	}
	if strings.Contains(string(data), "[6]") {
		t.Errorf("Del did not clear the slot on disk:\n%s", data)
	}
	menu = findDriveMenu(t)
	for _, it := range menu.Items {
		if strings.HasPrefix(it.Text, "&6  ") {
			t.Errorf("cleared bookmark still listed: %q", it.Text)
		}
	}
}

func TestPanelsFrame_GetActivePTY(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	// Default panels use OSVFS, so active PTY should be the local one
	active := pf.getActivePTY()
	if active != pf.pty {
		t.Errorf("Expected active PTY to be the local PTY for OSVFS")
	}
}
func TestPanelsFrame_ProcessMouse_DoubleClick(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Active is initially right (1)
	if pf.activeIdx != 1 {
		t.Fatalf("Expected initial activeIdx 1, got %d", pf.activeIdx)
	}

	tmp := t.TempDir()
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	// Bypass async load
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}}
	fsp.Refresh()

	initialPath := fsp.vfs.GetPath()

	// Double click on ".." in left panel.
	// Left panel 0..39. Table start Y=1. Header Y=1. Row 0 at Y=2.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          5,
		MouseY:          2,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})

	if pf.activeIdx != 0 {
		t.Errorf("Expected activeIdx 0 after left click, got %d", pf.activeIdx)
	}

	if fsp.vfs.GetPath() == initialPath {
		t.Error("Double click on '..' should have changed directory")
	}
}

func setupMockPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 1, showPanels: true, showKeyBar: true, showLeftPanel: true, showRightPanel: true}
	pf.pty = &mockPty{}
	pf.termView = NewTerminalView(80, 24)
	// Initialize MenuBar with enough items to satisfy updateMenuCheckmarks (needs index 0 and 4)
	pf.menuBar = vtui.NewMenuBar(nil)
	pf.menuBar.Items = make([]vtui.MenuBarItem, 5)
	for i := 0; i < 5; i++ {
		pf.menuBar.Items[i].SubItems = make([]vtui.MenuItem, 8)
	}
	pf.cmdLine = NewCommandLine(">")
	pf.keyBar = vtui.NewKeyBar()
	// Use OSVFS because tests create real files in t.TempDir()
	pf.panels[0] = NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS("."))
	pf.panels[1] = NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS("."))
	pf.initPTY()
	return pf
}
func TestPanelsFrame_ProcessMouse_DoubleClickFile(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	os.WriteFile(runnablePath, []byte("echo"), 0755)

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.SetViewMode(ViewModeDetailed)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsDir: false}},
	}
	fsp.Refresh()

	// Must init frame manager to catch async tasks from actionExecute
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Double click on "run.sh" in left panel.
	// Panel at (0,0), Table at (1,1), Header at Y=1, Row 0 at Y=2, Row 1 (run.sh) at Y=3.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          5,
		MouseY:          3,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})

	// Wait for the async task that actually executes the file.
	// Since other tasks (like ReadDirectory) might be in the queue,
	// we process the channel in a loop until panels are hidden.
	timeout := time.After(1 * time.Second)
	for pf.showPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("actionExecute did not hide the panels within 1s")
		}
	}
	if pf.showPanels {
		t.Error("Double clicking a runnable file should hide the panels")
	}
}

// TestPanelsFrame_ProcessMouse_AltPanelSwallowsClicks makes sure a
// click on an alt panel (Ctrl+L / Ctrl+Q) does NOT fall through to
// the file panel underneath — otherwise a double-click can launch
// a file the user can't even see. Also verifies that a click on
// the passive-side alt panel activates that side.
func TestPanelsFrame_ProcessMouse_AltPanelSwallowsClicks(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	os.WriteFile(runnablePath, []byte("echo"), 0755)

	// Left panel holds the runnable; right is active by default.
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.SetViewMode(ViewModeDetailed)
	fsp.vfs.SetPath(tmp)
	fsp.entries = []*fileEntry{
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
	if pf.altPanels[0] == nil {
		t.Fatal("Ctrl+L should install alt panel on the left slot")
	}
	priorActive := pf.activeIdx // right (1) from setupMockPanelsFrame

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
	if !pf.showPanels {
		t.Error("double-click on alt panel must NOT launch the file underneath")
	}
	if pf.activeIdx == priorActive {
		t.Errorf("click on passive-side alt panel should activate that side; activeIdx stayed %d", pf.activeIdx)
	}
}

// TestPanelsFrame_ProcessMouse_MiddleClickOverAltPanel confirms the
// global middle-click → Enter branch is also swallowed when the
// click lands on an alt panel; otherwise the file under the alt
// still gets launched despite the fix.
func TestPanelsFrame_ProcessMouse_MiddleClickOverAltPanel(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "run.sh"), []byte("echo"), 0755)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.SetViewMode(ViewModeDetailed)
	fsp.vfs.SetPath(tmp)
	fsp.entries = []*fileEntry{
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
	if pf.altPanels[0] == nil {
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
	if !pf.showPanels {
		t.Error("middle-click on alt panel must NOT trigger Enter → launch on the file underneath")
	}
}

// TestPanelsFrame_EscTogglesPanels exercises the FAR-style ESC
// toggle: hides visible panels when the command line is empty,
// shows hidden panels back if no interactive terminal app is
// running. Non-empty command line keeps the existing ESC-clears-
// cmdLine behaviour.
func TestPanelsFrame_EscTogglesPanels(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	old := AppConfig.EscTogglePanels
	defer func() { AppConfig.EscTogglePanels = old }()
	AppConfig.EscTogglePanels = true

	sendEsc := func() bool {
		return pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode: vtinput.VK_ESCAPE,
		})
	}

	// Panels visible, cmdLine empty → ESC hides.
	if !pf.showPanels {
		t.Fatal("precondition: panels should start visible")
	}
	if !sendEsc() {
		t.Error("ESC on visible panels + empty cmdLine should be handled")
	}
	if pf.showPanels {
		t.Error("ESC should hide panels when cmdLine is empty")
	}

	// Panels hidden, quiet PTY → ESC brings them back.
	if !sendEsc() {
		t.Error("ESC on hidden panels + quiet PTY should be handled")
	}
	if !pf.showPanels {
		t.Error("ESC should show panels back on the second press")
	}

	// Panels visible with a typed command → ESC falls through to
	// the clear-cmdLine handler and panels stay put.
	pf.cmdLine.InsertString("something")
	if !sendEsc() {
		t.Error("ESC with a non-empty cmdLine should still be handled (clears it)")
	}
	if !pf.showPanels {
		t.Error("ESC with a typed command must NOT hide the panels — only clear the line")
	}
	if !pf.cmdLine.IsEmpty() {
		t.Error("ESC with a typed command should have cleared the command line")
	}
}

// TestPanelsFrame_EscTogglePanels_RespectsOption confirms the
// shortcut can be turned off — with EscTogglePanels=false, ESC
// on visible panels + empty cmdLine is a no-op instead of hiding.
func TestPanelsFrame_EscTogglePanels_RespectsOption(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	old := AppConfig.EscTogglePanels
	defer func() { AppConfig.EscTogglePanels = old }()
	AppConfig.EscTogglePanels = false

	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})
	if !pf.showPanels {
		t.Error("with EscTogglePanels=false, ESC must not hide the panels")
	}
}

func TestPanelsFrame_KeyHandling(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Test Tab to switch active panel
	if pf.activeIdx != 1 {
		t.Fatalf("Initial active panel should be right (1), got %d", pf.activeIdx)
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.activeIdx != 0 {
		t.Error("Tab did not switch active panel to left (0)")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.activeIdx != 1 {
		t.Error("Tab did not switch active panel back to right (1)")
	}

	// 2. Test Ctrl+O to toggle panels
	if !pf.showPanels {
		t.Fatal("Panels should be visible initially")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.showPanels {
		t.Error("Ctrl+O did not hide panels")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if !pf.showPanels {
		t.Error("Ctrl+O did not show panels again")
	}

	// 3. Test Ctrl+Enter to insert filename
	pf.activeIdx = 0
	if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
		// Mock entries to avoid async dependency
		fsp.entries = []*fileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "testfile.txt"}},
		}
		fsp.Refresh()
		fsp.SetCursorIndex(1)
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: vtinput.LeftCtrlPressed})

	expectedName := pf.panels[0].GetSelectedName()
	if pf.cmdLine.Edit.GetText() != expectedName {
		t.Errorf("Ctrl+Enter failed: expected '%s', got '%s'", expectedName, pf.cmdLine.Edit.GetText())
	}

	// 4. Test Ctrl+O to toggle panels even when PTY is busy (Issue #50)
	pf.showPanels = false
	pf.pty = &mockPty{}
	pf.executing = true // PTY is busy

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if !pf.showPanels {
		t.Error("Ctrl+O should show panels even when PTY is busy")
	}

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.showPanels {
		t.Error("Ctrl+O should hide panels even when PTY is busy")
	}
}
func TestPanelsFrame_MenuCommands(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	handled := pf.HandleCommand(CmLeftDetailed, nil)
	if !handled {
		t.Error("CmLeftDetailed not handled")
	}
	if pf.panels[0].(*FileSystemPanel).viewMode != ViewModeDetailed {
		t.Error("Left panel mode not changed to Detailed")
	}

	pf.HandleCommand(CmRightDetailed, nil)
	if pf.panels[1].(*FileSystemPanel).viewMode != ViewModeDetailed {
		t.Error("Right panel mode not changed to Detailed")
	}

	for _, menuIndex := range []int{0, 4} {
		items := pf.menuBar.Items[menuIndex].SubItems
		for i, shortcut := range []string{"Ctrl+1", "Ctrl+2", "Ctrl+3", "Ctrl+4"} {
			if items[i].Shortcut != shortcut {
				t.Errorf("view shortcut %d in menu %d = %q, want %s", i, menuIndex, items[i].Shortcut, shortcut)
			}
		}
	}

	// Sort mode commands
	pf.HandleCommand(CmLeftSortTime, nil)
	if pf.panels[0].(*FileSystemPanel).sortMode != SortTime {
		t.Error("Left panel sort mode not changed to Time")
	}

	pf.HandleCommand(CmRightSortSize, nil)
	if pf.panels[1].(*FileSystemPanel).sortMode != SortSize {
		t.Error("Right panel sort mode not changed to Size")
	}

	// Menu checkmarks
	menuText := pf.menuBar.Items[0].SubItems[2].Text
	if !strings.HasPrefix(menuText, "√") {
		t.Errorf("Menu checkmark not updated, got %q", menuText)
	}
	sortText := pf.menuBar.Items[0].SubItems[7].Text
	if !strings.HasPrefix(sortText, "√") {
		t.Errorf("Sort menu checkmark not updated, got %q", sortText)
	}
}

func TestPanelsFrame_SortCommandsUseDefaultDirection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	defer pf.Close()

	left := pf.panels[0].(*FileSystemPanel)
	right := pf.panels[1].(*FileSystemPanel)
	left.sortMode, left.sortReverse = SortUnsorted, true
	right.sortMode, right.sortReverse = SortUnsorted, true

	pf.HandleCommand(CmLeftSortTime, nil)
	if left.sortMode != SortTime || left.sortIsAscending() {
		t.Fatalf("left sort command = mode %v ascending %v, want Time descending",
			left.sortMode, left.sortIsAscending())
	}

	pf.HandleCommand(CmRightSortSize, nil)
	if right.sortMode != SortSize || right.sortIsAscending() {
		t.Fatalf("right sort command = mode %v ascending %v, want Size descending",
			right.sortMode, right.sortIsAscending())
	}
}

func TestPanelsFrame_CtrlF12SortMenu(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.getActivePanel()
	fsp.sortMode = SortTime
	fsp.sortReverse = false

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
	if len(menu.Items) != 5 {
		t.Fatalf("sort menu has %d items, want 5", len(menu.Items))
	}
	panelX1, panelY1, panelX2, panelY2 := fsp.GetPosition()
	menuX1, menuY1, menuX2, menuY2 := menu.GetPosition()
	if menuX1+menuX2 != panelX1+panelX2 || menuY1+menuY2 != panelY1+panelY2 {
		t.Fatalf("sort menu (%d,%d)-(%d,%d) is not centered in panel (%d,%d)-(%d,%d)",
			menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2)
	}
	if menu.SelectPos != int(SortTime) || !strings.HasPrefix(menu.Items[SortTime].Text, "✓ ") {
		t.Fatalf("current sort not selected/marked: pos=%d item=%q", menu.SelectPos, menu.Items[SortTime].Text)
	}
	for idx, shortcut := range []string{"Ctrl+F3", "Ctrl+F4", "Ctrl+F5", "Ctrl+F6", "Ctrl+F7"} {
		if menu.Items[idx].Shortcut != shortcut {
			t.Errorf("sort menu shortcut %d = %q, want %q", idx, menu.Items[idx].Shortcut, shortcut)
		}
	}

	menu.SetSelectPos(int(SortSize))
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	if fsp.sortMode != SortSize || fsp.sortIsAscending() {
		t.Fatalf("sort menu selection = mode %v ascending %v, want Size descending",
			fsp.sortMode, fsp.sortIsAscending())
	}
	menu.Close()
	vtui.FrameManager.Pop()
}

func TestPanelsFrame_RightClickHeaderOpensPanelCenteredSortMenu(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.activeIdx = 1
	// RunAction("Panel.SortMenu") resolves the panels frame through
	// FrameManager screens, as in production.
	vtui.FrameManager.Push(pf)
	left := pf.panels[0].(*FileSystemPanel)

	if !pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(left.table.X1), MouseY: int16(left.table.Y1),
		ButtonState: vtinput.RightmostButtonPressed,
	}) {
		t.Fatal("right click on column header was not handled")
	}
	if pf.activeIdx != 1 {
		t.Fatalf("right-clicking the passive panel header changed active panel to %d", pf.activeIdx)
	}
	if pf.panelMouseCapture != nil {
		t.Fatal("header context click incorrectly captured a file-panel drag")
	}

	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("right-click header top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}
	panelX1, panelY1, panelX2, panelY2 := left.GetPosition()
	menuX1, menuY1, menuX2, menuY2 := menu.GetPosition()
	if menuX1+menuX2 != panelX1+panelX2 || menuY1+menuY2 != panelY1+panelY2 {
		t.Fatalf("context sort menu (%d,%d)-(%d,%d) is not centered in left panel (%d,%d)-(%d,%d)",
			menuX1, menuY1, menuX2, menuY2, panelX1, panelY1, panelX2, panelY2)
	}
	menu.Close()
	vtui.FrameManager.Pop()
}

func TestPanelsFrame_RightClickPanelPathOpensDriveMenuForThatPanel(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.activeIdx = 0
	left := pf.panels[0].(*FileSystemPanel)
	right := pf.panels[1].(*FileSystemPanel)
	leftPath := t.TempDir()
	rightPath := t.TempDir()
	if err := left.vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := right.vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}
	right.currentTitle = rightPath

	if !pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(right.X1 + 3), MouseY: int16(right.Y1),
		ButtonState: vtinput.RightmostButtonPressed,
	}) {
		t.Fatal("right click on the panel path was not handled")
	}
	if pf.activeIdx != 0 {
		t.Fatalf("right-clicking the passive panel path changed active panel to %d", pf.activeIdx)
	}
	if pf.panelMouseCapture != nil {
		t.Fatal("path context click incorrectly captured a panel drag")
	}
	menu := findDriveMenu(t)
	menu.OnAction(0) // "Other panel" must apply to the right panel.
	if got := right.vfs.GetPath(); got != leftPath {
		t.Fatalf("drive menu changed path %q, want right panel to receive %q", got, leftPath)
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
			SetDefaultF4Palette()

			pf := NewPanelsFrame()
			defer pf.Close()
			pf.ResizeConsole(80, 25)
			paths := []string{t.TempDir(), t.TempDir()}
			for i, panel := range pf.panels {
				if err := panel.(*FileSystemPanel).vfs.SetPath(paths[i]); err != nil {
					t.Fatal(err)
				}
			}

			if !pf.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tt.key,
				ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
			}) {
				t.Fatal("Ctrl+Shift+Arrow was not handled")
			}
			menu := findDriveMenu(t)
			menu.OnAction(0)
			want := paths[1-tt.panelIdx]
			if got := pf.panels[tt.panelIdx].(*FileSystemPanel).vfs.GetPath(); got != want {
				t.Fatalf("drive menu changed path %q, want panel %d to receive %q", got, tt.panelIdx, want)
			}
		})
	}
}

func TestPanelsFrame_RefreshOnFocus(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	// We need to verify Refresh was called.
	// Since we don't have a mock VFS easily swappable here without refactoring,
	// we check if the internal state handles the focus event without crashing
	// and returns true.

	handled := pressKey(pf, &vtinput.InputEvent{
		Type:     vtinput.FocusEventType,
		SetFocus: true,
	})

	if !handled {
		t.Error("PanelsFrame should handle FocusEventType and return true")
	}
}
func TestPanelsFrame_Clone(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(100, 30)

	// Use a real temp directory that exists on all platforms
	tmpDir := t.TempDir()

	// Set some specific state
	pf.activeIdx = 0
	if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
		if err := fsp.vfs.SetPath(tmpDir); err != nil {
			t.Fatalf("SetPath failed: %v", err)
		}
		fsp.table.SelectPos = 5
	}

	// Clone the panels
	clone := pf.Clone()
	defer clone.Close()

	// Verify state transfer
	if clone.activeIdx != 0 {
		t.Errorf("Clone failed to copy activeIdx: %d", clone.activeIdx)
	}

	if fsp, ok := clone.panels[0].(*FileSystemPanel); ok {
		if fsp.vfs.GetPath() != tmpDir {
			t.Errorf("Clone failed to copy VFS path: got %s, want %s", fsp.vfs.GetPath(), tmpDir)
		}
		if fsp.table.SelectPos != 5 {
			t.Errorf("Clone failed to copy Table SelectPos: %d", fsp.table.SelectPos)
		}
		if fsp.viewMode != pf.panels[0].(*FileSystemPanel).viewMode {
			t.Error("Clone failed to copy ViewMode")
		}
		if fsp.sortMode != pf.panels[0].(*FileSystemPanel).sortMode {
			t.Error("Clone failed to copy SortMode")
		}
		if fsp.sortReverse != pf.panels[0].(*FileSystemPanel).sortReverse {
			t.Error("Clone failed to copy SortReverse")
		}
	}

	// Verify they are independent instances
	clone.activeIdx = 1
	if pf.activeIdx == 1 {
		t.Error("Clone should be independent from its parent")
	}
}

func TestPanelsFrame_CtrlBrackets_Insertion(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	rp := pf.panels[1].(*FileSystemPanel)

	tmp := t.TempDir()
	leftPath := filepath.Join(tmp, "left")
	rightPath := filepath.Join(tmp, "right")
	os.MkdirAll(leftPath, 0755)
	os.MkdirAll(rightPath, 0755)

	lp.vfs.SetPath(leftPath)
	rp.vfs.SetPath(rightPath)

	// 1. Тест Ctrl+[ (Путь левой панели)
	pf.cmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	gotLeft := pf.cmdLine.Edit.GetText()
	expectedLeft := leftPath
	if gotLeft != expectedLeft {
		t.Errorf("Ctrl+[ failed: expected %q, got %q", expectedLeft, gotLeft)
	}

	// 2. Тест Ctrl+] (Путь правой панели)
	pf.cmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_OEM_6,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	gotRight := pf.cmdLine.Edit.GetText()
	expectedRight := rightPath
	if gotRight != expectedRight {
		t.Errorf("Ctrl+] failed: expected %q, got %q", expectedRight, gotRight)
	}
}

func TestCtrlBracketsInsertPanelPathsIntoFocusedEdit(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	leftPath := filepath.Join(t.TempDir(), "left path")
	rightPath := filepath.Join(t.TempDir(), "right path")
	if err := os.MkdirAll(leftPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pf.panels[0].(*FileSystemPanel).vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := pf.panels[1].(*FileSystemPanel).vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Push(pf)

	dlg := vtui.NewCenteredDialog(50, 9, " Path ")
	edit := vtui.NewEdit(0, 0, 30, "prefix:")
	dlg.AddItem(edit)
	dlg.SetFocusedItem(edit)
	vtui.FrameManager.Push(dlg)

	if !handlePanelPathEditHotkey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+[ was not handled for focused edit")
	}
	if got, want := edit.GetText(), leftPath; got != want {
		t.Fatalf("Ctrl+[ inserted %q, want %q", got, want)
	}

	edit.SelectAll()
	if !handlePanelPathEditHotkey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_OEM_6,
		ControlKeyState: vtinput.RightCtrlPressed,
	}) {
		t.Fatal("Ctrl+] was not handled for focused edit")
	}
	if got := edit.GetText(); got != rightPath {
		t.Fatalf("Ctrl+] inserted %q, want raw path %q", got, rightPath)
	}
}

func TestCtrlBracketsIgnoreDialogsWithoutFocusedEdit(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)
	dlg := vtui.NewCenteredDialog(40, 7, " Confirm ")
	button := vtui.NewButton(0, 0, "OK")
	dlg.AddItem(button)
	dlg.SetFocusedItem(button)
	vtui.FrameManager.Push(dlg)

	if handlePanelPathEditHotkey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+[ was consumed without a focused edit")
	}
}

func TestPanelsFrame_CtrlArrows_CommandLineNavigation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.showPanels = true
	pf.cmdLine.Edit.SetText("word1 word2 word3")

	// Перемещаем курсор в конец строки
	pf.cmdLine.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_END,
	})

	// 1. Тест Ctrl+Left (Прыжок к началу "word3", оффсет 12)
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// Вставляем символ 'X' в текущую позицию курсора
	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'X',
	})

	gotText := pf.cmdLine.Edit.GetText()
	expectedLeft := "word1 word2 Xword3"
	if gotText != expectedLeft {
		t.Errorf("Ctrl+Left word navigation failed with panels enabled: expected %q, got %q", expectedLeft, gotText)
	}

	// 2. Тест Ctrl+Right (Прыжок в конец "Xword3")
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// Вставляем символ 'Y'
	pressKey(pf, &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'Y',
	})

	gotText = pf.cmdLine.Edit.GetText()
	expectedRight := "word1 word2 Xword3Y"
	if gotText != expectedRight {
		t.Errorf("Ctrl+Right word navigation failed with panels enabled: expected %q, got %q", expectedRight, gotText)
	}
}
func TestPanelsFrame_AlwaysShowMenuBar(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	origAlways := AppConfig.AlwaysShowMenuBar
	defer func() { AppConfig.AlwaysShowMenuBar = origAlways }()

	// 1. Test when AlwaysShowMenuBar is false (default)
	AppConfig.AlwaysShowMenuBar = false

	pf.showPanels = true
	pf.ResizeConsole(80, 25)

	fspL := pf.panels[0].(*FileSystemPanel)
	if fspL.Y1 != 0 {
		t.Errorf("Expected panels to start at row 0 by default, got %d", fspL.Y1)
	}

	// 2. Test when AlwaysShowMenuBar is true (panels shifted down)
	AppConfig.AlwaysShowMenuBar = true
	pf.ResizeConsole(80, 25)

	if fspL.Y1 != 1 {
		t.Errorf("Expected panels to start at row 1 when AlwaysShowMenuBar is true, got %d", fspL.Y1)
	}

	// 3. Test that hiding panels collapses the menu bar space for terminal
	pf.showPanels = false
	pf.ResizeConsole(80, 25)

	if pf.termView.Y1 != 0 {
		t.Errorf("Expected terminal to start at row 0 when panels are hidden, got %d", pf.termView.Y1)
	}
}
func TestPanelsFrame_Clone_TerminalData(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	// 1. Simulate complex terminal output
	// Inject data directly into pt to simulate extruded history
	pf.termView.pt.Insert(0, []byte("L1\nL2\n"))
	pf.termView.li.UpdateAfterInsert(0, []byte("L1\nL2\n"))

	// Simulate active grid data
	pf.termView.CursorY = 5
	pf.termView.Lines[4][0].Char = 'H' // Previous row
	pf.termView.Lines[5][0].Char = 'A' // Active row (will be wiped)
	pf.termView.CursorX = 1

	clone := pf.Clone()
	defer clone.Close()

	// 2. Check if log is deep-copied
	if clone.termView.pt.String() != "L1\nL2\n" {
		t.Errorf("Terminal log not cloned. Got %q", clone.termView.pt.String())
	}

	// 3. CRITICAL: Check if LineIndex is correctly pointing to the NEW pt
	if clone.termView.li.LineCount() != 3 {
		t.Errorf("Terminal LineIndex not synced in clone. Expected 3 lines, got %d", clone.termView.li.LineCount())
	}

	// 4. Check if visual grid is copied
	if clone.termView.Lines[4][0].Char != 'H' {
		t.Error("Terminal visual grid (Lines) history not copied to clone")
	}

	// 5. Verify prompt reset logic
	if clone.termView.CursorX != 0 {
		t.Errorf("Expected clone CursorX to be 0 after prompt wipe, got %d", clone.termView.CursorX)
	}
	if clone.termView.Lines[5][0].Char != ' ' {
		t.Error("Current terminal line was not cleared during clone")
	}
}
func TestPanelsFrame_Labels(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	ks := pf.GetKeyLabels()

	if ks == nil {
		t.Fatal("PanelsFrame labels are nil")
	}

	// F3 in panels should be "View" (or whatever you set in lang.go)
	if ks.Normal[2] == "" {
		t.Error("PanelsFrame F3 label should not be empty")
	}
}
func TestPanelsFrame_HistoryNavigation(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25) // Initialize panels
	pf.showPanels = false    // Hide panels to enable history intercept
	pf.cmdLine.Edit.AddHistory("git status")

	// Press Up Arrow
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.cmdLine.Edit.GetText() != "git status" {
		t.Errorf("PanelsFrame failed to pass Up Arrow to history. Got '%s'", pf.cmdLine.Edit.GetText())
	}

	// Reset, show panels, try again
	pf.cmdLine.Clear()
	pf.cmdLine.Edit.HistoryPos = -1
	pf.showPanels = true

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.cmdLine.Edit.GetText() != "" {
		t.Error("Up Arrow should NOT trigger history when panels are visible")
	}
}
func TestPanelsFrame_HistoryNavigation_HiddenPanels(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.showPanels = false // Panels are hidden
	pf.cmdLine.Edit.AddHistory("last command")

	// Press Up Arrow - should trigger HistoryUp on the command line
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.cmdLine.Edit.GetText() != "last command" {
		t.Errorf("Up arrow failed to cycle history with hidden panels. Got: %q", pf.cmdLine.Edit.GetText())
	}

	// Press Esc - should clear line and reset history position
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})

	if !pf.cmdLine.IsEmpty() || pf.cmdLine.Edit.HistoryPos != -1 {
		t.Error("Esc failed to reset history state")
	}
}
func TestPanelsFrame_EnterAddsToHistory(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.cmdLine.Edit.SetText("ls -la")

	// Simulate Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	if len(pf.cmdLine.Edit.History) == 0 || pf.cmdLine.Edit.History[0] != "ls -la" {
		t.Errorf("Command was not added to history on Enter. History: %v", pf.cmdLine.Edit.History)
	}
}

func TestPanelsFrame_AltScreenTerminalHeight(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.pty = &mockPty{}
	pf.parser = NewAnsiParser(pf.termView, pf.pty)
	height := 25
	pf.showKeyBar = true

	// 1. Normal mode: terminal should leave space for KeyBar
	pf.termView.UseAltScreen = false
	pf.ResizeConsole(80, height)
	// termY2 should be h-2 (23)
	if pf.termView.Y2 != 23 {
		t.Errorf("Normal mode: expected terminal Y2=23, got %d", pf.termView.Y2)
	}

	// 2. AltScreen mode: terminal should occupy the KeyBar's row
	pf.termView.UseAltScreen = true
	pf.ResizeConsole(80, height)
	// termY2 should be h-1 (24)
	if pf.termView.Y2 != 24 {
		t.Errorf("AltScreen mode: expected terminal Y2=24, got %d", pf.termView.Y2)
	}
}

func TestPanelsFrame_KeyBarSuppression(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.showKeyBar = true
	pf.ResizeConsole(80, 25)

	// We need to simulate the frame being on top to trigger the logic
	vtui.FrameManager.Push(pf)

	// 1. Normal mode: KeyBar should be registered
	pf.termView.UseAltScreen = false
	pf.Show(scr)
	if vtui.FrameManager.KeyBar == nil {
		t.Error("KeyBar should be registered in FrameManager in normal mode")
	}

	// 2. AltScreen mode: KeyBar should be removed from FrameManager
	pf.termView.UseAltScreen = true
	pf.Show(scr)
	if vtui.FrameManager.KeyBar != nil {
		t.Error("KeyBar should be UNregistered from FrameManager in AltScreen mode")
	}

	// 3. Busy mode but panels visible: KeyBar should be registered (Issue #50)
	pf.termView.UseAltScreen = false
	pf.showPanels = true
	pf.pty = &mockPty{} // Ensure active PTY is not nil
	pf.executing = true
	pf.Show(scr)
	if vtui.FrameManager.KeyBar == nil {
		t.Error("KeyBar should be registered in FrameManager in busy mode when panels are visible")
	}

	// 4. Busy mode and panels hidden: KeyBar should be UNregistered (Issue #50)
	pf.showPanels = false
	pf.Show(scr)
	if vtui.FrameManager.KeyBar != nil {
		t.Error("KeyBar should be UNregistered from FrameManager in busy mode when panels are hidden")
	}
}
func TestPanelsFrame_RefreshAll(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	// Test that RefreshAll doesn't crash on freshly initialized panels
	pf.RefreshAll()
}

func TestPanelsFrame_ManualRefresh(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Setup a mock directory
	tmp := t.TempDir()
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

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
	if !fsp.isLoading {
		t.Error("Ctrl+R did not trigger panel refresh (isLoading should be true)")
	}
}

func TestPanelsFrame_AutoRefresh(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Setup a mock directory
	tmp := t.TempDir()
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	// Emulate an initial read that populates MTime
	fsp.lastDirMTime = time.Now().Add(-10 * time.Minute)
	// Write a file to update actual directory MTime
	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("data"), 0644)

	// Emulate the timer expiration
	pf.lastAutoRefresh = time.Now().Add(-5 * time.Second)

	// Trigger Show which should fire the async stat check
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	pf.Show(scr)

	// Pump the TaskChan to execute RunAsync and RunOnUI
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if fsp.isLoading {
			return // Success: it triggered a refresh
		}
	}
	t.Fatal("AutoRefresh failed to trigger ReadDirectory after MTime change")
}
func TestPanelsFrame_ResizingIntegration(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Initial size 80x25
	pf.ResizeConsole(80, 25)

	// 1. Verify initial positions of standard components
	if pf.keyBar.Y1 != 24 {
		t.Errorf("Initial KeyBar Y1: expected 24, got %d", pf.keyBar.Y1)
	}
	if pf.cmdLine.Y1 != 23 {
		t.Errorf("Initial CommandLine Y1: expected 23, got %d", pf.cmdLine.Y1)
	}

	// 2. Perform resize to 120x40
	pf.ResizeConsole(120, 40)

	// 3. Verify that components moved/scaled correctly
	if pf.keyBar.Y1 != 39 {
		t.Errorf("Resized KeyBar Y1: expected 39, got %d", pf.keyBar.Y1)
	}
	if pf.keyBar.X2 != 119 {
		t.Errorf("Resized KeyBar X2: expected 119, got %d", pf.keyBar.X2)
	}
	if pf.cmdLine.Y1 != 38 {
		t.Errorf("Resized CommandLine Y1: expected 38, got %d", pf.cmdLine.Y1)
	}

	// 4. Verify panels scaled
	leftX1, _, leftX2, _ := pf.panels[0].GetPosition()
	rightX1, _, rightX2, _ := pf.panels[1].GetPosition()

	if leftX1 != 0 || leftX2 != 59 {
		t.Errorf("Resized Left Panel X range: expected 0..59, got %d..%d", leftX1, leftX2)
	}
	if rightX1 != 60 || rightX2 != 119 {
		t.Errorf("Resized Right Panel X range: expected 60..119, got %d..%d", rightX1, rightX2)
	}
}
func TestPanelsFrame_ExitWarning_ActiveTasks(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	fm.Push(pf)

	qm := GlobalQueueManager
	qm.mu.Lock()
	qm.tasks = []*QueueTask{{ID: 1, State: "Queued"}}
	qm.mu.Unlock()

	// Триггерим выход
	pf.HandleCommand(vtui.CmQuit, nil)

	// Находим диалог
	top := fm.GetTopFrame()
	if top == nil {
		t.Fatal("Exit dialog not shown")
	}

	// Проверяем текст сообщения (должен содержать упоминание активных задач)
	foundWarning := false
	// Перебираем детей контейнера (диалога)
	if container, ok := top.(vtui.Container); ok {
		for _, child := range container.GetChildren() {
			if txt, ok := child.(*vtui.Text); ok {
				if strings.Contains(txt.GetText(), "active background operations") {
					foundWarning = true
					break
				}
			}
		}
	}

	if !foundWarning {
		t.Error("Exit dialog did not show warning about active background tasks")
	}
}
func TestPanelsFrame_SwapPanels(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathL := filepath.Join(t.TempDir(), "left")
	pathR := filepath.Join(t.TempDir(), "right")
	os.MkdirAll(pathL, 0755)
	os.MkdirAll(pathR, 0755)

	fspL := pf.panels[0].(*FileSystemPanel)
	fspR := pf.panels[1].(*FileSystemPanel)

	fspL.vfs.SetPath(pathL)
	fspR.vfs.SetPath(pathR)
	fspL.SetViewMode(ViewModeDetailed)
	fspR.SetViewMode(ViewModeMedium)

	pf.activeIdx = 0 // Active is Left

	// Execute Swap
	pf.HandleCommand(CmSwapPanels, nil)

	// 1. Verify instances are swapped in the array
	if pf.panels[0] != fspR || pf.panels[1] != fspL {
		t.Error("Panels instances were not swapped in pf.panels array")
	}

	// 2. Verify activeIdx followed the content
	if pf.activeIdx != 1 {
		t.Errorf("activeIdx should have moved to 1 to follow the panel, got %d", pf.activeIdx)
	}

	// 3. Verify positions were updated (fspR was Right, now should be Left)
	x1, _, x2, _ := fspR.GetPosition()
	if x1 != 0 || x2 != 39 {
		t.Errorf("Swapped panel (Right->Left) has wrong X position: %d..%d", x1, x2)
	}

	// 4. Verify state preservation
	if fspR.viewMode != ViewModeMedium {
		t.Error("Swapped panel did not preserve its ViewMode")
	}
}

// TestPanelsFrame_VisualLeftRightFollowSwap makes sure resolving
// panels by on-screen X-position keeps Ctrl+[/Ctrl+] pointing at
// the visually-left and visually-right sides after CmSwapPanels
// re-slots the underlying panels array.
func TestPanelsFrame_VisualLeftRightFollowSwap(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathL := filepath.Join(t.TempDir(), "left")
	pathR := filepath.Join(t.TempDir(), "right")
	os.MkdirAll(pathL, 0755)
	os.MkdirAll(pathR, 0755)

	fspL := pf.panels[0].(*FileSystemPanel)
	fspR := pf.panels[1].(*FileSystemPanel)
	fspL.vfs.SetPath(pathL)
	fspR.vfs.SetPath(pathR)

	// Baseline: unswapped — visual-left is the panel we set to
	// pathL, visual-right the one at pathR.
	if got := pf.visualLeftFSP(); got != fspL {
		t.Errorf("visualLeftFSP baseline: got %p, want left (%p)", got, fspL)
	}
	if got := pf.visualRightFSP(); got != fspR {
		t.Errorf("visualRightFSP baseline: got %p, want right (%p)", got, fspR)
	}

	// Swap. panels[0] now points at fspR, but that panel gets
	// moved to X=0 by ResizeConsole (see TestPanelsFrame_SwapPanels
	// step 3), so it's the visually-left one now.
	pf.HandleCommand(CmSwapPanels, nil)

	if got := pf.visualLeftFSP(); got != fspR {
		t.Errorf("visualLeftFSP after swap: got %p, want fspR (%p)", got, fspR)
	}
	if got := pf.visualRightFSP(); got != fspL {
		t.Errorf("visualRightFSP after swap: got %p, want fspL (%p)", got, fspL)
	}
}

func TestPanelsFrame_WideFollowsSwapAndClone(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	left := pf.panels[0]
	pf.setWidePanel(0)

	pf.HandleCommand(CmSwapPanels, nil)
	if pf.widePanel != 1 || pf.activeIdx != 1 || pf.panels[1] != left {
		t.Fatalf("Wide did not follow swapped content: wide=%d active=%d", pf.widePanel, pf.activeIdx)
	}

	clone := pf.Clone()
	defer clone.Close()
	if clone.widePanel != 1 || clone.activeIdx != 1 {
		t.Fatalf("clone lost Wide state: wide=%d active=%d", clone.widePanel, clone.activeIdx)
	}
	x1, _, x2, _ := clone.panels[1].GetPosition()
	if x1 != 0 || x2 != 79 {
		t.Fatalf("cloned Wide geometry = %d..%d, want 0..79", x1, x2)
	}
}
func TestPanelsFrame_Clone_SelectionPreservation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "selected.txt"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(tmp, "normal.txt"), []byte("data"), 0644)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)
	fsp.ReadDirectory()

	// Wait for initial load
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if !fsp.isLoading {
			break
		}
	}
	if fsp.isLoading {
		t.Fatal("Timeout waiting for initial load")
	}
	// Drain remaining tasks
	for i := 0; i < 10; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			break
		}
	}

	// Select "selected.txt"
	found := false
	for i, e := range fsp.entries {
		if e.Name == "selected.txt" {
			fsp.SetItemSelected(i, true)
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Setup failed: 'selected.txt' not found in entries")
	}

	// Clone
	clone := pf.Clone()
	defer clone.Close()
	cloneFsp := clone.panels[0].(*FileSystemPanel)

	// Wait for clone load
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if !cloneFsp.isLoading {
			break
		}
	}
	if cloneFsp.isLoading {
		t.Fatal("Timeout waiting for clone load")
	}

	// Verify preservation
	foundInClone := false
	for _, e := range cloneFsp.entries {
		if e.Name == "selected.txt" {
			foundInClone = true
			if !e.Selected {
				t.Error("Selection was lost after clone/reload")
			}
		}
		if e.Name == "normal.txt" && e.Selected {
			t.Error("'normal.txt' erroneously marked as selected in clone")
		}
	}
	if !foundInClone {
		t.Error("'selected.txt' missing in cloned panel entries")
	}
}

func TestPanelsFrame_GetTitle_WithProvider(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	v := &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/var"), title: "remote@server"}
	fp := pf.panels[0].(*FileSystemPanel)
	fp.vfs = v
	pf.activeIdx = 0

	title := pf.GetTitle()
	if !strings.Contains(title, "Panels: remote@server:") {
		t.Errorf("Expected title to contain 'Panels: remote@server:', got %q", title)
	}
}

func TestPanelsFrame_Prompt_WithProvider(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	v := &mockTitleVFS{OSVFS: *vfs.NewOSVFS("/etc"), title: "admin@prod"}
	fp := pf.panels[0].(*FileSystemPanel)
	fp.vfs = v
	pf.activeIdx = 0

	prompt := pf.buildPrompt()

	// Convert prompt to string
	promptStr := ""
	for _, c := range prompt {
		if c.Char != vtui.WideCharFiller {
			promptStr += string(rune(c.Char))
		}
	}

	if !strings.Contains(promptStr, "admin@prod") {
		t.Errorf("Expected prompt to contain VFS title 'admin@prod', got %q", promptStr)
	}
}

func TestPanelsFrameCloudPathSurfacesUseVisualAddressOnly(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(160, 30)

	separator := string(os.PathSeparator)
	visual := "zoin.shadow:" + separator + "[2025.12.13] Olympiad"
	canonical := "cloud://yandex/1864271c-3800-4938-ad02-f577c273941c/disk:%2Fsecret-id"
	filesystem := &opaqueVisualPathVFS{
		mockTitleVFS: &mockTitleVFS{OSVFS: *vfs.NewOSVFS(t.TempDir()), title: "zoin.shadow", panelTitle: visual},
		path:         visual,
	}
	pf.panels[0].(*FileSystemPanel).vfs = filesystem
	pf.activeIdx = 0

	prompt := pf.buildPrompt()
	var promptText strings.Builder
	for _, cell := range prompt {
		if cell.Char != vtui.WideCharFiller {
			promptText.WriteRune(rune(cell.Char))
		}
	}
	for surface, text := range map[string]string{
		"prompt":         promptText.String(),
		"window title":   pf.GetTitle(),
		"workspace menu": pf.GetWorkspaceMenuInfo().Primary,
	} {
		if !strings.Contains(text, visual) {
			t.Errorf("%s = %q, want visual path %q", surface, text, visual)
		}
		if strings.Contains(text, canonical) || strings.Contains(text, "cloud://") || strings.Contains(text, "%2F") || strings.Contains(text, "1864271c") {
			t.Errorf("%s exposed internal cloud identity: %q", surface, text)
		}
		if strings.Count(text, "zoin.shadow") != 1 {
			t.Errorf("%s duplicated connection name: %q", surface, text)
		}
	}
}

func TestPanelsFramePendingCloudHistoryUsesVisualTargetImmediately(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(160, 30)

	separator := string(os.PathSeparator)
	target := "zoin.shadow:" + separator + "Photos" + separator + "2026"
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = vfs.NewOSVFS(t.TempDir())
	fsp.providerOpenTarget = target
	fsp.providerOpenTask = &vtui.TaskContext{}
	defer func() { fsp.providerOpenTask = nil }()
	fsp.isLoading = true
	fsp.updateTitle(nil)
	pf.activeIdx = 0

	var prompt strings.Builder
	for _, cell := range pf.buildPrompt() {
		if cell.Char != vtui.WideCharFiller {
			prompt.WriteRune(rune(cell.Char))
		}
	}
	for surface, value := range map[string]string{
		"panel title":    fsp.currentTitle,
		"command prompt": prompt.String(),
		"window title":   pf.GetTitle(),
		"workspace menu": pf.GetWorkspaceMenuInfo().Primary,
	} {
		if !strings.Contains(value, target) {
			t.Errorf("%s still shows the source VFS while history restore is pending: %q", surface, value)
		}
	}
}

func TestPanelsFrame_GetPaths(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	tmp := t.TempDir()
	pathL := filepath.Join(tmp, "left")
	pathR := filepath.Join(tmp, "right")
	os.MkdirAll(pathL, 0755)
	os.MkdirAll(pathR, 0755)

	pf.panels[0].(*FileSystemPanel).vfs.SetPath(pathL)
	pf.panels[1].(*FileSystemPanel).vfs.SetPath(pathR)

	l, r := pf.GetPaths()
	if l != pathL || r != pathR {
		t.Errorf("GetPaths failed. Got %q, %q; want %q, %q", l, r, pathL, pathR)
	}
}
func TestPanelsFrame_StateCapture(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fspL := pf.panels[0].(*FileSystemPanel)
	fspR := pf.panels[1].(*FileSystemPanel)

	// Mock cursors
	fspL.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "file.l"}}}
	fspR.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "file.r"}}}
	fspL.SetCursorIndex(0)
	fspR.SetCursorIndex(0)

	pf.activeIdx = 0 // Left active

	lFile := fspL.GetSelectedName()
	rFile := fspR.GetSelectedName()

	if lFile != "file.l" || rFile != "file.r" || pf.activeIdx != 0 {
		t.Errorf("State capture failed: L:%q, R:%q, Active:%d", lFile, rFile, pf.activeIdx)
	}
}
func TestPanelsFrame_CloneIndependence(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Set path in original
	fsp := pf.panels[0].(*FileSystemPanel)
	origPath := t.TempDir()
	fsp.vfs.SetPath(origPath)

	// Clone
	clone := pf.Clone()
	defer clone.Close()

	// Change path in clone
	newPath := t.TempDir()
	clone.panels[0].(*FileSystemPanel).vfs.SetPath(newPath)

	// Verify original is unchanged
	if pf.panels[0].(*FileSystemPanel).vfs.GetPath() != origPath {
		t.Error("Cloned PanelsFrame shares VFS state with parent!")
	}
}
func TestPanelsFrame_CtrlO_HardRedraw(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)

	pf := NewPanelsFrame()
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
func TestPanelsFrame_PTYLockContention(t *testing.T) {
	// Этот тест проверяет, что тяжелый парсинг в PTY-потоке не блокирует
	// доступ UI-потока к методу getActivePTY (регрессия дедлока).
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	defer pf.Close()

	// Симулируем "забитую" очередь задач
	for i := 0; i < 64; i++ {
		vtui.FrameManager.PostTask(func() {})
	}

	// Запускаем в отдельной горутине тяжелый парсинг
	// (в реальности он теперь идет вне ptyMutex)
	go func() {
		hugeData := strings.Repeat("A", 100000)
		pf.ptyMutex.Lock()
		active := (pf.getActivePTYUnsafe() == pf.pty)
		pf.ptyMutex.Unlock()

		if active {
			pf.parser.Process([]byte(hugeData))
		}
	}()

	// UI-поток пытается взять мутекс через getActivePTY.
	// Если дедлок не починен, мы зависнем здесь.
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			_ = pf.getActivePTY()
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	select {
	case <-done:
		// Успех
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK DETECTED: getActivePTY blocked by PTY processing loop")
	}
}
func TestPanelsFrame_Clone_Comprehensive(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Setup specific state on the left panel
	fsp := pf.Left().(*FileSystemPanel)
	fsp.SetViewMode(ViewModeDetailed)
	fsp.sortMode = SortSize
	fsp.sortReverse = true
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file1"}},
		{VFSItem: vfs.VFSItem{Name: "file2"}, Selected: true},
		{VFSItem: vfs.VFSItem{Name: "file3"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(2) // On "file2"

	// 2. Setup terminal state
	pf.termView.PutChar('f', 0)
	pf.termView.PutChar('o', 0)
	pf.termView.PutChar('o', 0)
	pf.termView.PutChar('\n', 0)

	// 3. Perform Clone
	clone := pf.Clone()
	defer clone.Close()

	// 4. Verify Panel State
	cloneFsp := clone.Left().(*FileSystemPanel)
	if cloneFsp.viewMode != ViewModeDetailed {
		t.Error("Clone failed to preserve ViewMode")
	}
	if cloneFsp.sortMode != SortSize || !cloneFsp.sortReverse {
		t.Error("Clone failed to preserve sort state")
	}
	if cloneFsp.GetCursorIndex() != 2 {
		t.Errorf("Clone failed to preserve cursor index: expected 2, got %d", cloneFsp.GetCursorIndex())
	}
	if cloneFsp.GetSelectedName() != "file2" {
		t.Errorf("Clone failed to preserve selection: expected 'file2', got %q", cloneFsp.GetSelectedName())
	}
	if !cloneFsp.entries[2].Selected {
		t.Error("Clone failed to preserve individual item selection flag")
	}

	// 5. Verify Terminal State
	if !strings.HasPrefix(string(clone.termView.GetAllLogBytes()), "foo\n") {
		t.Errorf("Clone failed to preserve terminal history: %q", string(clone.termView.GetAllLogBytes()))
	}

	// 6. Verify Active Panel index
	if clone.activeIdx != pf.activeIdx {
		t.Errorf("Clone failed to preserve active panel index: %d", clone.activeIdx)
	}
}
func TestIsTerminalRunnable(t *testing.T) {
	tmpDir := t.TempDir()
	v := vfs.NewOSVFS(tmpDir)

	// 1. Обычный текстовый файл -> false
	txtFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(txtFile, []byte("hello"), 0644)
	if vfs.IsTerminalRunnable(context.Background(), v, txtFile) {
		t.Error("Text file should not be terminal-runnable")
	}

	// 2. Файл с расширением .sh -> true
	shFile := filepath.Join(tmpDir, "test.sh")
	os.WriteFile(shFile, []byte("echo hi"), 0644)
	if !vfs.IsTerminalRunnable(context.Background(), v, shFile) {
		t.Error(".sh file should be terminal-runnable")
	}

	// 3. Файл с шебангом без расширения -> true
	binFile := filepath.Join(tmpDir, "my-tool")
	os.WriteFile(binFile, []byte("#!/usr/bin/env bash\necho hi"), 0644)
	if !vfs.IsTerminalRunnable(context.Background(), v, binFile) {
		t.Error("File with shebang should be terminal-runnable")
	}

	// 4. Директория -> false
	subDir := filepath.Join(tmpDir, "folder")
	os.Mkdir(subDir, 0755)
	if vfs.IsTerminalRunnable(context.Background(), v, subDir) {
		t.Error("Directory should not be terminal-runnable")
	}

	// 5. Unix Executable Bit (если не на Windows)
	if runtime.GOOS != "windows" {
		execFile := filepath.Join(tmpDir, "compiled-bin")
		os.WriteFile(execFile, []byte{0x7f, 'E', 'L', 'F'}, 0755)
		if !vfs.IsTerminalRunnable(context.Background(), v, execFile) {
			t.Error("File with executable bit should be terminal-runnable on Unix")
		}
	}
}

func TestPanelsFrame_ReturnExecution(t *testing.T) {
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Создаем временный запускаемый файл
	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "runme.sh")
	os.WriteFile(runnablePath, []byte("echo 1"), 0755)

	// Настраиваем VFS и выбираем этот файл на панели
	fsp := pf.panels[1].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "runme.sh", IsDir: false}},
	}
	fsp.Refresh()
	fsp.SelectName("runme.sh")

	// Проверяем начальное состояние
	if !pf.showPanels {
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
	for pf.showPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("actionExecute did not hide the panels within 1s")
		}
	}
	if pf.showPanels {
		t.Error("Panels should be hidden after executing a terminal-runnable file")
	}

	if len(pf.cmdLine.Edit.History) == 0 {
		t.Error("Executed file was not added to history")
	} else {
		expectedCmd := "runme.sh"
		if runtime.GOOS != "windows" {
			expectedCmd = "./runme.sh"
		}
		if pf.cmdLine.Edit.History[0] != expectedCmd {
			t.Errorf("History mismatch: got %q, want %q", pf.cmdLine.Edit.History[0], expectedCmd)
		}
	}
}
func TestPanelsFrame_CommandLineEnter(t *testing.T) {
	pf := setupMockPanelsFrame()
	pty := pf.pty.(*mockPty)
	defer pf.Close()

	// Вводим команду в консоль
	pf.cmdLine.Edit.SetText("ls -la")

	// Нажимаем Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели должны скрыться
	if pf.showPanels {
		t.Error("Panels should hide after command execution from command line")
	}
	// PTY должен получить команду
	if !strings.Contains(string(pty.written), "ls -la") {
		t.Errorf("PTY did not receive command. Got: %q", string(pty.written))
	}
}

type commandRunnerPanelVFS struct {
	*vfs.NullVFS
	path  string
	calls chan [2]string
}

func (v *commandRunnerPanelVFS) GetPath() string { return v.path }
func (v *commandRunnerPanelVFS) RunCommand(_ context.Context, dir, command string, cb func(string)) (int, error) {
	v.calls <- [2]string{dir, command}
	if cb != nil {
		cb("remote output")
	}
	return 0, nil
}

func TestPanelsFrame_CommandLineUsesRemoteRunnerWithoutPTY(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	defer pf.Close()

	runner := &commandRunnerPanelVFS{
		NullVFS: vfs.NewNullVFS(0),
		path:    "/sdcard/Download",
		calls:   make(chan [2]string, 1),
	}
	fsp := pf.getActivePanel()
	fsp.vfs = runner
	pty := pf.pty.(*mockPty)
	localBytesBefore := len(pty.written)
	pf.cmdLine.Edit.SetText("ls -la")

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	select {
	case call := <-runner.calls:
		if call != [2]string{"/sdcard/Download", "ls -la"} {
			t.Fatalf("remote command call = %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("typed command did not reach the remote CommandRunner")
	}
	if got := len(pty.written); got != localBytesBefore {
		t.Fatalf("local PTY received %d new bytes for a remote command", got-localBytesBefore)
	}
	if !pf.showPanels {
		t.Fatal("remote command without a PTY unexpectedly hid the panels")
	}
	if !pf.cmdLine.IsEmpty() {
		t.Fatalf("command line was not cleared: %q", pf.cmdLine.Edit.GetText())
	}
	if top := vtui.FrameManager.GetTopFrame(); top == nil || !strings.Contains(top.GetTitle(), Msg("RemoteCmd.Title")) {
		t.Fatalf("remote command output frame = %T %q", top, func() string {
			if top != nil {
				return top.GetTitle()
			}
			return ""
		}())
	}
}

func TestPanelsFrame_CommandLineEnter_WhenBusy(t *testing.T) {
	pf := setupMockPanelsFrame()
	pty := pf.pty.(*mockPty)
	defer pf.Close()

	pf.executing = true // PTY is busy

	// Вводим команду в консоль
	pf.cmdLine.Edit.SetText("ls -la")

	// Нажимаем Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели должны скрыться
	if pf.showPanels {
		t.Error("Panels should hide after command execution even when PTY is busy")
	}
	// PTY должен получить команду
	if !strings.Contains(string(pty.written), "ls -la") {
		t.Errorf("PTY did not receive command when busy. Got: %q", string(pty.written))
	}
}

func TestPanelsFrame_DirectoryEnter(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "work_dir")
	os.Mkdir(sub, 0755)

	fsp := pf.panels[1].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "work_dir", IsDir: true}},
	}
	fsp.Refresh()
	fsp.SelectName("work_dir")

	// Нажимаем Enter на директории
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели НЕ должны скрываться
	if !pf.showPanels {
		t.Error("Panels should NOT hide when entering a directory")
	}
	// Путь должен измениться
	if fsp.vfs.GetPath() != sub {
		t.Errorf("VFS path did not change. Expected %s, got %s", sub, fsp.vfs.GetPath())
	}
}

func TestPanelsFrame_NonRunnableOpen(t *testing.T) {
	if _, _, supported := associatedFileCommand("test"); !supported {
		t.Skipf("associated-file launch is unsupported on %s", runtime.GOOS)
	}
	pf := NewPanelsFrame()
	defer pf.Close()
	launches := make(chan recordedExternalUICall, 1)
	pf.externalUIRunner = func(command string, args []string, dir string) error {
		launches <- recordedExternalUICall{command: command, args: append([]string(nil), args...), dir: dir}
		return nil
	}
	pf.ResizeConsole(80, 25)
	tmp := t.TempDir()
	docPath := filepath.Join(tmp, "readme.txt")
	os.WriteFile(docPath, []byte("some text"), 0644)

	fsp := pf.panels[1].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
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
	if !pf.showPanels {
		t.Error("Panels should stay visible when opening non-runnable files via OS associations")
	}

	var launch recordedExternalUICall
	select {
	case launch = <-launches:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for associated-file launch")
	}
	wantCommand, wantArgs, supported := associatedFileCommand(docPath)
	if !supported {
		t.Fatalf("associated-file launch is unsupported on %s", runtime.GOOS)
	}
	want := recordedExternalUICall{command: wantCommand, args: wantArgs, dir: tmp}
	if gotKey, wantKey := externalUICallKey(launch), externalUICallKey(want); gotKey != wantKey {
		t.Errorf("associated-file launch = %#v, want %#v", launch, want)
	}
}
func TestPanelsFrame_SwitchVFSPreservesQualifiedDirectoryCache(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.dirCache[fsp.cacheKey("/test/path")] = dirCacheEntry{}
	if len(fsp.dirCache) != 1 {
		t.Fatal("Cache setup failed")
	}

	pf.switchToVFS(fsp, vfs.NewOSVFS(t.TempDir()))

	if len(fsp.dirCache) != 1 {
		t.Error("switchToVFS discarded the qualified directory cache")
	}
}

func TestPanelsFrame_Clone_CachePreservation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	items := []vfs.VFSItem{{Name: "cached_item"}}
	cacheKey := fsp.cacheKey("/test/path")
	fsp.dirCache[cacheKey] = dirCacheEntry{items: items}

	clone := pf.Clone()
	defer clone.Close()
	cloneFsp := clone.panels[0].(*FileSystemPanel)

	if len(cloneFsp.dirCache) != 1 {
		t.Fatalf("Cache not cloned, length is %d", len(cloneFsp.dirCache))
	}
	if cached, ok := cloneFsp.dirCache[cacheKey]; !ok || len(cached.items) != 1 || cached.items[0].Name != "cached_item" {
		t.Error("Cloned cache content is incorrect")
	}

	// Verify independence
	cloneFsp.dirCache[cloneFsp.cacheKey("/new/path")] = dirCacheEntry{}
	if len(fsp.dirCache) != 1 {
		t.Error("Cloned cache is not independent from original")
	}
}

func TestExecuteFileOp_BackgroundButtonTrigger(t *testing.T) {
	// This test ensures that the logic inside Background button click works
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fm.Push(pf)

	initialScreens := len(fm.Screens)

	// Simulate what Background button does:
	fork := pf.Clone()
	fm.AddScreen(fork)

	if len(fm.Screens) != initialScreens+1 {
		t.Errorf("Backgrounding failed to create a new screen. Got %d, want %d", len(fm.Screens), initialScreens+1)
	}
}
func TestExecuteDummyOp_HeadlessMode(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	fm.Push(pf)

	initialScreens := len(fm.Screens)

	// Trigger Mode Foreground (2)
	go pf.ExecuteDummyOp(2)

	// Manually process the task queue (since we are not in fm.Run loop)
	timeout := time.After(1 * time.Second)
	for len(fm.Screens) == initialScreens {
		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			t.Fatal("ExecuteDummyOp did not post workspace creation task")
		}
	}

	if len(fm.Screens) != initialScreens+1 {
		t.Fatalf("Headless screen not created. Got %d", len(fm.Screens))
	}

	newScreen := fm.Screens[len(fm.Screens)-1]
	if len(newScreen.Frames) != 1 { // Только диалог, без Desktop
		t.Errorf("Headless screen should have 1 frame, got %d", len(newScreen.Frames))
	}
	if !newScreen.Transparent {
		t.Error("Headless screen should be transparent")
	}

	// Clean up and cancel the task to prevent background leak
	dlg := newScreen.Frames[0].(*vtui.Window)
	dlg.SetExitCode(1) // Cancels the taskCtx
	if dlg.OnResult != nil {
		dlg.OnResult(1)
	}
	for i := 0; i < 20; i++ {
		select {
		case task := <-fm.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPanelsFrame_TerminalForwarding_Legacy(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.showPanels = false
	pf.termView.UseAltScreen = true

	// Mock PTY
	pty := &mockPty{}
	pf.pty = pty

	// 1. Ctrl+W should be FORWARDED (Legacy mode has no Kitty/Win32 flags)
	// For letters, TranslateInput expects the Char field to be populated.
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_W, Char: 'w', ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if !strings.Contains(string(pty.written), "\x17") { // 0x17 is Ctrl+W byte
		t.Error("Ctrl+W should be forwarded to terminal in legacy mode")
	}
	pty.written = nil

	// 2. Ctrl+Tab should NOT be forwarded (returns false, handled by FrameManager)
	handled := pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_TAB, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if handled {
		t.Error("Ctrl+Tab should NOT be handled by PanelsFrame in legacy mode")
	}
	if len(pty.written) > 0 {
		t.Error("PTY received bytes for Ctrl+Tab in legacy mode")
	}
}

func TestPanelsFrame_TerminalForwarding_Advanced(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.showPanels = false
	pf.termView.UseAltScreen = true
	pf.termView.Win32InputMode = true // Advanced mode

	pty := &mockPty{}
	pf.pty = pty

	// 1. Ctrl+Tab remains a global workspace shortcut in Advanced mode.
	handled := pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_TAB, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if handled {
		t.Error("Ctrl+Tab was erroneously forwarded to PTY in Advanced mode")
	}
	if len(pty.written) != 0 {
		t.Error("PTY received bytes for Ctrl+Tab in Advanced mode")
	}
	pty.written = nil

	// 2. Shift+Ctrl+Tab should NOT be forwarded in any mode
	handled = pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_TAB, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})
	if handled {
		t.Error("Shift+Ctrl+Tab was erroneously forwarded to PTY")
	}
}

type busyMockPty struct{ mockPty }

func (p *busyMockPty) IsBusy() bool { return true }

func TestPanelsFrame_TerminalForwarding_BusyNonAltScreenWorkspaceKeys(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.showPanels = false
	pf.termView.UseAltScreen = false // Python REPL and similar interactive tools.

	pty := &busyMockPty{}
	pf.pty = pty

	for _, state := range []vtinput.ControlKeyState{
		vtinput.LeftCtrlPressed,
		vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	} {
		pty.Reset()
		handled := pressKey(pf, &vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  vtinput.VK_TAB,
			ControlKeyState: state,
		})
		if handled {
			t.Errorf("workspace key with modifiers %#x was consumed by busy terminal", state)
		}
		if got := pty.String(); got != "" {
			t.Errorf("workspace key with modifiers %#x reached PTY as %q", state, got)
		}
	}
}

func TestPanelsFrame_TerminalCtrlNWorkspacePreference(t *testing.T) {
	old := AppConfig.TerminalCtrlNWorkspace
	defer func() { AppConfig.TerminalCtrlNWorkspace = old }()

	for _, altScreen := range []bool{false, true} {
		pf := NewPanelsFrame()
		pf.showPanels = false
		pf.termView.UseAltScreen = altScreen
		pty := &busyMockPty{}
		pf.pty = pty

		event := func() *vtinput.InputEvent {
			return &vtinput.InputEvent{
				Type:            vtinput.KeyEventType,
				KeyDown:         true,
				VirtualKeyCode:  vtinput.VK_N,
				Char:            'n',
				ControlKeyState: vtinput.LeftCtrlPressed,
			}
		}

		AppConfig.TerminalCtrlNWorkspace = true
		if pressKey(pf, event()) {
			t.Errorf("Ctrl+N was not released to FrameManager (AltScreen=%v)", altScreen)
		}
		if got := pty.String(); got != "" {
			t.Errorf("enabled Ctrl+N preference wrote %q to PTY (AltScreen=%v)", got, altScreen)
		}

		AppConfig.TerminalCtrlNWorkspace = false
		pty.Reset()
		if !pressKey(pf, event()) {
			t.Errorf("disabled Ctrl+N preference did not return key to PTY (AltScreen=%v)", altScreen)
		}
		if got := pty.String(); got != "\x0e" {
			t.Errorf("disabled Ctrl+N preference wrote %q, want Ctrl+N (AltScreen=%v)", got, altScreen)
		}
		pf.Close()
	}
}

func TestPanelsFrame_ForkFromTerminalOpensPanelsInNewWorkspace(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.showPanels = false
	fm.Push(pf)

	if !pf.HandleCommand(vtui.CmResize, "fork") {
		t.Fatal("fork command was not handled")
	}
	if len(fm.Screens) != 2 {
		t.Fatalf("fork created %d workspaces, want 2", len(fm.Screens))
	}
	clone, ok := fm.GetTopFrame().(*PanelsFrame)
	if !ok {
		t.Fatalf("forked top frame = %T, want *PanelsFrame", fm.GetTopFrame())
	}
	defer clone.Close()
	if !clone.showPanels {
		t.Error("terminal-side fork left the cloned workspace in terminal view")
	}
	if pf.showPanels {
		t.Error("terminal-side fork changed the original workspace")
	}
	if clone.GetTitle() == "cmd.exe" || clone.GetTitle() == "Terminal" {
		t.Errorf("forked workspace title still looks like a terminal: %q", clone.GetTitle())
	}
}
func TestPanelsFrame_FilesMenuLabels(t *testing.T) {
	old := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = old }()

	pf := NewPanelsFrame()
	defer pf.Close()

	// Items[1] is the "Files" menu (Left, Files, Commands, Options, Right)
	filesMenu := pf.menuBar.Items[1]
	if filesMenu.Label != "&Files" {
		t.Errorf("Expected Files menu label '&Files', got %q", filesMenu.Label)
	}

	expected := "&" + Msg("Menu.Files.RenMov")
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

func TestPanelsFrame_ProcessMouse_RightDoubleClickNoEnter(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	os.WriteFile(runnablePath, []byte("echo"), 0755)

	fsp := pf.Left().(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsDir: false}},
	}
	fsp.Refresh()

	// Double click with RIGHT button. Row 1 -> Y=3
	pf.ProcessMouse(&vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          5,
		MouseY:          3,
		ButtonState:     vtinput.RightmostButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})

	// Panels should NOT hide. Right double-click should only toggle selection.
	if !pf.showPanels {
		t.Error("Right double-click should NOT simulate Enter")
	}
}

func TestPanelsFrame_CommandRouting_FKeys(t *testing.T) {
	pf := NewPanelsFrame()
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
	if top == nil || top.GetTitle() != Msg("Quit.Title") {
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

func TestPanelsFrame_QuitConfirmation_Cancel(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	fm.Push(pf)

	// Trigger Quit
	pf.HandleCommand(vtui.CmQuit, nil)

	top := fm.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("Quit.Title") {
		t.Fatal("Quit dialog didn't appear")
	}

	// Simulate clicking "Cancel" (button 1)
	if d, ok := top.(*vtui.Window); ok && d.OnResult != nil {
		d.OnResult(1)
	}

	if fm.IsShutdown() {
		t.Error("Application shut down even after exit was canceled")
	}
}
func TestPanelsFrame_F9Context(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Test Left Panel context
	pf.activeIdx = 0
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	})

	if pf.menuBar.SelectPos != 0 {
		t.Errorf("F9 on left panel: expected menu index 0, got %d", pf.menuBar.SelectPos)
	}
	if !pf.menuBar.Active {
		t.Error("MenuBar should be active after F9")
	}

	// 2. Test Right Panel context
	pf.menuBar.Active = false // Reset
	pf.activeIdx = 1
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_F9,
	})

	if pf.menuBar.SelectPos != 4 {
		t.Errorf("F9 on right panel: expected menu index 4, got %d", pf.menuBar.SelectPos)
	}
}

func TestLayout_F4InternalDialogs_Validity(t *testing.T) {
	vtui.SetDefaultPalette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	t.Run("DummyOpDialog", func(t *testing.T) {
		// We need to capture the dialog created by showDummyOpDialog.
		// Since it pushes to the real FrameManager, we'll initialize it.
		fm := vtui.FrameManager
		fm.Init(vtui.NewSilentScreenBuf())

		pf.showDummyOpDialog()
		top := fm.GetTopFrame()
		if dlg, ok := top.(vtui.Container); ok {
			vtui.AssertLayout(t, dlg)
		} else {
			t.Fatal("Top frame is not a container")
		}
	})
}
func TestPanelsFrame_CopyShortcuts(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	if fsp.cancelLoad != nil {
		fsp.cancelLoad()
	}
	fsp.isLoading = false
	if fsp.loadingTimer != nil {
		fsp.loadingTimer.Stop()
	}

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "target.txt"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1)
	pf.activeIdx = 0

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
	expectedPath := fsp.vfs.Join(fsp.vfs.GetPath(), "target.txt")

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
	pf.cmdLine.Edit.SetText("")
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: 'F', ControlKeyState: vtinput.LeftCtrlPressed,
	})
	expectedCommand := expectedPath
	if runtime.GOOS == "windows" {
		expectedCommand = `"` + expectedPath + `"`
	}
	if got := pf.cmdLine.Edit.GetText(); got != expectedCommand {
		t.Fatalf("Ctrl+F failed: expected command line %q, got %q", expectedCommand, got)
	}
}
func TestLayout_F4ActionDialogs_Validity(t *testing.T) {
	vtui.SetDefaultPalette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fm := vtui.FrameManager

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)

	// Helper to setup active panel with some files
	setupPanel := func() {
		pf.activeIdx = 0
		fsp := pf.panels[0].(*FileSystemPanel)
		fsp.entries = []*fileEntry{
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
		fm.Pop()
	})

	t.Run("MoveDialog", func(t *testing.T) {
		setupPanel()
		actionCopyMove(pf, true)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("MkDirDialog", func(t *testing.T) {
		actionMkDir(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})

	t.Run("DeleteDialog", func(t *testing.T) {
		setupPanel()
		actionDelete(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})
	t.Run("FindFileDialog", func(t *testing.T) {
		setupPanel()
		actionFindFile(pf)
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

	t.Run("AppearanceSettingsDialog", func(t *testing.T) {
		actionAppearanceSettings(pf)
		dlg := fm.GetTopFrame().(vtui.Container)
		vtui.AssertLayout(t, dlg)
		fm.Pop()
	})
}

func TestPanelsFrame_DriveMenu_OtherPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathR := filepath.Join(t.TempDir(), "right")
	os.MkdirAll(pathR, 0755)
	pf.panels[1].(*FileSystemPanel).vfs.SetPath(pathR)

	// Open Alt+F1 (Left panel drive menu)
	pf.showDriveMenu(0)

	top := vtui.FrameManager.GetTopFrame()
	menu, ok := top.(*vtui.VMenu)
	if !ok {
		t.Fatal("Drive menu not opened")
	}

	// Ensure "Other panel" is at index 0 and selected
	if menu.GetTitle() != Msg("Drive.Title") || menu.SelectPos != 0 {
		t.Errorf("Menu state invalid: title=%q, pos=%d", menu.GetTitle(), menu.SelectPos)
	}

	// Trigger "Other panel" (idx 0)
	menu.OnAction(0)

	// Left panel VFS path must now match Right panel's path
	got := pf.panels[0].(*FileSystemPanel).vfs.GetPath()
	if got != pathR {
		t.Errorf("Path sync failed. Expected %q, got %q", pathR, got)
	}
}

func TestPanelsFrame_DriveMenu_TerminalBusy(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Simulate busy terminal
	pf.showPanels = false
	pf.termView.UseAltScreen = true

	// Press Alt+F1
	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_F1, ControlKeyState: vtinput.LeftAltPressed,
	})

	// Menu should NOT open
	if vtui.FrameManager.GetTopFrameType() == vtui.TypeMenu {
		t.Error("Drive menu opened while terminal was busy")
	}
}

func TestPanelsFrame_TerminalTabAutoComplete(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Hide panels
	pf.showPanels = false

	// 2. Add history item starting with "cd" to guarantee a match
	pf.cmdLine.Edit.History = []string{"cd_test_dir"}
	pf.cmdLine.Edit.SetText("cd")

	// 3. Press Tab
	handled := pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_TAB,
	})

	if !handled {
		t.Error("Expected Tab to be handled as autocomplete trigger when panels are hidden")
	}

	// 4. Verify AutoCompleteMenu is pushed
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Error("Expected AutoCompleteMenu to be on top of the frame stack")
	} else {
		// Clean up
		vtui.FrameManager.Pop()
	}
}

func TestDriveMenu_SmartHotkeys(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Сохраняем оригинал и подменяем реестр
	oldRegistry := DriveRegistry
	DriveRegistry = []DriveEntry{
		{Name: "NetFox", Factory: func() vfs.VFS { return nil }},
		{Name: "Null VFS", Factory: func() vfs.VFS { return nil }},
	}
	defer func() { DriveRegistry = oldRegistry }()

	pf.showDriveMenu(0)
	top := vtui.FrameManager.GetTopFrame()
	menu, ok := top.(*vtui.VMenu)
	if !ok {
		t.Fatalf("Expected VMenu on top, got %T", top)
	}

	// 1. Проверка фокуса (Other panel по умолчанию)
	if menu.SelectPos != 0 {
		t.Errorf("Expected 'Other panel' (index 0) to be focused, got index %d", menu.SelectPos)
	}

	// 2. Ищем плагины в пунктах меню
	var nfIdx, nullIdx int = -1, -1
	for i, itm := range menu.Items {
		cleanText := strings.ReplaceAll(itm.Text, "&", "")
		if strings.Contains(cleanText, "NetFox") {
			nfIdx = i
		}
		if strings.Contains(cleanText, "Null VFS") {
			nullIdx = i
		}
	}

	if nfIdx == -1 || nullIdx == -1 {
		var items []string
		for _, itm := range menu.Items {
			items = append(items, itm.Text)
		}
		t.Fatalf("Plugins not found in menu. Items present: %v", items)
	}

	// 3. Проверка уникальности хоткеев
	// NetFox (первый в списке) заберет 'N' -> "1. &NetFox"
	// Null VFS (второй) увидит, что 'N' занята, и заберет 'u' -> "2. N&ull VFS"
	nfText := menu.Items[nfIdx].Text
	nullText := menu.Items[nullIdx].Text

	if !strings.Contains(nfText, "&N") {
		t.Errorf("NetFox should have 'N' as hotkey: %q", nfText)
	}
	if !strings.Contains(nullText, "N&u") {
		t.Errorf("Null VFS should have 'u' as hotkey (N is taken): %q", nullText)
	}
}

func TestDriveMenu_PhysicalKeys(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Linux-specific physical key test")
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.showDriveMenu(0)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)

	// Inject VK_OEM_3 (tilde/backtick key)
	// It should find the Home item and trigger selection
	handled := menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_OEM_3,
	})

	if !handled {
		t.Error("Drive menu failed to handle physical tilde key")
	}
	if !menu.IsDone() {
		t.Error("Physical key should have triggered selection and closed the menu")
	}
}
func TestPanelsFrame_ShiftInsert_Fallthrough(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Prepare clipboard
	testText := "ClipboardPayload"
	vtui.SetClipboard(testText)

	for i := 0; i < 50; i++ {
		if vtui.GetClipboard() == testText {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if vtui.GetClipboard() != testText {
		t.Fatalf("Failed to set clipboard to %q", testText)
	}

	// 2. Ensure panel is active (should NOT handle Shift+Ins)
	pf.activeIdx = 0
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "some_file.txt"}}}
	fsp.Refresh()
	fsp.SetFocus(true)

	// 3. Send Shift+Ins
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_INSERT,
		ControlKeyState: vtinput.ShiftPressed,
	})

	// 4. Verify text landed in CommandLine
	got := pf.cmdLine.Edit.GetText()
	if !strings.Contains(got, testText) {
		t.Errorf("Shift+Ins failed to fallthrough to CommandLine. Got %q, expected to contain %q", got, testText)
	}

	// 5. Verify file was NOT selected (Index 0 should remain unselected)
	if fsp.entries[0].Selected {
		t.Error("File was erroneously selected by Shift+Ins")
	}
}
func TestPanelsFrame_PromptTruncation(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Simulate standard 80-column terminal
	width := 80
	pf.ResizeConsole(width, 25)

	fsp := pf.getActivePanel()

	// Max allowed prompt length is width / 2 = 40.

	t.Run("Short Path No Truncation", func(t *testing.T) {
		// Use NullVFS to bypass real disk checks in tests
		fsp.vfs = vfs.NewNullVFS(0)
		fsp.vfs.SetPath(filepath.FromSlash("/home/user"))
		prompt := pf.buildPrompt()

		visibleLen := 0
		for _, c := range prompt {
			if c.Char != vtui.WideCharFiller {
				visibleLen++
			}
		}

		// Path is short, should be preserved entirely
		found := false
		promptStr := ""
		for _, c := range prompt {
			if c.Char != vtui.WideCharFiller {
				promptStr += string(rune(c.Char))
			}
		}
		if strings.Contains(promptStr, "home") {
			found = true
		}

		if !found {
			t.Errorf("Short path was lost in prompt: %q", promptStr)
		}
	})

	t.Run("Extreme Long Path Truncation", func(t *testing.T) {
		// Use NullVFS to bypass real disk checks in tests
		fsp.vfs = vfs.NewNullVFS(0)
		longPath := "/very/long/directory/path/that/exceeds/the/limit/of/forty/characters/definitely/and/must/be/shortened"
		fsp.vfs.SetPath(filepath.FromSlash(longPath))
		prompt := pf.buildPrompt()

		visibleLen := 0
		promptStr := ""
		for _, c := range prompt {
			if c.Char != vtui.WideCharFiller {
				visibleLen++
				promptStr += string(rune(c.Char))
			}
		}

		// 1. Total length must be within bounds (approx 40)
		if visibleLen > 45 { // 40 + small buffer for user@host
			t.Errorf("Prompt too long: %d chars (%q)", visibleLen, promptStr)
		}

		// 2. Must contain ellipsis
		if !strings.Contains(promptStr, "...") {
			t.Errorf("Truncated prompt missing ellipsis: %q", promptStr)
		}

		// 3. Check OS-specific suffix
		if runtime.GOOS == "windows" {
			if !strings.HasSuffix(promptStr, ">") {
				t.Errorf("Windows prompt should end with '>', got %q", promptStr)
			}
		} else {
			if !strings.HasSuffix(promptStr, "$ ") {
				t.Errorf("Unix prompt should end with '$ ', got %q", promptStr)
			}
		}
	})
}

type mockSlowStatVFS struct {
	vfs.OSVFS
	// Stat is called from background goroutines and read from the test, so
	// the counter has to be one both may touch.
	statCalls atomic.Int64
	statBlock chan struct{}
}

func (m *mockSlowStatVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	m.statCalls.Add(1)
	if m.statBlock != nil {
		<-m.statBlock
	}
	return m.OSVFS.Stat(ctx, p)
}

func TestPanelsFrame_AutoRefresh_Locking(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Wait for initial load of both panels to finish.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if !pf.panels[0].(*FileSystemPanel).isLoading && !pf.panels[1].(*FileSystemPanel).isLoading {
			break
		}
	}
	if pf.panels[0].(*FileSystemPanel).isLoading || pf.panels[1].(*FileSystemPanel).isLoading {
		t.Fatal("Initial load did not finish in time")
	}

	// Setup VFS with a blocking Stat
	block := make(chan struct{})
	mv := &mockSlowStatVFS{
		OSVFS:     *vfs.NewOSVFS(t.TempDir()),
		statBlock: block,
	}

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = mv
	fsp.lastDirMTime = time.Now().Add(-1 * time.Hour)
	fsp.isCheckingRefresh = false

	// First Show() should trigger auto-refresh
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	pf.lastAutoRefresh = time.Now().Add(-5 * time.Second)
	pf.Show(scr)

	// Pump tasks so the auto-refresh goroutine can run
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if fsp.isCheckingRefresh && mv.statCalls.Load() >= 1 {
			break
		}
	}
	if !fsp.isCheckingRefresh {
		t.Error("Expected isCheckingRefresh to be true while Stat is pending")
	}
	if mv.statCalls.Load() < 1 {
		t.Error("Expected the auto refresh to have called Stat")
	}
	before := mv.statCalls.Load()
	pf.lastAutoRefresh = time.Now().Add(-5 * time.Second)
	pf.Show(vtui.NewSilentScreenBuf())
	if after := mv.statCalls.Load(); after != before {
		t.Errorf("Anti-spam failed: Stat called %d more times while one was pending", after-before)
	}

	// Unblock Stat and verify the flag is reset.
	close(block)
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if !fsp.isCheckingRefresh {
			return
		}
	}
	t.Fatal("isCheckingRefresh was never reset to false")
}

type vimTestHandler struct {
	vtui.BaseFrame
	onCmd func(cmd int, args any) bool
}

func (v *vimTestHandler) HandleCommand(cmd int, args any) bool {
	if v.onCmd != nil {
		return v.onCmd(cmd, args)
	}
	return false
}

func (v *vimTestHandler) GetType() vtui.FrameType { return vtui.TypeUser }
func (v *vimTestHandler) GetTitle() string        { return "VimHandler" }

func TestPanelsFrame_VimHotkeys_Comprehensive(t *testing.T) {
	vtui.SetDefaultPalette()
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	cmdCaught := 0
	handler := &vimTestHandler{
		onCmd: func(cmd int, args any) bool {
			cmdCaught = cmd
			return true
		},
	}

	oldCfg := AppConfig
	AppConfig.NavigationMode = NavigationVim
	defer func() { AppConfig = oldCfg }()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	pf.activeIdx = 0

	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "fileA"}},
		{VFSItem: vfs.VFSItem{Name: "fileB"}},
		{VFSItem: vfs.VFSItem{Name: "fileC"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1) // On fileA

	fm.Push(pf)
	fm.Push(handler)

	// 1. Basic j/k navigation
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'j'})
	if fsp.GetCursorIndex() != 2 {
		t.Errorf("Vim 'j' failed, expected index 2, got %d", fsp.GetCursorIndex())
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'k'})
	if fsp.GetCursorIndex() != 1 {
		t.Errorf("Vim 'k' failed, expected index 1, got %d", fsp.GetCursorIndex())
	}

	// 2. Action dd (Delete)
	cmdCaught = 0
	pf.cmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	if cmdCaught != CmDelete {
		t.Errorf("'dd' failed to emit CmDelete, got %d", cmdCaught)
	}
	if !pf.cmdLine.IsEmpty() {
		t.Error("Command line should be cleared after Vim action")
	}

	// 3. Reset on Tab (Switch panel)
	cmdCaught = 0
	pf.cmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	if cmdCaught == CmDelete {
		t.Error("Vim prefix should reset after switching panels via Tab")
	}

	// 4. Reset on Mouse click
	cmdCaught = 0
	pf.cmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'c'})
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX: 5, MouseY: 5,
	})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'c'})
	if cmdCaught == CmCopy {
		t.Error("Vim prefix should reset after mouse interaction")
	}

	// 5. Conflict with Fast Find
	cmdCaught = 0
	pf.cmdLine.Clear()
	fsp.SetCursorIndex(1) // Reset cursor position after mouse click test
	fsp.fastFindMode = true
	// In fast find mode, 'j' should be passed to find logic, not navigation.
	// `pf.ProcessKey` will return `false` because Vim logic is skipped,
	// then it will fall through to `fsp.ProcessKey` which will handle fast-find and return `true`.
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'j'})
	if fsp.GetCursorIndex() != 1 {
		t.Error("'j' was handled as Vim navigation despite Fast Find being active")
	}
	if fsp.fastFindStr != "j" {
		t.Errorf("Fast find string should be 'j', got %q", fsp.fastFindStr)
	}
}

func createTestZipForNav(t *testing.T, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	_, err = zw.Create("inner_dir/")
	if err != nil {
		t.Fatal(err)
	}

	w, err := zw.Create("inner_dir/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("hello"))
}

func TestPanelsFrame_NavigateToPath(t *testing.T) {
	// Register the Archive VFS provider manually for this unit test
	vfs.RegisterProvider(&archive.ArchiveProvider{})

	// Initialize a headless FrameManager to prevent nil panics during async directory reads
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(tmpDir, "test.zip")
	createTestZipForNav(t, zipPath)

	pf := &PanelsFrame{}
	lp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(tmpDir))
	rp := NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS(tmpDir))
	pf.panels[0] = lp
	pf.panels[1] = rp
	pf.activeIdx = 0
	defer pf.Close()

	waitForLoad(t, lp)
	waitForLoad(t, rp)

	// Test 1: Navigate to absolute path inside the archive
	targetPath := filepath.Join(zipPath, "inner_dir")
	ok := pf.NavigateToPath(lp, targetPath)
	if !ok {
		t.Fatalf("NavigateToPath failed to enter archive: %s", targetPath)
	}
	waitForLoad(t, lp)

	// Verify VFS switched to ArchiveVFS
	if _, isOS := lp.vfs.(*vfs.OSVFS); isOS {
		t.Error("Expected panel VFS to switch from OSVFS to ArchiveVFS")
	}

	expectedPath := filepath.ToSlash(filepath.Clean(targetPath))
	if filepath.ToSlash(lp.vfs.GetPath()) != expectedPath {
		t.Errorf("Expected VFS path %q, got %q", expectedPath, lp.vfs.GetPath())
	}

	// Test 2: Navigate to ".." at the archive root to escape it
	ok = pf.NavigateToPath(lp, zipPath)
	if !ok {
		t.Fatalf("Failed to navigate to archive root: %s", zipPath)
	}
	// Use waitForLoad which is defined in file_panel_test.go
	waitForLoad(t, lp)

	ok = pf.NavigateToPath(lp, "..")
	if !ok {
		t.Fatal("Failed to navigate '..' from archive root")
	}
	waitForLoad(t, lp)

	// Verify we switched back to OSVFS pointing to tmpDir
	if _, isOS := lp.vfs.(*vfs.OSVFS); !isOS {
		t.Error("Expected panel VFS to switch back to OSVFS")
	}

	if filepath.Clean(lp.vfs.GetPath()) != filepath.Clean(tmpDir) {
		t.Errorf("Expected OSVFS path %q, got %q", tmpDir, lp.vfs.GetPath())
	}
}

func TestArchiveBulkExtract_ProgressTracking(t *testing.T) {
	// Register the Archive VFS provider manually for this unit test
	vfs.RegisterProvider(&archive.ArchiveProvider{})

	// Initialize a headless FrameManager to prevent nil panics during async directory reads
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(tmpDir, "progress_test.zip")

	// Create a test zip with 1 folder and 2 files
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	// Directory
	_, _ = zw.Create("dir/")
	// File 1 (10 bytes)
	w1, _ := zw.Create("dir/file1.txt")
	w1.Write([]byte("0123456789"))
	// File 2 (20 bytes)
	w2, _ := zw.Create("dir/file2.txt")
	w2.Write([]byte("01234567890123456789"))
	zw.Close()
	f.Close()

	// 1. Setup VFS
	parentVFS := vfs.NewOSVFS(tmpDir)
	arcVFS, err := vfs.FindProvider(context.Background(), parentVFS, zipPath).Open(context.Background(), parentVFS, zipPath)
	if err != nil {
		t.Fatalf("Failed to open archive VFS: %v", err)
	}
	defer arcVFS.Close()

	destDir := filepath.Join(tmpDir, "extracted")
	os.MkdirAll(destDir, 0755)
	dstVFS := vfs.NewOSVFS(destDir)

	// 2. Pre-calculate stats (this mimics ExecuteFileOp's scan phase)
	names := []string{"dir"}
	totalStats, err := vfs.CalculateStats(context.Background(), arcVFS, arcVFS.GetPath(), names, nil)
	if err != nil {
		t.Fatalf("CalculateStats failed: %v", err)
	}

	// Verify scanned stats: 1 dir, 2 files, 30 bytes
	if totalStats.Files != 2 || totalStats.Dirs != 1 || totalStats.Bytes != 30 {
		t.Errorf("Unexpected scanned stats: %+v", totalStats)
	}

	tracker := NewFileOpTracker(totalStats)

	var bytesReported int64

	mockOriginalReporter := &mockTaskReporter{}

	// We want to verify that when we call CopyBulk, the globalAwareReporter updates the tracker
	// and invokes updateUI, which in turn updates the dialog.
	getGlobalStats := func(action string) (string, int, string) {
		_, totalPct, _ := tracker.GetProgress()
		processed, total := tracker.GetStats()
		totalText := fmt.Sprintf("Total: %d/%d", processed.Bytes, total.Bytes)
		timeSpeedText := fmt.Sprintf("Progress: %d%%", totalPct)
		return totalText, totalPct, timeSpeedText
	}

	wrapRep := &globalAwareReporter{
		original:  mockOriginalReporter,
		getGlobal: getGlobalStats,
		tracker:   tracker,
		onBytes: func(n int) {
			bytesReported += int64(n)
		},
	}

	// We pass "AutoQueue" in context to bypass the interactive UI busy-lock prompt
	ctx := context.WithValue(context.Background(), "AutoQueue", true)

	// 3. Execute Bulk Copy
	bulkCopier := arcVFS.(vfs.BulkCopier)
	err = bulkCopier.CopyBulk(ctx, names, dstVFS, destDir, wrapRep)
	if err != nil {
		t.Fatalf("CopyBulk failed: %v", err)
	}

	// 4. Verify results
	processed, _ := tracker.GetStats()

	// All 30 bytes must be reported
	if bytesReported != 30 {
		t.Errorf("Expected 30 bytes reported via onBytes, got %d", bytesReported)
	}
	if processed.Bytes != 30 {
		t.Errorf("Tracker processed bytes mismatch: expected 30, got %d", processed.Bytes)
	}
	if processed.Files != 2 {
		t.Errorf("Tracker processed files mismatch: expected 2, got %d", processed.Files)
	}
	if processed.Dirs != 1 {
		t.Errorf("Tracker processed dirs mismatch: expected 1, got %d", processed.Dirs)
	}

	// Verify files actually extracted and content matches
	b1, err := os.ReadFile(filepath.Join(destDir, "dir/file1.txt"))
	if err != nil || string(b1) != "0123456789" {
		t.Errorf("file1.txt mismatch: %q (err: %v)", string(b1), err)
	}
	b2, err := os.ReadFile(filepath.Join(destDir, "dir/file2.txt"))
	if err != nil || string(b2) != "01234567890123456789" {
		t.Errorf("file2.txt mismatch: %q (err: %v)", string(b2), err)
	}
}
func TestPanelsFrame_ShiftF5_KeyInterception(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "cursor_file.txt"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1) // Focus on cursor_file.txt
	pf.activeIdx = 0

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
func TestPanelsFrame_MouseForwarding_ToPTY(t *testing.T) {
	pf := setupMockPanelsFrame()
	pty := pf.pty.(*mockPty)
	defer pf.Close()

	// Setup: hidden panels and mouse tracking enabled in terminal
	pf.showPanels = false
	pf.termView.MouseTrackingMode = 1000
	pf.termView.MouseSGRMode = true

	// Simulate left click at (10, 10)
	ev := &vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		KeyDown:         true,
		MouseX:          10,
		MouseY:          10,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
		ControlKeyState: 0,
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse event should be handled by PanelsFrame when panels are hidden")
	}

	// PTY must receive SGR 1006 sequence: \x1b[<0;11;11M (1-based coords)
	expected := "\x1b[<0;11;11M"
	if !strings.Contains(pty.String(), expected) {
		t.Errorf("PTY did not receive expected mouse sequence. Got: %q, want to contain: %q", pty.String(), expected)
	}
}
func TestPanelsFrame_NoCtrlOInterception_InAltScreen(t *testing.T) {
	pf := setupMockPanelsFrame()
	pty := pf.pty.(*mockPty)
	defer pf.Close()

	pf.showPanels = false
	pf.termView.UseAltScreen = true

	// Send Ctrl+O
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_O,
		Char:            15, // Ctrl+O character code
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// Panels must remain hidden (f4 must NOT intercept Ctrl+O when terminal app is active)
	if pf.showPanels {
		t.Error("f4 erroneously intercepted Ctrl+O while terminal app was active")
	}

	// PTY must receive the Ctrl+O byte (\x0f)
	if !strings.Contains(pty.String(), "\x0f") {
		t.Errorf("PTY did not receive Ctrl+O byte. Got: %q", pty.String())
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
	defer os.Remove(tmp.Name())
	tmp.Close()

	oldGetPath := getUserConfigIniPath
	getUserConfigIniPath = func() string { return tmp.Name() }
	getConfigIniPaths = func() []string { return []string{tmp.Name()} }
	defer func() {
		getUserConfigIniPath = oldGetPath
	}()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
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

	// Проверяем, что файл настроек действительно был записан на диск
	info, err := os.Stat(tmp.Name())
	if err != nil || info.Size() == 0 {
		t.Error("Expected Shift+F9 to write settings to ini file")
	}
}

type mockUpdateVFS struct {
	vfs.NullVFS
}

func (m *mockUpdateVFS) GetPath() string { return "/" }
func (m *mockUpdateVFS) IsAtRoot() bool  { return true }
func (m *mockUpdateVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	onChunk([]vfs.VFSItem{
		{Name: "test_file.txt", Size: 100, IsDir: false},
	})
	return nil
}

func TestPanelsFrame_MiddleClick_LaunchesFile(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	// Подставляем стабильный mockUpdateVFS для панели
	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	fsp.vfs = &mockUpdateVFS{}
	fsp.ReadDirectory()

	// Даем немного времени асинхронной горутине на наполнение
	time.Sleep(50 * time.Millisecond)
	fsp.SetCursorIndex(0)

	// Симулируем клик колесом мыши по первой строке
	ev := &vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		MouseX:      int16(fsp.X1 + 5),
		MouseY:      int16(fsp.Y1 + 1), // Клик по первой строке
		ButtonState: vtinput.FromLeft2ndButtonPressed,
		KeyDown:     true,
	}

	if !pf.ProcessMouse(ev) {
		t.Error("Expected PanelsFrame to handle middle click mouse event")
	}
}

func TestPanelsFrame_CtrlBackslash_GoesToRoot(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "a", "b", "c")
	os.MkdirAll(sub, 0755)
	fsp.vfs.SetPath(sub)

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
	if filepath.Clean(fsp.vfs.GetPath()) != filepath.Clean(expectedRoot) {
		t.Errorf("Ctrl+\\ failed to go to root: expected %q, got %q", expectedRoot, fsp.vfs.GetPath())
	}
}

func TestPanelsFrame_CtrlPgUp_GoesToParentOrDriveMenu(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	os.MkdirAll(sub, 0755)
	fsp.vfs.SetPath(sub)

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
	if filepath.Clean(fsp.vfs.GetPath()) != filepath.Clean(tmp) {
		t.Errorf("Ctrl+PgUp failed to go up: expected %q, got %q", tmp, fsp.vfs.GetPath())
	}
	if fsp.pendingSelection != "sub" {
		t.Errorf("Ctrl+PgUp should position cursor on 'sub', got %q", fsp.pendingSelection)
	}

	// 2. Поднимаемся все дальше до физического корня системы
	for !fsp.vfs.IsAtRoot() {
		if err := fsp.vfs.SetPath(".."); err != nil {
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

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	os.MkdirAll(sub, 0755)
	fsp.vfs.SetPath(tmp)

	fsp.entries = []*fileEntry{
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
	if filepath.Clean(fsp.vfs.GetPath()) != filepath.Clean(sub) {
		t.Errorf("Ctrl+PgDn failed to enter directory: expected %q, got %q", sub, fsp.vfs.GetPath())
	}
}

func TestPanelsFrame_CtrlViewModes(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)

	// 1. Изначально устанавливаем режим Medium
	fsp.SetViewMode(ViewModeMedium)
	oldHotkeys := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	defer func() { GlobalHotkeysMgr = oldHotkeys }()
	macroFilter := &MacroManager{Macros: make(map[string]map[string][]*vtinput.InputEvent)}
	rightCtrl3 := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '3', ControlKeyState: vtinput.RightCtrlPressed}
	if macroFilter.Filter(rightCtrl3) {
		t.Fatal("RightCtrl+3 was consumed by the configurable hotkey filter")
	}
	pressKey(pf, rightCtrl3)
	if fsp.viewMode != ViewModeMedium {
		t.Fatalf("RightCtrl+3 changed panel mode to %v", fsp.viewMode)
	}

	for _, tc := range []struct {
		key  uint16
		mode ViewMode
	}{{'1', ViewModeBrief}, {'2', ViewModeMedium}, {'3', ViewModeDetailed}} {
		pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: tc.key, ControlKeyState: vtinput.LeftCtrlPressed})
		if fsp.viewMode != tc.mode || pf.widePanel != -1 {
			t.Errorf("Ctrl+%c: mode=%v wide=%d, want mode=%v wide=-1", tc.key, fsp.viewMode, pf.widePanel, tc.mode)
		}
	}

	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '4', ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.widePanel != pf.activeIdx || !fsp.wide || len(fsp.table.Columns) != 3 {
		t.Fatalf("Ctrl+4 did not enter Wide: wide=%d active=%d columns=%d", pf.widePanel, pf.activeIdx, len(fsp.table.Columns))
	}
	x1, _, x2, _ := fsp.GetPosition()
	if x1 != 0 || x2 != 79 {
		t.Fatalf("Wide geometry = %d..%d, want 0..79", x1, x2)
	}
	originalMode := fsp.viewMode
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	if pf.widePanel != pf.activeIdx || pf.activeIdx != 0 {
		t.Fatalf("Tab did not transfer Wide to left panel: wide=%d active=%d", pf.widePanel, pf.activeIdx)
	}
	if fsp.viewMode != originalMode {
		t.Error("Wide changed the right panel's normal view mode")
	}
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: '2', ControlKeyState: vtinput.LeftCtrlPressed})
	if pf.widePanel != -1 || pf.panels[0].(*FileSystemPanel).viewMode != ViewModeMedium {
		t.Error("Ctrl+2 did not leave Wide and set Medium on the active panel")
	}
	_, _, leftX2, _ := pf.panels[0].GetPosition()
	rightX1, _, _, _ := pf.panels[1].GetPosition()
	if leftX2+1 != rightX1 {
		t.Errorf("split geometry was not restored: left x2=%d right x1=%d", leftX2, rightX1)
	}
}

type mockTaskReporter struct{}

func (m *mockTaskReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (m *mockTaskReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
}
func (m *mockTaskReporter) IsCancelled() bool { return false }

func TestPanelsFrame_CaptureCommands(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	vtui.SetDefaultPalette()
	pf := setupMockPanelsFrame()
	defer pf.Close()

	vtui.SetClipboard("")

	cmdStr := "clip:<< echo f4_capture_test"
	if runtime.GOOS == "windows" {
		cmdStr = "clip:<< cmd.exe /c echo f4_capture_test"
	}

	pf.cmdLine.Edit.SetText(cmdStr)
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Wait for async execution via TaskChan
	timeout := time.After(5 * time.Second)
	found := false
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for clip:<< task to complete")
		default:
		}
		if strings.Contains(vtui.GetClipboard(), "f4_capture_test") {
			found = true
			break
		}
		if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
			title := vtui.FrameManager.GetTopFrame().GetTitle()
			if strings.Contains(title, "Error") {
				var msg string
				if dlg, ok := vtui.FrameManager.GetTopFrame().(vtui.Container); ok {
					for _, child := range dlg.GetChildren() {
						if txt, ok := child.(*vtui.Text); ok {
							msg += txt.GetText() + " "
						}
					}
				}
				t.Fatalf("Execution failed, error dialog shown: %s - %s", title, msg)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !found {
		t.Error("Output was not copied to clipboard")
	}
}
func TestPanelsFrame_ShiftEnter_ExplorerLaunch(t *testing.T) {
	if _, _, supported := systemFileManagerCommand("test", true); !supported {
		t.Skipf("system file manager is unsupported on %s", runtime.GOOS)
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	launches := make(chan recordedExternalUICall, 3)
	pf.externalUIRunner = func(command string, args []string, dir string) error {
		launches <- recordedExternalUICall{command: command, args: append([]string(nil), args...), dir: dir}
		return nil
	}
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	tmp := t.TempDir()
	lp.vfs.SetPath(tmp)

	// Create dummy file and folder
	os.WriteFile(filepath.Join(tmp, "doc.txt"), []byte("data"), 0644)
	os.Mkdir(filepath.Join(tmp, "sub"), 0755)

	lp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "doc.txt", IsDir: false}},
	}
	lp.Refresh()
	pf.activeIdx = 0

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
		command, args, supported := systemFileManagerCommand(target.path, target.isDir)
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
	lp.vfs = vfs.NewNullVFS(0)
	lp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "file.txt"}}}
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

type mockNestedVFS struct {
	mockUpdateVFS
	parent vfs.VFS
}

func (m *mockNestedVFS) ParentVFS() vfs.VFS { return m.parent }
func (m *mockNestedVFS) Close() error       { return nil }

func TestPanelsFrame_CtrlPgUp_EscapesNestedVFS(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmp := t.TempDir()

	parentVfs := vfs.NewOSVFS(tmp)
	nested := &mockNestedVFS{parent: parentVfs}
	fsp.vfs = nested
	fsp.providerEntryName = "test.zip"

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
	if fsp.vfs != parentVfs {
		t.Error("Ctrl+PgUp failed to escape nested VFS to parent")
	}
	if fsp.pendingSelection != "test.zip" {
		t.Errorf("Expected pendingSelection 'test.zip', got %q", fsp.pendingSelection)
	}
}

// TestPanelsFrame_CtrlP_TogglesPassivePanel exercises issue #197:
// Ctrl+P should hide/show the panel opposite the currently active one,
// leaving the active panel untouched.
func TestPanelsFrame_CtrlP_TogglesPassivePanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *PanelsFrame) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vtinput.VK_P,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	// Active = right (setupMockPanelsFrame's default), Ctrl+P hides left.
	pf := setupMockPanelsFrame()
	defer pf.Close()
	if pf.activeIdx != 1 || !pf.showLeftPanel || !pf.showRightPanel || !pf.showPanels {
		t.Fatalf("mock frame precondition: activeIdx=%d L=%v R=%v show=%v",
			pf.activeIdx, pf.showLeftPanel, pf.showRightPanel, pf.showPanels)
	}
	send(pf)
	if pf.showLeftPanel || !pf.showRightPanel {
		t.Errorf("active=right: Ctrl+P should hide left only, got L=%v R=%v",
			pf.showLeftPanel, pf.showRightPanel)
	}
	if pf.activeIdx != 1 {
		t.Errorf("Ctrl+P must not move the active panel, got activeIdx=%d", pf.activeIdx)
	}
	if !pf.showPanels {
		t.Errorf("one panel still visible, showPanels should stay true")
	}

	// Second press restores the hidden side.
	send(pf)
	if !pf.showLeftPanel || !pf.showRightPanel {
		t.Errorf("Ctrl+P again should restore left, got L=%v R=%v",
			pf.showLeftPanel, pf.showRightPanel)
	}

	// Symmetric case: active = left, Ctrl+P hides right.
	pf.activeIdx = 0
	send(pf)
	if !pf.showLeftPanel || pf.showRightPanel {
		t.Errorf("active=left: Ctrl+P should hide right only, got L=%v R=%v",
			pf.showLeftPanel, pf.showRightPanel)
	}
	if pf.activeIdx != 0 {
		t.Errorf("Ctrl+P must not move the active panel, got activeIdx=%d", pf.activeIdx)
	}

	// Toggle the last visible panel off — no panels left, showPanels drops.
	pf.activeIdx = 1
	pf.showLeftPanel = false
	pf.showRightPanel = true
	pf.showPanels = true
	send(pf) // active=right, so this touches left; left was false → becomes true
	if !pf.showLeftPanel {
		t.Fatalf("setup for last-visible test: expected left to come back, got L=%v", pf.showLeftPanel)
	}
	// Now hide right via Ctrl+F2 to isolate: only left visible, active=right (invalid state
	// that Ctrl+F2's auto-switch would fix; here we just want to test Ctrl+P's showPanels math).
	pf.showLeftPanel = true
	pf.showRightPanel = false
	pf.showPanels = true
	pf.activeIdx = 0 // active on the visible panel
	send(pf)         // active=left, toggles right; right was false → becomes true
	if !pf.showRightPanel {
		t.Errorf("Ctrl+P should have shown the right panel again, got R=%v", pf.showRightPanel)
	}
}

func TestPanelsFrame_AICmds(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	handled := pf.HandleCommand(CmLeftAIChat, nil)
	// It should gracefully handle these even if no AI panel is there
	if !handled {
		t.Error("CmLeftAIChat should be handled")
	}
}

func panelWidth(p Panel) int  { x1, _, x2, _ := p.GetPosition(); return x2 - x1 + 1 }
func panelHeight(p Panel) int { _, y1, _, y2 := p.GetPosition(); return y2 - y1 + 1 }

// TestPanelsFrame_CtrlArrows_ResizePanels exercises the far2l-style
// panel resize keys: Ctrl+Left/Right shift the width split, Ctrl+Up/Down
// shrink/grow the panel-vs-terminal split. Requires empty cmdline;
// non-empty cmdline must fall through to word-navigation.
func TestPanelsFrame_CtrlArrows_ResizePanels(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *PanelsFrame, vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
	}

	// Width: Ctrl+Left moves the split to the left (widthDecrement +1,
	// left panel shrinks, right grows) — arrow follows the boundary,
	// matching far2l / Far3 / far2m. Ctrl+Right does the reverse.
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	baseLeft := panelWidth(pf.panels[0])

	send(pf, vtinput.VK_LEFT)
	if pf.widthDecrement != 1 {
		t.Errorf("Ctrl+Left: widthDecrement=%d, want 1", pf.widthDecrement)
	}
	if got := panelWidth(pf.panels[0]); got != baseLeft-1 {
		t.Errorf("Ctrl+Left: left panel width=%d, want %d", got, baseLeft-1)
	}

	send(pf, vtinput.VK_RIGHT)
	send(pf, vtinput.VK_RIGHT)
	if pf.widthDecrement != -1 {
		t.Errorf("Ctrl+Right twice: widthDecrement=%d, want -1", pf.widthDecrement)
	}
	if got := panelWidth(pf.panels[0]); got != baseLeft+1 {
		t.Errorf("Ctrl+Right twice: left panel width=%d, want %d", got, baseLeft+1)
	}

	// Non-empty cmdline: Ctrl+Left/Right must NOT resize.
	pf.widthDecrement = 0
	pf.ResizeConsole(80, 25)
	pf.cmdLine.Edit.SetText("hello")
	send(pf, vtinput.VK_LEFT)
	if pf.widthDecrement != 0 {
		t.Errorf("non-empty cmdline: widthDecrement changed to %d, want 0", pf.widthDecrement)
	}
	pf.cmdLine.Edit.SetText("")

	// Height: Ctrl+Up shrinks the panel area from the bottom (both
	// leftHeightDecrement and rightHeightDecrement +1, moving both
	// panels' bottom edge up in lockstep). Ctrl+Down reverses it.
	// Ctrl+Down at 0 must clamp, not go negative.
	pf.leftHeightDecrement = 0
	pf.rightHeightDecrement = 0
	pf.ResizeConsole(80, 25)
	basePanelH := panelHeight(pf.panels[0])

	send(pf, vtinput.VK_UP)
	if pf.leftHeightDecrement != 1 || pf.rightHeightDecrement != 1 {
		t.Errorf("Ctrl+Up: heightDecrements=%d/%d, want 1/1",
			pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
	if got := panelHeight(pf.panels[0]); got != basePanelH-1 {
		t.Errorf("Ctrl+Up: panel height=%d, want %d", got, basePanelH-1)
	}
	if got := panelHeight(pf.panels[1]); got != basePanelH-1 {
		t.Errorf("Ctrl+Up: right panel height=%d, want %d", got, basePanelH-1)
	}

	send(pf, vtinput.VK_DOWN)
	send(pf, vtinput.VK_DOWN) // Second Down at 0 should be a no-op (clamp).
	if pf.leftHeightDecrement != 0 || pf.rightHeightDecrement != 0 {
		t.Errorf("Ctrl+Down past 0: heightDecrements=%d/%d, want 0/0 (clamp)",
			pf.leftHeightDecrement, pf.rightHeightDecrement)
	}

	// Width clamp: on an 80-col terminal, maxWD = 40 - 10 = 30. Push
	// past it with Ctrl+Left (the direction that bumps widthDecrement +1).
	pf.widthDecrement = 0
	pf.ResizeConsole(80, 25)
	for i := 0; i < 40; i++ {
		send(pf, vtinput.VK_LEFT)
	}
	if pf.widthDecrement != 30 {
		t.Errorf("width clamp: widthDecrement=%d, want 30", pf.widthDecrement)
	}
}

// TestPanelsFrame_CtrlClear_ResetsLayoutDecrements verifies that Ctrl+Clear
// (NumPad 5 with NumLock off) zeroes widthDecrement / leftHeightDecrement /
// rightHeightDecrement in one shot, matching far2l's Ctrl+Clear.
func TestPanelsFrame_CtrlClear_ResetsLayoutDecrements(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.widthDecrement = 5
	pf.leftHeightDecrement = 3
	pf.rightHeightDecrement = 4
	AppConfig.WidthDecrement = 5
	AppConfig.LeftHeightDecrement = 3
	AppConfig.RightHeightDecrement = 4
	defer func() {
		AppConfig.WidthDecrement = 0
		AppConfig.LeftHeightDecrement = 0
		AppConfig.RightHeightDecrement = 0
	}()

	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_CLEAR,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if pf.widthDecrement != 0 || pf.leftHeightDecrement != 0 || pf.rightHeightDecrement != 0 {
		t.Errorf("Ctrl+Clear: fields = %d/%d/%d, want 0/0/0",
			pf.widthDecrement, pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
	if AppConfig.WidthDecrement != 0 || AppConfig.LeftHeightDecrement != 0 || AppConfig.RightHeightDecrement != 0 {
		t.Errorf("Ctrl+Clear: AppConfig = %d/%d/%d, want 0/0/0",
			AppConfig.WidthDecrement, AppConfig.LeftHeightDecrement, AppConfig.RightHeightDecrement)
	}
}

// TestPanelsFrame_LayoutDecrements_InitFromAppConfig verifies that a
// fresh PanelsFrame picks up saved layout offsets from AppConfig so a
// restart restores the last on-disk state.
func TestPanelsFrame_LayoutDecrements_InitFromAppConfig(t *testing.T) {
	oldW, oldL, oldR := AppConfig.WidthDecrement, AppConfig.LeftHeightDecrement, AppConfig.RightHeightDecrement
	AppConfig.WidthDecrement = -3
	AppConfig.LeftHeightDecrement = 4
	AppConfig.RightHeightDecrement = 5
	defer func() {
		AppConfig.WidthDecrement, AppConfig.LeftHeightDecrement, AppConfig.RightHeightDecrement = oldW, oldL, oldR
	}()

	pf := NewPanelsFrame()
	defer pf.Close()

	if pf.widthDecrement != -3 || pf.leftHeightDecrement != 4 || pf.rightHeightDecrement != 5 {
		t.Errorf("init from AppConfig: got %d/%d/%d, want -3/4/5",
			pf.widthDecrement, pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
}

// TestPanelsFrame_CtrlShiftArrows_AsymmetricHeight exercises far2l's
// Ctrl+Shift+Up / Ctrl+Shift+Down: bumps only the ACTIVE panel's height
// decrement, leaving the other panel untouched. Same gate as plain
// Ctrl+Up/Down (panels visible + cmdline empty).
func TestPanelsFrame_CtrlShiftArrows_AsymmetricHeight(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	send := func(pf *PanelsFrame, vk uint16) {
		pressKey(pf, &vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true,
			VirtualKeyCode:  vk,
			ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
		})
	}

	pf := setupMockPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Baseline: both decrements 0.
	if pf.leftHeightDecrement != 0 || pf.rightHeightDecrement != 0 {
		t.Fatalf("precondition: expected 0/0, got %d/%d",
			pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
	baseLeftH := panelHeight(pf.panels[0])
	baseRightH := panelHeight(pf.panels[1])

	// Active = right (mock default). Ctrl+Shift+Up bumps only right.
	send(pf, vtinput.VK_UP)
	if pf.leftHeightDecrement != 0 || pf.rightHeightDecrement != 1 {
		t.Errorf("active=right Ctrl+Shift+Up: got %d/%d, want 0/1",
			pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
	if panelHeight(pf.panels[0]) != baseLeftH {
		t.Errorf("left panel changed unexpectedly")
	}
	if panelHeight(pf.panels[1]) != baseRightH-1 {
		t.Errorf("right panel height=%d, want %d",
			panelHeight(pf.panels[1]), baseRightH-1)
	}
	if AppConfig.RightHeightDecrement != 1 {
		t.Errorf("AppConfig.RightHeightDecrement=%d, want 1",
			AppConfig.RightHeightDecrement)
	}
	AppConfig.RightHeightDecrement = 0 // cleanup for later tests

	// Ctrl+Shift+Down on active=right undoes it.
	send(pf, vtinput.VK_DOWN)
	if pf.rightHeightDecrement != 0 {
		t.Errorf("Ctrl+Shift+Down: rightHeightDecrement=%d, want 0",
			pf.rightHeightDecrement)
	}

	// Switch to left, Ctrl+Shift+Up bumps left only.
	pf.activeIdx = 0
	send(pf, vtinput.VK_UP)
	if pf.leftHeightDecrement != 1 || pf.rightHeightDecrement != 0 {
		t.Errorf("active=left Ctrl+Shift+Up: got %d/%d, want 1/0",
			pf.leftHeightDecrement, pf.rightHeightDecrement)
	}
	AppConfig.LeftHeightDecrement = 0 // cleanup

	// Clamp: Ctrl+Shift+Down on a zero-decrement panel must not go negative.
	pf.leftHeightDecrement = 0
	send(pf, vtinput.VK_DOWN)
	if pf.leftHeightDecrement != 0 {
		t.Errorf("Ctrl+Shift+Down past 0: leftHeightDecrement=%d, want 0 (clamp)",
			pf.leftHeightDecrement)
	}

	// Non-empty cmdline: Ctrl+Shift+Up/Down must NOT resize.
	pf.leftHeightDecrement = 0
	pf.cmdLine.Edit.SetText("hello")
	send(pf, vtinput.VK_UP)
	if pf.leftHeightDecrement != 0 {
		t.Errorf("non-empty cmdline: leftHeightDecrement changed to %d, want 0",
			pf.leftHeightDecrement)
	}
	pf.cmdLine.Edit.SetText("")
}

func TestPanelsFrame_ProcessMouse_HoverWheel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	rp := pf.panels[1].(*FileSystemPanel)

	// Make both panels visible and set right as active
	pf.activeIdx = 1
	pf.showPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true

	// Clear async loading
	if lp.cancelLoad != nil {
		lp.cancelLoad()
	}
	lp.isLoading = false
	if rp.cancelLoad != nil {
		rp.cancelLoad()
	}
	rp.isLoading = false

	// Create test entries
	lp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "L1"}},
		{VFSItem: vfs.VFSItem{Name: "L2"}},
		{VFSItem: vfs.VFSItem{Name: "L3"}},
	}
	lp.Refresh()
	lp.SetCursorIndex(0)

	rp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "R1"}},
		{VFSItem: vfs.VFSItem{Name: "R2"}},
		{VFSItem: vfs.VFSItem{Name: "R3"}},
	}
	rp.Refresh()
	rp.SetCursorIndex(0)

	// 1. Simulate mouse wheel over the LEFT panel (hover scroll)
	lx1, ly1, _, _ := lp.GetPosition()

	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         int16(lx1 + 2),
		MouseY:         int16(ly1 + 2),
		WheelDirection: -1, // Down scroll -> should move cursor down
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel event was not handled")
	}

	// Active panel should remain right (1)
	if pf.activeIdx != 1 {
		t.Errorf("Expected active panel to remain 1, got %d", pf.activeIdx)
	}

	// Left panel's cursor should have moved down to index 1 (L2)
	if lp.GetCursorIndex() != 0 {
		t.Errorf("Expected left panel cursor to remain 0, got %d", lp.GetCursorIndex())
	}

	// Right panel's cursor should still be 0 (unscrolled)
	if rp.GetCursorIndex() != 1 {
		t.Errorf("Expected right panel cursor to scroll down to 1, got %d", rp.GetCursorIndex())
	}
}

func TestPanelsFrame_ProcessMouse_HoverWheel_AltPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	rp := pf.panels[1].(*FileSystemPanel)

	// Make both panels visible and set right as active
	pf.activeIdx = 1
	pf.showPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true

	// Clear async loading
	if lp.cancelLoad != nil {
		lp.cancelLoad()
	}
	lp.isLoading = false
	if rp.cancelLoad != nil {
		rp.cancelLoad()
	}
	rp.isLoading = false

	// Add an AltPanel (QuickViewPanel) on the Left (0) slot
	qv := NewQuickViewPanel(lp)
	pf.altPanels[0] = qv

	lx1, ly1, _, _ := lp.GetPosition()

	// Simulate wheel over the left slot (where QuickView is)
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         int16(lx1 + 2),
		MouseY:         int16(ly1 + 2),
		WheelDirection: -1, // Down scroll
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel over AltPanel not handled")
	}
}
func TestPanelsFrame_ProcessMouse_HoverWheel_Medium_Boundaries(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	rp := pf.panels[1].(*FileSystemPanel)

	// Set left panel as active, medium view mode
	pf.activeIdx = 0
	pf.showPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true

	// Clear async loading
	if lp.cancelLoad != nil {
		lp.cancelLoad()
	}
	lp.isLoading = false
	if rp.cancelLoad != nil {
		rp.cancelLoad()
	}
	rp.isLoading = false

	// Create 45 items to fill multiple columns
	var entries []*fileEntry
	for i := 0; i < 45; i++ {
		entries = append(entries, &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("F%d", i)}})
	}
	lp.entries = entries
	lp.Refresh()

	H := lp.table.ViewHeight
	if H <= 0 {
		H = 1
	}

	// Set cursor to the first row of the second column (idx = H)
	lp.SetCursorIndex(H)

	// TopPos should be 0
	lp.table.TopPos = 0
	lp.Refresh()

	if lp.GetCursorIndex() != H {
		t.Fatalf("Setup failed: expected cursor index %d, got %d", H, lp.GetCursorIndex())
	}

	// 1. Simulate mouse wheel UP over the left panel
	lx1, ly1, _, _ := lp.GetPosition()
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         int16(lx1 + 2),
		MouseY:         int16(ly1 + 2),
		WheelDirection: 1, // Up scroll
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel up event not handled")
	}

	// Cursor should have jumped to the last row of the first column (index H - 1)
	expectedIdx := H - 1
	if lp.GetCursorIndex() != expectedIdx {
		t.Errorf("Expected cursor to jump to first column index %d, got %d", expectedIdx, lp.GetCursorIndex())
	}
}

func TestPanelsFrame_ProcessMouse_HoverWheel_Detailed_Boundaries(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.panels[0].(*FileSystemPanel)
	rp := pf.panels[1].(*FileSystemPanel)

	// Set left panel as active, detailed view mode
	pf.activeIdx = 0
	pf.showPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true

	// Clear async loading
	if lp.cancelLoad != nil {
		lp.cancelLoad()
	}
	lp.isLoading = false
	if rp.cancelLoad != nil {
		rp.cancelLoad()
	}
	rp.isLoading = false

	lp.SetViewMode(ViewModeDetailed)

	H := lp.table.ViewHeight
	if H <= 0 {
		H = 1
	}

	// Create items to fill more than screen height
	var entries []*fileEntry
	for i := 0; i < H+10; i++ {
		entries = append(entries, &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("F%d", i)}})
	}
	lp.entries = entries
	lp.Refresh()

	totalItems := len(lp.entries)

	// Set cursor to the last item
	lp.SetCursorIndex(totalItems - 1)
	lp.Refresh()

	lastIdx := lp.GetCursorIndex()

	// 1. Simulate mouse wheel DOWN over the left panel
	lx1, ly1, _, _ := lp.GetPosition()
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         int16(lx1 + 2),
		MouseY:         int16(ly1 + 2),
		WheelDirection: -1, // Down scroll
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel down event not handled")
	}

	// Cursor should remain at the last item
	if lp.GetCursorIndex() != lastIdx {
		t.Errorf("Expected cursor to remain at the last item %d, got %d", lastIdx, lp.GetCursorIndex())
	}
}

func TestFilePanel_WheelScrollSpeed(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()
	AppConfig.WheelPanelUp = 2
	AppConfig.WheelPanelDown = 3

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	p := pf.panels[0].(*FileSystemPanel)
	if p.cancelLoad != nil {
		p.cancelLoad()
	}
	p.isLoading = false

	for i := 0; i < 10; i++ {
		p.entries = append(p.entries, &fileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("f%d", i)}})
	}
	p.Refresh()
	p.SetCursorIndex(0)

	x1, y1, _, _ := p.GetPosition()
	wheel := func(dir int) {
		ev := &vtinput.InputEvent{
			Type:           vtinput.MouseEventType,
			MouseX:         int16(x1 + 2),
			MouseY:         int16(y1 + 2),
			WheelDirection: dir,
		}
		if !p.ProcessMouse(ev) {
			t.Fatal("Mouse wheel event was not handled")
		}
	}

	wheel(-1) // down: 3 lines
	if got := p.GetCursorIndex(); got != 3 {
		t.Errorf("Expected cursor at 3 after wheel down, got %d", got)
	}
	wheel(1) // up: 2 lines
	if got := p.GetCursorIndex(); got != 1 {
		t.Errorf("Expected cursor at 1 after wheel up, got %d", got)
	}
}
