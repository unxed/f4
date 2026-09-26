package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// mockMetadataVFS allows intercepting Stat and SetAttributes for testing.
type mockMetadataVFS struct {
	vfs.VFS
	onSetAttr     func(vfs.VFSItem)
	onSetAttrPath func(string, vfs.VFSItem)
	statErr       error
	setAttrErr    error
	statToReturn  vfs.VFSItem
}

type lstatMetadataVFS struct {
	*mockMetadataVFS
	item vfs.VFSItem
}

type symlinkTargetVFS struct {
	*vfs.OSVFS
	failTarget  string
	removeCalls int
	symlinkArgs []string
}

func (v *symlinkTargetVFS) Remove(ctx context.Context, path string) error {
	v.removeCalls++
	return v.OSVFS.Remove(ctx, path)
}

func (v *symlinkTargetVFS) Symlink(ctx context.Context, target, linkPath string) error {
	v.symlinkArgs = append(v.symlinkArgs, target)
	if target == v.failTarget {
		return errors.New("test symlink failure")
	}
	return v.OSVFS.Symlink(ctx, target, linkPath)
}

func (m *lstatMetadataVFS) Lstat(context.Context, string) (vfs.VFSItem, error) {
	return m.item, nil
}

func (m *mockMetadataVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	if m.statErr != nil {
		return vfs.VFSItem{}, m.statErr
	}
	if m.statToReturn.Name != "" {
		return m.statToReturn, nil
	}
	return m.VFS.Stat(ctx, path)
}

func (m *mockMetadataVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	if m.setAttrErr != nil {
		return m.setAttrErr
	}
	if m.onSetAttr != nil {
		m.onSetAttr(item)
	}
	if m.onSetAttrPath != nil {
		m.onSetAttrPath(path, item)
	}
	return nil
}

func TestAttributesDialog_StatFailure(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{
		VFS:     vfs.NewOSVFS(t.TempDir()),
		statErr: os.ErrPermission,
	}
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "locked.txt"}}}
	fsp.Vfs = mockVFS

	// This should trigger an async Stat call that fails
	actionFileAttributes(pf)

	// Pump tasks and wait for the error dialog
	timeout := time.After(1 * time.Second)
	foundDialog := false
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if fm.GetTopFrameType() == vtui.TypeDialog && strings.Contains(fm.GetTopFrame().GetTitle(), "Error") {
				foundDialog = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}

	if !foundDialog {
		t.Error("Expected an error dialog when initial Stat fails")
	}
}

func TestActionFileAttributes_UsesLstat(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	base := &mockMetadataVFS{
		VFS:     vfs.NewOSVFS(t.TempDir()),
		statErr: os.ErrPermission,
	}
	mockVFS := &lstatMetadataVFS{
		mockMetadataVFS: base,
		item: vfs.VFSItem{
			Name:     "link.txt",
			UnixMode: 0o777,
			MTime:    time.Now(),
		},
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 30)
	fsp := pf.GetActivePanel()
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "link.txt"}}}
	fsp.Vfs = mockVFS

	actionFileAttributes(pf)

	deadline := time.After(time.Second)
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			top := fm.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Attributes") {
				top.SetExitCode(-1)
				fm.Pop()
				return
			}
		case <-deadline:
			top := fm.GetTopFrame()
			if top == nil {
				t.Fatal("attributes dialog was not shown")
			}
			t.Fatalf("attributes dialog was not shown; top frame is %q", top.GetTitle())
		}
	}
}

func TestAttributesDialog_SetAttributesFailure(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{
		VFS:        vfs.NewOSVFS(t.TempDir()),
		setAttrErr: os.ErrPermission,
	}

	item := vfs.VFSItem{Name: "file.txt"}
	dialog.ShowAttributesUnix(nil, mockVFS, "/file.txt", item)
	attrDlg := fm.GetTopFrame()

	var btnSet *vtui.Button
	walkUI(attrDlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
			return false
		}
		return true
	})

	if btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	timeout := time.After(1 * time.Second)
	errorDialogShown := false
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			// Check if an error dialog appeared ON TOP of the attributes dialog
			if fm.GetTopFrame() != attrDlg && fm.GetTopFrameType() == vtui.TypeDialog {
				errorDialogShown = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}

	if !errorDialogShown {
		t.Error("Expected an error dialog when SetAttributes fails")
	}
	if attrDlg.IsDone() {
		t.Error("Attributes dialog should remain open after a SetAttributes failure")
	}
}

