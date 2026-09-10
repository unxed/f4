package panel

import (
	"context"
	"fmt"
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/plugins/archive"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"github.com/unxed/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type opaqueVisualPathVFS struct {
	*mockTitleVFS
	path string
}

func (v *opaqueVisualPathVFS) GetPath() string { return v.path }

type mouseCaptureTestPanel struct {
	vtui.ScreenObject
	events []vtinput.InputEvent
}

func TestPanelsFrame_WorkspaceTabTitleUsesFolderNames(t *testing.T) {
	root := t.TempDir()
	leftPath := filepath.Join(root, "left-leaf")
	rightPath := filepath.Join(root, "right-leaf")
	if err := os.MkdirAll(leftPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0700); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{ShowPanels: true}
	pf.Panels[0] = &FileSystemPanel{Vfs: vfs.NewOSVFS(leftPath)}
	pf.Panels[1] = &FileSystemPanel{Vfs: vfs.NewOSVFS(rightPath)}
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
	if err := os.MkdirAll(leftPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0700); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{ShowPanels: true}
	pf.Panels[0] = &FileSystemPanel{Vfs: vfs.NewOSVFS(leftPath)}
	pf.Panels[1] = &FileSystemPanel{Vfs: vfs.NewOSVFS(rightPath)}
	info := pf.GetWorkspaceMenuInfo()
	if info.Icon != "P" || info.Primary != leftPath || info.Secondary != rightPath {
		t.Fatalf("workspace menu info = %#v, want full left/right paths", info)
	}

	pf.ShowPanels = false
	pf.Executing = true
	pf.workspaceCommandTitle = "Python"
	info = pf.GetWorkspaceMenuInfo()
	if info.Icon != "T" || info.Primary != "Python" || info.Secondary != "" {
		t.Fatalf("terminal workspace menu info = %#v", info)
	}
}

func TestPanelsFrame_WorkspaceTabTitleTracksTerminalTitle(t *testing.T) {
	pf := &PanelsFrame{
		ShowPanels:            false,
		Executing:             true,
		workspaceCommandTitle: workspaceCommandName("python script.py"),
		TermView:              &terminal.TerminalView{Title: "Administrator: C:\\Windows\\System32\\cmd.exe - Python"},
	}
	if got := pf.GetWorkspaceTabTitle(); got != "Python" {
		t.Fatalf("terminal workspace tab title = %q, want %q", got, "Python")
	}
	if marker := pf.GetWorkspaceTabMarker(); marker != "T" {
		t.Fatalf("terminal workspace marker = %q, want T", marker)
	}
	pf.Executing = false
	pf.workspaceCommandTitle = ""
	if got := pf.GetWorkspaceTabTitle(); got != "Terminal" {
		t.Fatalf("manually revealed terminal tab title = %q, want %q", got, "Terminal")
	}
	pf.ShowPanels = true
	pf.Panels[0] = &FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())}
	pf.Panels[1] = &FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())}
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
	oldConfig := config.App
	defer func() { config.App = oldConfig }()
	config.App.NavigationMode = config.NavigationClassic
	config.App.AlwaysShowMenuBar = false

	left := &mouseCaptureTestPanel{}
	right := &mouseCaptureTestPanel{}
	left.SetPosition(0, 0, 39, 20)
	right.SetPosition(40, 0, 79, 20)
	pf := &PanelsFrame{
		Panels:         [2]Panel{left, right},
		ShowPanels:     true,
		ShowLeftPanel:  true,
		ShowRightPanel: true,
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
	if pf.PanelMouseCapture != nil {
		t.Fatal("panel mouse capture was not released")
	}

	// A new gesture after release is free to start in the right panel.
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 79, MouseY: 5, ButtonState: vtinput.FromLeft1stButtonPressed,
	})
	if len(right.events) != 1 || pf.PanelMouseCapture != right {
		t.Fatalf("new right-panel gesture was not captured: events=%d capture=%T", len(right.events), pf.PanelMouseCapture)
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
	oldConfig := config.App
	defer func() { config.App = oldConfig }()
	config.App.NavigationMode = config.NavigationClassic
	config.App.AlwaysShowMenuBar = false

	left := &mouseCaptureTestPanel{}
	right := &mouseCaptureTestPanel{}
	left.SetPosition(0, 0, 39, 20)
	right.SetPosition(40, 0, 79, 20)
	pf := &PanelsFrame{
		Panels:          [2]Panel{left, right},
		ShowPanels:      true,
		ShowLeftPanel:   true,
		ShowRightPanel:  true,
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
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Simulate 80x25 terminal
	pf.ResizeConsole(80, 25)

	// Calculate expected positions for 80x25 with KeyBar
	expectedKeyBarY := 24
	expectedCmdLineY := 23 // Always 1 line above KeyBar if KeyBar is present

	// 1. Check reserved rows with KeyBar visible
	if pf.KeyBar.Y1 != expectedKeyBarY {
		t.Errorf("KeyBar position error: expected %d, got %d", expectedKeyBarY, pf.KeyBar.Y1)
	}
	if pf.CmdLine.Y1 != expectedCmdLineY {
		t.Errorf("CommandLine position error: expected %d, got %d", expectedCmdLineY, pf.CmdLine.Y1)
	}

	// 2. Check layout after hiding KeyBar
	pf.ShowKeyBar = false
	pf.ResizeConsole(80, 25)

	// After hiding KeyBar, CommandLine should move to the bottom row
	expectedCmdLineY = 24
	if pf.KeyBar.Y1 != expectedKeyBarY {
		t.Errorf("KeyBar should remain at %d when hidden, got %d", expectedKeyBarY, pf.KeyBar.Y1)
	}
	if pf.CmdLine.Y1 != expectedCmdLineY {
		t.Errorf("CommandLine should be at %d when KeyBar hidden, got %d", expectedCmdLineY, pf.CmdLine.Y1)
	}
	if pf.KeyBar.IsVisible() {
		t.Error("KeyBar should be invisible")
	}
}
func TestPanelsFrame_DriveMenuListsAssignedBookmarks(t *testing.T) {
	cfg := t.TempDir()
	// os.UserConfigDir ignores XDG_CONFIG_HOME on darwin; go through the seam.
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "f4", "settings", "bookmarks.ini"),
		[]byte("[6]\nPath="+target+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	vtui.FrameManager.Push(pf)

	pf.ShowDriveMenu(1)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
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
	fsp := pf.Panels[1].(*FileSystemPanel)
	menu.SetSelectPos(0)
	menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_6, Char: '6',
	})
	if got := fsp.Vfs.GetPath(); got != target {
		t.Errorf("panel at %q, want %q", got, target)
	}
}

func TestPanelsFrame_DriveMenuExpandsBookmarkPath(t *testing.T) {
	cfg := t.TempDir()
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, dir := range []string{
		filepath.Join(home, "x"),
		filepath.Join(home, "sub"),
		filepath.Join(home, "literal"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"dollar variable", filepath.Join("$HOME", "x"), filepath.Join(home, "x")},
		{"percent variable", filepath.Join("%USERPROFILE%", "x"), filepath.Join(home, "x")},
		{"tilde", "~", home},
		{"tilde subdirectory", filepath.Join("~", "sub"), filepath.Join(home, "sub")},
		{"literal", filepath.Join(home, "literal"), filepath.Join(home, "literal")},
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	vtui.FrameManager.Push(pf)
	fsp := pf.Panels[1].(*FileSystemPanel)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(BookmarksFilePath(),
				[]byte("[6]\nPath="+tc.path+"\nPlugin=\nPluginData=\nPluginFile=\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := fsp.Vfs.SetPath(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			pf.ShowDriveMenu(1)
			menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
			if !ok {
				t.Fatalf("drive menu not shown, top frame is %T", vtui.FrameManager.GetTopFrame())
			}
			menu.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_6, Char: '6',
			})

			if got := fsp.Vfs.GetPath(); got != tc.want {
				t.Errorf("bookmark %q navigated to %q, want %q", tc.path, got, tc.want)
			}
			waitForLoad(t, fsp)
			vtui.FrameManager.RemoveFrame(menu)
		})
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
		if m, ok := driveMenuFromFrame(frames[i]); ok && m.GetTitle() == i18n.Msg("Drive.Title") {
			return m
		}
	}
	t.Fatalf("drive menu not on the frame stack: %#v", frames)
	return nil
}

func driveMenuFromFrame(frame vtui.Frame) (*vtui.VMenu, bool) {
	switch f := frame.(type) {
	case *vtui.VMenu:
		return f, true
	case *DriveMenuFrame:
		return f.VMenu, true
	default:
		return nil, false
	}
}

// wantDriveMenuRow re-derives from the rendered menu the row the cursor is
// supposed to open on for a panel sitting at cur: the drive entry that owns
// cur when the menu lists one (Windows drive letters), otherwise the "Other
// panel" entry at row 0. Deliberately independent of driveMenuDefaultPos, so
// the assertions still test something, and independent of the platform, so
// the runner's drive layout cannot flip them -- GitHub's Windows images check
// the tree out on D:, which is what made this a hard-coded 0 no longer true.
func wantDriveMenuRow(menu *vtui.VMenu, cur string) int {
	vol := strings.ToUpper(filepath.VolumeName(cur))
	if vol == "" {
		return 0
	}
	for i, it := range menu.Items {
		text := strings.ToUpper(strings.ReplaceAll(it.Text, "&", ""))
		if strings.HasPrefix(text, vol) {
			return i
		}
	}
	return 0
}

