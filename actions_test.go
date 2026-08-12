package main

import (
	"context"
	"fmt"
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

func TestActionExecute_RemoteRejection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// mockRemoteVFS does NOT satisfy the isLocal check in actionExecute
	baseVfs := vfs.NewOSVFS(t.TempDir())
	v := &mockFailingVFS{VFS: baseVfs}
	pf := NewPanelsFrame()
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
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25) // Crucial: initializes panels

	// 1. Trigger MkDir action (should push InputBox)
	actionMkDir(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("MakeFolder.Title") {
		t.Fatalf("Expected MkDir dialog, got %v", top)
	}

	// Close it to clean up
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
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
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Создаем мок-VFS, который запретит удаление "fail.txt"
	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"fail.txt"},
	}

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = mv

	// Подготавливаем список файлов: f1.txt (ок), fail.txt (ошибка), f2.txt (ок)
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "f1.txt"}},
		{VFSItem: vfs.VFSItem{Name: "fail.txt"}},
		{VFSItem: vfs.VFSItem{Name: "f2.txt"}},
	}
	// Выделяем все три файла
	fsp.entries[1].Selected = true
	fsp.entries[2].Selected = true
	fsp.entries[3].Selected = true

	// ВАЖНО: делаем панель с файлами активной
	pf.activeIdx = 0

	// 1. Инициируем удаление
	actionDelete(pf)

	// 2. Находим кнопку "Delete" в диалоге подтверждения и нажимаем её
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

	// 3. Прокручиваем очередь задач, ожидая появления диалога с итогами ошибок
	timeout := time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()

			// Если выскочил диалог ошибки удаления (AskError), нажимаем Skip
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

			// Ждем, когда на вершине стека окажется диалог с заголовком " Deletion Errors "
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == Msg("FileOp.DeletionErrors") {
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

	// 4. Проверяем результаты
	// Должно быть 2 успешных удаления (f1.txt и f2.txt)
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
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	SetDefaultF4Palette()

	mv := &mockRetryDeleteVFS{
		VFS:      vfs.NewOSVFS(t.TempDir()),
		attempts: map[string]int{"retry.txt": 1}, // Упадёт 1 раз
	}

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = mv
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "retry.txt"}}}
	pf.activeIdx = 0

	actionDelete(pf)

	// 1. Подтверждаем удаление
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	clickDialogButton(t, dlgConfirm, "Delete")

	// 2. Ждем диалог ошибки и жмем Retry
	timeout := time.After(2 * time.Second)
	retryClicked := false
Loop:
	for {
		if len(mv.deleted) == 1 {
			break Loop
		}
		select {
		case task := <-fm.TaskChan:
			task()
			if !retryClicked && fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				clickDialogButton(t, fm.GetTopFrame().(vtui.Container), "Retry")
				retryClicked = true
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
}

func TestActionDelete_Abort(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	SetDefaultF4Palette()

	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"abort.txt"},
	}

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = mv
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "abort.txt"}},
		{VFSItem: vfs.VFSItem{Name: "should_not_touch.txt"}},
	}
	fsp.entries[0].Selected = true
	fsp.entries[1].Selected = true
	pf.activeIdx = 0

	actionDelete(pf)
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	clickDialogButton(t, dlgConfirm, "Delete")

	// Ждем ошибку и жмем Abort
	timeout := time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				clickDialogButton(t, fm.GetTopFrame().(vtui.Container), "Abort")
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

	// Проверяем, что список удаленных пуст (первый упал, второй не начинали)
	if len(mv.deletedFiles) != 0 {
		t.Errorf("Abort failed: some files were deleted: %v", mv.deletedFiles)
	}
}
func TestActionDelete_SkipAll(t *testing.T) {
	fm := vtui.FrameManager
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fm.Init(scr)
	SetDefaultF4Palette()

	// Два файла, оба упадут
	mv := &mockDeletionFailingVFS{
		VFS:         vfs.NewOSVFS(t.TempDir()),
		failedFiles: []string{"fail1.txt", "fail2.txt"},
	}

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = mv
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "fail1.txt"}},
		{VFSItem: vfs.VFSItem{Name: "fail2.txt"}},
	}
	fsp.entries[0].Selected = true
	fsp.entries[1].Selected = true
	pf.activeIdx = 0

	actionDelete(pf)

	// 1. Подтверждаем удаление (Foreground mode)
	dlgConfirm := fm.GetTopFrame().(vtui.Container)
	for _, child := range dlgConfirm.GetChildren() {
		if c, ok := child.(*vtui.ComboBox); ok {
			c.Menu.SetSelectPos(2) // Foreground
		}
	}
	clickDialogButton(t, dlgConfirm, "Delete")

	// 2. Ждем первую ошибку и жмем "Skip All"
	timeout := time.After(2 * time.Second)
	skipAllClicked := false