func TestAttributesDialog_UnixSetAll(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var capturedItem vfs.VFSItem
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}
	item := vfs.VFSItem{Name: "test.sh", Uid: 1000, Gid: 1000, UnixMode: 0644, MTime: time.Now()}

	dialog.ShowAttributesUnix(nil, mockVFS, "test.sh", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editOwner, editGroup, editOctal, editMTime *vtui.Edit
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok {
			if e.Validator != nil {
				editOctal = e
			} else if editOwner == nil {
				editOwner = e // First edit is owner
			} else if editGroup == nil {
				editGroup = e // Second is group
			} else {
				editMTime = e
			}
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	// Change values
	newTime := "01.02.2030 10:20:30"
	editOwner.SetText("root")
	editGroup.SetText("wheel")
	editOctal.SetText("0755")
	editMTime.SetText(newTime)
	// Trigger octal -> checkbox sync
	editOctal.OnTextChange("0755")

	// Click "Set"
	if btnSet != nil && btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)

	if capturedItem.UnixMode != 0755 {
		t.Errorf("Unix mode not set. Expected 0755, got %04o", capturedItem.UnixMode)
	}
	expectedTime, _ := time.ParseInLocation("02.01.2006 15:04:05", newTime, time.Local)
	if !capturedItem.MTime.Equal(expectedTime) {
		t.Errorf("MTime not set. Expected %v, got %v", expectedTime, capturedItem.MTime)
	}
	// Verify that the VFS received the correct data from UI strings
	if capturedItem.UnixMode != 0755 {
		t.Errorf("Unix mode not set. Expected 0755, got %04o", capturedItem.UnixMode)
	}
}

func TestAttributesDialog_WindowsSetFlags(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var capturedItem vfs.VFSItem
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}

	item := vfs.VFSItem{Name: "win.exe", MTime: time.Now()}
	dialog.ShowAttributesWindows(nil, mockVFS, "win.exe", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var chkRO, chkHidden *vtui.Checkbox
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if c, ok := el.(*vtui.Checkbox); ok {
			if strings.Contains(c.GetText(), "Read only") {
				chkRO = c
			}
			if strings.Contains(c.GetText(), "Hidden") {
				chkHidden = c
			}
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	// Toggle checkboxes
	chkRO.State = 1
	chkHidden.State = 1

	if btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)

	// Verify the VFS call was made
	if capturedItem.Name == "" {
		t.Error("SetAttributes was not called after clicking Save in Windows attributes dialog")
	}
}

func TestAttributesDialog_WindowsSetFlagsForSelectedTargets(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	type attrCall struct {
		path string
		item vfs.VFSItem
	}
	var calls []attrCall
	mockVFS := &mockMetadataVFS{
		VFS: vfs.NewOSVFS(t.TempDir()),
		onSetAttrPath: func(path string, item vfs.VFSItem) {
			calls = append(calls, attrCall{path: path, item: item})
		},
	}
	targets := []dialog.AttributesTarget{
		{Path: "first.txt", Item: vfs.VFSItem{Name: "first.txt", WinAttrs: 0x10 | 1, MTime: time.Now()}},
		{Path: "second.txt", Item: vfs.VFSItem{Name: "second.txt", WinAttrs: 0x10 | 2, MTime: time.Now().Add(-time.Hour)}},
	}

	dialog.ShowAttributesWindowsForTargets(nil, mockVFS, targets)
	dlg := fm.GetTopFrame().(vtui.Container)
	var chkRO, chkHidden *vtui.Checkbox
	var setButton *vtui.Button
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if c, ok := el.(*vtui.Checkbox); ok {
			switch {
			case strings.Contains(c.GetText(), "Read only"):
				chkRO = c
			case strings.Contains(c.GetText(), "Hidden"):
				chkHidden = c
			}
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			setButton = b
		}
		return true
	})
	if chkRO == nil || chkHidden == nil || setButton == nil {
		t.Fatal("Windows multi-target dialog controls not found")
	}
	chkRO.State = 0
	chkHidden.State = 1
	setButton.OnClick()

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)
	if len(calls) != 2 {
		t.Fatalf("SetAttributes called %d times, want 2", len(calls))
	}
	for _, call := range calls {
		if call.item.WinAttrs != 0x12 {
			t.Errorf("%s attributes = %#x, want %#x", call.path, call.item.WinAttrs, uint32(0x12))
		}
	}
}