func TestPanelsFrame_DriveMenuBookmarkKeys(t *testing.T) {
	cfg := t.TempDir()
	// os.UserConfigDir ignores XDG_CONFIG_HOME on darwin; go through the seam.
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	if err := os.MkdirAll(filepath.Join(cfg, "f4", "settings"), 0o700); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(cfg, "f4", "settings", "drive-bookmarks.ini")
	target := t.TempDir()
	if err := SaveDriveBookmarks(ini, []DriveBookmark{{Name: "Favorite folder", Path: target, Hotkey: "Ф"}}); err != nil {
		t.Fatal(err)
	}

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

	// The drive menu shows the named link, not its path, after the Bookmarks
	// section caption. The Cyrillic hotkey is an actual menu accelerator.
	pf.ShowDriveMenu(1)
	menu := findDriveMenu(t)
	row := -1
	for i, item := range menu.Items {
		if strings.Contains(item.Text, "Favorite folder") {
			row = i
			if strings.Contains(item.Text, target) {
				t.Fatalf("named bookmark leaked its path into menu text: %q", item.Text)
			}
		}
	}
	if row < 0 || row == 0 || menu.Items[row-1].Text != i18n.Msg("Drive.Links") {
		t.Fatalf("named bookmark section is malformed: row=%d items=%#v", row, menu.Items)
	}
	fsp := pf.Panels[1].(*FileSystemPanel)
	menu.SetSelectPos(0)
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'ф'})
	if got := fsp.Vfs.GetPath(); got != target {
		t.Errorf("Cyrillic bookmark opened %q, want %q", got, target)
	}
	settleFrames(t)

	// F4 on the named link opens the editor with its fields pre-filled.
	pf.ShowDriveMenu(1)
	menu = findDriveMenu(t)
	menu.SetSelectPos(row)
	press(menu, vtinput.VK_F4)
	settleFrames(t)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*driveBookmarkEditDialog)
	if !ok {
		t.Fatalf("F4 did not open drive bookmark editor: %T", vtui.FrameManager.GetTopFrame())
	}
	if dlg.nameEdit.GetText() != "Favorite folder" || dlg.pathEdit.GetText() != target || dlg.HotkeyEdit.GetText() != "Ф" {
		t.Fatalf("editor fields = name %q path %q hotkey %q", dlg.nameEdit.GetText(), dlg.pathEdit.GetText(), dlg.HotkeyEdit.GetText())
	}
	dlg.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE})
	settleFrames(t)
	menu = findDriveMenu(t)

	// Del asks for confirmation and removes the named entry only after Yes.
	menu.SetSelectPos(row)
	press(menu, vtinput.VK_DELETE)
	settleFrames(t)
	confirmation, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok || confirmation.GetTitle() != i18n.Msg("DriveLink.DeleteTitle") {
		t.Fatalf("Del did not open delete confirmation: %T", vtui.FrameManager.GetTopFrame())
	}
	confirmation.OnResult(1)
	settleFrames(t)
	menu = findDriveMenu(t)
	menu.SetSelectPos(row)
	press(menu, vtinput.VK_DELETE)
	settleFrames(t)
	confirmation, ok = vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("second Del did not open confirmation: %T", vtui.FrameManager.GetTopFrame())
	}
	confirmation.OnResult(0)
	settleFrames(t)
	bookmarks, err := LoadDriveBookmarks(ini)
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 0 {
		t.Fatalf("deleted named bookmarks = %#v", bookmarks)
	}

	// Ins opens the named-link editor from any row and pre-fills the active
	// panel path when creating a new entry.
	menu.SetSelectPos(0)
	press(menu, vtinput.VK_INSERT)
	settleFrames(t)
	newDlg, ok := vtui.FrameManager.GetTopFrame().(*driveBookmarkEditDialog)
	if !ok {
		t.Fatalf("Ins did not open drive bookmark editor: %T", vtui.FrameManager.GetTopFrame())
	}
	if newDlg.pathEdit.GetText() == "" {
		t.Fatal("Ins did not pre-fill the panel path")
	}
	newDlg.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE})
	settleFrames(t)
}

func TestPanelsFrame_GetActivePTY(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	// Default panels use OSVFS, so active terminal.PTY should be the local one
	active := pf.GetActivePTY()
	if active != pf.Pty {
		t.Errorf("Expected active term.PTY to be the local term.PTY for OSVFS")
	}
}
func setupMockPanelsFrame(t *testing.T) *PanelsFrame {
	t.Helper()
	if vtui.FrameManager.TaskChan == nil {
		vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	}
	pf := &PanelsFrame{ActiveIdx: 1, ShowPanels: true, ShowKeyBar: true, ShowLeftPanel: true, ShowRightPanel: true}
	pf.Pty = &mockPty{}
	pf.TermView = terminal.NewTerminalView(80, 24)
	// Initialize MenuBar with enough items to satisfy updateMenuCheckmarks (needs index 0 and 4)
	pf.MenuBar = vtui.NewMenuBar(nil)
	pf.MenuBar.Items = make([]vtui.MenuBarItem, 5)
	for i := 0; i < 5; i++ {
		pf.MenuBar.Items[i].SubItems = make([]vtui.MenuItem, 8)
	}
	pf.CmdLine = cmdline.NewCommandLine(">")
	pf.KeyBar = vtui.NewKeyBar()
	// Use OSVFS because tests create real files in t.TempDir()
	pf.Panels[0] = NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS("."))
	pf.Panels[1] = NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS("."))
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	pf.InitPTY()
	return pf
}

// TestPanelsFrame_EscTogglePanels_RespectsOption confirms the
// shortcut can be turned off — with EscTogglePanels=false, ESC
// on visible panels + empty cmdLine is a no-op instead of hiding.
func TestPanelsFrame_EscTogglePanels_RespectsOption(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	old := config.App.EscTogglePanels
	defer func() { config.App.EscTogglePanels = old }()
	config.App.EscTogglePanels = false

	pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})
	if !pf.ShowPanels {
		t.Error("with EscTogglePanels=false, ESC must not hide the panels")
	}
}

func TestPanelsFrame_SortCommandsUseDefaultDirection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	left := pf.Panels[0].(*FileSystemPanel)
	right := pf.Panels[1].(*FileSystemPanel)
	left.SortMode, left.SortReverse = SortUnsorted, true
	right.SortMode, right.SortReverse = SortUnsorted, true

	pf.HandleCommand(appcmd.CmLeftSortTime, nil)
	if left.SortMode != SortTime || left.SortIsAscending() {
		t.Fatalf("left sort command = mode %v ascending %v, want Time descending",
			left.SortMode, left.SortIsAscending())
	}

	pf.HandleCommand(appcmd.CmRightSortSize, nil)
	if right.SortMode != SortSize || right.SortIsAscending() {
		t.Fatalf("right sort command = mode %v ascending %v, want Size descending",
			right.SortMode, right.SortIsAscending())
	}
}

func TestPanelsFrame_RightClickPanelPathOpensDriveMenuForThatPanel(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ActiveIdx = 0
	left := pf.Panels[0].(*FileSystemPanel)
	right := pf.Panels[1].(*FileSystemPanel)
	leftPath := t.TempDir()
	rightPath := t.TempDir()
	if err := left.Vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := right.Vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}
	right.currentTitle = rightPath

	if !pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: testutil.Int16(right.X1 + 3), MouseY: testutil.Int16(right.Y1),
		ButtonState: vtinput.RightmostButtonPressed,
	}) {
		t.Fatal("right click on the panel path was not handled")
	}
	if pf.ActiveIdx != 0 {
		t.Fatalf("right-clicking the passive panel path changed active panel to %d", pf.ActiveIdx)
	}
	if pf.PanelMouseCapture != nil {
		t.Fatal("path context click incorrectly captured a panel drag")
	}
	menu := findDriveMenu(t)
	menu.OnAction(0) // "Other panel" must apply to the right panel.
	if got := right.Vfs.GetPath(); got != leftPath {
		t.Fatalf("drive menu changed path %q, want right panel to receive %q", got, leftPath)
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
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))

	// Use a real temp directory that exists on all platforms
	tmpDir := t.TempDir()

	// Set some specific state
	pf.ActiveIdx = 0
	if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
		if err := fsp.Vfs.SetPath(tmpDir); err != nil {
			t.Fatalf("SetPath failed: %v", err)
		}
		fsp.Table.SelectPos = 5
	}

	// Clone the panels
	clone := pf.Clone()
	defer clone.Close()

	// Verify state transfer
	if clone.ActiveIdx != 0 {
		t.Errorf("Clone failed to copy activeIdx: %d", clone.ActiveIdx)
	}

	if fsp, ok := clone.Panels[0].(*FileSystemPanel); ok {
		if fsp.Vfs.GetPath() != tmpDir {
			t.Errorf("Clone failed to copy VFS path: got %s, want %s", fsp.Vfs.GetPath(), tmpDir)
		}
		if fsp.Table.SelectPos != 5 {
			t.Errorf("Clone failed to copy Table SelectPos: %d", fsp.Table.SelectPos)
		}
		if fsp.ViewMode != pf.Panels[0].(*FileSystemPanel).ViewMode {
			t.Error("Clone failed to copy ViewMode")
		}
		if fsp.SortMode != pf.Panels[0].(*FileSystemPanel).SortMode {
			t.Error("Clone failed to copy SortMode")
		}
		if fsp.SortReverse != pf.Panels[0].(*FileSystemPanel).SortReverse {
			t.Error("Clone failed to copy SortReverse")
		}
	}

	// Verify they are independent instances
	clone.ActiveIdx = 1
	if pf.ActiveIdx == 1 {
		t.Error("Clone should be independent from its parent")
	}
	waitForLoad(t, clone.Panels[0].(*FileSystemPanel))
	waitForLoad(t, clone.Panels[1].(*FileSystemPanel))
}