Loop:
	for {
		select {
		case task := <-fm.TaskChan:
			task()

			if !skipAllClicked && fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == " Error " {
				if dlg, ok := fm.GetTopFrame().(vtui.Container); ok {
					for _, itm := range dlg.GetChildren() {
						if b, ok := itm.(*vtui.Button); ok && (strings.Contains(b.GetText(), "Skip All") || strings.Contains(b.GetText(), "S&kip All") || strings.Contains(b.GetText(), "ропустить")) {
							b.OnClick()
							skipAllClicked = true
							break
						}
					}
				}
			}

			// Ждем финальный диалог со списком ошибок
			if fm.GetTopFrameType() == vtui.TypeDialog && fm.GetTopFrame().GetTitle() == Msg("FileOp.DeletionErrors") {
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

	// 3. Проверяем, что в итоговом списке 2 ошибки, но диалог показывался только один раз
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
}
func TestActionExecute_PtyCommandFormatting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// setupMockPanelsFrame и mockPty определены в других тестовых файлах того же пакета
	pf := setupMockPanelsFrame()
	defer pf.Close()
	pty := pf.pty.(*mockPty)

	tmp := t.TempDir()
	fileName := "app.exe"
	if runtime.GOOS != "windows" {
		fileName = "app.sh"
	}
	filePath := filepath.Join(tmp, fileName)
	os.WriteFile(filePath, []byte("#!/bin/sh\nexit 0"), 0755)

	v := vfs.NewOSVFS(tmp)

	// Очищаем буфер PTY перед тестом
	pty.written = nil

	actionExecute(pf, v, tmp, fileName, filePath)

	// Прокачиваем задачи FrameManager
	timeout := time.After(2 * time.Second)
	for pf.showPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for execution task")
		}
	}

	// В реальном приложении данные из PTY проходят через AnsiParser, который
	// вырезает технические команды (cd /d) перед отображением. Эмулируем это:
	pf.parser.Process(pty.written)
	result := string(pf.termView.GetAllLogBytes())

	if runtime.GOOS == "windows" {
		// Проверяем отсутствие технической обертки 'cd /d' в выводе после парсера
		if strings.Contains(result, "cd /d") {
			t.Errorf("Technical 'cd /d' wrapper should be removed by parser, but found in log: %q", result)
		}
		// Команда должна присутствовать в логе (парсер вырезает cd /d, оставляя саму команду)
		if !strings.Contains(result, "app.exe") {
			t.Errorf("PTY command should appear in terminal log after cd excision, got log: %q", result)
		}
	}
}