func TestActionFileAttributesUsesAllSelectedEntries(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	base := t.TempDir()
	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	mockVFS := &mockMetadataVFS{
		VFS:           vfs.NewOSVFS(base),
		onSetAttrPath: func(path string, _ vfs.VFSItem) { calls = append(calls, path) },
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.GetActivePanel()
	fsp.Vfs = mockVFS
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "first.txt"}},
		{VFSItem: vfs.VFSItem{Name: "second.txt"}},
	}
	fsp.SetCursorIndex(0)
	fsp.SetItemSelected(0, true)
	fsp.SetItemSelected(1, true)

	actionFileAttributes(pf)
	runUITasksUntil(t, fm.TaskChan, func() bool {
		top := fm.GetTopFrame()
		return top != nil && strings.Contains(top.GetTitle(), "Attributes")
	})
	attributesDialog := fm.GetTopFrame()
	var setButton *vtui.Button
	if top := fm.GetTopFrame(); top != nil {
		if element, ok := top.(vtui.UIElement); ok {
			walkUI(element, func(el vtui.UIElement) bool {
				if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
					setButton = b
				}
				return true
			})
		}
	}
	if setButton == nil {
		t.Fatal("attributes dialog did not open for selected entries")
	}
	setButton.OnClick()
	runUITasksUntil(t, fm.TaskChan, attributesDialog.IsDone)
	if len(calls) != 2 {
		t.Fatalf("SetAttributes called for %d selected entries, want 2: %v", len(calls), calls)
	}
	if !strings.HasSuffix(calls[0], "first.txt") || !strings.HasSuffix(calls[1], "second.txt") {
		t.Errorf("SetAttributes paths = %v, want first.txt then second.txt", calls)
	}
}