func TestCtrlBracketsInsertPanelPathsIntoFocusedEdit(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	leftPath := filepath.Join(t.TempDir(), "left path")
	rightPath := filepath.Join(t.TempDir(), "right path")
	if err := os.MkdirAll(leftPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rightPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pf.Panels[0].(*FileSystemPanel).Vfs.SetPath(leftPath); err != nil {
		t.Fatal(err)
	}
	if err := pf.Panels[1].(*FileSystemPanel).Vfs.SetPath(rightPath); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Push(pf)

	dlg := vtui.NewCenteredDialog(50, 9, " Path ")
	edit := vtui.NewEdit(0, 0, 30, "prefix:")
	dlg.AddItem(edit)
	dlg.SetFocusedItem(edit)
	vtui.FrameManager.Push(dlg)

	if !HandlePanelPathEditHotkey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+[ was not handled for focused edit")
	}
	if got, want := edit.GetText(), leftPath; got != want {
		t.Fatalf("Ctrl+[ inserted %q, want %q", got, want)
	}

	edit.SelectAll()
	if !HandlePanelPathEditHotkey(&vtinput.InputEvent{
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
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)
	dlg := vtui.NewCenteredDialog(40, 7, " Confirm ")
	button := vtui.NewButton(0, 0, "OK")
	dlg.AddItem(button)
	dlg.SetFocusedItem(button)
	vtui.FrameManager.Push(dlg)

	if HandlePanelPathEditHotkey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_OEM_4,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("Ctrl+[ was consumed without a focused edit")
	}
}

func TestPanelsFrame_CtrlArrows_CommandLineNavigation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.ShowPanels = true
	pf.CmdLine.Edit.SetText("word1 word2 word3")

	// Перемещаем курсор в конец строки
	pf.CmdLine.ProcessKey(&vtinput.InputEvent{
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

	gotText := pf.CmdLine.Edit.GetText()
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

	gotText = pf.CmdLine.Edit.GetText()
	expectedRight := "word1 word2 Xword3Y"
	if gotText != expectedRight {
		t.Errorf("Ctrl+Right word navigation failed with panels enabled: expected %q, got %q", expectedRight, gotText)
	}
}
func TestPanelsFrame_AlwaysShowMenuBar(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	origAlways := config.App.AlwaysShowMenuBar
	defer func() { config.App.AlwaysShowMenuBar = origAlways }()

	// 1. Test when AlwaysShowMenuBar is false (default)
	config.App.AlwaysShowMenuBar = false

	pf.ShowPanels = true
	pf.ResizeConsole(80, 25)

	fspL := pf.Panels[0].(*FileSystemPanel)
	if fspL.Y1 != 0 {
		t.Errorf("Expected panels to start at row 0 by default, got %d", fspL.Y1)
	}
	_, menuY, _, _ := pf.MenuBar.GetPosition()
	if menuY >= 0 || pf.MenuBar.IsVisible() {
		t.Errorf("hidden menu bar occupies row %d (visible=%v), want off-screen and hidden", menuY, pf.MenuBar.IsVisible())
	}

	// 2. Test when AlwaysShowMenuBar is true (panels shifted down)
	config.App.AlwaysShowMenuBar = true
	pf.ResizeConsole(80, 25)

	if fspL.Y1 != 1 {
		t.Errorf("Expected panels to start at row 1 when AlwaysShowMenuBar is true, got %d", fspL.Y1)
	}
	_, menuY, _, _ = pf.MenuBar.GetPosition()
	if menuY != 0 || !pf.MenuBar.IsVisible() {
		t.Errorf("visible menu bar position/visibility = (%d, %v), want (0, true)", menuY, pf.MenuBar.IsVisible())
	}

	// 3. Test that hiding panels collapses the menu bar space for terminal
	pf.ShowPanels = false
	pf.ResizeConsole(80, 25)

	if pf.TermView.Y1 != 0 {
		t.Errorf("Expected terminal to start at row 0 when panels are hidden, got %d", pf.TermView.Y1)
	}
	_, menuY, _, _ = pf.MenuBar.GetPosition()
	if menuY >= 0 || pf.MenuBar.IsVisible() {
		t.Errorf("hidden terminal menu bar occupies row %d (visible=%v), want off-screen and hidden", menuY, pf.MenuBar.IsVisible())
	}
}

func TestPanelsFrame_ActiveMenuBarAppearsAfterWorkspaceInset(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	oldAlways := config.App.AlwaysShowMenuBar
	config.App.AlwaysShowMenuBar = false
	t.Cleanup(func() { config.App.AlwaysShowMenuBar = oldAlways })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.AddScreenBackground(vtui.NewDesktop())

	if inset := vtui.FrameManager.WorkspaceTopInset(); inset != 1 {
		t.Fatalf("workspace top inset = %d, want 1 with multiple workspaces", inset)
	}
	pf.MenuBar.Active = true
	pf.Show(vtui.FrameManager.Screen())

	_, menuY, _, _ := pf.MenuBar.GetPosition()
	if menuY != 1 || !pf.MenuBar.IsVisible() {
		t.Fatalf("active menu bar position/visibility = (%d, %v), want (1, true)", menuY, pf.MenuBar.IsVisible())
	}

	pf.MenuBar.Active = false
	pf.Show(vtui.FrameManager.Screen())
	_, menuY, _, _ = pf.MenuBar.GetPosition()
	if menuY >= 0 || pf.MenuBar.IsVisible() {
		t.Fatalf("inactive menu bar position/visibility = (%d, %v), want off-screen and hidden", menuY, pf.MenuBar.IsVisible())
	}
}

// TestPanelsFrame_HiddenTerminalFirstRowDoesNotOpenMenu covers issue #1093:
// a click on the first line of micro (or far2l started from f4) used to hit
// f4's stale menu-bar geometry and open the f4 menu over the terminal app.
func TestPanelsFrame_HiddenTerminalFirstRowDoesNotOpenMenu(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	oldAlways := config.App.AlwaysShowMenuBar
	config.App.AlwaysShowMenuBar = true
	t.Cleanup(func() { config.App.AlwaysShowMenuBar = oldAlways })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	pf.ShowPanels = false
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	vtui.FrameManager.InjectEvents([]*vtinput.InputEvent{{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 0,
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}})
	vtui.FrameManager.Step(0)

	if vtui.FrameManager.GetTopFrame() != pf {
		t.Fatalf("first-row terminal click opened %T, want PanelsFrame terminal view", vtui.FrameManager.GetTopFrame())
	}
	if pf.MenuBar.Active {
		t.Fatal("first-row terminal click activated the hidden f4 menu bar")
	}
}

func TestPanelsFrame_Clone_TerminalData(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	// 1. Simulate complex terminal output
	// Inject data directly into pt to simulate extruded history
	pf.TermView.Pt.Insert(0, []byte("L1\nL2\n"))
	pf.TermView.Li.UpdateAfterInsert(0, []byte("L1\nL2\n"))

	// Simulate active grid data
	pf.TermView.CursorY = 5
	pf.TermView.Lines[4][0].Char = 'H' // Previous row
	pf.TermView.Lines[5][0].Char = 'A' // Active row (will be wiped)
	pf.TermView.CursorX = 1

	clone := pf.Clone()
	defer clone.Close()

	// 2. Check if log is deep-copied
	if clone.TermView.Pt.String() != "L1\nL2\n" {
		t.Errorf("Terminal log not cloned. Got %q", clone.TermView.Pt.String())
	}

	// 3. CRITICAL: Check if LineIndex is correctly pointing to the NEW pt
	if clone.TermView.Li.LineCount() != 3 {
		t.Errorf("Terminal LineIndex not synced in clone. Expected 3 lines, got %d", clone.TermView.Li.LineCount())
	}

	// 4. Check if visual grid is copied
	if clone.TermView.Lines[4][0].Char != 'H' {
		t.Error("Terminal visual grid (Lines) history not copied to clone")
	}

	// 5. Verify prompt reset logic
	if clone.TermView.CursorX != 0 {
		t.Errorf("Expected clone CursorX to be 0 after prompt wipe, got %d", clone.TermView.CursorX)
	}
	if clone.TermView.Lines[5][0].Char != ' ' {
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
	pf.ShowPanels = false    // Hide panels to enable history intercept
	pf.CmdLine.Edit.AddHistory("git status")

	// Press Up Arrow
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.CmdLine.Edit.GetText() != "git status" {
		t.Errorf("PanelsFrame failed to pass Up Arrow to history. Got '%s'", pf.CmdLine.Edit.GetText())
	}

	// Reset, show panels, try again
	pf.CmdLine.Clear()
	pf.CmdLine.Edit.HistoryPos = -1
	pf.ShowPanels = true

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.CmdLine.Edit.GetText() != "" {
		t.Error("Up Arrow should NOT trigger history when panels are visible")
	}
}
func TestPanelsFrame_HistoryNavigation_HiddenPanels(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShowPanels = false // Panels are hidden
	pf.CmdLine.Edit.AddHistory("last command")

	// Press Up Arrow - should trigger HistoryUp on the command line
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	})

	if pf.CmdLine.Edit.GetText() != "last command" {
		t.Errorf("Up arrow failed to cycle history with hidden panels. Got: %q", pf.CmdLine.Edit.GetText())
	}

	// Press Esc - should clear line and reset history position
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})

	if !pf.CmdLine.IsEmpty() || pf.CmdLine.Edit.HistoryPos != -1 {
		t.Error("Esc failed to reset history state")
	}
}
func TestPanelsFrame_EnterAddsToHistory(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.CmdLine.Edit.SetText("ls -la")

	// Simulate Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	if len(pf.CmdLine.Edit.History) == 0 || pf.CmdLine.Edit.History[0] != "ls -la" {
		t.Errorf("Command was not added to history on Enter. History: %v", pf.CmdLine.Edit.History)
	}
}

func TestPanelsFrame_AltScreenTerminalHeight(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.Pty = &mockPty{}
	pf.Parser = terminal.NewAnsiParser(pf.TermView, pf.Pty)
	height := 25
	pf.ShowKeyBar = true

	// 1. Normal mode: terminal should leave space for KeyBar
	pf.TermView.UseAltScreen = false
	pf.ResizeConsole(80, height)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	// termY2 should be h-2 (23)
	if pf.TermView.Y2 != 23 {
		t.Errorf("Normal mode: expected terminal Y2=23, got %d", pf.TermView.Y2)
	}

	// 2. AltScreen mode: terminal should occupy the KeyBar's row
	pf.TermView.UseAltScreen = true
	pf.ResizeConsole(80, height)
	// termY2 should be h-1 (24)
	if pf.TermView.Y2 != 24 {
		t.Errorf("AltScreen mode: expected terminal Y2=24, got %d", pf.TermView.Y2)
	}
}

func TestPanelsFrame_KeyBarSuppression(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShowKeyBar = true
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	pf.LastAutoRefresh = time.Now()

	// We need to simulate the frame being on top to trigger the logic
	vtui.FrameManager.Push(pf)

	// 1. Normal mode: KeyBar should be registered
	pf.TermView.UseAltScreen = false
	pf.Show(scr)
	if vtui.FrameManager.KeyBar == nil {
		t.Error("KeyBar should be registered in FrameManager in normal mode")
	}

	// 2. AltScreen mode: KeyBar should be removed from FrameManager
	pf.TermView.UseAltScreen = true
	pf.Show(scr)
	if vtui.FrameManager.KeyBar != nil {
		t.Error("KeyBar should be UNregistered from FrameManager in AltScreen mode")
	}

	// 3. Busy mode but panels visible: KeyBar should be registered (Issue #50)
	pf.TermView.UseAltScreen = false
	pf.ShowPanels = true
	pf.Pty = &mockPty{} // Ensure active terminal.PTY is not nil
	pf.Executing = true
	pf.Show(scr)
	if vtui.FrameManager.KeyBar == nil {
		t.Error("KeyBar should be registered in FrameManager in busy mode when panels are visible")
	}

	// 4. Busy mode and panels hidden: KeyBar should be UNregistered (Issue #50)
	pf.ShowPanels = false
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

func TestPanelsFrame_AutoRefresh(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))

	// Setup a mock directory
	tmp := t.TempDir()
	fsp := pf.Panels[0].(*FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Emulate an initial read that populates MTime
	fsp.lastDirMTime = time.Now().Add(-10 * time.Minute)
	// Write a file to update actual directory MTime
	if err := os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	// Emulate the timer expiration
	pf.LastAutoRefresh = time.Now().Add(-5 * time.Second)

	// Trigger Show which should fire the async stat check
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	pf.Show(scr)

	// Pump both stat callbacks; the changed side starts a directory load.
	timeout := time.After(2 * time.Second)
	for pf.Panels[0].(*FileSystemPanel).isCheckingRefresh || pf.Panels[1].(*FileSystemPanel).isCheckingRefresh {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("AutoRefresh stat check did not finish")
		}
	}
	if !fsp.IsLoading {
		t.Fatal("AutoRefresh failed to trigger ReadDirectory after MTime change")
	}
	waitForLoad(t, fsp)
}
func TestPanelsFrame_ResizingIntegration(t *testing.T) {
	oldWidthDecrement := config.App.WidthDecrement
	config.App.WidthDecrement = 0
	t.Cleanup(func() { config.App.WidthDecrement = oldWidthDecrement })

	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Initial size 80x25
	pf.ResizeConsole(80, 25)

	// 1. Verify initial positions of standard components
	if pf.KeyBar.Y1 != 24 {
		t.Errorf("Initial KeyBar Y1: expected 24, got %d", pf.KeyBar.Y1)
	}
	if pf.CmdLine.Y1 != 23 {
		t.Errorf("Initial CommandLine Y1: expected 23, got %d", pf.CmdLine.Y1)
	}

	// 2. Perform resize to 120x40
	pf.ResizeConsole(120, 40)

	// 3. Verify that components moved/scaled correctly
	if pf.KeyBar.Y1 != 39 {
		t.Errorf("Resized KeyBar Y1: expected 39, got %d", pf.KeyBar.Y1)
	}
	if pf.KeyBar.X2 != 119 {
		t.Errorf("Resized KeyBar X2: expected 119, got %d", pf.KeyBar.X2)
	}
	if pf.CmdLine.Y1 != 38 {
		t.Errorf("Resized CommandLine Y1: expected 38, got %d", pf.CmdLine.Y1)
	}

	// 4. Verify panels scaled
	leftX1, _, leftX2, _ := pf.Panels[0].GetPosition()
	rightX1, _, rightX2, _ := pf.Panels[1].GetPosition()

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
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	fm.Push(pf)

	qm := fileops.GlobalQueueManager
	oldTasks := qm.SetTasks([]*fileops.QueueTask{{ID: 1, State: "Running"}})
	t.Cleanup(func() { qm.SetTasks(oldTasks) })

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
	oldWidthDecrement := config.App.WidthDecrement
	config.App.WidthDecrement = 0
	t.Cleanup(func() { config.App.WidthDecrement = oldWidthDecrement })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathL := filepath.Join(t.TempDir(), "left")
	pathR := filepath.Join(t.TempDir(), "right")
	if err := os.MkdirAll(pathL, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pathR, 0700); err != nil {
		t.Fatal(err)
	}

	fspL := pf.Panels[0].(*FileSystemPanel)
	fspR := pf.Panels[1].(*FileSystemPanel)

	if err := fspL.Vfs.SetPath(pathL); err != nil {
		t.Fatal(err)
	}
	if err := fspR.Vfs.SetPath(pathR); err != nil {
		t.Fatal(err)
	}
	fspL.SetViewMode(ViewModeDetailed)
	fspR.SetViewMode(ViewModeMedium)

	pf.ActiveIdx = 0 // Active is Left

	// Execute Swap
	pf.HandleCommand(appcmd.CmSwapPanels, nil)

	// 1. Verify instances are swapped in the array
	if pf.Panels[0] != fspR || pf.Panels[1] != fspL {
		t.Error("Panels instances were not swapped in pf.panels array")
	}

	// 2. Verify activeIdx followed the content
	if pf.ActiveIdx != 1 {
		t.Errorf("activeIdx should have moved to 1 to follow the panel, got %d", pf.ActiveIdx)
	}

	// 3. Verify positions were updated (fspR was Right, now should be Left)
	x1, _, x2, _ := fspR.GetPosition()
	if x1 != 0 || x2 != 39 {
		t.Errorf("Swapped panel (Right->Left) has wrong X position: %d..%d", x1, x2)
	}

	// 4. Verify state preservation
	if fspR.ViewMode != ViewModeMedium {
		t.Error("Swapped panel did not preserve its ViewMode")
	}
}