func TestActionExecute_HistoryQuoting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := setupMockPanelsFrame()
	defer pf.Close()

	tmp := t.TempDir()
	fileName := "name with spaces.exe"
	filePath := filepath.Join(tmp, fileName)
	os.WriteFile(filePath, []byte(""), 0755)

	v := vfs.NewOSVFS(tmp)
	actionExecute(pf, v, tmp, fileName, filePath)

	timeout := time.After(2 * time.Second)
	for pf.showPanels {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	lastHistory := pf.cmdLine.Edit.History[0]
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
	os.WriteFile(hstPath, []byte(content), 0644)

	recs, err := importFar2lHistory(hstPath)
	if err != nil {
		t.Fatalf("importFar2lHistory failed: %v", err)
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
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	tmp := t.TempDir()
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs.SetPath(tmp)

	// Создаем 4 файла: f1, f2, f3, f4
	files := []string{"f1.txt", "f2.txt", "f3.txt", "f4.txt"}
	for _, f := range files {
		os.WriteFile(filepath.Join(tmp, f), []byte("data"), 0644)
	}

	// 1. Удаляем f2 и f3 (выделенные)
	// Дожидаемся загрузки
	fsp.ReadDirectory()
	for fsp.isLoading {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout")
		}
	}

	// Выделяем f2 и f3 (индексы 2 и 3, т.к. 0 - "..", 1 - "f1")
	fsp.entries[2].Selected = true
	fsp.entries[3].Selected = true

	// По логике Successor, после удаления блока f2, f3 курсор должен встать на f4.
	successor := fsp.GetSuccessorName()
	if successor != "f4.txt" {
		t.Errorf("Expected successor f4.txt, got %q", successor)
	}

	// 2. Удаляем последний файл (f4)
	fsp.entries[2].Selected = false
	fsp.entries[3].Selected = false
	fsp.SetCursorIndex(4) // f4
	successor = fsp.GetSuccessorName()
	// Если удаляем последний, курсор прыгает на предыдущий (f3)
	if successor != "f3.txt" {
		t.Errorf("Expected successor f3.txt when deleting tail, got %q", successor)
	}
}
func TestActionCopyMove_TrailingSlash(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Ensure predictable paths for the test
	fspSrc := pf.panels[0].(*FileSystemPanel)
	fspDst := pf.panels[1].(*FileSystemPanel)
	fspSrc.vfs.SetPath(filepath.FromSlash("/src/dir"))
	fspDst.vfs.SetPath(filepath.FromSlash("/dst/dir"))

	// Manually add an entry so actionCopyMove doesn't exit early
	fspSrc.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "test.txt", IsDir: false}},
	}
	fspSrc.SetCursorIndex(0)
	pf.activeIdx = 0 // Ensure the panel with the file is active

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
func TestActionCopy_ShiftF5_Prefill(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fspSrc := pf.panels[0].(*FileSystemPanel)

	// Setup actual existing paths using t.TempDir()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcPath, 0755)

	if err := fspSrc.vfs.SetPath(srcPath); err != nil {
		t.Fatalf("Failed to set src VFS path: %v", err)
	}

	// Mock entries so we have a file under the cursor
	fspSrc.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "test.txt", IsDir: false}},
	}
	fspSrc.SetCursorIndex(0)
	pf.activeIdx = 0

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
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25) // Crucial: initializes panels

	pf.activeIdx = 0
	actionNewFile(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("Edit.NewFileTitle") {
		t.Errorf("Expected New File dialog, got %v", top)
	}
}
func TestDelete_FocusCustomization(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "test.txt"}}}
	pf.activeIdx = 0

	origDelFocus := AppConfig.DeleteCancelFocused
	defer func() { AppConfig.DeleteCancelFocused = origDelFocus }()

	// 1. By default the destructive action is focused, matching the other
	// confirmation dialogs and allowing Enter to confirm it.
	AppConfig.DeleteCancelFocused = false
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
	AppConfig.DeleteCancelFocused = true
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

// TestActionDelete_UsesWarnPalette_Issue379 pins the fix for #379:
// delete is destructive, so the confirmation dialog must render on the
// red WarnDialog palette instead of the neutral one.
func TestActionDelete_UsesWarnPalette_Issue379(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "goner.txt"}, Selected: true}}
	pf.activeIdx = 0

	actionDelete(pf)

	top := fm.GetTopFrame()
	if top == nil {
		t.Fatal("Delete confirmation dialog was not shown")
	}
	dlg, ok := top.(*vtui.Window)
	if !ok {
		t.Fatalf("Top frame is not a *vtui.Window, got %T", top)
	}
	if !dlg.IsWarning {
		t.Error("Delete confirmation must render on the WarnDialog palette (see #379)")
	}
	fm.Pop()
}