func TestAttributesDialog_InvalidTime(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	item := vfs.VFSItem{Name: "file.txt", MTime: time.Now()}

	dialog.ShowAttributesUnix(nil, mockVFS, "file.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editMTime *vtui.Edit
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok && strings.Contains(e.GetText(), ":") {
			editMTime = e
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	// Enter completely broken date
	editMTime.SetText("99.99.9999 25:61:99")

	if btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	// f4#1404: an unparseable date must not be silently dropped. The
	// attributes dialog stays open (it never closes on its own here, so
	// there is nothing to wait for) and an error dialog appears over it.
	if dlg.(vtui.Frame).IsDone() {
		t.Error("Dialog should not close when date is invalid")
	}
	if top := fm.GetTopFrame(); any(top) == any(dlg) {
		t.Error("an error dialog should have appeared over the attributes dialog")
	}
}

func runUITasksUntil(t *testing.T, taskChan <-chan func(), done func() bool) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for !done() {
		select {
		case task := <-taskChan:
			task()
		case <-timeout:
			t.Fatal("timeout waiting for UI task")
		}
	}
}

func TestAttributesDialog_Layout(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	v := vfs.NewOSVFS(".")
	item := vfs.VFSItem{Name: "test.txt", Uid: 1000, Gid: 1000, UnixMode: 0644}

	// We test only Unix layout in this env, but it proves the engine works
	dialog.ShowAttributesUnix(nil, v, "test.txt", item)

	top := vtui.FrameManager.GetTopFrame()
	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatal("Dialog not found on top")
	}

	vtui.AssertLayout(t, dlg)
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestAttributesDialog_WindowsCheckboxes(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var capturedItem vfs.VFSItem
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}

	// WinAttrs: 1 (ReadOnly) | 32 (Archive) = 33
	item := vfs.VFSItem{Name: "win.exe", WinAttrs: 33}
	dialog.ShowAttributesWindows(nil, mockVFS, "win.exe", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var chkRO, chkHD, chkSY, chkAR *vtui.Checkbox
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if c, ok := el.(*vtui.Checkbox); ok {
			if strings.Contains(c.GetText(), "Read only") {
				chkRO = c
			}
			if strings.Contains(c.GetText(), "Hidden") {
				chkHD = c
			}
			if strings.Contains(c.GetText(), "System") {
				chkSY = c
			}
			if strings.Contains(c.GetText(), "Archive") {
				chkAR = c
			}
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	if chkRO.State != 1 || chkHD.State != 0 || chkSY.State != 0 || chkAR.State != 1 {
		t.Errorf("Initial checkboxes state from WinAttrs is incorrect")
	}

	// Change state
	chkRO.State = 0
	chkHD.State = 1

	if btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)

	// New WinAttrs should have Hidden (2) and Archive (32), but NOT ReadOnly (1). Total 34.
	if capturedItem.WinAttrs != 34 {
		t.Errorf("WinAttrs not updated correctly. Expected 34, got %d", capturedItem.WinAttrs)
	}
}

func TestAttributesDialog_UnixSync(t *testing.T) {
	vtui.SetDefaultPalette()
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	v := vfs.NewOSVFS(".")
	item := vfs.VFSItem{Name: "test.txt", UnixMode: 0644} // rw-r--r--

	dialog.ShowAttributesUnix(nil, v, "test.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editOct *vtui.Edit
	var checkUserRead *vtui.Checkbox

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if c, ok := el.(*vtui.Checkbox); ok && strings.Contains(c.GetText(), "Read") && checkUserRead == nil {
			checkUserRead = c
		}
		if e, ok := el.(*vtui.Edit); ok && e.GetText() == "0644" {
			editOct = e
		}
		return true
	})

	if editOct == nil || checkUserRead == nil {
		t.Fatal("Required UI elements for syncing test not found")
	}

	// 1. CheckBox -> Edit Sync
	// Uncheck 'Read' for user (bit 0400)
	checkUserRead.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_SPACE})

	// 0644 - 0400 = 0244
	if editOct.GetText() != "0244" {
		t.Errorf("Checkbox to Octal sync failed. Expected '0244', got %q", editOct.GetText())
	}

	// 2. Edit -> CheckBox Sync
	// Set mode to 0777 (all checked)
	editOct.SetText("0777")
	if editOct.OnTextChange != nil {
		editOct.OnTextChange("0777")
	}

	if checkUserRead.State != 1 {
		t.Error("Octal to Checkbox sync failed: Read box should be checked for 0777")
	}

	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}

func TestAttributesDialog_Validation(t *testing.T) {
	vtui.SetDefaultPalette()
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(".")
	item := vfs.VFSItem{Name: "test", UnixMode: 0644}

	dialog.ShowAttributesUnix(nil, v, "test", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editOct *vtui.Edit
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok {
			// Octal field is the only one with OctalValidator
			if _, ok := e.Validator.(*vtui.OctalValidator); ok {
				editOct = e
				return false
			}
		}
		return true
	})

	if editOct == nil {
		t.Fatal("Octal edit field not found")
	}

	// Try to type invalid octal digit '8'
	oldText := editOct.GetText()
	// ProcessKey returns true because it handled (swallowed) the invalid character
	_ = editOct.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '8'})

	if editOct.GetText() != oldText {
		t.Errorf("Octal field accepted invalid digit '8'. Text is now %q", editOct.GetText())
	}

	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}