// TestPanelsFrame_VisualLeftRightFollowSwap makes sure resolving
// panels by on-screen X-position keeps Ctrl+[/Ctrl+] pointing at
// the visually-left and visually-right sides after appcmd.CmSwapPanels
// re-slots the underlying panels array.
func TestPanelsFrame_VisualLeftRightFollowSwap(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathL := filepath.Join(t.TempDir(), "left")
	pathR := filepath.Join(t.TempDir(), "right")
	if err := os.MkdirAll(pathL, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pathR, 0700); err != nil {
		t.Fatal(err)
	}

	fspL := pf.Panels[0].(*FileSystemPanel)
	fspR := pf.Panels[1].(*FileSystemPanel)
	if err := fspL.Vfs.SetPath(pathL); err != nil {
		t.Fatal(err)
	}
	if err := fspR.Vfs.SetPath(pathR); err != nil {
		t.Fatal(err)
	}

	// Baseline: unswapped — visual-left is the panel we set to
	// pathL, visual-right the one at pathR.
	if got := pf.VisualLeftFSP(); got != fspL {
		t.Errorf("visualLeftFSP baseline: got %p, want left (%p)", got, fspL)
	}
	if got := pf.VisualRightFSP(); got != fspR {
		t.Errorf("visualRightFSP baseline: got %p, want right (%p)", got, fspR)
	}

	// Swap. panels[0] now points at fspR, but that panel gets
	// moved to X=0 by ResizeConsole (see TestPanelsFrame_SwapPanels
	// step 3), so it's the visually-left one now.
	pf.HandleCommand(appcmd.CmSwapPanels, nil)

	if got := pf.VisualLeftFSP(); got != fspR {
		t.Errorf("visualLeftFSP after swap: got %p, want fspR (%p)", got, fspR)
	}
	if got := pf.VisualRightFSP(); got != fspL {
		t.Errorf("visualRightFSP after swap: got %p, want fspL (%p)", got, fspL)
	}
}

func TestPanelsFrame_SingleVisiblePanelUsesFullWidth(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	left := pf.Panels[0]
	right := pf.Panels[1]
	pf.ShowRightPanel = false
	pf.ResizeConsole(80, 25)
	if x1, _, x2, _ := left.GetPosition(); x1 != 0 || x2 != 79 {
		t.Fatalf("left-only panel geometry = %d..%d, want 0..79", x1, x2)
	}
	if got := pf.VisualLeftFSP(); got != left || pf.VisualRightFSP() != left {
		t.Fatal("left-only layout did not resolve the visible panel on both visual sides")
	}

	pf.ShowLeftPanel = false
	pf.ShowRightPanel = true
	pf.ResizeConsole(80, 25)
	if x1, _, x2, _ := right.GetPosition(); x1 != 0 || x2 != 79 {
		t.Fatalf("right-only panel geometry = %d..%d, want 0..79", x1, x2)
	}
	if got := pf.VisualLeftFSP(); got != right || pf.VisualRightFSP() != right {
		t.Fatal("right-only layout did not resolve the visible panel on both visual sides")
	}
}