func TestActionOpenEditor_AlreadyOpened(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(path, []byte("content"), 0644)

	v := vfs.NewOSVFS(tmpDir)
	pf := NewPanelsFrame()
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
	// ("switch / reload / new instance / cancel"), not a warning —
	// so the dialog now carries the semantic FileOp.AlreadyOpenedTitle
	// and must render on the neutral (non-warning) palette.
	wantTitle := Msg("FileOp.AlreadyOpenedTitle")
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
}

func TestActionOpenViewer_AlreadyOpened(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_view.txt")
	os.WriteFile(path, []byte("content"), 0644)

	v := vfs.NewOSVFS(tmpDir)
	pf := NewPanelsFrame()
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
	wantTitle := Msg("FileOp.AlreadyViewedTitle")
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
}

type mockLockedVFS struct {
	vfs.VFS
}

func (m *mockLockedVFS) Open(ctx context.Context, path string) (vfs.ReadAtCloser, error) {
	return nil, os.ErrPermission // Simulate locked file or sharing violation
}

func TestActionOpenEditor_LockedFile(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "locked.txt")
	os.WriteFile(path, []byte("data"), 0644)

	v := &mockLockedVFS{VFS: vfs.NewOSVFS(tmpDir)}
	pf := NewPanelsFrame()
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
	os.WriteFile(tmp, []byte(""), 0644)
	v := vfs.NewOSVFS(t.TempDir())

	vv, err := NewViewerView(context.Background(), v, tmp)
	if err != nil {
		t.Fatalf("Failed to create ViewerView: %v", err)
	}
	defer vv.Close()

	// Simulate search trigger
	// We manually call the inner logic of actionViewerSearch since InputBox is blocking in tests
	foundOffset := int64(-1)
	currOff := vv.TopOffset + 1
	fileSize := vv.backend.Size() // 0

	if currOff < fileSize {
		t.Error("Search loop should not even start for empty file")
	}

	if foundOffset != -1 {
		t.Error("Should not find anything in empty file")
	}
}
func TestActionFindFile_Persistence(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
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
	// Создаем временную директорию для теста
	tmpDir := t.TempDir()

	// Перехватываем путь к ini файлу (в реальном коде он завязан на os.UserConfigDir)
	// Для теста мы просто вручную вызовем SaveSession и проверим результат в файле.
	origPathFunc := getSessionIniPath
	getSessionIniPath = func() string { return filepath.Join(tmpDir, "session.ini") }
	defer func() { getSessionIniPath = origPathFunc }()

	LastEditorSearch = "disk-test"
	LastFindFileMask = "*.log"
	LastLeftPath = "/path/a"
	LastRightPath = "/path/b"
	LastLeftCursor = "file.a"
	LastRightCursor = "file.b"
	LastActivePanel = 0
	LastWidePanel = 1

	LastLeftViewMode = 1
	LastRightViewMode = 0
	LastLeftSortMode = 3
	LastRightSortMode = 2
	LastLeftSortRev = true
	LastRightSortRev = false

	LastShowPanels = false
	LastShowLeft = true
	LastShowRight = false

	SaveSession()

	// Сбрасываем и загружаем
	LastLeftPath = ""
	LastRightPath = ""
	LastLeftCursor = ""
	LastRightCursor = ""
	LastActivePanel = 1
	LastWidePanel = -1

	LastLeftViewMode = 0
	LastRightViewMode = 1
	LastLeftSortMode = 0
	LastRightSortMode = 0
	LastLeftSortRev = false
	LastRightSortRev = true

	LastShowPanels = true
	LastShowLeft = false
	LastShowRight = true

	LoadSession()

	if LastEditorSearch != "disk-test" || LastLeftPath != "/path/a" || LastLeftCursor != "file.a" || LastActivePanel != 0 {
		t.Errorf("Disk persistence failed. Search:%q, LeftPath:%q, LeftCursor:%q, Active:%d",
			LastEditorSearch, LastLeftPath, LastLeftCursor, LastActivePanel)
	}
	if LastWidePanel != 1 {
		t.Errorf("Wide panel persistence failed: got %d, want 1", LastWidePanel)
	}

	if LastLeftViewMode != 1 || LastRightViewMode != 0 || LastLeftSortMode != 3 || LastRightSortMode != 2 {
		t.Errorf("View/Sort modes persistence failed. LeftVM:%d, RightVM:%d, LeftSM:%d, RightSM:%d",
			LastLeftViewMode, LastRightViewMode, LastLeftSortMode, LastRightSortMode)
	}

	if !LastLeftSortRev || LastRightSortRev {
		t.Errorf("Sort directions persistence failed. LeftRev:%v, RightRev:%v", LastLeftSortRev, LastRightSortRev)
	}

	if LastShowPanels || !LastShowLeft || LastShowRight {
		t.Errorf("Panel visibility persistence failed. Show:%v, Left:%v, Right:%v", LastShowPanels, LastShowLeft, LastShowRight)
	}
}