func TestAttributesDialog_SetFlow(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	baseVfs := vfs.NewOSVFS(tmpDir)
	var capturedItem vfs.VFSItem
	mock := &mockMetadataVFS{
		VFS:       baseVfs,
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}

	item := vfs.VFSItem{Name: "file.txt", UnixMode: 0644, Uid: 10, Gid: 10}

	dialog.ShowAttributesUnix(nil, mock, path, item)
	frame := fm.GetTopFrame()
	dlg := frame.(vtui.Container)

	var editOwner *vtui.Edit
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok {
			// Owner edit is at the top and doesn't have any validators attached
			if e.Validator == nil && editOwner == nil {
				editOwner = e
			}
		}
		if b, ok := el.(*vtui.Button); ok {
			clean, _, _ := vtui.ParseAmpersandString(b.GetText())
			if strings.Contains(clean, "Set") {
				btnSet = b
			}
		}
		return true
	})

	if editOwner == nil || btnSet == nil {
		t.Fatal("Required UI elements (Owner edit or Set button) not found")
	}

	// 1. Change values in UI
	editOwner.SetText("2000")

	// 2. Click Set
	if btnSet.OnClick != nil {
		btnSet.OnClick()
	}

	// 3. Pump tasks
	timeout := time.After(5 * time.Second)
Loop:
	for {
		if frame.IsDone() {
			break Loop
		}
		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for Set task")
		}
	}

	if capturedItem.Uid != 2000 {
		t.Errorf("Attribute update failed. Expected UID 2000, got %d", capturedItem.Uid)
	}
}

func TestAttributesDialog_WindowsLayout(t *testing.T) {
	vtui.SetDefaultPalette()
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)

	v := vfs.NewOSVFS(".")
	item := vfs.VFSItem{Name: "winfile.exe", MTime: time.Now()}

	dialog.ShowAttributesWindows(nil, v, "winfile.exe", item)

	top := fm.GetTopFrame()
	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatal("Windows attributes dialog not found")
	}

	vtui.AssertLayout(t, dlg)
	top.SetExitCode(-1)
	fm.Pop()
}
func TestAttributesDialog_UnixNameResolution(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var capturedItem vfs.VFSItem
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}

	// Initial item with different IDs
	item := vfs.VFSItem{Name: "file", Uid: 10, Gid: 10}
	dialog.ShowAttributesUnix(nil, mockVFS, "file", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editOwner, editGroup *vtui.Edit
	var btnSet *vtui.Button

	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok {
			if editOwner == nil {
				editOwner = e
			} else if editGroup == nil {
				editGroup = e
			}
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	// Enter numeric IDs as strings (guaranteed to resolve via strconv.Atoi in OnClick)
	editOwner.SetText("2000")
	editGroup.SetText("3000")

	btnSet.OnClick()

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)

	if capturedItem.Uid != 2000 || capturedItem.Gid != 3000 {
		t.Errorf("ID resolution failed. Got UID:%d GID:%d", capturedItem.Uid, capturedItem.Gid)
	}
}

func TestAttributesDialog_Truncation(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(".")

	longName := "this_is_a_very_long_filename_that_should_definitely_be_truncated_by_the_attributes_dialog_header_logic.txt"
	item := vfs.VFSItem{Name: longName}

	dialog.ShowAttributesUnix(nil, v, longName, item)
	dlg := fm.GetTopFrame().(vtui.Container)

	foundTruncated := false
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if txt, ok := el.(*vtui.Text); ok {
			if strings.Contains(txt.GetText(), "...") {
				foundTruncated = true
				return false
			}
		}
		return true
	})

	if !foundTruncated {
		t.Error("Long filename was not truncated in the dialog header")
	}
	fm.GetTopFrame().SetExitCode(-1)
}