func TestPanelsFrame_WideFollowsSwapAndClone(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	left := pf.Panels[0]
	pf.SetWidePanel(0)

	pf.HandleCommand(appcmd.CmSwapPanels, nil)
	if pf.WidePanel != 1 || pf.ActiveIdx != 1 || pf.Panels[1] != left {
		t.Fatalf("Wide did not follow swapped content: wide=%d active=%d", pf.WidePanel, pf.ActiveIdx)
	}

	clone := pf.Clone()
	defer clone.Close()
	if clone.WidePanel != 1 || clone.ActiveIdx != 1 {
		t.Fatalf("clone lost Wide state: wide=%d active=%d", clone.WidePanel, clone.ActiveIdx)
	}
	x1, _, x2, _ := clone.Panels[1].GetPosition()
	if x1 != 0 || x2 != 79 {
		t.Fatalf("cloned Wide geometry = %d..%d, want 0..79", x1, x2)
	}
}
func TestPanelsFrame_Clone_SelectionPreservation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "selected.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "normal.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	fsp := pf.Panels[0].(*FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	fsp.ReadDirectory()
	waitForLoad(t, fsp)

	// Select "selected.txt"
	found := false
	for i, e := range fsp.Entries {
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
	cloneFsp := clone.Panels[0].(*FileSystemPanel)
	waitForLoad(t, cloneFsp)
	waitForLoad(t, clone.Panels[1].(*FileSystemPanel))

	// Verify preservation
	foundInClone := false
	for _, e := range cloneFsp.Entries {
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
	fp := pf.Panels[0].(*FileSystemPanel)
	fp.Vfs = v
	pf.ActiveIdx = 0

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
	fp := pf.Panels[0].(*FileSystemPanel)
	fp.Vfs = v
	pf.ActiveIdx = 0

	prompt := pf.BuildPrompt()

	// Convert prompt to string
	promptStr := ""
	for _, c := range prompt {
		if c.Char != vtui.WideCharFiller {
			promptStr += string(testutil.Rune(c.Char))
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
	pf.Panels[0].(*FileSystemPanel).Vfs = filesystem
	pf.ActiveIdx = 0

	prompt := pf.BuildPrompt()
	var promptText strings.Builder
	for _, cell := range prompt {
		if cell.Char != vtui.WideCharFiller {
			if _, err := promptText.WriteRune(vtui.CellBaseRune(cell.Char)); err != nil {
				t.Fatal(err)
			}
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
	fsp := pf.Panels[0].(*FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(t.TempDir())
	fsp.ProviderOpenTarget = target
	fsp.ProviderOpenTask = &vtui.TaskContext{}
	defer func() { fsp.ProviderOpenTask = nil }()
	fsp.IsLoading = true
	fsp.updateTitle(nil)
	pf.ActiveIdx = 0

	var prompt strings.Builder
	for _, cell := range pf.BuildPrompt() {
		if cell.Char != vtui.WideCharFiller {
			if _, err := prompt.WriteRune(vtui.CellBaseRune(cell.Char)); err != nil {
				t.Fatal(err)
			}
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
	if err := os.MkdirAll(pathL, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pathR, 0700); err != nil {
		t.Fatal(err)
	}

	if err := pf.Panels[0].(*FileSystemPanel).Vfs.SetPath(pathL); err != nil {
		t.Fatal(err)
	}
	if err := pf.Panels[1].(*FileSystemPanel).Vfs.SetPath(pathR); err != nil {
		t.Fatal(err)
	}

	l, r := pf.GetPaths()
	if l != pathL || r != pathR {
		t.Errorf("GetPaths failed. Got %q, %q; want %q, %q", l, r, pathL, pathR)
	}
}
func TestPanelsFrame_StateCapture(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fspL := pf.Panels[0].(*FileSystemPanel)
	fspR := pf.Panels[1].(*FileSystemPanel)

	// Mock cursors
	fspL.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "file.l"}}}
	fspR.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "file.r"}}}
	fspL.SetCursorIndex(0)
	fspR.SetCursorIndex(0)

	pf.ActiveIdx = 0 // Left active

	lFile := fspL.GetSelectedName()
	rFile := fspR.GetSelectedName()

	if lFile != "file.l" || rFile != "file.r" || pf.ActiveIdx != 0 {
		t.Errorf("State capture failed: L:%q, R:%q, Active:%d", lFile, rFile, pf.ActiveIdx)
	}
}
func TestPanelsFrame_CloneIndependence(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))

	// Set path in original
	fsp := pf.Panels[0].(*FileSystemPanel)
	origPath := t.TempDir()
	if err := fsp.Vfs.SetPath(origPath); err != nil {
		t.Fatal(err)
	}

	// Clone
	clone := pf.Clone()
	defer clone.Close()
	waitForLoad(t, clone.Panels[0].(*FileSystemPanel))
	waitForLoad(t, clone.Panels[1].(*FileSystemPanel))

	// Change path in clone
	newPath := t.TempDir()
	if err := clone.Panels[0].(*FileSystemPanel).Vfs.SetPath(newPath); err != nil {
		t.Fatal(err)
	}

	// Verify original is unchanged
	if pf.Panels[0].(*FileSystemPanel).Vfs.GetPath() != origPath {
		t.Error("Cloned PanelsFrame shares VFS state with parent!")
	}
}
func TestPanelsFrame_PTYLockContention(t *testing.T) {
	// Этот тест проверяет, что тяжелый парсинг в terminal.PTY-потоке не блокирует
	// доступ UI-потока к методу getActivePTY (регрессия дедлока).
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	// Симулируем "забитую" очередь задач
	for i := 0; i < 64; i++ {
		vtui.FrameManager.PostTask(func() {})
	}

	// Запускаем в отдельной горутине тяжелый парсинг
	// (в реальности он теперь идет вне ptyMutex)
	go func() {
		hugeData := strings.Repeat("A", 100000)
		pf.PtyMutex.Lock()
		active := (pf.getActivePTYUnsafe() == pf.Pty)
		pf.PtyMutex.Unlock()

		if active {
			pf.Parser.Process([]byte(hugeData))
		}
	}()

	// UI-поток пытается взять мутекс через getActivePTY.
	// Если дедлок не починен, мы зависнем здесь.
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			_ = pf.GetActivePTY()
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	select {
	case <-done:
		// Успех
	case <-time.After(2 * time.Second):
		t.Fatal("DEADLOCK DETECTED: getActivePTY blocked by term.PTY processing loop")
	}
}

func TestTerminalRedrawSchedulerCoalescesBurst(t *testing.T) {
	var mu sync.Mutex
	redraws := 0
	scheduler := terminal.NewTerminalRedrawScheduler(func() {
		mu.Lock()
		redraws++
		mu.Unlock()
	})

	for i := 0; i < 100; i++ {
		scheduler.Request()
	}

	time.Sleep(2 * time.Millisecond)
	mu.Lock()
	got := redraws
	mu.Unlock()
	if got != 1 {
		t.Fatalf("burst triggered %d redraws, want 1", got)
	}

	// The interval is cleared by a timer of its own, and a sleep of interval
	// plus a fixed margin is not a guarantee that the timer has run: on a
	// loaded machine, and under the race detector, it regularly has not. Ask
	// again until it does. A request made while the burst is still suppressed
	// is exactly what the first half of this test asserts costs nothing, so
	// asking repeatedly cannot inflate the count.
	deadline := time.Now().Add(5 * time.Second)
	for {
		scheduler.Request()
		mu.Lock()
		got = redraws
		mu.Unlock()
		if got == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got != 2 {
		t.Fatalf("redraw after interval counted %d times, want 2", got)
	}

	scheduler.Stop()
	scheduler.Request()
	time.Sleep(2 * time.Millisecond)
	mu.Lock()
	got = redraws
	mu.Unlock()
	if got != 2 {
		t.Fatalf("stopped scheduler triggered %d redraws, want 2", got)
	}
}
func TestPanelsFrame_Clone_Comprehensive(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Setup specific state on the left panel
	fsp := pf.Left().(*FileSystemPanel)
	fsp.SetViewMode(ViewModeDetailed)
	fsp.SortMode = SortSize
	fsp.SortReverse = true
	fsp.Entries = []*FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "file1"}},
		{VFSItem: vfs.VFSItem{Name: "file2"}, Selected: true},
		{VFSItem: vfs.VFSItem{Name: "file3"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(2) // On "file2"

	// 2. Setup terminal state
	pf.TermView.PutChar('f', 0)
	pf.TermView.PutChar('o', 0)
	pf.TermView.PutChar('o', 0)
	pf.TermView.PutChar('\n', 0)

	// 3. Perform Clone
	clone := pf.Clone()
	defer clone.Close()

	// 4. Verify Panel State
	cloneFsp := clone.Left().(*FileSystemPanel)
	if cloneFsp.ViewMode != ViewModeDetailed {
		t.Error("Clone failed to preserve ViewMode")
	}
	if cloneFsp.SortMode != SortSize || !cloneFsp.SortReverse {
		t.Error("Clone failed to preserve sort state")
	}
	if cloneFsp.GetCursorIndex() != 2 {
		t.Errorf("Clone failed to preserve cursor index: expected 2, got %d", cloneFsp.GetCursorIndex())
	}
	if cloneFsp.GetSelectedName() != "file2" {
		t.Errorf("Clone failed to preserve selection: expected 'file2', got %q", cloneFsp.GetSelectedName())
	}
	if !cloneFsp.Entries[2].Selected {
		t.Error("Clone failed to preserve individual item selection flag")
	}

	// 5. Verify Terminal State
	if !strings.HasPrefix(string(clone.TermView.GetAllLogBytes()), "foo\n") {
		t.Errorf("Clone failed to preserve terminal history: %q", string(clone.TermView.GetAllLogBytes()))
	}

	// 6. Verify Active Panel index
	if clone.ActiveIdx != pf.ActiveIdx {
		t.Errorf("Clone failed to preserve active panel index: %d", clone.ActiveIdx)
	}
}
func TestIsTerminalRunnable(t *testing.T) {
	tmpDir := t.TempDir()
	v := vfs.NewOSVFS(tmpDir)

	// 1. Обычный текстовый файл -> false
	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if vfs.IsTerminalRunnable(context.Background(), v, txtFile) {
		t.Error("Text file should not be terminal-runnable")
	}

	// 2. Файл с расширением .sh -> true
	shFile := filepath.Join(tmpDir, "test.sh")
	if err := os.WriteFile(shFile, []byte("echo hi"), 0600); err != nil {
		t.Fatal(err)
	}
	if !vfs.IsTerminalRunnable(context.Background(), v, shFile) {
		t.Error(".sh file should be terminal-runnable")
	}

	// 3. Файл с шебангом без расширения -> true
	binFile := filepath.Join(tmpDir, "my-tool")
	if err := os.WriteFile(binFile, []byte("#!/usr/bin/env bash\necho hi"), 0600); err != nil {
		t.Fatal(err)
	}
	if !vfs.IsTerminalRunnable(context.Background(), v, binFile) {
		t.Error("File with shebang should be terminal-runnable")
	}

	// 4. Директория -> false
	subDir := filepath.Join(tmpDir, "folder")
	if err := os.Mkdir(subDir, 0700); err != nil {
		t.Fatal(err)
	}
	if vfs.IsTerminalRunnable(context.Background(), v, subDir) {
		t.Error("Directory should not be terminal-runnable")
	}

	// 5. Unix Executable Bit (если не на Windows)
	if runtime.GOOS != "windows" {
		execFile := filepath.Join(tmpDir, "compiled-bin")
		if err := os.WriteFile(execFile, []byte{0x7f, 'E', 'L', 'F'}, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(execFile, 0700); err != nil { // #nosec G302 -- the executable bit is the behavior under test.
			t.Fatal(err)
		}
		if !vfs.IsTerminalRunnable(context.Background(), v, execFile) {
			t.Error("File with executable bit should be terminal-runnable on Unix")
		}
	}
}

func TestPanelsFrame_CommandLineEnter(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	// Вводим команду в консоль
	pf.CmdLine.Edit.SetText("ls -la")

	// Нажимаем Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели должны скрыться
	if pf.ShowPanels {
		t.Error("Panels should hide after command execution from command line")
	}
	// terminal.PTY должен получить команду
	if !strings.Contains(string(pty.written), "ls -la") {
		t.Errorf("PTY did not receive command. Got: %q", string(pty.written))
	}
}

func TestPanelsFrame_CommandLineEnterRejectsUnmatchedBacktick(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backticks are literal in the Windows shell")
	}

	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	before := len(pty.written)
	pf.CmdLine.Edit.SetText("echo `date")
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	if !pf.ShowPanels {
		t.Fatal("panels hid after an unmatched backtick instead of showing an error")
	}
	if got := string(pty.written[before:]); got != "" {
		t.Fatalf("unmatched backtick reached the term.PTY: %q", got)
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != " Error " {
		t.Fatalf("unmatched backtick did not open an error dialog: top=%T title=%q", top, func() string {
			if top != nil {
				return top.GetTitle()
			}
			return ""
		}())
	}
	pressKey(top, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_ESCAPE,
	})
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
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	runner := &commandRunnerPanelVFS{
		NullVFS: vfs.NewNullVFS(0),
		path:    "/sdcard/Download",
		calls:   make(chan [2]string, 1),
	}
	fsp := pf.GetActivePanel()
	fsp.Vfs = runner
	pty := pf.Pty.(*mockPty)
	localBytesBefore := len(pty.written)
	pf.CmdLine.Edit.SetText("ls -la")

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
		t.Fatalf("local term.PTY received %d new bytes for a remote command", got-localBytesBefore)
	}
	if !pf.ShowPanels {
		t.Fatal("remote command without a term.PTY unexpectedly hid the panels")
	}
	if !pf.CmdLine.IsEmpty() {
		t.Fatalf("command line was not cleared: %q", pf.CmdLine.Edit.GetText())
	}
	deadline := time.After(time.Second)
	for {
		top, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		finished := false
		if ok {
			for _, child := range top.GetChildren() {
				if list, ok := child.(*vtui.ListBox); ok {
					for _, item := range list.Items {
						if item == "[exit status 0]" {
							finished = true
							break
						}
					}
				}
			}
		}
		if finished {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("remote command completion did not reach the output frame")
		}
	}
	if top := vtui.FrameManager.GetTopFrame(); top == nil || !strings.Contains(top.GetTitle(), i18n.Msg("RemoteCmd.Title")) {
		t.Fatalf("remote command output frame = %T %q", top, func() string {
			if top != nil {
				return top.GetTitle()
			}
			return ""
		}())
	}
}

func TestPanelsFrame_CommandLineEnter_WhenBusy(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	pf.Executing = true // terminal.PTY is busy

	// Вводим команду в консоль
	pf.CmdLine.Edit.SetText("ls -la")

	// Нажимаем Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	// Панели должны скрыться
	if pf.ShowPanels {
		t.Error("Panels should hide after command execution even when term.PTY is busy")
	}
	// terminal.PTY должен получить команду
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
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Panels[1].(*FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*FileEntry{
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
	if !pf.ShowPanels {
		t.Error("Panels should NOT hide when entering a directory")
	}
	// Путь должен измениться
	if fsp.Vfs.GetPath() != sub {
		t.Errorf("VFS path did not change. Expected %s, got %s", sub, fsp.Vfs.GetPath())
	}
}

func TestPanelsFrame_SwitchVFSPreservesQualifiedDirectoryCache(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*FileSystemPanel)
	fsp.DirCache[fsp.CacheKey("/test/path")] = DirCacheEntry{}
	if len(fsp.DirCache) != 1 {
		t.Fatal("Cache setup failed")
	}

	pf.SwitchToVFS(fsp, vfs.NewOSVFS(t.TempDir()))

	if len(fsp.DirCache) != 1 {
		t.Error("switchToVFS discarded the qualified directory cache")
	}
}

func TestPanelsFrame_Clone_CachePreservation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*FileSystemPanel)
	items := []vfs.VFSItem{{Name: "cached_item"}}
	cacheKey := fsp.CacheKey("/test/path")
	fsp.DirCache[cacheKey] = DirCacheEntry{Items: items}

	clone := pf.Clone()
	defer clone.Close()
	cloneFsp := clone.Panels[0].(*FileSystemPanel)

	if len(cloneFsp.DirCache) != 1 {
		t.Fatalf("Cache not cloned, length is %d", len(cloneFsp.DirCache))
	}
	if cached, ok := cloneFsp.DirCache[cacheKey]; !ok || len(cached.Items) != 1 || cached.Items[0].Name != "cached_item" {
		t.Error("Cloned cache content is incorrect")
	}

	// Verify independence
	cloneFsp.DirCache[cloneFsp.CacheKey("/new/path")] = DirCacheEntry{}
	if len(fsp.DirCache) != 1 {
		t.Error("Cloned cache is not independent from original")
	}
}