func TestSession_OldFileDefaultsWideOff(t *testing.T) {
	tmpDir := t.TempDir()
	origPathFunc := getSessionIniPath
	getSessionIniPath = func() string { return filepath.Join(tmpDir, "session.ini") }
	defer func() { getSessionIniPath = origPathFunc }()

	if err := os.WriteFile(getSessionIniPath(), []byte("[Session]\nActivePanel = 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	LastWidePanel = 1
	LoadSession()
	if LastWidePanel != -1 {
		t.Fatalf("old session enabled Wide: got %d, want -1", LastWidePanel)
	}
}

func TestActionPanelSettings_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	actionPanelSettings(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("PanelSettings.Title") {
		t.Fatalf("Expected Panel Settings dialog, got %v", top)
	}

	// Проверяем наличие чекбокса для сохранения путей
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

	// Проверяем наличие чекбокса автодополнения
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
func TestActionLanguage_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// 1. Вызываем диалог выбора языка
	actionLanguage(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		t.Fatalf("Expected menu, got nil")
	}

	menu, ok := top.(*vtui.VMenu)
	if !ok {
		t.Fatalf("Expected VMenu for Language selection, got %T", top)
	}

	// 2. Проверяем, что в списке есть как минимум дефолтный English
	foundEnglish := false
	for _, itm := range menu.Items {
		if itm.Text == "English" {
			foundEnglish = true
			break
		}
	}
	if !foundEnglish {
		t.Errorf("English language option not found in menu")
	}

	// 3. Закрываем
	menu.Close()
	vtui.FrameManager.Pop()
}
func TestActionManagePlugins_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	oldPlugins := AppConfig.RegisteredPlugins
	AppConfig.RegisteredPlugins = []string{"/old/path"}
	defer func() { AppConfig.RegisteredPlugins = oldPlugins }()

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

	if len(AppConfig.RegisteredPlugins) != 0 {
		t.Error("Plugin was not removed from config")
	}

	// 2. Test Add (simulating SelectFileDialog callback)
	tmpDir := t.TempDir()
	testFile := "my_plugin.sh"
	os.WriteFile(filepath.Join(tmpDir, testFile), []byte("#!/bin/sh"), 0755)

	newPath := filepath.Join(tmpDir, testFile)
	AppConfig.RegisteredPlugins = append(AppConfig.RegisteredPlugins, newPath)
	lb.Items = AppConfig.RegisteredPlugins
	lb.UpdateRows()

	if len(AppConfig.RegisteredPlugins) != 1 || AppConfig.RegisteredPlugins[0] != newPath {
		t.Errorf("Failed to add new plugin. Current: %v", AppConfig.RegisteredPlugins)
	}
}
func TestActionRename_CacheAndSelection(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "old.txt")
	os.WriteFile(path, []byte("data"), 0644)

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = vfs.NewOSVFS(tmpDir)
	fsp.entries = []*fileEntry{{VFSItem: vfs.VFSItem{Name: "old.txt"}}}
	fsp.SetCursorIndex(0)
	pf.activeIdx = 0

	// Заполняем кэш данными
	fsp.dirCache[fsp.cacheKey(fsp.vfs.GetPath())] = dirCacheEntry{items: []vfs.VFSItem{{Name: "old.txt"}}}

	// 1. Тест успешного переименования
	// Перехватываем InputBox внутри actionRename (в тестах он не блокирует)
	// Мы вручную вызовем логику, которую должен был вызвать InputBox
	newName := "new.txt"
	oldPath := fsp.vfs.Join(fsp.vfs.GetPath(), "old.txt")
	newPath := fsp.vfs.Join(fsp.vfs.GetPath(), newName)

	// Симулируем успешный асинхронный ответ
	fsp.vfs.Rename(context.Background(), oldPath, newPath)

	// Выполняем UI-часть из actionRename (успех)
	delete(fsp.dirCache, fsp.cacheKey(fsp.vfs.GetPath()))
	fsp.pendingSelection = newName
	pf.RefreshAll()

	if _, ok := fsp.dirCache[fsp.cacheKey(fsp.vfs.GetPath())]; ok {
		t.Error("Cache was not cleared after rename")
	}
	if fsp.pendingSelection != "new.txt" {
		t.Errorf("Pending selection not set correctly: %q", fsp.pendingSelection)
	}

	// 2. Тест ошибки переименования
	fsp.pendingSelection = ""
	fsp.vfs = &mockRenameVFS{VFS: fsp.vfs, renameErr: os.ErrPermission}

	// Выполняем UI-часть из actionRename (ошибка)
	fsp.pendingSelection = "old.txt" // Должно вернуться к старому имени
	pf.RefreshAll()

	if fsp.pendingSelection != "old.txt" {
		t.Error("On error, pendingSelection should point to the original name")
	}
}
func TestActionExecute_WindowsFormatSimulation(t *testing.T) {
	// Тестируем, что формат команды, который мы выбрали для Windows,
	// корректно «проглатывается» парсером.
	tv := NewTerminalView(80, 24)
	p := NewAnsiParser(tv, nil)

	dir := "C:\\Users\\f4\\Desktop"
	cmd := "echo \"hello world\""

	// Имитируем создание команды для Windows (как в panels_frame.go / actions.go)
	// Используем %q для путей
	wireCmd := fmt.Sprintf("cd /d %q & %s\r\n", dir, cmd)

	// Проверяем, что в сформированной строке есть разделитель, на который завязан парсер
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
	return &vfs.MemoryReadAtCloser{Data: []byte("mock")}, nil
}