func TestAttributesDialog_Cancel(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	called := false
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { called = true },
	}

	dialog.ShowAttributesUnix(nil, mockVFS, "test", vfs.VFSItem{Name: "test"})
	dlg := fm.GetTopFrame().(vtui.Container)

	var btnCancel *vtui.Button
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Cancel") {
			btnCancel = b
			return false
		}
		return true
	})

	btnCancel.OnClick()

	if !fm.GetTopFrame().IsDone() {
		t.Error("Dialog did not close after clicking Cancel")
	}

	// Process any pending tasks to ensure onSetAttr is NOT called
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			goto done
		}
	}
done:
	if called {
		t.Error("SetAttributes was called despite clicking Cancel")
	}
}

func TestShowAttributesDialog_Dispatch(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Create mock VFS with Unix permissions capability
	mockUnixVFS := &mockMetadataVFS{
		VFS: vfs.NewOSVFS(t.TempDir()),
	}

	item := vfs.VFSItem{Name: "test"}
	dialog.ShowAttributesDialog(nil, mockUnixVFS, "test", item)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Error("Dialog not shown")
	} else {
		top.SetExitCode(-1)
		vtui.FrameManager.Pop()
	}
}
func TestAttributesDialog_SymlinkUsesLinkMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink metadata requires privileges on Windows")
	}

	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	targetName := "target-file.txt"
	targetPath := filepath.Join(tmpDir, targetName)
	if err := os.WriteFile(targetPath, make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetTime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.Local)
	if err := os.Chtimes(targetPath, targetTime, targetTime); err != nil {
		t.Fatalf("set target timestamps: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(targetName, linkPath); err != nil {
		t.Skipf("Symlink creation not supported: %v", err)
	}

	v := vfs.NewOSVFS(tmpDir)
	targetItem, err := v.Stat(context.Background(), linkPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if targetItem.Size == linkInfo.Size() || targetItem.UnixMode == uint32(linkInfo.Mode().Perm()) || targetItem.MTime.Equal(linkInfo.ModTime()) {
		t.Fatalf("test setup did not distinguish target and link metadata: target=%+v link=%+v", targetItem, linkInfo)
	}

	dialog.ShowAttributesDialog(nil, v, linkPath, targetItem)
	dlg := fm.GetTopFrame().(vtui.Container)

	wantMode := fmt.Sprintf("%04o", linkInfo.Mode().Perm())
	wantMTime := linkInfo.ModTime().Format("02.01.2006 15:04:05")
	found := map[string]bool{}
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if control, ok := el.(*vtui.Edit); ok {
			found[control.GetText()] = true
		}
		return true
	})

	for _, want := range []string{targetName, wantMode, wantMTime} {
		if !found[want] {
			t.Errorf("link property %q not found in attributes dialog", want)
		}
	}
	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}

func TestAttributesDialog_SymlinkToDirectoryIsIdentifiedAsLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink metadata requires privileges on Windows")
	}

	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	targetName := "target-dir"
	if err := os.Mkdir(filepath.Join(tmpDir, targetName), 0o700); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "link-dir")
	if err := os.Symlink(targetName, linkPath); err != nil {
		t.Skipf("Symlink creation not supported: %v", err)
	}

	v := vfs.NewOSVFS(tmpDir)
	targetItem, err := v.Stat(context.Background(), linkPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !targetItem.IsDir {
		t.Fatal("test setup: symlink target is not reported as a directory")
	}

	dialog.ShowAttributesDialog(nil, v, linkPath, targetItem)
	dlg := fm.GetTopFrame().(vtui.Container)
	foundTarget := false
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if control, ok := el.(*vtui.Edit); ok {
			foundTarget = foundTarget || control.GetText() == targetName
		}
		return true
	})

	if !foundTarget {
		t.Error("symlink-to-directory Target edit is not present with the link target")
	}
	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}