func TestExecuteFileOp_BackgroundButtonTrigger(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
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
	t.Cleanup(fork.Close)
	fm.AddScreen(fork)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	waitForLoad(t, fork.Panels[0].(*FileSystemPanel))
	waitForLoad(t, fork.Panels[1].(*FileSystemPanel))

	if len(fm.Screens) != initialScreens+1 {
		t.Errorf("Backgrounding failed to create a new screen. Got %d, want %d", len(fm.Screens), initialScreens+1)
	}
}
func TestExecuteDummyOp_HeadlessMode(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	fm.Push(pf)

	initialScreens := len(fm.Screens)

	// Trigger Mode Foreground (2)
	pf.ExecuteDummyOp(2)

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
	dlg, ok := newScreen.Frames[0].(*vtui.Window)
	if !ok {
		t.Fatalf("headless frame = %T, want progress dialog", newScreen.Frames[0])
	}
	dlg.OnResult(1)
	timeout = time.After(2 * time.Second)
	for !dlg.IsDone() {
		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			t.Fatal("cancelled dummy operation did not finish")
		}
	}
}

func TestPanelsFrame_TerminalForwarding_Legacy(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = true

	// Mock terminal.PTY
	pty := &mockPty{}
	pf.Pty = pty

	// 1. Ctrl+W should be FORWARDED (Legacy mode has no Kitty/Win32 flags)
	// For letters, keymap.TranslateInput expects the Char field to be populated.
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
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = true
	pf.TermView.Win32InputMode = true // Advanced mode

	pty := &mockPty{}
	pf.Pty = pty

	// 1. Ctrl+Tab remains a global workspace shortcut in Advanced mode.
	handled := pressKey(pf, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_TAB, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if handled {
		t.Error("Ctrl+Tab was erroneously forwarded to term.PTY in Advanced mode")
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
		t.Error("Shift+Ctrl+Tab was erroneously forwarded to term.PTY")
	}
}

type busyMockPty struct{ mockPty }

func (p *busyMockPty) IsBusy() bool { return true }

func TestPanelsFrame_TerminalForwarding_BusyNonAltScreenWorkspaceKeys(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = false // Python REPL and similar interactive tools.

	pty := &busyMockPty{}
	pf.Pty = pty

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
			t.Errorf("workspace key with modifiers %#x reached term.PTY as %q", state, got)
		}
	}
}

func TestPanelsFrame_TerminalCtrlNWorkspacePreference(t *testing.T) {
	old := config.App.TerminalCtrlNWorkspace
	defer func() { config.App.TerminalCtrlNWorkspace = old }()

	for _, altScreen := range []bool{false, true} {
		pf := NewPanelsFrame()
		pf.ShowPanels = false
		pf.TermView.UseAltScreen = altScreen
		pty := &busyMockPty{}
		pf.Pty = pty

		event := func() *vtinput.InputEvent {
			return &vtinput.InputEvent{
				Type:            vtinput.KeyEventType,
				KeyDown:         true,
				VirtualKeyCode:  vtinput.VK_N,
				Char:            'n',
				ControlKeyState: vtinput.LeftCtrlPressed,
			}
		}

		config.App.TerminalCtrlNWorkspace = true
		if pressKey(pf, event()) {
			t.Errorf("Ctrl+N was not released to FrameManager (AltScreen=%v)", altScreen)
		}
		if got := pty.String(); got != "" {
			t.Errorf("enabled Ctrl+N preference wrote %q to term.PTY (AltScreen=%v)", got, altScreen)
		}

		config.App.TerminalCtrlNWorkspace = false
		pty.Reset()
		if !pressKey(pf, event()) {
			t.Errorf("disabled Ctrl+N preference did not return key to term.PTY (AltScreen=%v)", altScreen)
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
	pf.ShowPanels = false
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
	if !clone.ShowPanels {
		t.Error("terminal-side fork left the cloned workspace in terminal view")
	}
	if pf.ShowPanels {
		t.Error("terminal-side fork changed the original workspace")
	}
	if clone.GetTitle() == "cmd.exe" || clone.GetTitle() == "Terminal" {
		t.Errorf("forked workspace title still looks like a terminal: %q", clone.GetTitle())
	}
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
	waitForLoad(t, clone.Panels[0].(*FileSystemPanel))
	waitForLoad(t, clone.Panels[1].(*FileSystemPanel))
}
func TestPanelsFrame_ProcessMouse_RightDoubleClickNoEnter(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	runnablePath := filepath.Join(tmp, "run.sh")
	if err := os.WriteFile(runnablePath, []byte("echo"), 0600); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Left().(*FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	fsp.Entries = []*FileEntry{
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
	if !pf.ShowPanels {
		t.Error("Right double-click should NOT simulate Enter")
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
	if top == nil || top.GetTitle() != i18n.Msg("Quit.Title") {
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
func TestPanelsFrame_DriveMenu_OtherPanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pathR := filepath.Join(t.TempDir(), "right")
	if err := os.MkdirAll(pathR, 0700); err != nil {
		t.Fatal(err)
	}
	if err := pf.Panels[1].(*FileSystemPanel).Vfs.SetPath(pathR); err != nil {
		t.Fatal(err)
	}

	// Open Alt+F1 (Left panel drive menu)
	pf.ShowDriveMenu(0)

	top := vtui.FrameManager.GetTopFrame()
	menu, ok := driveMenuFromFrame(top)
	if !ok {
		t.Fatal("Drive menu not opened")
	}

	// "Other panel" stays at index 0, but the cursor now opens on the drive
	// the panel currently shows when the menu lists it (driveMenuDefaultPos,
	// far2l parity), so the expected row depends on where the panel sits.
	if menu.GetTitle() != i18n.Msg("Drive.Title") {
		t.Errorf("Menu title invalid: %q", menu.GetTitle())
	}
	cur := pf.Panels[0].(*FileSystemPanel).Vfs.GetPath()
	if want := wantDriveMenuRow(menu, cur); menu.SelectPos != want {
		t.Errorf("Menu state invalid: pos=%d, want %d (panel at %q)", menu.SelectPos, want, cur)
	}

	// Trigger "Other panel" (idx 0)
	menu.OnAction(0)

	// Left panel VFS path must now match Right panel's path
	got := pf.Panels[0].(*FileSystemPanel).Vfs.GetPath()
	if got != pathR {
		t.Errorf("Path sync failed. Expected %q, got %q", pathR, got)
	}
}

func TestPanelsFrame_DriveMenu_TerminalBusy(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Simulate busy terminal
	pf.ShowPanels = false
	pf.TermView.UseAltScreen = true

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
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Hide panels
	pf.ShowPanels = false

	// 2. Add history item starting with "cd" to guarantee a match
	pf.CmdLine.Edit.History = []string{"cd_test_dir"}
	pf.CmdLine.Edit.SetText("cd")

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
	defer sysinfo.SnapshotDrives()()
	sysinfo.SetDrives([]sysinfo.DriveEntry{
		{Name: "NetFox", Factory: func() vfs.VFS { return nil }},
		{Name: "Null VFS", Factory: func() vfs.VFS { return nil }},
	})

	pf.ShowDriveMenu(0)
	top := vtui.FrameManager.GetTopFrame()
	menu, ok := driveMenuFromFrame(top)
	if !ok {
		t.Fatalf("Expected VMenu on top, got %T", top)
	}

	// 1. Проверка фокуса: "Other panel" по умолчанию, но если панель стоит
	// на диске, который есть в меню, курсор садится на него (Windows).
	cur := pf.Panels[0].(*FileSystemPanel).Vfs.GetPath()
	if want := wantDriveMenuRow(menu, cur); menu.SelectPos != want {
		t.Errorf("Expected row %d to be focused, got index %d (panel at %q)", want, menu.SelectPos, cur)
	}

	// 2. Ищем плагины в пунктах меню
	var nfIdx, nullIdx = -1, -1
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

	pf.ShowDriveMenu(0)
	menu, ok := driveMenuFromFrame(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatalf("Expected drive menu on top, got %T", vtui.FrameManager.GetTopFrame())
	}

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
	theme.SetDefaultF4Palette()
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
	pf.ActiveIdx = 0
	fsp := pf.Panels[0].(*FileSystemPanel)
	fsp.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "some_file.txt"}}}
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
	got := pf.CmdLine.Edit.GetText()
	if !strings.Contains(got, testText) {
		t.Errorf("Shift+Ins failed to fallthrough to CommandLine. Got %q, expected to contain %q", got, testText)
	}

	// 5. Verify file was NOT selected (Index 0 should remain unselected)
	if fsp.Entries[0].Selected {
		t.Error("File was erroneously selected by Shift+Ins")
	}
}
func TestPanelsFrame_PromptTruncation(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()

	// Simulate standard 80-column terminal
	width := 80
	pf.ResizeConsole(width, 25)

	fsp := pf.GetActivePanel()

	// Max allowed prompt length is width / 2 = 40.

	t.Run("Short Path No Truncation", func(t *testing.T) {
		// Use NullVFS to bypass real disk checks in tests
		fsp.Vfs = vfs.NewNullVFS(0)
		if err := fsp.Vfs.SetPath(filepath.FromSlash("/home/user")); err != nil {
			t.Fatal(err)
		}
		prompt := pf.BuildPrompt()

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
				promptStr += string(testutil.Rune(c.Char))
			}
		}
		if strings.Contains(promptStr, "home") {
			found = true
		}

		if !found {
			t.Errorf("Short path was lost in prompt: %q", promptStr)
		}
	})

	t.Run("Extreme Long Hostname Truncation", func(t *testing.T) {
		oldHostname := osHostname
		osHostname = func() (string, error) {
			return "sjc20-bb714-b90e13a3-e1f2-4dc9-95b6-3e55cc291be4-4E177BE7E70B.local", nil
		}
		defer func() { osHostname = oldHostname }()

		fsp.Vfs = vfs.NewNullVFS(0)
		if err := fsp.Vfs.SetPath(filepath.FromSlash("/very/long/directory/path/that/exceeds/the/limit/of/forty/characters")); err != nil {
			t.Fatal(err)
		}
		prompt := pf.BuildPrompt()

		visibleLen := 0
		promptStr := ""
		for _, c := range prompt {
			if c.Char != vtui.WideCharFiller {
				visibleLen++
				promptStr += string(testutil.Rune(c.Char))
			}
		}
		if visibleLen > 45 {
			t.Errorf("Prompt too long with long hostname: %d chars (%q)", visibleLen, promptStr)
		}
	})

	t.Run("Extreme Long Path Truncation", func(t *testing.T) {
		// Use NullVFS to bypass real disk checks in tests
		fsp.Vfs = vfs.NewNullVFS(0)
		longPath := "/very/long/directory/path/that/exceeds/the/limit/of/forty/characters/definitely/and/must/be/shortened"
		if err := fsp.Vfs.SetPath(filepath.FromSlash(longPath)); err != nil {
			t.Fatal(err)
		}
		prompt := pf.BuildPrompt()

		visibleLen := 0
		promptStr := ""
		for _, c := range prompt {
			if c.Char != vtui.WideCharFiller {
				visibleLen++
				promptStr += string(testutil.Rune(c.Char))
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
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp0 := pf.Panels[0].(*FileSystemPanel)
	fsp1 := pf.Panels[1].(*FileSystemPanel)
	if fsp0.CancelLoad != nil {
		fsp0.CancelLoad()
	}
	fsp0.IsLoading = false
	if fsp1.CancelLoad != nil {
		fsp1.CancelLoad()
	}
	fsp1.IsLoading = false

	// Setup VFS with a blocking Stat
	block := make(chan struct{})
	mv := &mockSlowStatVFS{
		OSVFS:     *vfs.NewOSVFS(t.TempDir()),
		statBlock: block,
	}

	fsp := pf.Panels[0].(*FileSystemPanel)
	fsp.Vfs = mv
	fsp.lastDirMTime = time.Now().Add(-1 * time.Hour)
	fsp.isCheckingRefresh = false

	// First Show() should trigger auto-refresh
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	pf.LastAutoRefresh = time.Now().Add(-5 * time.Second)
	pf.Show(scr)

	// Pump tasks so the auto-refresh goroutine can run
	deadline := time.Now().Add(1 * time.Second)
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
	pf.LastAutoRefresh = time.Now().Add(-5 * time.Second)
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

	oldCfg := config.App
	config.App.NavigationMode = config.NavigationVim
	config.App.AutoSaveSettings = false
	defer func() { config.App = oldCfg }()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*FileSystemPanel)
	pf.ActiveIdx = 0

	fsp.Entries = []*FileEntry{
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

	// 2. action.Action dd (Delete)
	cmdCaught = 0
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	if cmdCaught != appcmd.CmDelete {
		t.Errorf("'dd' failed to emit CmDelete, got %d", cmdCaught)
	}
	if !pf.CmdLine.IsEmpty() {
		t.Error("Command line should be cleared after Vim action")
	}

	// 3. Reset on Tab (Switch panel)
	cmdCaught = 0
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'd'})
	if cmdCaught == appcmd.CmDelete {
		t.Error("Vim prefix should reset after switching panels via Tab")
	}

	// 4. Reset on Mouse click
	cmdCaught = 0
	pf.CmdLine.Clear()
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'c'})
	pf.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX: 5, MouseY: 5,
	})
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'c'})
	if cmdCaught == appcmd.CmCopy {
		t.Error("Vim prefix should reset after mouse interaction")
	}

	// 5. Conflict with Fast Find
	cmdCaught = 0
	pf.CmdLine.Clear()
	fsp.SetCursorIndex(1) // Reset cursor position after mouse click test
	fsp.FastFindMode = true
	// In fast find mode, 'j' should be passed to find logic, not navigation.
	// `pf.ProcessKey` will return `false` because Vim logic is skipped,
	// then it will fall through to `fsp.ProcessKey` which will handle fast-find and return `true`.
	pressKey(pf, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'j'})
	if fsp.GetCursorIndex() != 1 {
		t.Error("'j' was handled as Vim navigation despite Fast Find being active")
	}
	if fsp.FastFindStr != "j" {
		t.Errorf("Fast find string should be 'j', got %q", fsp.FastFindStr)
	}
}

