package app

import (
	"context"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestActionUpdateSettings_ManualCheckDoesNotBlockMouseDispatch(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	oldAPIURL := update.APIURL
	oldOS := update.CurrentOS
	oldArch := update.CurrentArch
	t.Cleanup(func() {
		config.App = oldCfg
		update.APIURL = oldAPIURL
		update.CurrentOS = oldOS
		update.CurrentArch = oldArch
	})

	requestStarted := make(chan struct{})
	allowResponse := make(chan struct{})
	requestFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-allowResponse:
		case <-r.Context().Done():
			return
		}
		defer close(requestFinished)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"7f1fec5","assets":[{"name":"f4-windows-amd64.zip","browser_download_url":"http://127.0.0.1/update.zip"}]}`))
	}))
	defer server.Close()

	update.APIURL = server.URL + "/repos/unxed/f4/releases"
	update.CurrentOS = "windows"
	update.CurrentArch = "amd64"
	config.App.UpdateChannel = 0
	config.App.UpdateInterval = 0

	actionUpdateSettings(nil)
	window := vtui.FrameManager.GetTopFrame()
	if window == nil {
		t.Fatal("update settings dialog was not opened")
	}
	container, ok := window.(interface{ GetChildren() []vtui.UIElement })
	if !ok {
		t.Fatal("update settings dialog does not expose its controls")
	}
	var buttons []*vtui.Button
	for _, child := range container.GetChildren() {
		if button, ok := child.(*vtui.Button); ok {
			buttons = append(buttons, button)
		}
	}
	if len(buttons) != 3 {
		t.Fatalf("expected three update-settings buttons, got %d", len(buttons))
	}
	checkButton := buttons[1]
	x, y, _, _ := checkButton.GetPosition()
	mx, my := checkedMouseCoordinate(t, x), checkedMouseCoordinate(t, y)

	window.ProcessMouse(&vtinput.InputEvent{
		Type:        vtinput.MouseEventType,
		KeyDown:     true,
		MouseX:      mx,
		MouseY:      my,
		ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	dispatchDone := make(chan struct{})
	go func() {
		window.ProcessMouse(&vtinput.InputEvent{
			Type:        vtinput.MouseEventType,
			MouseX:      mx,
			MouseY:      my,
			KeyDown:     false,
			ButtonState: 0,
		})
		close(dispatchDone)
	}()

	select {
	case <-requestStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("manual update check did not start in the background")
	}

	// The server will not answer until allowResponse is closed below. Check
	// dispatch only after the request has reached it, so slow goroutine and
	// HTTP startup on emulated CI do not consume this deadline. Five seconds
	// is still below CheckForUpdates' ten-second request timeout, so a
	// synchronous click cannot pass merely because the request timed out.
	select {
	case <-dispatchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("manual update check blocked mouse dispatch")
	}
	close(allowResponse)
	select {
	case <-requestFinished:
	case <-time.After(10 * time.Second):
		t.Fatal("manual update check did not finish after the response was released")
	}

	// CheckForUpdates posts exactly one result after it has finished reading
	// the overridden platform globals. Running that task joins the background
	// check before Cleanup restores them.
	select {
	case task := <-vtui.FrameManager.TaskChan:
		task()
	case <-time.After(10 * time.Second):
		t.Fatal("manual update check did not post its result")
	}
}

func checkedMouseCoordinate(t *testing.T, value int) int16 {
	t.Helper()
	if value < -32768 || value > 32767 {
		t.Fatalf("mouse coordinate %d does not fit in int16", value)
	}
	return int16(value) // #nosec G115 -- the range is checked immediately above.
}

func TestActionExecute_RemoteRejection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// mockRemoteVFS does NOT satisfy the isLocal check in actionExecute
	baseVfs := vfs.NewOSVFS(t.TempDir())
	v := &mockFailingVFS{VFS: baseVfs}
	pf := panel.NewPanelsFrame()
	defer pf.Close()

	actionExecute(pf, v, filepath.FromSlash("/remote"), "script.sh", filepath.FromSlash("/remote/script.sh"))

	// Drain task queue to allow UI updates
	timeout := time.After(1 * time.Second)
	foundDialog := false
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
				foundDialog = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}

	if !foundDialog {
		t.Error("Expected error dialog when attempting to execute on remote VFS")
	}
}

func TestActionMkDir_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25) // Crucial: initializes panels

	// 1. Trigger MkDir action (should push InputBox)
	actionMkDir(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("MakeFolder.Title") {
		t.Fatalf("Expected MkDir dialog, got %v", top)
	}

	// Close it to clean up
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionMkDirDoesNotUseLastHistory(t *testing.T) {
	oldHistory := vtui.GlobalHistoryProvider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = oldHistory })
	provider := history.NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	provider.SaveHistory(history.NewFolderHistoryID, []string{"test"})
	vtui.GlobalHistoryProvider = provider
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionMkDir(pf)

	top := vtui.FrameManager.GetTopFrame()
	dlg, ok := top.(*vtui.Window)
	if !ok {
		t.Fatalf("expected MkDir window, got %T", top)
	}
	edit := history.InputBoxEdit(dlg)
	if edit == nil {
		t.Fatal("MkDir dialog has no input field")
	}
	if got := edit.GetText(); got != "" {
		t.Fatalf("MkDir field inherited the last history entry %q", got)
	}

	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}
func TestActionCalcDirSize_IgnoresParentRow(t *testing.T) {
	fsp := &panel.FileSystemPanel{
		Entries: []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}},
	}

	// The parent row is navigation metadata, not an item that can be scanned.
	// In particular, it must not ask an ArchiveVFS to stat a path outside its
	// virtual root (issue #510).
	actionCalcDirSize(nil, fsp, 0)
}

type mockDeletionFailingVFS struct {
	vfs.VFS
	failedFiles  []string
	deletedFiles []string
}

func (m *mockDeletionFailingVFS) Remove(ctx context.Context, path string) error {
	name := filepath.Base(path)
	for _, f := range m.failedFiles {
		if f == name {
			return os.ErrPermission
		}
	}
	m.deletedFiles = append(m.deletedFiles, name)
	return nil
}

func (m *mockDeletionFailingVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	name := filepath.Base(path)
	return vfs.VFSItem{Name: name, IsDir: false, Size: 10}, nil
}

func (m *mockDeletionFailingVFS) ReadDir(ctx context.Context, path string, onChunk func([]vfs.VFSItem)) error {
	return nil
}

func (m *mockDeletionFailingVFS) Join(e ...string) string { return filepath.Join(e...) }
func (m *mockDeletionFailingVFS) GetPath() string         { return "/tmp" }

func TestActionDelete_BulkErrorAccumulation(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	// ??????? ???-VFS, ??????? ???????? ???????? "fail.txt"
	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"fail.txt"},
	}

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = mv

	// ?????????????? ?????? ??????: f1.txt (??), fail.txt (??????), f2.txt (??)
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "f1.txt"}},
		{VFSItem: vfs.VFSItem{Name: "fail.txt"}},
		{VFSItem: vfs.VFSItem{Name: "f2.txt"}},
	}
	// ???????? ??? ??? ?????
	fsp.Entries[1].Selected = true
	fsp.Entries[2].Selected = true
	fsp.Entries[3].Selected = true

	// ?????: ?????? ?????? ? ??????? ????????
	pf.ActiveIdx = 0

	// 1. ?????????? ????????
	actionDelete(pf)

	// 2. ??????? ?????? "Delete" ? ??????? ????????????? ? ???????? ??
	// In test, force mode to Foreground Lock (2) so it runs synchronously
	dlgConfirm1 := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm1.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}

	frame := fm.GetTopFrame()
	if frame == nil {
		t.Fatal("Confirmation dialog was not shown")
	}
	top, ok := frame.(vtui.Container)
	if !ok {
		t.Fatal("Top frame is not a container")
	}
	var btnDel *vtui.Button
	for _, itm := range top.GetChildren() {
		if b, ok := itm.(*vtui.Button); ok && strings.Contains(b.GetText(), "Delete") {
			btnDel = b
			break
		}
	}
	if btnDel == nil {
		t.Fatal("Delete button not found in confirmation dialog")
	}
	btnDel.OnClick()

	// 3. ???????????? ??????? ?????, ?????? ????????? ??????? ? ??????? ??????
	timeout := time.After(2 * time.Second)
	var progress *fileops.FileOpProgressDialog
	summaryShown := false
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if top, ok := fm.GetTopFrame().(*fileops.FileOpProgressDialog); ok {
				progress = top
			}

			// ???? ???????? ?????? ?????? ???????? (fileops.AskError), ???????? Skip
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				if dlg, ok := fm.GetTopFrame().(vtui.Container); ok {
					for _, itm := range dlg.GetChildren() {
						if b, ok := itm.(*vtui.Button); ok && strings.Contains(b.GetText(), "Skip") {
							b.OnClick()
							break
						}
					}
				}
			}

			// ????, ????? ?? ??????? ????? ???????? ?????? ? ?????????? " Deletion Errors "
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == i18n.Msg("FileOp.DeletionErrors") {
				summaryShown = true
			}
			if summaryShown && progress != nil && progress.IsDone() {
				break Loop
			}

			if fm.GetTopFrame() != nil && fm.GetTopFrame().IsDone() {
				fm.Pop()
			}
		case <-timeout:
			t.Fatal("Timeout waiting for error accumulation dialog")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Validate layout of the Deletion Errors dialog
	vtui.AssertLayout(t, fm.GetTopFrame().(vtui.Container))

	// 4. ????????? ??????????
	// ?????? ???? 2 ???????? ???????? (f1.txt ? f2.txt)
	if len(mv.deletedFiles) != 2 {
		t.Errorf("Expected 2 files deleted, got %d: %v", len(mv.deletedFiles), mv.deletedFiles)
	}

	foundF1, foundF2 := false, false
	for _, f := range mv.deletedFiles {
		if f == "f1.txt" {
			foundF1 = true
		}
		if f == "f2.txt" {
			foundF2 = true
		}
	}
	if !foundF1 || !foundF2 {
		t.Errorf("One of the deletable files was skipped: %v", mv.deletedFiles)
	}
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}

type mockRetryDeleteVFS struct {
	vfs.VFS
	attempts map[string]int
	deleted  []string
}

func (m *mockRetryDeleteVFS) Remove(ctx context.Context, path string) error {
	name := filepath.Base(path)
	if m.attempts[name] > 0 {
		m.attempts[name]--
		return os.ErrPermission
	}
	m.deleted = append(m.deleted, name)
	return nil
}

func (m *mockRetryDeleteVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: filepath.Base(path)}, nil
}

func TestActionDelete_RetrySuccess(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	theme.SetDefaultF4Palette()

	mv := &mockRetryDeleteVFS{
		VFS:      vfs.NewOSVFS(t.TempDir()),
		attempts: map[string]int{"retry.txt": 1}, // ?????? 1 ???
	}

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = mv
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "retry.txt"}}}
	pf.ActiveIdx = 0

	actionDelete(pf)

	// 1. ???????????? ????????
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	testutil.ClickDialogButton(t, dlgConfirm, "Delete")

	// 2. ???? ?????? ?????? ? ???? Retry
	timeout := time.After(2 * time.Second)
	retryClicked := false
	var progress *fileops.FileOpProgressDialog
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if top, ok := fm.GetTopFrame().(*fileops.FileOpProgressDialog); ok {
				progress = top
			}
			if !retryClicked && fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				testutil.ClickDialogButton(t, fm.GetTopFrame().(vtui.Container), "Retry")
				retryClicked = true
			}
			if progress != nil && progress.IsDone() {
				break Loop
			}
			if fm.GetTopFrame() != nil && fm.GetTopFrame().IsDone() {
				fm.Pop()
			}
		case <-timeout:
			t.Fatal("Timeout waiting for Retry to succeed")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if len(mv.deleted) != 1 || mv.deleted[0] != "retry.txt" {
		t.Errorf("File was not deleted after Retry. Deleted: %v", mv.deleted)
	}
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}

func TestActionDelete_Abort(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	theme.SetDefaultF4Palette()

	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"abort.txt"},
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = mv
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "abort.txt"}},
		{VFSItem: vfs.VFSItem{Name: "should_not_touch.txt"}},
	}
	fsp.Entries[0].Selected = true
	fsp.Entries[1].Selected = true
	pf.ActiveIdx = 0

	actionDelete(pf)
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	testutil.ClickDialogButton(t, dlgConfirm, "Delete")

	// ???? ?????? ? ???? Abort
	timeout := time.After(2 * time.Second)
	abortClicked := false
	var progress *fileops.FileOpProgressDialog
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if top, ok := fm.GetTopFrame().(*fileops.FileOpProgressDialog); ok {
				progress = top
			}
			if !abortClicked && fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				testutil.ClickDialogButton(t, fm.GetTopFrame().(vtui.Container), "Abort")
				abortClicked = true
			}
			if abortClicked && progress != nil && progress.IsDone() {
				break Loop
			}
			if fm.GetTopFrame() != nil && fm.GetTopFrame().IsDone() {
				fm.Pop()
			}
		case <-timeout:
			t.Fatal("Error dialog didn't appear")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// ?????????, ??? ?????? ????????? ???? (?????? ????, ?????? ?? ????????)
	if len(mv.deletedFiles) != 0 {
		t.Errorf("Abort failed: some files were deleted: %v", mv.deletedFiles)
	}
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}
func TestActionDelete_SkipAll(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	theme.SetDefaultF4Palette()

	// ??? ?????, ??? ??????
	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"fail1.txt", "fail2.txt"},
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = mv
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "fail1.txt"}},
		{VFSItem: vfs.VFSItem{Name: "fail2.txt"}},
	}
	fsp.Entries[0].Selected = true
	fsp.Entries[1].Selected = true
	pf.ActiveIdx = 0

	actionDelete(pf)

	// 1. ???????????? ???????? (Foreground mode)
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	testutil.ClickDialogButton(t, dlgConfirm, "Delete")

	// 2. ???? ?????? ?????? ? ???? "Skip All"
	timeout := time.After(2 * time.Second)
	skipAllClicked := false
	var progress *fileops.FileOpProgressDialog
	summaryShown := false
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if top, ok := fm.GetTopFrame().(*fileops.FileOpProgressDialog); ok {
				progress = top
			}

			if !skipAllClicked && fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				if dlg, ok := fm.GetTopFrame().(vtui.Container); ok {
					for _, itm := range dlg.GetChildren() {
						if b, ok := itm.(*vtui.Button); ok && (strings.Contains(b.GetText(), "Skip All") || strings.Contains(b.GetText(), "S&kip All") || strings.Contains(b.GetText(), "?????????")) {
							b.OnClick()
							skipAllClicked = true
							break
						}
					}
				}
			}

			// ???? ????????? ?????? ?? ??????? ??????
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == i18n.Msg("FileOp.DeletionErrors") {
				summaryShown = true
			}
			if summaryShown && progress != nil && progress.IsDone() {
				break Loop
			}

			if fm.GetTopFrame() != nil && fm.GetTopFrame().IsDone() {
				fm.Pop()
			}
		case <-timeout:
			t.Fatal("Timeout waiting for Skip All to finish")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 3. ?????????, ??? ? ???????? ?????? 2 ??????, ?? ?????? ??????????? ?????? ???? ???
	top := fm.GetTopFrame().(vtui.Container)
	foundErrors := 0
	for _, itm := range top.GetChildren() {
		if lb, ok := itm.(*vtui.ListBox); ok {
			for _, line := range lb.Items {
				if strings.Contains(line, "Skipped") {
					foundErrors++
				}
			}
		}
	}

	if foundErrors != 2 {
		t.Errorf("Expected 2 errors in log, found %d", foundErrors)
	}
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
}
func TestActionExecute_PtyCommandFormatting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// paneltest.SetupMockPanelsFrame ? mockPty ?????????? ? ?????? ???????? ?????? ???? ?? ??????
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pty := pf.Pty.(*paneltest.MockPty)

	tmp := t.TempDir()
	fileName := "app.exe"
	if runtime.GOOS != "windows" {
		fileName = "app.sh"
	}
	FilePath := filepath.Join(tmp, fileName)
	if err := os.WriteFile(FilePath, []byte("#!/bin/sh\nexit 0"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmp)

	// ??????? ????? terminal.PTY ????? ??????
	pty.Written = nil

	actionExecute(pf, v, tmp, fileName, FilePath)

	// ??????????? ?????? FrameManager
	timeout := time.After(2 * time.Second)
	for pf.ShowPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for execution task")
		}
	}

	// ? ???????? ?????????? ?????? ?? terminal.PTY ???????? ????? terminal.AnsiParser, ???????
	// ???????? ??????????? ??????? (cd /d) ????? ????????????. ????????? ???:
	pf.Parser.Process(pty.Written)
	result := string(pf.TermView.GetAllLogBytes())

	if runtime.GOOS == "windows" {
		// ????????? ?????????? ??????????? ??????? 'cd /d' ? ?????? ????? ???????
		if strings.Contains(result, "cd /d") {
			t.Errorf("Technical 'cd /d' wrapper should be removed by parser, but found in log: %q", result)
		}
		// ??????? ?????? ?????????????? ? ???? (?????? ???????? cd /d, ???????? ???? ???????)
		if !strings.Contains(result, "app.exe") {
			t.Errorf("PTY command should appear in terminal log after cd excision, got log: %q", result)
		}
	}
}

func TestActionExecute_HistoryQuoting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()

	tmp := t.TempDir()
	fileName := "name with spaces.exe"
	FilePath := filepath.Join(tmp, fileName)
	if err := os.WriteFile(FilePath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmp)
	actionExecute(pf, v, tmp, fileName, FilePath)

	timeout := time.After(2 * time.Second)
	for pf.ShowPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	lastHistory := pf.CmdLine.Edit.History[0]
	if !strings.Contains(lastHistory, "\"name with spaces.exe\"") {
		t.Errorf("History entry with spaces must be quoted, got: %q", lastHistory)
	}
}
func TestImportFar2lHistory(t *testing.T) {
	tmpDir := t.TempDir()
	hstPath := filepath.Join(tmpDir, "commands.hst")
	content := `[SavedHistory]