func TestReplaceSymlinkTargetRestoresOriginalOnCreateFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink replacement test requires symlink privileges on Windows")
	}

	root := t.TempDir()
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink("old-target", linkPath); err != nil {
		t.Skipf("Symlink creation not supported: %v", err)
	}
	v := &symlinkTargetVFS{OSVFS: vfs.NewOSVFS(root), failTarget: "new-target"}

	err := dialog.ReplaceSymlinkTarget(context.Background(), v, linkPath, "new-target")
	if err == nil || !strings.Contains(err.Error(), "original target restored") {
		t.Fatalf("replaceSymlinkTarget error = %v, want restored-target error", err)
	}
	got, readErr := os.Readlink(linkPath)
	if readErr != nil {
		t.Fatalf("read restored link: %v", readErr)
	}
	if got != "old-target" {
		t.Fatalf("restored target = %q, want %q", got, "old-target")
	}
	if v.removeCalls != 1 || strings.Join(v.symlinkArgs, ",") != "new-target,old-target" {
		t.Fatalf("replacement calls: removes=%d symlinks=%v, want one remove and create/restore", v.removeCalls, v.symlinkArgs)
	}
}

func TestReplaceSymlinkTargetPreservesTargetSpelling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink replacement test requires symlink privileges on Windows")
	}

	root := t.TempDir()
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink("initial-target", linkPath); err != nil {
		t.Skipf("Symlink creation not supported: %v", err)
	}
	v := &symlinkTargetVFS{OSVFS: vfs.NewOSVFS(root)}

	targets := []string{"relative-target", filepath.Join(root, "absolute-target"), "missing-target"}
	for _, want := range targets {
		if err := dialog.ReplaceSymlinkTarget(context.Background(), v, linkPath, want); err != nil {
			t.Fatalf("replaceSymlinkTarget(%q): %v", want, err)
		}
		got, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("read link after %q: %v", want, err)
		}
		if got != want {
			t.Fatalf("target after %q = %q, want exact spelling %q", want, got, want)
		}
	}
}

func TestReplaceSymlinkTargetRejectsEmptyTargetWithoutMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink replacement test requires symlink privileges on Windows")
	}

	root := t.TempDir()
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink("old-target", linkPath); err != nil {
		t.Skipf("Symlink creation not supported: %v", err)
	}
	v := &symlinkTargetVFS{OSVFS: vfs.NewOSVFS(root)}

	if err := dialog.ReplaceSymlinkTarget(context.Background(), v, linkPath, ""); err == nil {
		t.Fatal("replaceSymlinkTarget accepted an empty target")
	}
	got, readErr := os.Readlink(linkPath)
	if readErr != nil {
		t.Fatalf("read unchanged link: %v", readErr)
	}
	if got != "old-target" || v.removeCalls != 0 {
		t.Fatalf("empty-target edit mutated link: target=%q removes=%d", got, v.removeCalls)
	}
}