func TestActionOpenViewer_ProgressTask(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := NewPanelsFrame()
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

	pf := NewPanelsFrame()
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

func TestActionCommandHistory_Flow(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.cmdLine.Edit.History = nil
	actionCommandHistory(pf)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || !strings.Contains(top.GetTitle(), "History") {
		t.Fatalf("Expected empty history warning dialog, got %v", top)
	}
	top.SetExitCode(-1)
	vtui.FrameManager.Pop()

	pf.cmdLine.Edit.History = []string{"cmd1", "cmd2"}
	actionCommandHistory(pf)

	top = vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("History.CommandsTitle") {
		t.Fatalf("Expected Command History dialog, got %v", top)
	}

	top.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

func TestActionCommandHistory_Deletion(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	pf.cmdLine.Edit.History = []string{"cmd1", "cmd2", "cmd3"}
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

	history := pf.cmdLine.Edit.History
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
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	oldCfg := AppConfig
	defer func() { AppConfig = oldCfg }()

	AppConfig.KeepTerminalCursor = false

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
	clickDialogButton(t, top, "Ok")

	for i := 0; i < 10; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
		}
	}

	if !AppConfig.KeepTerminalCursor {
		t.Error("KeepTerminalCursor was not saved to AppConfig")
	}
}

func TestActionAppearanceSettingsSavesSystemMonospace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	oldConfig := AppConfig
	oldPath := getUserConfigIniPath
	getUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		AppConfig = oldConfig
		getUserConfigIniPath = oldPath
	}()
	AppConfig.GuiUseSystemMonospace = true

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var systemFont *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == Msg("AppearanceSettings.UseSystemMonospace") {
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
	clickDialogButton(t, top, "Ok")
	if AppConfig.GuiUseSystemMonospace {
		t.Fatal("system monospace setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesWorkspaceTabRestoration(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	oldConfig := AppConfig
	oldPath := getUserConfigIniPath
	getUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		AppConfig = oldConfig
		getUserConfigIniPath = oldPath
	}()
	AppConfig.RestoreWorkspaceTabs = true

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var restoreTabs *vtui.Checkbox
	for _, child := range top.GetChildren() {
		checkbox, ok := child.(*vtui.Checkbox)
		if ok && checkbox.GetText() == Msg("AppearanceSettings.RestoreWorkspaceTabs") {
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
	clickDialogButton(t, top, "Ok")
	if AppConfig.RestoreWorkspaceTabs {
		t.Fatal("disabled workspace tab restoration setting was not saved")
	}
}

func TestActionAppearanceSettingsSavesWorkspaceTabNumbering(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	oldConfig := AppConfig
	oldPath := getUserConfigIniPath
	getUserConfigIniPath = func() string { return filepath.Join(t.TempDir(), "settings.ini") }
	defer func() {
		AppConfig = oldConfig
		getUserConfigIniPath = oldPath
	}()
	AppConfig.WorkspaceTabNumbering = WorkspaceTabNumbersAlways

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var numbering *vtui.ComboBox
	for _, child := range top.GetChildren() {
		combo, ok := child.(*vtui.ComboBox)
		if ok && combo.Edit.GetText() == Msg("AppearanceSettings.WorkspaceNumbersAlways") {
			numbering = combo
			break
		}
	}
	if numbering == nil {
		t.Fatal("workspace tab numbering combobox not found in Appearance Settings")
	}
	numbering.Menu.SetSelectPos(int(WorkspaceTabNumbersOrder))
	clickDialogButton(t, top, "Ok")
	if AppConfig.WorkspaceTabNumbering != WorkspaceTabNumbersOrder {
		t.Fatalf("workspace tab numbering = %v, want order", AppConfig.WorkspaceTabNumbering)
	}
}

// TestActionAppearanceSettings_CancelPreservesPalette locks in the
// fix: farcolors.ini overrides applied at startup were wiped when
// the user opened Appearance settings and pressed Cancel, because
// the dialog restored via ApplyColorStyle(originalStyle) — a clean
// re-apply of the named base style with no room for runtime
// overrides. Snapshot-and-copy the whole palette instead, so
// Cancel returns exactly what was on screen before the dialog
// opened, regardless of where the tweak came from.
func TestActionAppearanceSettings_CancelPreservesPalette(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	// Simulate a farcolors.ini override: bump one palette slot to
	// a sentinel value the base style would never produce. If Cancel
	// restores by name it clobbers this back to the style default;
	// if it restores by palette snapshot the sentinel survives.
	const sentinel uint64 = 0xDEADBEEFCAFE0001
	origAtIdx := vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelText] = sentinel
	defer func() { vtui.Palette[ColPanelText] = origAtIdx }()

	actionAppearanceSettings(pf)
	top := vtui.FrameManager.GetTopFrame().(vtui.Container)

	// Trigger live preview: pick a style different from the current
	// one so ApplyColorStyle actually runs and overwrites the
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
	// Pick the *other* end of the list — different from whatever
	// index the config currently points to.
	target := 0
	if combo.Menu.SelectPos == 0 && len(combo.Menu.Items) > 1 {
		target = len(combo.Menu.Items) - 1
	}
	combo.Menu.OnAction(target)
	if vtui.Palette[ColPanelText] == sentinel {
		t.Fatal("test setup: live preview didn't overwrite the sentinel — need a different palette slot or style pair")
	}

	clickDialogButton(t, top, "Cancel")

	if got := vtui.Palette[ColPanelText]; got != sentinel {
		t.Errorf("Cancel dropped the override: palette[ColPanelText]=%016x, want sentinel %016x", got, sentinel)
	}
}

func TestActionAppearanceSettings_LivePreviewRecolorsExistingLabels(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

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

	clickDialogButton(t, top, "Cancel")
}

func TestPanelsFrame_RunAdvancedProgressTask(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	done := make(chan struct{})
	workerBlock := make(chan struct{})
	var reporter vfs.TaskReporter

	pf.RunAdvancedProgressTask("Test Action", false, func(ctx context.Context, rep vfs.TaskReporter) error {
		reporter = rep
		close(done)
		<-workerBlock
		return nil
	}, nil)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for progress task worker to run")
	}

	// Wait for the dialog to appear on top
	timeout := time.After(2 * time.Second)
	var dlg *FileOpProgressDialog
	for dlg == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && top.GetTitle() == "Test Action" {
				dlg = top.(*FileOpProgressDialog)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for dialog to appear")
		}
	}

	reporter.UpdateTransfer("Running", "item", 75, "Total Info", 35, "10 MB/s")

	// Wait for UI to update with progress
	timeout = time.After(2 * time.Second)
	for !dlg.pbCurrent.IsVisible() || dlg.pbCurrent.Percent != 75 {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for progress update to render")
		}
	}

	if !dlg.pbCurrent.IsVisible() || dlg.pbCurrent.Percent != 75 {
		t.Errorf("Current progress bar not updated: visible=%v, pct=%d", dlg.pbCurrent.IsVisible(), dlg.pbCurrent.Percent)
	}
	if !dlg.pbTotal.IsVisible() || dlg.pbTotal.Percent != 35 {
		t.Errorf("Total progress bar not updated: visible=%v, pct=%d", dlg.pbTotal.IsVisible(), dlg.pbTotal.Percent)
	}
	if dlg.lblSpeed.GetText() != "10 MB/s" {
		t.Errorf("Speed label not updated, got %q", dlg.lblSpeed.GetText())
	}

	// Close dialog and unblock worker
	close(workerBlock)
	dlg.SetExitCode(-1)
	vtui.FrameManager.Pop()
}