func createTestZipForNav(t *testing.T, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	zw := zip.NewWriter(f)

	_, err = zw.Create("inner_dir/")
	if err != nil {
		t.Fatal(err)
	}

	w, err := zw.Create("inner_dir/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
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
	pf.Panels[0] = lp
	pf.Panels[1] = rp
	pf.ActiveIdx = 0
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
	if _, isOS := lp.Vfs.(*vfs.OSVFS); isOS {
		t.Error("Expected panel VFS to switch from OSVFS to ArchiveVFS")
	}

	expectedPath := filepath.ToSlash(filepath.Clean(targetPath))
	if filepath.ToSlash(lp.Vfs.GetPath()) != expectedPath {
		t.Errorf("Expected VFS path %q, got %q", expectedPath, lp.Vfs.GetPath())
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
	if _, isOS := lp.Vfs.(*vfs.OSVFS); !isOS {
		t.Error("Expected panel VFS to switch back to OSVFS")
	}

	if filepath.Clean(lp.Vfs.GetPath()) != filepath.Clean(tmpDir) {
		t.Errorf("Expected OSVFS path %q, got %q", tmpDir, lp.Vfs.GetPath())
	}
}

func TestFileSystemPanel_SFXRequiresCtrlPgDn(t *testing.T) {
	vfs.RegisterProvider(&archive.ArchiveProvider{})
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	root := t.TempDir()
	zipPath := filepath.Join(root, "payload.zip")
	createTestZipForNav(t, zipPath)
	archiveBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	sfxPath := filepath.Join(root, "bundle.exe")
	if err := os.WriteFile(sfxPath, append([]byte("self-extractor stub\n"), archiveBytes...), 0600); err != nil { // #nosec G703 -- sfxPath is inside the private test temp directory.
		t.Fatal(err)
	}

	fp := NewFileSystemPanel(0, 0, 80, 25, vfs.NewOSVFS(root))
	t.Cleanup(func() {
		fp.cancelProviderOpen()
		if fp.CancelLoad != nil {
			fp.CancelLoad()
		}
		fp.StopLoadingAnimation()
	})
	waitForLoad(t, fp)
	fp.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "bundle.exe"}}}
	fp.SetCursorIndex(0)

	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}
	if !fp.ProcessKey(enter) {
		t.Fatal("ordinary Enter on an SFX row was not consumed")
	}
	if _, ok := fp.Vfs.(*vfs.OSVFS); !ok {
		t.Fatalf("ordinary Enter changed VFS to %T", fp.Vfs)
	}
	if fp.ProviderOpenTask != nil {
		t.Fatal("ordinary Enter started an SFX provider open")
	}

	if !fp.EnterSelectedFromAction() {
		t.Fatal("Ctrl+PgDn action did not start SFX entry")
	}
	waitForLoad(t, fp)
	if _, ok := fp.Vfs.(*archive.ArchiveVFS); !ok {
		t.Fatalf("Ctrl+PgDn left VFS as %T, want archive VFS", fp.Vfs)
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
	if _, err := zw.Create("dir/"); err != nil {
		t.Fatal(err)
	}
	// File 1 (10 bytes)
	w1, err := zw.Create("dir/file1.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	// File 2 (20 bytes)
	w2, err := zw.Create("dir/file2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write([]byte("01234567890123456789")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// 1. Setup VFS
	parentVFS := vfs.NewOSVFS(tmpDir)
	arcVFS, err := vfs.FindProvider(context.Background(), parentVFS, zipPath).Open(context.Background(), parentVFS, zipPath)
	if err != nil {
		t.Fatalf("Failed to open archive VFS: %v", err)
	}
	defer func() { _ = arcVFS.Close() }()

	destDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(destDir, 0700); err != nil {
		t.Fatal(err)
	}
	dstVFS := vfs.NewOSVFS(destDir)

	// 2. Pre-calculate stats (this mimics fileops.ExecuteFileOp's scan phase)
	names := []string{"dir"}
	totalStats, err := vfs.CalculateStats(context.Background(), arcVFS, arcVFS.GetPath(), names, nil)
	if err != nil {
		t.Fatalf("CalculateStats failed: %v", err)
	}

	// Verify scanned stats: 1 dir, 2 files, 30 bytes
	if totalStats.Files != 2 || totalStats.Dirs != 1 || totalStats.Bytes != 30 {
		t.Errorf("Unexpected scanned stats: %+v", totalStats)
	}

	tracker := fileops.NewFileOpTracker(totalStats)

	var bytesReported int64

	mockOriginalReporter := &mockTaskReporter{}

	// We want to verify that when we call CopyBulk, the fileops.GlobalAwareReporter updates the tracker
	// and invokes updateUI, which in turn updates the dialog.
	getGlobalStats := func(action string) (string, int, string) {
		_, totalPct, _ := tracker.GetProgress()
		processed, total := tracker.GetStats()
		totalText := fmt.Sprintf("Total: %d/%d", processed.Bytes, total.Bytes)
		timeSpeedText := fmt.Sprintf("Progress: %d%%", totalPct)
		return totalText, totalPct, timeSpeedText
	}

	wrapRep := fileops.NewGlobalAwareReporter(mockOriginalReporter, getGlobalStats, tracker, func(n int) {
		bytesReported += int64(n)
	})

	// Auto-queue to bypass the interactive UI busy-lock prompt.
	ctx := archive.WithAutoQueue(context.Background())

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
func TestPanelsFrame_MouseForwarding_ToPTY(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	// Setup: hidden panels and mouse tracking enabled in terminal
	pf.ShowPanels = false
	pf.TermView.MouseTrackingMode = 1000
	pf.TermView.MouseSGRMode = true

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

	// terminal.PTY must receive SGR 1006 sequence: \x1b[<0;11;11M (1-based coords)
	expected := "\x1b[<0;11;11M"
	if !strings.Contains(pty.String(), expected) {
		t.Errorf("PTY did not receive expected mouse sequence. Got: %q, want to contain: %q", pty.String(), expected)
	}

	// Normal tracking mode (1000), which htop selects, does not request
	// hover motion from the terminal.
	move := &vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		MouseX:          12,
		MouseY:          11,
		MouseEventFlags: vtinput.MouseMoved,
	}
	beforeMove := pty.String()
	if pf.ProcessMouse(move) {
		t.Fatal("Normal mouse tracking must not capture hover motion")
	}
	if got := pty.String(); got != beforeMove {
		t.Errorf("PTY received hover motion in normal tracking mode: got %q, want %q", got, beforeMove)
	}
}

// Button-event tracking (1002) reports motion only while a button is held.
// A GUI backend delivering hover motion for URL underlining (#459) must not
// leak it into a TUI that asked for 1002 -- xterm would not send it either.
func TestPanelsFrame_MouseForwarding_ButtonEventTracking(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	pf.ShowPanels = false
	pf.TermView.MouseTrackingMode = 1002
	pf.TermView.MouseSGRMode = true

	hover := &vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		MouseX:          12,
		MouseY:          11,
		MouseEventFlags: vtinput.MouseMoved,
	}
	before := pty.String()
	pf.ProcessMouse(hover)
	if got := pty.String(); got != before {
		t.Errorf("PTY received hover motion in button-event tracking mode: got %q, want %q", got, before)
	}

	drag := &vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		MouseX:          12,
		MouseY:          11,
		MouseEventFlags: vtinput.MouseMoved,
		ButtonState:     vtinput.FromLeft1stButtonPressed,
	}
	if !pf.ProcessMouse(drag) {
		t.Fatal("Button-event mouse tracking must capture drag motion")
	}
	if dragExpected := "\x1b[<32;13;12M"; !strings.Contains(pty.String(), dragExpected) {
		t.Errorf("PTY did not receive expected drag sequence. Got: %q, want to contain: %q", pty.String(), dragExpected)
	}
}