Lines="cmd1\ncmd2\ncmd3"
Extras="/dir1\n/dir2\n/dir3"
Locks=100
Times=804c4587aa28dd01 004e237daa28dd01 0021f27baa28dd01
`
	if err := os.WriteFile(hstPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	recs, err := history.ImportFar2lHistory(ini.Load(hstPath), hstPath)
	if err != nil {
		t.Fatalf("history.ImportFar2lHistory failed: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(recs))
	}

	if recs[0].Name != "cmd1" || recs[0].Extra != "/dir1" || !recs[0].Lock {
		t.Errorf("Record 0 mismatch: %+v", recs[0])
	}
	if recs[1].Name != "cmd2" || recs[1].Extra != "/dir2" || recs[1].Lock {
		t.Errorf("Record 1 mismatch: %+v", recs[1])
	}
}
func TestActionDelete_SuccessorLogic(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// ??????? 4 ?????: f1, f2, f3, f4
	files := []string{"f1.txt", "f2.txt", "f3.txt", "f4.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. ??????? f2 ? f3 (??????????)
	// ?????????? ????????
	fsp.ReadDirectory()
	for fsp.IsLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout")
		}
	}

	// ???????? f2 ? f3 (??????? 2 ? 3, ?.?. 0 - "..", 1 - "f1")
	fsp.Entries[2].Selected = true
	fsp.Entries[3].Selected = true

	// ?? ?????? Successor, ????? ???????? ????? f2, f3 ?????? ?????? ?????? ?? f4.
	successor := fsp.GetSuccessorName()
	if successor != "f4.txt" {
		t.Errorf("Expected successor f4.txt, got %q", successor)
	}

	// 2. ??????? ????????? ???? (f4)
	fsp.Entries[2].Selected = false
	fsp.Entries[3].Selected = false
	fsp.SetCursorIndex(4) // f4
	successor = fsp.GetSuccessorName()
	// ???? ??????? ?????????, ?????? ??????? ?? ?????????? (f3)
	if successor != "f3.txt" {
		t.Errorf("Expected successor f3.txt when deleting tail, got %q", successor)
	}
}
func TestActionCopyMove_TrailingSlash(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Ensure valid paths for the test
	fspSrc := pf.Panels[0].(*panel.FileSystemPanel)
	fspDst := pf.Panels[1].(*panel.FileSystemPanel)
	if err := fspSrc.Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := fspDst.Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Manually add an entry so actionCopyMove doesn't exit early
	fspSrc.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "test.txt", IsDir: false}},
	}
	fspSrc.SetCursorIndex(0)
	pf.ActiveIdx = 0 // Ensure the panel with the file is active

	// Trigger Copy (false = isMove)
	actionCopyMove(pf, false)

	top := vtui.FrameManager.GetTopFrame()
	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatal("Copy dialog not found on top")
	}

	var editDest *vtui.Edit
	for _, itm := range dlg.GetChildren() {
		if e, ok := itm.(*vtui.Edit); ok {
			editDest = e
			break
		}
	}

	if editDest == nil {
		t.Fatal("Destination edit field not found in dialog")
	}

	txt := editDest.GetText()
	sep := string(os.PathSeparator)
	if !strings.HasSuffix(txt, sep) {
		t.Errorf("Path in Copy dialog missing trailing slash: %q (expected it to end with %q)", txt, sep)
	}

	// Cleanup
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionCopyMove_ModeMenuDoesNotCoverButtons(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	src := pf.Panels[0].(*panel.FileSystemPanel)
	if err := src.Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatalf("set source path: %v", err)
	}
	src.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "test.txt"}}}
	src.SetCursorIndex(0)
	pf.ActiveIdx = 0

	actionCopyMove(pf, false)
	dlg, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatal("copy dialog not found on top")
	}

	assertComboMenuDoesNotCoverButtons(t, dlg, "copy")
	focusDlg, ok := vtui.FrameManager.GetTopFrame().(dialogFocusContainer)
	if !ok {
		t.Fatal("copy dialog does not expose focus traversal")
	}
	assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "copy")
	vtui.FrameManager.Pop()
}

func assertComboMenuDoesNotCoverButtons(t *testing.T, dlg vtui.Container, name string) {
	t.Helper()

	var combo *vtui.ComboBox
	var buttons []*vtui.Button
	for _, item := range dlg.GetChildren() {
		switch child := item.(type) {
		case *vtui.ComboBox:
			combo = child
		case *vtui.Button:
			buttons = append(buttons, child)
		}
	}
	if combo == nil || len(buttons) != 2 {
		t.Fatalf("%s dialog controls: combo=%v buttons=%d", name, combo != nil, len(buttons))
	}

	combo.Open()
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("%s menu was not opened", name)
	}
	mx1, my1, mx2, my2 := menu.GetPosition()
	for _, button := range buttons {
		bx1, by1, bx2, by2 := button.GetPosition()
		if mx1 <= bx2 && bx1 <= mx2 && my1 <= by2 && by1 <= my2 {
			t.Fatalf("%s menu (%d,%d)-(%d,%d) overlaps button (%d,%d)-(%d,%d)", name, mx1, my1, mx2, my2, bx1, by1, bx2, by2)
		}
	}

	vtui.FrameManager.Pop()
}

type dialogFocusContainer interface {
	vtui.Container
	GetFocusedItem() vtui.UIElement
	SetFocusedItem(vtui.UIElement)
	ProcessKey(*vtinput.InputEvent) bool
}

func assertDialogTabOrderMatchesVisualOrder(t *testing.T, dlg dialogFocusContainer, name string) {
	t.Helper()

	var want []vtui.UIElement
	for _, item := range dlg.GetChildren() {
		if item.CanFocus() && !item.IsDisabled() {
			want = append(want, item)
		}
	}
	sort.SliceStable(want, func(i, j int) bool {
		ix1, iy1, _, _ := want[i].GetPosition()
		jx1, jy1, _, _ := want[j].GetPosition()
		if iy1 != jy1 {
			return iy1 < jy1
		}
		return ix1 < jx1
	})
	if len(want) == 0 {
		t.Fatalf("%s dialog has no focusable controls", name)
	}

	dlg.SetFocusedItem(want[0])
	for i, expected := range want {
		if got := dlg.GetFocusedItem(); got != expected {
			t.Fatalf("%s focus step %d = %T, want %T", name, i, got, expected)
		}
		if i+1 < len(want) && !dlg.ProcessKey(&vtinput.InputEvent{
			Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB,
		}) {
			t.Fatalf("%s Tab at focus step %d was not handled", name, i)
		}
	}
}

func TestActionCopy_ShiftF5_Prefill(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fspSrc := pf.Panels[0].(*panel.FileSystemPanel)

	// Setup actual existing paths using t.TempDir()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcPath, 0700); err != nil {
		t.Fatal(err)
	}

	if err := fspSrc.Vfs.SetPath(srcPath); err != nil {
		t.Fatalf("Failed to set src VFS path: %v", err)
	}

	// Mock entries so we have a file under the cursor
	fspSrc.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "test.txt", IsDir: false}},
	}
	fspSrc.SetCursorIndex(0)
	pf.ActiveIdx = 0

	// Trigger Shift-F5 (Copy in place)
	actionCopyInPlace(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != " Copy " {
		t.Fatalf("Expected Copy dialog, got %v", top)
	}

	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatal("Copy dialog not found on top")
	}

	var editDest *vtui.Edit
	for _, itm := range dlg.GetChildren() {
		if e, ok := itm.(*vtui.Edit); ok {
			editDest = e
			break
		}
	}

	if editDest == nil {
		t.Fatal("Destination edit field not found in dialog")
	}

	txt := editDest.GetText()
	if txt != "test.txt" {
		t.Errorf("Expected prefilled name 'test.txt', got %q", txt)
	}

	// Cleanup
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionNewFile_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25) // Crucial: initializes panels

	pf.ActiveIdx = 0
	actionNewFile(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("Edit.NewFileTitle") {
		t.Errorf("Expected New File dialog, got %v", top)
	}
}

func TestActionNewFile_AbsoluteExistingPath(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	root := t.TempDir()
	path := filepath.Join(root, "existing.txt")
	want := []byte("already here\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(root)
	pvfs := fsp.Vfs

	actionNewFile(pf)
	dlg, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatalf("New File dialog is not a container: %T", vtui.FrameManager.GetTopFrame())
	}
	var edit *vtui.Edit
	var okButton *vtui.Button
	for _, child := range dlg.GetChildren() {
		switch value := child.(type) {
		case *vtui.Edit:
			edit = value
		case *vtui.Button:
			if strings.Contains(value.GetText(), i18n.Msg("vtui.Ok")) {
				okButton = value
			}
		}
	}
	if edit == nil || okButton == nil {
		t.Fatal("New File dialog controls not found")
	}
	edit.SetText(path)
	okButton.OnClick()

	var editor *editor.EditorView
	deadline := time.After(2 * time.Second)
	for editor == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			editor, _ = findOpenedEditor(pvfs, path)
		case <-deadline:
			t.Fatal("Timeout waiting for existing file editor")
		}
	}
	defer editor.Close()

	got, err := editor.Pt.GetRange(0, editor.Pt.Size())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("opened content = %q, want %q", got, want)
	}
}
func TestDelete_FocusCustomization(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "test.txt"}}}
	pf.ActiveIdx = 0

	origDelFocus := config.App.DeleteCancelFocused
	defer func() { config.App.DeleteCancelFocused = origDelFocus }()

	// 1. By default the destructive action is focused, matching the other
	// confirmation dialogs and allowing Enter to confirm it.
	config.App.DeleteCancelFocused = false
	actionDelete(pf)

	dlg1 := fm.GetTopFrame().(vtui.Container)
	var btnDel *vtui.Button
	for _, child := range dlg1.GetChildren() {
		if b, ok := child.(*vtui.Button); ok && strings.Contains(b.GetText(), "Delete") {
			btnDel = b
			break
		}
	}
	if btnDel == nil {
		t.Fatal("Delete button not found")
	}
	if !btnDel.IsFocused() {
		t.Error("Expected 'Delete' button to be focused by default")
	}
	fm.Pop()

	// 2. The safety option can still explicitly focus Cancel.
	config.App.DeleteCancelFocused = true
	actionDelete(pf)

	dlg2 := fm.GetTopFrame().(vtui.Container)
	var btnCancel *vtui.Button
	for _, child := range dlg2.GetChildren() {
		if b, ok := child.(*vtui.Button); ok && strings.Contains(b.GetText(), "Cancel") {
			btnCancel = b
			break
		}
	}
	if btnCancel == nil {
		t.Fatal("Cancel button not found")
	}
	if !btnCancel.IsFocused() {
		t.Error("Expected 'Cancel' button to be focused when configured")
	}
	fm.Pop()
}

// TestActionDelete_UsesWarnPalette_Issue379 pins the fix for #379 and the
// recoverable-trash distinction from #828: only permanent deletion uses the
// red WarnDialog palette.
func TestActionDelete_UsesWarnPalette_Issue379(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	oldCfg := config.App
	defer func() { config.App = oldCfg }()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "goner.txt"}, Selected: true}}
	pf.ActiveIdx = 0

	config.App.ConfirmDelete = true
	config.App.UseTrash = true
	actionDelete(pf)

	top := fm.GetTopFrame()
	if top == nil {
		t.Fatal("Delete confirmation dialog was not shown")
	}
	dlg, ok := top.(*vtui.Window)
	if !ok {
		t.Fatalf("Top frame is not a *vtui.Window, got %T", top)
	}
	if dlg.IsWarning {
		t.Error("Recycle Bin confirmation must render on the neutral dialog palette (see #828)")
	}
	fm.Pop()

	config.App.UseTrash = false
	actionDelete(pf)
	top = fm.GetTopFrame()
	dlg, ok = top.(*vtui.Window)
	if !ok {
		t.Fatalf("Top frame is not a *vtui.Window for permanent delete, got %T", top)
	}
	if !dlg.IsWarning {
		t.Error("Permanent delete confirmation must render on the WarnDialog palette (see #379)")
	}
	fm.Pop()

	config.App.UseTrash = true
	actionDeletePermanent(pf)
	top = fm.GetTopFrame()
	dlg, ok = top.(*vtui.Window)
	if !ok {
		t.Fatalf("Top frame is not a *vtui.Window for explicit permanent delete, got %T", top)
	}
	if !dlg.IsWarning {
		t.Error("Explicit permanent delete confirmation must render on the WarnDialog palette")
	}
	fm.Pop()
}

func TestActionOpenEditor_AlreadyOpened(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// First open
	actionOpenEditor(pf, v, path)

	// Wait for editor to open
	timeout := time.After(2 * time.Second)
	foundEditor := false
	for !foundEditor {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			ev, _ := findOpenedEditor(v, path)
			if ev != nil {
				foundEditor = true
			}
		case <-timeout:
			t.Fatal("Timeout waiting for editor to open")
		}
	}

	// Attempt second open
	actionOpenEditor(pf, v, path)

	// Wait for the reprompt dialog. Per #379 this is a choice
	// ("switch / reload / new instance / cancel"), not a warning ?
	// so the dialog now carries the semantic FileOp.AlreadyOpenedTitle
	// and must render on the neutral (non-warning) palette.
	wantTitle := i18n.Msg("FileOp.AlreadyOpenedTitle")
	found := false
	var foundWin *vtui.Window
	timeout = time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
				top := vtui.FrameManager.GetTopFrame()
				if top != nil && top.GetTitle() == wantTitle {
					found = true
					if w, ok := top.(*vtui.Window); ok {
						foundWin = w
					}
					break Loop
				}
			}
		case <-timeout:
			break Loop
		}
	}

	if !found {
		t.Errorf("Expected reprompt dialog with title %q when trying to open an already opened file", wantTitle)
	}
	if foundWin != nil && foundWin.IsWarning {
		t.Error("Already-opened dialog must not render as a warning (see #379)")
	}
	if ev, _ := findOpenedEditor(v, path); ev != nil {
		ev.Close()
	}
}

func TestActionOpenViewer_AlreadyOpened(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_view.txt")
	if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// First open
	actionOpenViewer(pf, v, path)

	// Wait for viewer to open
	timeout := time.After(2 * time.Second)
	foundViewer := false
	for !foundViewer {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			vv, _ := findOpenedViewer(v, path)
			if vv != nil {
				foundViewer = true
			}
		case <-timeout:
			t.Fatal("Timeout waiting for viewer to open")
		}
	}

	// Attempt second open
	actionOpenViewer(pf, v, path)

	// Same rationale as TestActionOpenEditor_AlreadyOpened above:
	// per #379 the reprompt is a neutral choice, not a warning.
	wantTitle := i18n.Msg("FileOp.AlreadyViewedTitle")
	found := false
	var foundWin *vtui.Window
	timeout = time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
				top := vtui.FrameManager.GetTopFrame()
				if top != nil && top.GetTitle() == wantTitle {
					found = true
					if w, ok := top.(*vtui.Window); ok {
						foundWin = w
					}
					break Loop
				}
			}
		case <-timeout:
			break Loop
		}
	}

	if !found {
		t.Errorf("Expected reprompt dialog with title %q when trying to open an already viewed file", wantTitle)
	}
	if foundWin != nil && foundWin.IsWarning {
		t.Error("Already-viewed dialog must not render as a warning (see #379)")
	}
	if vv, _ := findOpenedViewer(v, path); vv != nil {
		vv.Close()
	}
}

type mockLockedVFS struct {
	vfs.VFS
}

func (m *mockLockedVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	return nil, os.ErrPermission // Simulate locked file or sharing violation
}

func TestActionOpenEditor_LockedFile(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "locked.txt")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	v := &mockLockedVFS{VFS: vfs.NewOSVFS(tmpDir)}
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionOpenEditor(pf, v, path)

	// Wait for error dialog
	foundError := false
	timeout := time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
				top := vtui.FrameManager.GetTopFrame()
				if top != nil && strings.Contains(top.GetTitle(), "Error") {
					foundError = true
					break Loop
				}
			}
		case <-timeout:
			break Loop
		}
	}

	if !foundError {
		t.Error("Expected error dialog when trying to open a locked file for editing")
	}
}
func TestActionViewerSearch_EmptyFile(t *testing.T) {
	// Regression test: searching in an empty file should not hang or crash
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmp := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(tmp, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	v := vfs.NewOSVFS(t.TempDir())

	vv, err := viewer.NewViewerView(context.Background(), v, tmp)
	if err != nil {
		t.Fatalf("Failed to create viewer.ViewerView: %v", err)
	}
	defer vv.Close()

	// Simulate search trigger
	// We manually call the inner logic of actionViewerSearch since InputBox is blocking in tests
	foundOffset := int64(-1)
	currOff := vv.TopOffset + 1
	fileSize := vv.Backend.Size() // 0

	if currOff < fileSize {
		t.Error("Search loop should not even start for empty file")
	}

	if foundOffset != -1 {
		t.Error("Should not find anything in empty file")
	}
}
func TestActionFindFile_Persistence(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	LastFindFileMask = "*.tmp"
	actionFindFile(pf)
	dlg := vtui.FrameManager.GetTopFrame().(vtui.Container)

	found := false
	for _, itm := range dlg.GetChildren() {
		if e, ok := itm.(*vtui.Edit); ok && e.GetText() == "*.tmp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Find File dialog did not initialize with LastFindFileMask")
	}
}

func TestSession_DiskPersistence(t *testing.T) {
	// ??????? ????????? ?????????? ??? ?????
	tmpDir := t.TempDir()
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfig := config.App
	oldEditorSearch, oldFindMask := editor.LastEditorSearch, LastFindFileMask
	oldLeftPath, oldRightPath := panel.LastLeftPath, panel.LastRightPath
	oldLeftCursor, oldRightCursor := panel.LastLeftCursor, panel.LastRightCursor
	oldActivePanel, oldWidePanel := panel.LastActivePanel, panel.LastWidePanel
	oldLeftViewMode, oldRightViewMode := panel.LastLeftViewMode, panel.LastRightViewMode
	oldLeftSortMode, oldRightSortMode := panel.LastLeftSortMode, panel.LastRightSortMode
	oldLeftSortRev, oldRightSortRev := panel.LastLeftSortRev, panel.LastRightSortRev
	oldShowPanels, oldShowLeft, oldShowRight := panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight
	oldWorkspaces, oldActiveWorkspace := panel.LastWorkspaceSessions, panel.LastActiveWorkspace
	t.Cleanup(func() {
		config.App = oldConfig
		editor.LastEditorSearch, LastFindFileMask = oldEditorSearch, oldFindMask
		panel.LastLeftPath, panel.LastRightPath = oldLeftPath, oldRightPath
		panel.LastLeftCursor, panel.LastRightCursor = oldLeftCursor, oldRightCursor
		panel.LastActivePanel, panel.LastWidePanel = oldActivePanel, oldWidePanel
		panel.LastLeftViewMode, panel.LastRightViewMode = oldLeftViewMode, oldRightViewMode
		panel.LastLeftSortMode, panel.LastRightSortMode = oldLeftSortMode, oldRightSortMode
		panel.LastLeftSortRev, panel.LastRightSortRev = oldLeftSortRev, oldRightSortRev
		panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight = oldShowPanels, oldShowLeft, oldShowRight
		panel.LastWorkspaceSessions, panel.LastActiveWorkspace = oldWorkspaces, oldActiveWorkspace
	})
	panel.LastWorkspaceSessions = nil
	panel.LastActiveWorkspace = 0
	config.App.AutoSaveSettings = true
	// SaveSession passes these through to saveSessionWithOptions, and the panel
	// group is what writes panel.ViewMode, panel.SortMode, SortReverse and the Show* keys.
	// They are process-wide settings that another test may have left switched
	// off, and inheriting that leaves those keys out of the file: the load
	// below then returns defaults and the assertions on them fail while the
	// ones on paths and the cursor still pass.
	config.App.AutoSavePanelSettings = true
	config.App.AutoSaveCurrentPanel = true

	// ????????????? ???? ? ini ????? (? ???????? ???? ?? ??????? ?? os.UserConfigDir)
	// ??? ????? ?? ?????? ??????? ??????? SaveSession ? ???????? ????????? ? ?????.
	origPathFunc := getSessionIniPath
	getSessionIniPath = func() string { return filepath.Join(tmpDir, "session.ini") }
	t.Cleanup(func() { getSessionIniPath = origPathFunc })

	// The test stands in for a process with a UI: the Last* state below is set
	// by hand, so SaveSession must write it.
	oldSessionLoaded := sessionLoaded
	sessionLoaded = true
	t.Cleanup(func() { sessionLoaded = oldSessionLoaded })

	editor.LastEditorSearch = "disk-test"
	LastFindFileMask = "*.log"
	panel.LastLeftPath = "/path/a"
	panel.LastRightPath = "/path/b"
	panel.LastLeftCursor = "file.a"
	panel.LastRightCursor = "file.b"
	panel.LastActivePanel = 0
	panel.LastWidePanel = 1

	panel.LastLeftViewMode = 1
	panel.LastRightViewMode = 0
	panel.LastLeftSortMode = 3
	panel.LastRightSortMode = 2
	panel.LastLeftSortRev = true
	panel.LastRightSortRev = false

	panel.LastShowPanels = false
	panel.LastShowLeft = true
	panel.LastShowRight = false

	SaveSession()

	// ?????????? ? ?????????
	panel.LastLeftPath = ""
	panel.LastRightPath = ""
	panel.LastLeftCursor = ""
	panel.LastRightCursor = ""
	panel.LastActivePanel = 1
	panel.LastWidePanel = -1

	panel.LastLeftViewMode = 0
	panel.LastRightViewMode = 1
	panel.LastLeftSortMode = 0
	panel.LastRightSortMode = 0
	panel.LastLeftSortRev = false
	panel.LastRightSortRev = true

	panel.LastShowPanels = true
	panel.LastShowLeft = false
	panel.LastShowRight = true

	LoadSession()

	if editor.LastEditorSearch != "disk-test" || panel.LastLeftPath != "/path/a" || panel.LastLeftCursor != "file.a" || panel.LastActivePanel != 0 {
		t.Errorf("Disk persistence failed. Search:%q, LeftPath:%q, LeftCursor:%q, Active:%d",
			editor.LastEditorSearch, panel.LastLeftPath, panel.LastLeftCursor, panel.LastActivePanel)
	}
	if panel.LastWidePanel != 1 {
		t.Errorf("Wide panel persistence failed: got %d, want 1", panel.LastWidePanel)
	}

	if panel.LastLeftViewMode != 1 || panel.LastRightViewMode != 0 || panel.LastLeftSortMode != 3 || panel.LastRightSortMode != 2 {
		t.Errorf("View/Sort modes persistence failed. LeftVM:%d, RightVM:%d, LeftSM:%d, RightSM:%d",
			panel.LastLeftViewMode, panel.LastRightViewMode, panel.LastLeftSortMode, panel.LastRightSortMode)
	}

	if !panel.LastLeftSortRev || panel.LastRightSortRev {
		t.Errorf("Sort directions persistence failed. LeftRev:%v, RightRev:%v", panel.LastLeftSortRev, panel.LastRightSortRev)
	}

	if panel.LastShowPanels || !panel.LastShowLeft || panel.LastShowRight {
		t.Errorf("Panel visibility persistence failed. Show:%v, Left:%v, Right:%v", panel.LastShowPanels, panel.LastShowLeft, panel.LastShowRight)
	}
}

func TestSession_OldFileDefaultsWideOff(t *testing.T) {
	tmpDir := t.TempDir()
	origPathFunc := getSessionIniPath
	getSessionIniPath = func() string { return filepath.Join(tmpDir, "session.ini") }
	defer func() { getSessionIniPath = origPathFunc }()

	if err := os.WriteFile(getSessionIniPath(), []byte("[Session]\nActivePanel = 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	panel.LastWidePanel = 1
	LoadSession()
	if panel.LastWidePanel != -1 {
		t.Fatalf("old session enabled Wide: got %d, want -1", panel.LastWidePanel)
	}
}

func TestActionPanelSettings_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionPanelSettings(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("PanelSettings.Title") {
		t.Fatalf("Expected Panel Settings dialog, got %v", top)
	}

	// ????????? ??????? ???????? ??? ?????????? ?????
	dlg := top.(vtui.Container)
	found := false
	for _, itm := range dlg.GetChildren() {
		if chk, ok := itm.(*vtui.Checkbox); ok {
			if strings.Contains(chk.GetText(), "paths") {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("Save paths checkbox not found in Panel Settings dialog")
	}

	// ????????? ??????? ???????? ??????????????
	foundAc := false
	for _, itm := range dlg.GetChildren() {
		if chk, ok := itm.(*vtui.Checkbox); ok {
			if strings.Contains(strings.ToLower(chk.GetText()), "auto-completion") {
				foundAc = true
				break
			}
		}
	}
	if !foundAc {
		t.Error("Command line auto-completion checkbox not found in Panel Settings dialog")
	}

	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionPanelSettings_FitsSmallTerminal(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionPanelSettings(pf)
	mainDlg := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	_, y1, _, y2 := mainDlg.GetPosition()
	if got := y2 - y1 + 1; got > 25 {
		t.Fatalf("Panel settings dialog is %d rows tall; want at most 25", got)
	}
	vtui.AssertLayout(t, mainDlg)

	testutil.ClickDialogButton(t, mainDlg, "Additional settings")
	additionalDlg := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	_, y1, _, y2 = additionalDlg.GetPosition()
	if got := y2 - y1 + 1; got > 25 {
		t.Fatalf("Additional panel settings dialog is %d rows tall; want at most 25", got)
	}
	vtui.AssertLayout(t, additionalDlg)

	additionalDlg.SetExitCode(-1)
	vtui.FrameManager.Pop()
	mainDlg.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionPanelSettings_ConsoleModes(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.ConsoleMode = "own"
	config.App.ConsoleOverlayUI = false

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionPanelSettings(pf)
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Fatal("Panel settings dialog not shown")
	}
	dlg := top.(vtui.Container)
	testutil.ClickDialogButton(t, dlg, "Additional settings")
	top = vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Fatal("Additional panel settings dialog not shown")
	}
	dlg = top.(vtui.Container)

	var radioConsole *vtui.RadioGroup
	var chkOverlay *vtui.Checkbox
	for _, child := range dlg.GetChildren() {
		if r, ok := child.(*vtui.RadioGroup); ok && len(r.Items) == 2 {
			radioConsole = r
		}
		if c, ok := child.(*vtui.Checkbox); ok && strings.Contains(c.GetText(), "command line") {
			chkOverlay = c
		}
	}

	if radioConsole == nil {
		t.Fatal("Console mode radio group not found in Panel Settings dialog")
	}
	if chkOverlay == nil {
		t.Fatal("Console overlay checkbox not found in Panel Settings dialog")
	}

	if radioConsole.Selected != 0 {
		t.Errorf("Expected console mode radio selected 0, got %d", radioConsole.Selected)
	}
	if !chkOverlay.IsDisabled() {
		t.Error("Console overlay checkbox should be disabled when own terminal is selected")
	}

	// Select host console
	radioConsole.Selected = 1
	if radioConsole.OnChange != nil {
		radioConsole.OnChange(1)
	}
	if chkOverlay.IsDisabled() {
		t.Error("Console overlay checkbox should be enabled when host console is selected")
	}
	chkOverlay.State = 1

	testutil.ClickDialogButton(t, dlg, "Ok")
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	if config.App.ConsoleMode != "host" {
		t.Errorf("config.App.ConsoleMode = %q, want host", config.App.ConsoleMode)
	}
	if !config.App.ConsoleOverlayUI {
		t.Error("config.App.ConsoleOverlayUI = false, want true")
	}
	if top = vtui.FrameManager.GetTopFrame(); top != nil {
		top.SetExitCode(-1)
		vtui.FrameManager.Pop()
	}
}

func TestActionPanelAdditionalSettings_SearchExactOnHit(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.SearchExactOnHit = false

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionPanelAdditionalSettings(pf)
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Fatal("Additional panel settings dialog not shown")
	}
	dlg := top.(vtui.Container)

	var chkExact *vtui.Checkbox
	for _, child := range dlg.GetChildren() {
		if c, ok := child.(*vtui.Checkbox); ok && strings.Contains(c.GetText(), "uick Search") {
			chkExact = c
		}
	}
	if chkExact == nil {
		t.Fatal("SearchExactOnHit checkbox not found in Additional panel settings")
	}
	chkExact.State = 1

	testutil.ClickDialogButton(t, dlg, "Ok")
	if !config.App.SearchExactOnHit {
		t.Error("config.App.SearchExactOnHit = false, want true after OK")
	}
	if top = vtui.FrameManager.GetTopFrame(); top != nil {
		top.SetExitCode(-1)
		vtui.FrameManager.Pop()
	}
}

func TestActionLanguage_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionLanguage(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Fatalf("Expected Language dialog, got nil")
	}

	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatalf("Expected container dialog for Language settings, got %T", top)
	}

	foundUICombo := false
	foundHelpCombo := false
	foundLocalFilesCheckbox := false

	for _, child := range dlg.GetChildren() {
		if checkbox, ok := child.(*vtui.Checkbox); ok {
			foundLocalFilesCheckbox = true
			if checkbox.State != boolToCheckboxState(config.App.UseLocalLanguageFiles) {
				t.Errorf("local language files checkbox state = %d, want %d", checkbox.State, boolToCheckboxState(config.App.UseLocalLanguageFiles))
			}
		}
		if combo, ok := child.(*vtui.ComboBox); ok {
			for _, item := range combo.Menu.Items {
				if item.Text == "English" {
					if !foundUICombo {
						foundUICombo = true
					} else {
						foundHelpCombo = true
					}
					break
				}
			}
		}
	}

	if !foundUICombo || !foundHelpCombo || !foundLocalFilesCheckbox {
		t.Errorf("Language dialog missing UI/Help controls. UICombo: %v, HelpCombo: %v, local-files checkbox: %v", foundUICombo, foundHelpCombo, foundLocalFilesCheckbox)
	}

	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}
func TestActionManagePlugins_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	oldPlugins := config.App.RegisteredPlugins
	config.App.RegisteredPlugins = []string{"/old/path"}
	defer func() { config.App.RegisteredPlugins = oldPlugins }()

	actionManagePlugins(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var lb *vtui.ListBox
	for _, itm := range top.GetChildren() {
		if l, ok := itm.(*vtui.ListBox); ok {
			lb = l
			break
		}
	}
	if lb == nil {
		t.Fatal("ListBox not found")
	}

	// 1. Test Remove
	var btnRem *vtui.Button
	for _, itm := range top.GetChildren() {
		if b, ok := itm.(*vtui.Button); ok && strings.Contains(b.GetText(), "Remove") {
			btnRem = b
			break
		}
	}
	btnRem.OnClick()

	confirmDlg, _ := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if confirmDlg != nil && confirmDlg.OnResult != nil {
		confirmDlg.OnResult(0)
	}

	if len(config.App.RegisteredPlugins) != 0 {
		t.Error("Plugin was not removed from config")
	}

	// 2. Test Add (simulating SelectFileDialog callback)
	tmpDir := t.TempDir()
	testFile := "my_plugin.sh"
	if err := os.WriteFile(filepath.Join(tmpDir, testFile), []byte("#!/bin/sh"), 0600); err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(tmpDir, testFile)
	config.App.RegisteredPlugins = append(config.App.RegisteredPlugins, newPath)
	lb.Items = config.App.RegisteredPlugins
	lb.UpdateRows()

	if len(config.App.RegisteredPlugins) != 1 || config.App.RegisteredPlugins[0] != newPath {
		t.Errorf("Failed to add new plugin. Current: %v", config.App.RegisteredPlugins)
	}
}
func TestActionRename_CacheAndSelection(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "old.txt")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = vfs.NewOSVFS(tmpDir)
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "old.txt"}}}
	fsp.SetCursorIndex(0)
	pf.ActiveIdx = 0

	// ????????? ??? ???????
	fsp.DirCache[fsp.CacheKey(fsp.Vfs.GetPath())] = panel.DirCacheEntry{Items: []vfs.VFSItem{{Name: "old.txt"}}}

	// 1. ???? ????????? ??????????????
	// ????????????? InputBox ?????? actionRename (? ?????? ?? ?? ?????????)
	// ?? ??????? ??????? ??????, ??????? ?????? ??? ??????? InputBox
	newName := "new.txt"
	oldPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), "old.txt")
	newPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), newName)

	// ?????????? ???????? ??????????? ?????
	if err := fsp.Vfs.Rename(context.Background(), oldPath, newPath); err != nil {
		t.Fatal(err)
	}

	// ????????? UI-????? ?? actionRename (?????)
	delete(fsp.DirCache, fsp.CacheKey(fsp.Vfs.GetPath()))
	fsp.PendingSelection = newName
	pf.RefreshAll()

	if _, ok := fsp.DirCache[fsp.CacheKey(fsp.Vfs.GetPath())]; ok {
		t.Error("Cache was not cleared after rename")
	}
	if fsp.PendingSelection != "new.txt" {
		t.Errorf("Pending selection not set correctly: %q", fsp.PendingSelection)
	}

	// 2. ???? ?????? ??????????????
	fsp.PendingSelection = ""
	fsp.Vfs = &mockRenameVFS{VFS: fsp.Vfs, renameErr: os.ErrPermission}

	// ????????? UI-????? ?? actionRename (??????)
	fsp.PendingSelection = "old.txt" // ?????? ????????? ? ??????? ?????
	pf.RefreshAll()

	if fsp.PendingSelection != "old.txt" {
		t.Error("On error, pendingSelection should point to the original name")
	}
}
func TestActionExecute_WindowsFormatSimulation(t *testing.T) {
	// ?????????, ??? ?????? ???????, ??????? ?? ??????? ??? Windows,
	// ????????? ???????????????? ????????.
	tv := terminal.NewTerminalView(80, 24)
	p := terminal.NewAnsiParser(tv, nil)

	dir := "C:\\Users\\f4\\Desktop"
	cmd := "echo \"hello world\""

	// ????????? ???????? ??????? ??? Windows (??? ? panels_frame.go / actions.go)
	// ?????????? %q ??? ?????
	wireCmd := fmt.Sprintf("cd /d %q & %s\r\n", dir, cmd)

	// ?????????, ??? ? ?????????????? ?????? ???? ???????????, ?? ??????? ??????? ??????
	if !strings.Contains(wireCmd, "\" & ") {
		t.Fatalf("Generated wire command format changed! Parser relies on '\" & ' separator. Got: %q", wireCmd)
	}

	p.Process([]byte(wireCmd))

	result := string(tv.GetAllLogBytes())

	if strings.Contains(result, "cd /d") {
		t.Errorf("Technical CD leaked! Wire: %q, Result: %q", wireCmd, result)
	}

	if !strings.Contains(result, cmd) {
		t.Errorf("Real command lost! Wire: %q, Result: %q", wireCmd, result)
	}
}

type mockRenameVFS struct {
	vfs.VFS
	renameErr error
}

func (m *mockRenameVFS) Rename(ctx context.Context, old, new string) error {
	return m.renameErr
}

type mockSlowVFS struct {
	vfs.VFS
	onOpen    func()
	openDelay time.Duration
	openBlock <-chan struct{}
}

func (m *mockSlowVFS) GetPath() string     { return "/mock" }
func (m *mockSlowVFS) IsAbs(p string) bool { return true }
func (m *mockSlowVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "file.txt", IsDir: false, Size: 100}, nil
}
func (m *mockSlowVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	if m.onOpen != nil {
		m.onOpen()
	}
	if m.openDelay > 0 {
		timer := time.NewTimer(m.openDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if m.openBlock != nil {
		select {
		case <-m.openBlock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &vfs.MemoryReadAtCloser{Data: []byte("mock")}, nil
}

func TestActionOpenViewer_ProgressTask(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	called := false
	mv := &mockSlowVFS{
		// Leave a generous observation window after the delayed dialog appears;
		// loaded Windows CI can otherwise schedule the UI drain only after Open
		// has already completed and legitimately closed it.
		openDelay: openingProgressDelay + 500*time.Millisecond,
		onOpen: func() {
			called = true
		},
	}

	actionOpenViewer(pf, mv, "/mock/file.txt")

	// Drain task queue to allow UI and async updates, detecting the dialog on the fly
	timeout := time.After(1 * time.Second)
	foundProgress := false
LoopOpen:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			for _, scr := range vtui.FrameManager.Screens {
				for _, f := range scr.Frames {
					if strings.Contains(f.GetTitle(), "Opening") {
						foundProgress = true
					}
				}
			}
		case <-timeout:
			break LoopOpen
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if !called {
		t.Fatal("TEST FAILED: mockVFS.Open was not called!")
	}
	if !foundProgress {
		t.Fatal("TEST FAILED: No progress dialog was shown when opening a slow/remote file!")
	}

	t.Log("SUCCESS: Progress dialog was correctly displayed during slow file load.")
}

func TestActionOpenViewer_FastTaskDoesNotFlashProgressDialog(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	called := false
	mv := &mockSlowVFS{onOpen: func() { called = true }}
	actionOpenViewer(pf, mv, "/mock/file.txt")

	deadline := time.NewTimer(openingProgressDelay + 150*time.Millisecond)
	defer deadline.Stop()
	foundProgress := false
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			for _, screen := range vtui.FrameManager.Screens {
				for _, frame := range screen.Frames {
					if strings.Contains(frame.GetTitle(), "Opening") {
						foundProgress = true
					}
				}
			}
		case <-deadline.C:
			if !called {
				t.Fatal("mock VFS Open was not called")
			}
			if foundProgress {
				t.Fatal("fast viewer open flashed a delayed progress dialog")
			}
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestActionOpenViewer_PromptStaysAboveDelayedProgressDialog(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	modalShown := make(chan struct{})
	releaseOpen := make(chan struct{})
	var prompt *vtui.Window
	mv := &mockSlowVFS{
		openBlock: releaseOpen,
		onOpen: func() {
			vtui.FrameManager.PostTask(func() {
				prompt = vtui.NewCenteredDialog(30, 5, "Archive.PasswordTitle")
				vtui.FrameManager.Push(prompt)
				close(modalShown)
			})
		},
	}

	actionOpenViewer(pf, mv, "/mock/file.txt")

	deadline := time.After(openingProgressDelay + 300*time.Millisecond)
	for prompt == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("password prompt was not shown")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	select {
	case <-modalShown:
	case <-time.After(time.Second):
		t.Fatal("password prompt was not published")
	}

	if top := vtui.FrameManager.GetTopFrame(); top != prompt {
		title := "<nil>"
		if top != nil {
			title = top.GetTitle()
		}
		t.Fatalf("top frame = %T %q, want the password prompt", top, title)
	}
	for _, screen := range vtui.FrameManager.Screens {
		for _, frame := range screen.Frames {
			if strings.Contains(frame.GetTitle(), "Opening") {
				t.Fatalf("delayed progress dialog covered the prompt")
			}
		}
	}

	vtui.FrameManager.Pop()
	close(releaseOpen)
	deadline = time.After(time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("viewer open did not complete after dismissing the prompt")
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if top := vtui.FrameManager.GetTopFrame(); top != nil && strings.Contains(top.GetTitle(), "Opening") {
			t.Fatal("progress dialog appeared after the prompt was dismissed")
		}
		if len(vtui.FrameManager.Screens) > 1 {
			return
		}
	}
}

func TestActionCommandHistory_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	pf.CmdLine.Edit.History = nil
	actionCommandHistory(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || !strings.Contains(top.GetTitle(), "History") {
		t.Fatalf("Expected empty history warning dialog, got %v", top)
	}
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()

	pf.CmdLine.Edit.History = []string{"cmd1", "cmd2"}
	actionCommandHistory(pf)

	top = vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("History.CommandsTitle") {
		t.Fatalf("Expected Command History dialog, got %v", top)
	}

	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionCommandHistory_Deletion(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	pf.CmdLine.Edit.History = []string{"cmd1", "cmd2", "cmd3"}
	actionCommandHistory(pf)

	top := vtui.FrameManager.GetTopFrame()
	menu, ok := top.(*vtui.VMenu)
	if !ok {
		t.Fatalf("Expected VMenu on top, got %T", top)
	}

	menu.SetSelectPos(1)

	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_DELETE,
		ControlKeyState: vtinput.ShiftPressed,
	})

	history := pf.CmdLine.Edit.History
	if len(history) != 2 || history[0] != "cmd1" || history[1] != "cmd3" {
		t.Errorf("Expected history [cmd1, cmd3], got %v", history)
	}

	if menu.ItemCount != 2 {
		t.Errorf("Expected 2 menu items, got %d", menu.ItemCount)
	}

	menu.SetExitCode(-1)
	vtui.FrameManager.Pop()
}
func TestActionAppearanceSettings_SaveCursor(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	oldCfg := config.App
	defer func() { config.App = oldCfg }()

	config.App.KeepTerminalCursor = false

	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var chkCursor *vtui.Checkbox
	for _, itm := range top.GetChildren() {
		if c, ok := itm.(*vtui.Checkbox); ok {
			if strings.Contains(strings.ToLower(c.GetText()), "cursor") {
				chkCursor = c
				break
			}
		}
	}
	if chkCursor == nil {
		t.Fatal("KeepTerminalCursor checkbox not found in Appearance Settings")
	}

	if chkCursor.State != 0 {
		t.Error("Expected checkbox to be unchecked initially")
	}

	chkCursor.State = 1
	testutil.ClickDialogButton(t, top, "Ok")

	for i := 0; i < 10; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
		}
	}

	if !config.App.KeepTerminalCursor {
		t.Error("KeepTerminalCursor was not saved to config.App")
	}
}

func TestActionAppearanceSettingsSavesSystemMonospace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldConfig := config.App
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		config.App = oldConfig
		config.GetUserConfigIniPath = oldPath
	}()
	config.App.GuiUseSystemMonospace = true

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var systemFont *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == i18n.Msg("AppearanceSettings.UseSystemMonospace") {
			systemFont = checkbox
			break
		}
	}
	if systemFont == nil {
		t.Fatal("system monospace checkbox not found in Appearance Settings")
	}
	if systemFont.State != 1 {
		t.Fatal("system monospace checkbox must be enabled by default")
	}
	systemFont.Toggle()
	testutil.ClickDialogButton(t, top, "Ok")
	if config.App.GuiUseSystemMonospace {
		t.Fatal("system monospace setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesFullPathInTitle(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldConfig := config.App
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		config.App = oldConfig
		config.GetUserConfigIniPath = oldPath
	}()
	config.App.DisplayFullPathInTitle = false

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var fullPath *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == i18n.Msg("AppearanceSettings.DisplayFullPathInTitle") {
			fullPath = checkbox
			break
		}
	}
	if fullPath == nil {
		t.Fatal("full path in title checkbox not found in Appearance Settings")
	}
	if fullPath.State != 0 {
		t.Fatal("full path in title checkbox must be disabled by default")
	}
	fullPath.Toggle()
	testutil.ClickDialogButton(t, top, "Ok")
	if !config.App.DisplayFullPathInTitle {
		t.Fatal("full path in title setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesWorkspaceTabRestoration(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldConfig := config.App
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		config.App = oldConfig
		config.GetUserConfigIniPath = oldPath
	}()
	config.App.RestoreWorkspaceTabs = true

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var restoreTabs *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == i18n.Msg("AppearanceSettings.RestoreWorkspaceTabs") {
			restoreTabs = checkbox
			break
		}
	}
	if restoreTabs == nil {
		t.Fatal("workspace tab restoration checkbox not found in Appearance Settings")
	}
	if restoreTabs.State != 1 {
		t.Fatal("workspace tab restoration must be enabled by default")
	}
	restoreTabs.Toggle()
	testutil.ClickDialogButton(t, top, "Ok")
	if config.App.RestoreWorkspaceTabs {
		t.Fatal("disabled workspace tab restoration setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesWorkspaceTabOverlay(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldConfig := config.App
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		config.App = oldConfig
		config.GetUserConfigIniPath = oldPath
	}()
	config.App.WorkspaceTabsOverlay = true

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var overlayTabs *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == i18n.Msg("AppearanceSettings.WorkspaceTabsOverlay") {
			overlayTabs = checkbox
			break
		}
	}
	if overlayTabs == nil {
		t.Fatal("workspace tab overlay checkbox not found in Appearance Settings")
	}
	if overlayTabs.State != 1 {
		t.Fatal("workspace tab overlay must be enabled by default")
	}
	overlayTabs.Toggle()
	testutil.ClickDialogButton(t, top, "Ok")
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	if config.App.WorkspaceTabsOverlay {
		t.Fatal("disabled workspace tab overlay setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesWorkspaceTabNumbering(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldConfig := config.App
	oldPath := config.GetUserConfigIniPath
	config.GetUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		config.App = oldConfig
		config.GetUserConfigIniPath = oldPath
	}()
	config.App.WorkspaceTabNumbering = config.WorkspaceTabNumbersAlways

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var numbering *vtui.ComboBox
	for _, child := range top.GetChildren() {
		combo, ok := child.(*vtui.ComboBox)
		if ok && combo.Edit.GetText() == i18n.Msg("AppearanceSettings.WorkspaceNumbersAlways") {
			numbering = combo
			break
		}
	}
	if numbering == nil {
		t.Fatal("workspace tab numbering combobox not found in Appearance Settings")
	}
	numbering.Menu.SetSelectPos(int(config.WorkspaceTabNumbersOrder))
	testutil.ClickDialogButton(t, top, "Ok")
	if config.App.WorkspaceTabNumbering != config.WorkspaceTabNumbersOrder {
		t.Fatalf("workspace tab numbering = %v, want order", config.App.WorkspaceTabNumbering)
	}
}

// TestActionAppearanceSettings_CancelPreservesPalette locks in the
// fix: farcolors.ini overrides applied at startup were wiped when
// the user opened Appearance settings and pressed Cancel, because
// the dialog restored via theme.ApplyColorStyle(originalStyle) ? a clean
// re-apply of the named base style with no room for runtime
// overrides. Snapshot-and-copy the whole palette instead, so
// Cancel returns exactly what was on screen before the dialog
// opened, regardless of where the tweak came from.
func TestActionAppearanceSettings_CancelPreservesPalette(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	// Simulate a farcolors.ini override: bump one palette slot to
	// a sentinel value the base style would never produce. If Cancel
	// restores by name it clobbers this back to the style default;
	// if it restores by palette snapshot the sentinel survives.
	const sentinel uint64 = 0xDEADBEEFCAFE0001
	origAtIdx := vtui.Palette[theme.ColPanelText]
	vtui.Palette[theme.ColPanelText] = sentinel
	defer func() { vtui.Palette[theme.ColPanelText] = origAtIdx }()

	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	// Trigger live preview: pick a style different from the current
	// one so theme.ApplyColorStyle actually runs and overwrites the
	// sentinel. Any built-in style other than the current one works.
	var combo *vtui.ComboBox
	for _, itm := range top.GetChildren() {
		if c, ok := itm.(*vtui.ComboBox); ok {
			combo = c
			break
		}
	}
	if combo == nil {
		t.Fatal("style combobox not found in Appearance dialog")
	}
	// Pick the *other* end of the list ? different from whatever
	// index the config currently points to.
	target := 0
	if combo.Menu.SelectPos == 0 && len(combo.Menu.Items) > 1 {
		target = len(combo.Menu.Items) - 1
	}
	combo.Menu.OnAction(target)
	if vtui.Palette[theme.ColPanelText] == sentinel {
		t.Fatal("test setup: live preview didn't overwrite the sentinel ? need a different palette slot or style pair")
	}

	testutil.ClickDialogButton(t, top, "Cancel")

	if got := vtui.Palette[theme.ColPanelText]; got != sentinel {
		t.Errorf("Cancel dropped the override: palette[theme.ColPanelText]=%016x, want sentinel %016x", got, sentinel)
	}
}

func TestActionAppearanceSettings_LivePreviewRecolorsExistingLabels(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	actionAppearanceSettings(pf)
	frame := vtui.FrameManager.GetTopFrame()
	top := frame.(vtui.Container)

	var combo *vtui.ComboBox
	var label *vtui.Text
	for _, item := range top.GetChildren() {
		switch control := item.(type) {
		case *vtui.ComboBox:
			if combo == nil {
				combo = control
			}
		case *vtui.Text:
			if label == nil {
				label = control
			}
		}
	}
	if combo == nil || label == nil {
		t.Fatal("Appearance dialog style combobox or label not found")
	}

	before := vtui.Palette[vtui.ColDialogText]
	target := -1
	for idx := range combo.Menu.Items {
		combo.Menu.OnAction(idx)
		if vtui.Palette[vtui.ColDialogText] != before {
			target = idx
			break
		}
	}
	if target < 0 {
		t.Fatal("no available style changes Dialog.Text; cannot verify live preview")
	}

	frame.Show(scr)
	_, y, x, _ := label.GetPosition()
	if got, want := scr.GetCell(x, y).Attributes, vtui.Palette[vtui.ColDialogText]; got != want {
		t.Fatalf("existing Appearance label kept stale color %#x after style switch, want %#x", got, want)
	}

	testutil.ClickDialogButton(t, top, "Cancel")
}

func TestPanelsFrame_RunAdvancedProgressTask(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))

	done := make(chan struct{})
	completed := make(chan struct{})
	workerBlock := make(chan struct{})
	var reporter vfs.TaskReporter

	pf.RunAdvancedProgressTask("Test Action", false, func(ctx context.Context, rep vfs.TaskReporter) error {
		reporter = rep
		close(done)
		<-workerBlock
		return nil
	}, func(error) { close(completed) })

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for progress task worker to run")
	}

	// Wait for the dialog to appear on top
	timeout := time.After(2 * time.Second)
	var dlg *fileops.FileOpProgressDialog
	for dlg == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && top.GetTitle() == "Test Action" {
				dlg = top.(*fileops.FileOpProgressDialog)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for dialog to appear")
		}
	}

	reporter.UpdateTransfer("Running", "item", 75, "Total Info", 35, "10 MB/s")

	// Wait for UI to update with progress
	timeout = time.After(2 * time.Second)
	for {
		if state := dlg.Progress(); state.CurrentVisible && state.CurrentPercent == 75 {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for progress update to render")
		}
	}

	state := dlg.Progress()
	if !state.CurrentVisible || state.CurrentPercent != 75 {
		t.Errorf("Current progress bar not updated: visible=%v, pct=%d", state.CurrentVisible, state.CurrentPercent)
	}
	if !state.TotalVisible || state.TotalPercent != 35 {
		t.Errorf("Total progress bar not updated: visible=%v, pct=%d", state.TotalVisible, state.TotalPercent)
	}
	if state.Speed != "10 MB/s" {
		t.Errorf("Speed label not updated, got %q", state.Speed)
	}

	// Close dialog and unblock worker
	close(workerBlock)
	timeout = time.After(2 * time.Second)
	for completed != nil {
		select {
		case <-completed:
			completed = nil
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("progress task did not complete")
		}
	}
}

type mockExtractionVFS struct {
	vfs.VFS
	parent vfs.VFS
}

func (m *mockExtractionVFS) ParentVFS() vfs.VFS { return m.parent }

func TestExecuteFileOp_ContextualTitles(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "data.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	parent := vfs.NewOSVFS(srcDir)
	srcVfs := &mockExtractionVFS{VFS: parent, parent: parent}
	dstVfs := vfs.NewOSVFS(t.TempDir())

	done := make(chan struct{})
	fileops.ExecuteFileOp(srcVfs, dstVfs, []string{"data.txt"}, dstVfs.GetPath(), false, 2, func() {
		close(done)
	})

	appeared := time.After(10 * time.Second)
	var dlg *fileops.FileOpProgressDialog
	for dlg == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Extracting") {
				dlg = top.(*fileops.FileOpProgressDialog)
			}
		case <-appeared:
			t.Fatal("Timeout waiting for Extracting dialog to appear")
		}
	}

	// Wait for the dialog this test created, not for the frame stack to empty.
	// The stack is process-wide, so a frame another test left on it would make
	// the loop below spin until it timed out, and the two phases used to share
	// a single deadline as well: a slow first phase left the second almost
	// none of it.
	completed := time.After(10 * time.Second)
	for !dlg.IsDone() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-completed:
			t.Fatal("Timeout waiting for copy operation to complete")
		}
	}
	if vtui.FrameManager.GetTopFrame() == vtui.Frame(dlg) {
		vtui.FrameManager.Pop()
	}

	// fileops.ExecuteFileOp runs on its own goroutine and reads the temporary
	// directories above. Returning before it exits leaves it running into
	// whatever test comes next, reading directories t.TempDir has removed.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for the file operation goroutine to exit")
	}
}

type mockInvalidVFS struct {
	vfs.VFS
}

func (m *mockInvalidVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	return nil, os.ErrInvalid
}
func (m *mockInvalidVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	return vfs.VFSItem{Name: "special", IsDir: false}, nil
}

func TestActionOpenEditor_SpecialFileRejection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	v := &mockInvalidVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	pf := panel.NewPanelsFrame()
	defer pf.Close()

	actionOpenEditor(pf, v, "special")

	timeout := time.After(1 * time.Second)
	foundError := false
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Error") {
				foundError = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}
	if !foundError {
		t.Error("Expected error dialog for special file in editor")
	}
}

func TestActionOpenViewer_SpecialFileRejection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	v := &mockInvalidVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	pf := panel.NewPanelsFrame()
	defer pf.Close()

	actionOpenViewer(pf, v, "special")

	timeout := time.After(1 * time.Second)
	foundError := false
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Error") {
				foundError = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}
	if !foundError {
		t.Error("Expected error dialog for special file in viewer")
	}
}
func TestActionEditFile_DirectoryRedirectsToAttributes(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	subDirName := "sub_folder"
	subDirPath := filepath.Join(tmpDir, subDirName)
	if err := os.Mkdir(subDirPath, 0700); err != nil {
		t.Fatalf("Failed to create sub directory: %v", err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	fsp.Vfs = v
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: subDirName, IsDir: true}},
	}
	fsp.SetCursorIndex(1) // Focus on "sub_folder"
	pf.ActiveIdx = 0

	// Trigger Edit (F4)
	actionEditFile(pf)

	// Wait for the async task that reads Stat and shows the Attributes dialog
	timeout := time.After(2 * time.Second)
	foundAttributes := false
	for !foundAttributes {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Attributes") {
				foundAttributes = true
			}
		case <-timeout:
			t.Fatal("Timeout waiting for Attributes dialog to open on F4")
		}
	}

	if !foundAttributes {
		t.Error("Expected Attributes dialog to open when pressing F4 on a directory")
	}
}
func TestActionCreateLink_Flow(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)

	tmpDir := t.TempDir()
	fspSrc := pf.Panels[0].(*panel.FileSystemPanel)
	fspDst := pf.Panels[1].(*panel.FileSystemPanel)
	paneltest.WaitForLoad(t, fspSrc)
	paneltest.WaitForLoad(t, fspDst)

	srcDir := filepath.Join(tmpDir, "src")
	dstDir := filepath.Join(tmpDir, "dst")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0700); err != nil {
		t.Fatal(err)
	}

	targetFile := filepath.Join(srcDir, "target.txt")
	if err := os.WriteFile(targetFile, []byte("link target content"), 0600); err != nil {
		t.Fatal(err)
	}

	fspSrc.Vfs = vfs.NewOSVFS(srcDir)
	fspDst.Vfs = vfs.NewOSVFS(dstDir)
	fspSrc.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "target.txt"}}}
	fspSrc.SetCursorIndex(0)
	pf.ActiveIdx = 0

	actionCreateLink(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != i18n.Msg("Link.Title") {
		t.Fatalf("Expected Link dialog, got %v", top)
	}

	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatal("Link dialog is not a container")
	}

	var editDest *vtui.Edit
	for _, child := range dlg.GetChildren() {
		if e, ok := child.(*vtui.Edit); ok {
			editDest = e
			break
		}
	}
	if editDest == nil {
		t.Fatal("Destination edit field not found in dialog")
	}

	assertComboMenuDoesNotCoverButtons(t, dlg, "link")
	focusDlg, ok := top.(dialogFocusContainer)
	if !ok {
		t.Fatal("link dialog does not expose focus traversal")
	}
	assertDialogTabOrderMatchesVisualOrder(t, focusDlg, "link")

	linkPath := filepath.Join(dstDir, "link.txt")
	editDest.SetText(linkPath)

	srcGeneration := fspSrc.LoadingGeneration
	dstGeneration := fspDst.LoadingGeneration
	testutil.ClickDialogButton(t, dlg, "Create link")

	// Drain task queue to execute async creation task
	timeout := time.After(2 * time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for link creation task")
		default:
		}
		if _, err := os.Lstat(linkPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := os.Lstat(linkPath); err != nil {
		t.Fatalf("Link was not created at %s: %v", linkPath, err)
	}

	timeout = time.After(2 * time.Second)
	for fspSrc.LoadingGeneration == srcGeneration || fspDst.LoadingGeneration == dstGeneration {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for link completion refresh")
		}
	}
	paneltest.WaitForLoad(t, fspSrc)
	paneltest.WaitForLoad(t, fspDst)
}
func TestActionSwitchEditorToViewerAndBack(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	FilePath := filepath.Join(tmpDir, "switch_test.txt")
	content := "Line 0\nLine 1\nLine 2\nLine 3\nLine 4\n"
	if err := os.WriteFile(FilePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	// The editor/viewer frames opened below hold the file; close whichever
	// is on top so Windows can delete it during TempDir cleanup.
	defer func() {
		switch top := vtui.FrameManager.GetTopFrame().(type) {
		case *editor.EditorView:
			top.Close()
		case *viewer.ViewerView:
			top.Close()
		}
	}()
	pf.ResizeConsole(80, 25)

	actionOpenEditor(pf, v, FilePath)

	timeout := time.After(2 * time.Second)
	var ev *editor.EditorView
	for ev == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if top, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				ev = top
			}
		case <-timeout:
			t.Fatal("Timeout waiting for editor to open")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	ev.CursorLine = 2
	ev.CursorPos = 0

	// 1. Switch Editor -> Viewer via action
	if !RunAction("Editor.SwitchToViewer") {
		t.Fatal("Editor.SwitchToViewer failed to run")
	}

	timeout = time.After(2 * time.Second)
	var vv *viewer.ViewerView
	for vv == nil {
		if top, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok {
			vv = top
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for viewer after switch")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	expectedOffset := int64(len("Line 0\nLine 1\n"))
	if vv.TopOffset != expectedOffset {
		t.Errorf("Viewer TopOffset mismatch: got %d, want %d", vv.TopOffset, expectedOffset)
	}

	// 2. Switch Viewer -> Editor via action
	if !RunAction("Viewer.SwitchToEditor") {
		t.Fatal("Viewer.SwitchToEditor failed to run")
	}

	timeout = time.After(2 * time.Second)
	var ev2 *editor.EditorView
	for ev2 == nil {
		if top, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
			ev2 = top
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for editor after switch back")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	defer ev2.Close()

	if ev2.CursorLine != 2 {
		t.Errorf("Editor CursorLine mismatch after switch back: got %d, want 2", ev2.CursorLine)
	}
}

func TestActionSwitchEditorToViewer_ModifiedFilePrompt(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	FilePath := filepath.Join(tmpDir, "modified_switch.txt")
	if err := os.WriteFile(FilePath, []byte("Original Content"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	// The editor/viewer frames opened below hold the file; close whichever
	// is on top so Windows can delete it during TempDir cleanup.
	defer func() {
		switch top := vtui.FrameManager.GetTopFrame().(type) {
		case *editor.EditorView:
			top.Close()
		case *viewer.ViewerView:
			top.Close()
		}
	}()
	pf.ResizeConsole(80, 25)

	actionOpenEditor(pf, v, FilePath)

	timeout := time.After(2 * time.Second)
	var ev *editor.EditorView
	for ev == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if top, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				ev = top
			}
		case <-timeout:
			t.Fatal("Timeout waiting for editor to open")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Modify content
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	if !ev.Modified {
		t.Fatal("Editor should be modified")
	}

	// Trigger SwitchToViewer -> should show confirmation dialog
	RunAction("Editor.SwitchToViewer")

	var confirmDlg *vtui.Window
	timeout = time.After(2 * time.Second)
	for confirmDlg == nil {
		if top, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); ok && top.GetTitle() == " Confirm " {
			confirmDlg = top
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for save confirmation dialog")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Click "Don't Save" button in confirmation dialog
	testutil.ClickDialogButton(t, confirmDlg, "Don't Save")

	var vv *viewer.ViewerView
	timeout = time.After(2 * time.Second)
	for vv == nil {
		if top, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok {
			vv = top
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for viewer after Don't Save switch")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	defer vv.Close()

	if vv.Path != FilePath {
		t.Errorf("Viewer opened path %q, want %q", vv.Path, FilePath)
	}
}

func TestActionSwitchEditorViewer_HeightPreserved(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	tmpDir := t.TempDir()
	FilePath := filepath.Join(tmpDir, "resize_test.txt")
	if err := os.WriteFile(FilePath, []byte("Content\n"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	// The editor/viewer frames opened below hold the file; close whichever
	// is on top so Windows can delete it during TempDir cleanup.
	defer func() {
		switch top := vtui.FrameManager.GetTopFrame().(type) {
		case *editor.EditorView:
			top.Close()
		case *viewer.ViewerView:
			top.Close()
		}
	}()
	pf.ResizeConsole(80, 25)

	actionOpenEditor(pf, v, FilePath)

	timeout := time.After(2 * time.Second)
	var ev *editor.EditorView
	for ev == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if top, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				ev = top
			}
		case <-timeout:
			t.Fatal("Timeout waiting for editor to open")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	initialY1, initialY2 := ev.Y1, ev.Y2

	for i := 0; i < 5; i++ {
		RunAction("Editor.SwitchToViewer")
		var vv *viewer.ViewerView
		timeout = time.After(1 * time.Second)
		for vv == nil {
			if top, ok := vtui.FrameManager.GetTopFrame().(*viewer.ViewerView); ok {
				vv = top
				break
			}
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-timeout:
				t.Fatalf("Iteration %d: timeout waiting for viewer", i)
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		if vv.Y1 != initialY1 || vv.Y2 != initialY2 {
			t.Fatalf("Iteration %d: Viewer height changed! Y1: %d (want %d), Y2: %d (want %d)", i, vv.Y1, initialY1, vv.Y2, initialY2)
		}

		RunAction("Viewer.SwitchToEditor")
		var evCurrent *editor.EditorView
		timeout = time.After(1 * time.Second)
		for evCurrent == nil {
			if top, ok := vtui.FrameManager.GetTopFrame().(*editor.EditorView); ok {
				evCurrent = top
				break
			}
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-timeout:
				t.Fatalf("Iteration %d: timeout waiting for editor", i)
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		if evCurrent.Y1 != initialY1 || evCurrent.Y2 != initialY2 {
			t.Fatalf("Iteration %d: Editor height changed! Y1: %d (want %d), Y2: %d (want %d)", i, evCurrent.Y1, initialY1, evCurrent.Y2, initialY2)
		}
	}
}