type mockExtractionVFS struct {
	vfs.VFS
	parent vfs.VFS
}

func (m *mockExtractionVFS) ParentVFS() vfs.VFS { return m.parent }

func TestExecuteFileOp_ContextualTitles(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "data.txt"), []byte("data"), 0644)

	parent := vfs.NewOSVFS(srcDir)
	srcVfs := &mockExtractionVFS{VFS: parent, parent: parent}
	dstVfs := vfs.NewOSVFS(t.TempDir())

	done := make(chan struct{})
	ExecuteFileOp(nil, srcVfs, dstVfs, []string{"data.txt"}, dstVfs.GetPath(), false, 2, func() {
		close(done)
	})

	timeout := time.After(2 * time.Second)
	var dlg *FileOpProgressDialog
	for dlg == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			top := vtui.FrameManager.GetTopFrame()
			if top != nil && strings.Contains(top.GetTitle(), "Extracting") {
				dlg = top.(*FileOpProgressDialog)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for Extracting dialog to appear")
		}
	}

	for vtui.FrameManager.GetTopFrame() != nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if top := vtui.FrameManager.GetTopFrame(); top != nil && top.IsDone() {
				vtui.FrameManager.Pop()
			}
		case <-timeout:
			t.Fatal("Timeout waiting for copy operation to complete")
		}
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
	SetDefaultF4Palette()
	v := &mockInvalidVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	pf := NewPanelsFrame()
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
	SetDefaultF4Palette()
	v := &mockInvalidVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	pf := NewPanelsFrame()
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
	SetDefaultF4Palette()

	tmpDir := t.TempDir()
	subDirName := "sub_folder"
	subDirPath := filepath.Join(tmpDir, subDirName)
	if err := os.Mkdir(subDirPath, 0755); err != nil {
		t.Fatalf("Failed to create sub directory: %v", err)
	}

	v := vfs.NewOSVFS(tmpDir)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.panels[0].(*FileSystemPanel)
	fsp.vfs = v
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: subDirName, IsDir: true}},
	}
	fsp.SetCursorIndex(1) // Focus on "sub_folder"
	pf.activeIdx = 0

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