func TestAttributesDialog_WindowsSetTime(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	var capturedItem vfs.VFSItem
	mockVFS := &mockMetadataVFS{
		VFS:       vfs.NewOSVFS(t.TempDir()),
		onSetAttr: func(item vfs.VFSItem) { capturedItem = item },
	}

	oldTime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.Local)
	item := vfs.VFSItem{Name: "winfile", MTime: oldTime}

	dialog.ShowAttributesWindows(nil, mockVFS, "winfile", item)
	dlg := fm.GetTopFrame().(vtui.Container)

	var editTime *vtui.Edit
	var btnSet *vtui.Button
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok {
			editTime = e
		}
		if b, ok := el.(*vtui.Button); ok && strings.Contains(b.GetText(), "Set") {
			btnSet = b
		}
		return true
	})

	newTimeStr := "15.05.2025 10:00:00"
	editTime.SetText(newTimeStr)
	btnSet.OnClick()

	runUITasksUntil(t, fm.TaskChan, dlg.(vtui.Frame).IsDone)

	expected, _ := time.ParseInLocation("02.01.2006 15:04:05", newTimeStr, time.Local)
	if !capturedItem.MTime.Equal(expected) {
		t.Errorf("Windows MTime update failed. Expected %v, got %v", expected, capturedItem.MTime)
	}
}
func TestAttributesDialog_SecurityButton(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	// 1. Test local VFS -> button should be enabled
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "winfile.exe"), nil, 0o600); err != nil {
		t.Fatalf("create local properties target: %v", err)
	}
	mockVFS := &mockMetadataVFS{
		VFS: vfs.NewOSVFS(root),
	}
	item := vfs.VFSItem{Name: "winfile.exe", MTime: time.Now()}
	var openedPropertiesPath string
	dialog.ShowAttributesWindowsWithProperties(nil, mockVFS, "winfile.exe", item, func(path string) error {
		openedPropertiesPath = path
		return nil
	})
	dlg := fm.GetTopFrame().(vtui.Container)

	var btnSec *vtui.Button
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if b, ok := el.(*vtui.Button); ok {
			clean, _, _ := vtui.ParseAmpersandString(b.GetText())
			if strings.Contains(clean, "Security") {
				btnSec = b
			}
		}
		return true
	})

	if btnSec == nil {
		t.Fatal("Security button not found in Windows attributes dialog")
	}

	if btnSec.IsDisabled() {
		t.Error("Security button should be enabled for local OSVFS files")
	}

	// Exercise the callback through a stub. Calling the real Windows shell here
	// used to race t.TempDir cleanup and leave a native "file not found" dialog
	// on the developer's desktop after the test had passed.
	if btnSec.OnClick != nil {
		btnSec.OnClick()
	}
	expectedPropertiesPath, err := mockVFS.Abs("winfile.exe")
	if err != nil {
		t.Fatalf("resolve properties path: %v", err)
	}
	if openedPropertiesPath != expectedPropertiesPath {
		t.Errorf("properties path = %q, want %q", openedPropertiesPath, expectedPropertiesPath)
	}

	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()

	// 2. Test non-local VFS (e.g. NullVFS) -> button should be disabled
	mockNullVFS := vfs.NewNullVFS(0)
	dialog.ShowAttributesWindows(nil, mockNullVFS, "winfile.exe", item)
	dlgNull := fm.GetTopFrame().(vtui.Container)

	var btnSecNull *vtui.Button
	walkUI(dlgNull.(vtui.UIElement), func(el vtui.UIElement) bool {
		if b, ok := el.(*vtui.Button); ok {
			clean, _, _ := vtui.ParseAmpersandString(b.GetText())
			if strings.Contains(clean, "Security") {
				btnSecNull = b
			}
		}
		return true
	})

	if btnSecNull == nil {
		t.Fatal("Security button not found in non-local Windows attributes dialog")
	}

	if !btnSecNull.IsDisabled() {
		t.Error("Security button should be disabled for non-local VFS files")
	}

	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}
func TestDialogTaskPump_OverlayResilience(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	// 1. Push target dialog
	target := vtui.NewCenteredDialog(40, 10, "Target")
	fm.Push(target)

	// 2. Push overlay window on top (simulating autocomplete)
	overlay := vtui.NewCenteredDialog(20, 5, "Autocomplete")
	fm.Push(overlay)

	// 3. Process task queue with a timeout.
	// We close the target dialog in a task.
	fm.PostTask(func() {
		target.Close()
	})

	timeout := time.After(2 * time.Second)
	for !target.IsDone() {

		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for target dialog close with overlay present")
		}
	}

	// Target is closed, but overlay is still open and on top of the stack.
	// If the loop was based on GetTopFrame().IsDone(), it would have timed out.
	if fm.GetTopFrame() != overlay {
		t.Error("Overlay should still be on top of the stack")
	}
}

// walkUI is a local helper to find elements in nested containers
func walkUI(el vtui.UIElement, fn func(vtui.UIElement) bool) bool {
	if !fn(el) {
		return false
	}
	if c, ok := el.(vtui.Container); ok {
		for _, child := range c.GetChildren() {
			if !walkUI(child, fn) {
				return false
			}
		}
	}
	return true
}