func TestPanelsFrame_MouseForwarding_AnyEventTracking(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	pf.ShowPanels = false
	pf.TermView.MouseTrackingMode = 1003
	pf.TermView.MouseSGRMode = true

	move := &vtinput.InputEvent{
		Type:            vtinput.MouseEventType,
		MouseX:          12,
		MouseY:          11,
		MouseEventFlags: vtinput.MouseMoved,
	}
	if !pf.ProcessMouse(move) {
		t.Fatal("Any-event mouse tracking must capture hover motion")
	}
	moveExpected := "\x1b[<35;13;12M"
	if !strings.Contains(pty.String(), moveExpected) {
		t.Errorf("PTY did not receive expected mouse move sequence. Got: %q, want to contain: %q", pty.String(), moveExpected)
	}
}
func TestPanelsFrame_NoCtrlOInterception_InAltScreen(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	pty := pf.Pty.(*mockPty)
	defer pf.Close()

	pf.ShowPanels = false
	pf.TermView.UseAltScreen = true

	// Send Ctrl+O
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_O,
		Char:            15, // Ctrl+O character code
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// Panels must remain hidden (f4 must NOT intercept Ctrl+O when terminal app is active)
	if pf.ShowPanels {
		t.Error("f4 erroneously intercepted Ctrl+O while terminal app was active")
	}

	// terminal.PTY must receive the Ctrl+O byte (\x0f)
	if !strings.Contains(pty.String(), "\x0f") {
		t.Errorf("PTY did not receive Ctrl+O byte. Got: %q", pty.String())
	}
}

type mockTaskReporter struct{}

func (m *mockTaskReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (m *mockTaskReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
}
func (m *mockTaskReporter) IsCancelled() bool { return false }

func TestPanelsFrame_CaptureCommands(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	vtui.SetDefaultPalette()
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	vtui.SetClipboard("")

	cmdStr := "clip:<< echo f4_capture_test"
	if runtime.GOOS == "windows" {
		cmdStr = "clip:<< cmd.exe /c echo f4_capture_test"
	}

	pf.CmdLine.Edit.SetText(cmdStr)
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
	testutil.PumpUntilToastActive(t)
	testutil.WaitForToastExpiry(t, 4*time.Second)
	waitForLoad(t, pf.Panels[0].(*FileSystemPanel))
	waitForLoad(t, pf.Panels[1].(*FileSystemPanel))
}

func TestPanelsFrame_TabWithSinglePanelDoesNotEnablePassivePanel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	// Active = right (1), hide left panel
	pf.ActiveIdx = 1
	pf.ShowLeftPanel = false
	pf.ShowRightPanel = true
	pf.ShowPanels = true

	// Press Tab
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_TAB,
	})

	// Active panel must remain right (1) and left panel must remain hidden
	if pf.ActiveIdx != 1 {
		t.Errorf("Tab moved activeIdx to %d, expected 1", pf.ActiveIdx)
	}
	if pf.ShowLeftPanel {
		t.Error("Tab re-enabled the hidden left panel")
	}
}

func TestPanelsFrame_AICmds(t *testing.T) {
	pf := NewPanelsFrame()
	defer pf.Close()

	handled := pf.HandleCommand(appcmd.CmLeftAIChat, nil)
	// It should gracefully handle these even if no AI panel is there
	if !handled {
		t.Error("CmLeftAIChat should be handled")
	}
}

// TestPanelsFrame_LayoutDecrements_InitFromAppConfig verifies that a
// fresh panel.PanelsFrame picks up saved layout offsets from config.App so a
// restart restores the last on-disk state.
func TestPanelsFrame_LayoutDecrements_InitFromAppConfig(t *testing.T) {
	oldW, oldL, oldR := config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement
	config.App.WidthDecrement = -3
	config.App.LeftHeightDecrement = 4
	config.App.RightHeightDecrement = 5
	defer func() {
		config.App.WidthDecrement, config.App.LeftHeightDecrement, config.App.RightHeightDecrement = oldW, oldL, oldR
	}()

	pf := NewPanelsFrame()
	defer pf.Close()

	if pf.WidthDecrement != -3 || pf.LeftHeightDecrement != 4 || pf.RightHeightDecrement != 5 {
		t.Errorf("init from config.App: got %d/%d/%d, want -3/4/5",
			pf.WidthDecrement, pf.LeftHeightDecrement, pf.RightHeightDecrement)
	}
}

func TestPanelsFrame_ProcessMouse_HoverWheel(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*FileSystemPanel)
	rp := pf.Panels[1].(*FileSystemPanel)

	// Make both panels visible and set right as active
	pf.ActiveIdx = 1
	pf.ShowPanels = true
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true

	// Clear async loading
	if lp.CancelLoad != nil {
		lp.CancelLoad()
	}
	lp.IsLoading = false
	if rp.CancelLoad != nil {
		rp.CancelLoad()
	}
	rp.IsLoading = false

	// Create test entries
	lp.Entries = []*FileEntry{
		{VFSItem: vfs.VFSItem{Name: "L1"}},
		{VFSItem: vfs.VFSItem{Name: "L2"}},
		{VFSItem: vfs.VFSItem{Name: "L3"}},
	}
	lp.Refresh()
	lp.SetCursorIndex(0)

	rp.Entries = []*FileEntry{
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
		MouseX:         testutil.Int16(lx1 + 2),
		MouseY:         testutil.Int16(ly1 + 2),
		WheelDirection: -1, // Down scroll -> should move cursor down
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel event was not handled")
	}

	// Active panel should remain right (1)
	if pf.ActiveIdx != 1 {
		t.Errorf("Expected active panel to remain 1, got %d", pf.ActiveIdx)
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
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*FileSystemPanel)
	rp := pf.Panels[1].(*FileSystemPanel)

	// Make both panels visible and set right as active
	pf.ActiveIdx = 1
	pf.ShowPanels = true
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true

	// Clear async loading
	if lp.CancelLoad != nil {
		lp.CancelLoad()
	}
	lp.IsLoading = false
	if rp.CancelLoad != nil {
		rp.CancelLoad()
	}
	rp.IsLoading = false

	// Add an panel.AltPanel (panel.QuickViewPanel) on the Left (0) slot
	qv := NewQuickViewPanel(lp)
	pf.AltPanels[0] = qv

	lx1, ly1, _, _ := lp.GetPosition()

	// Simulate wheel over the left slot (where QuickView is)
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         testutil.Int16(lx1 + 2),
		MouseY:         testutil.Int16(ly1 + 2),
		WheelDirection: -1, // Down scroll
	}

	handled := pf.ProcessMouse(ev)
	if !handled {
		t.Fatal("Mouse wheel over AltPanel not handled")
	}
}
func TestPanelsFrame_ProcessMouse_HoverWheel_Medium_Boundaries(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*FileSystemPanel)
	rp := pf.Panels[1].(*FileSystemPanel)

	// Set left panel as active, medium view mode
	pf.ActiveIdx = 0
	pf.ShowPanels = true
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true

	// Clear async loading
	if lp.CancelLoad != nil {
		lp.CancelLoad()
	}
	lp.IsLoading = false
	if rp.CancelLoad != nil {
		rp.CancelLoad()
	}
	rp.IsLoading = false

	// Create 45 items to fill multiple columns
	var entries []*FileEntry
	for i := 0; i < 45; i++ {
		entries = append(entries, &FileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("F%d", i)}})
	}
	lp.Entries = entries
	lp.Refresh()

	H := lp.Table.ViewHeight
	if H <= 0 {
		H = 1
	}

	// Set cursor to the first row of the second column (idx = H)
	lp.SetCursorIndex(H)

	// TopPos should be 0
	lp.Table.TopPos = 0
	lp.Refresh()

	if lp.GetCursorIndex() != H {
		t.Fatalf("Setup failed: expected cursor index %d, got %d", H, lp.GetCursorIndex())
	}

	// 1. Simulate mouse wheel UP over the left panel
	lx1, ly1, _, _ := lp.GetPosition()
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         testutil.Int16(lx1 + 2),
		MouseY:         testutil.Int16(ly1 + 2),
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
	theme.SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	lp := pf.Panels[0].(*FileSystemPanel)
	rp := pf.Panels[1].(*FileSystemPanel)

	// Set left panel as active, detailed view mode
	pf.ActiveIdx = 0
	pf.ShowPanels = true
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true

	// Clear async loading
	if lp.CancelLoad != nil {
		lp.CancelLoad()
	}
	lp.IsLoading = false
	if rp.CancelLoad != nil {
		rp.CancelLoad()
	}
	rp.IsLoading = false

	lp.SetViewMode(ViewModeDetailed)

	H := lp.Table.ViewHeight
	if H <= 0 {
		H = 1
	}

	// Create items to fill more than screen height
	var entries []*FileEntry
	for i := 0; i < H+10; i++ {
		entries = append(entries, &FileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("F%d", i)}})
	}
	lp.Entries = entries
	lp.Refresh()

	totalItems := len(lp.Entries)

	// Set cursor to the last item
	lp.SetCursorIndex(totalItems - 1)
	lp.Refresh()

	lastIdx := lp.GetCursorIndex()

	// 1. Simulate mouse wheel DOWN over the left panel
	lx1, ly1, _, _ := lp.GetPosition()
	ev := &vtinput.InputEvent{
		Type:           vtinput.MouseEventType,
		MouseX:         testutil.Int16(lx1 + 2),
		MouseY:         testutil.Int16(ly1 + 2),
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
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.WheelPanelUp = 2
	config.App.WheelPanelDown = 3

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	p := pf.Panels[0].(*FileSystemPanel)
	if p.CancelLoad != nil {
		p.CancelLoad()
	}
	p.IsLoading = false

	for i := 0; i < 10; i++ {
		p.Entries = append(p.Entries, &FileEntry{VFSItem: vfs.VFSItem{Name: fmt.Sprintf("f%d", i)}})
	}
	p.Refresh()
	p.SetCursorIndex(0)

	x1, y1, _, _ := p.GetPosition()
	wheel := func(dir int) {
		ev := &vtinput.InputEvent{
			Type:           vtinput.MouseEventType,
			MouseX:         testutil.Int16(x1 + 2),
			MouseY:         testutil.Int16(y1 + 2),
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

func TestPanelsFrame_SyncPassivePanel(t *testing.T) {
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	pf := &PanelsFrame{}
	lp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(sub))
	rp := NewFileSystemPanel(40, 0, 40, 20, vfs.NewOSVFS(tmpDir))
	pf.Panels[0] = lp
	pf.Panels[1] = rp
	pf.ActiveIdx = 0
	defer pf.Close()

	waitForLoad(t, lp)
	waitForLoad(t, rp)

	if !pf.SyncPassivePanel() {
		t.Fatal("syncPassivePanel returned false for a differing passive panel")
	}
	waitForLoad(t, rp)
	if got, want := filepath.Clean(rp.Vfs.GetPath()), filepath.Clean(sub); got != want {
		t.Fatalf("passive panel path = %q, want %q", got, want)
	}
	if got, want := filepath.Clean(lp.Vfs.GetPath()), filepath.Clean(sub); got != want {
		t.Fatalf("active panel moved to %q, want %q", got, want)
	}

	// Already in sync: nothing to do.
	if pf.SyncPassivePanel() {
		t.Fatal("syncPassivePanel should be a no-op when both panels show the same directory")
	}
}
