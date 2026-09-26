package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPanelsFrame_CtrlEnter_Escaping(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	pf.ActiveIdx = 0

	// Имя файла со спецсимволами и пробелами
	complexName := "file with'quote & space.txt"
	fsp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: complexName}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1)

	// Нажимаем Ctrl+Enter
	pressKey(pf, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	got := pf.CmdLine.Edit.GetText()

	if runtime.GOOS == "windows" {
		// На Windows ожидаем двойные кавычки
		expected := "\"" + complexName + "\""
		if got != expected {
			t.Errorf("Windows escaping failed. Got %q, want %q", got, expected)
		}
	} else {
		// На Unix ожидаем одинарные кавычки и экранирование внутренней кавычки
		expected := "'file with'\\''quote & space.txt'"
		if got != expected {
			t.Errorf("Unix escaping failed. Got %q, want %q", got, expected)
		}
	}
}

func TestPanelsFrame_CtrlEnterOnDirectoryInsertsWithoutEntering(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()

	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	fsp := pf.Panels[0].(*panel.FileSystemPanel)
	pf.ActiveIdx = 0
	fsp.Vfs = vfs.NewOSVFS(tmp)
	fsp.Entries = []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "subdir", IsDir: true}}}
	fsp.Refresh()
	fsp.SetCursorIndex(0)

	mainCtrlEnter := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}
	pressKey(pf, mainCtrlEnter)
	if got := pf.CmdLine.Edit.GetText(); got != "subdir" {
		t.Fatalf("hotkey Ctrl+Enter inserted %q, want subdir", got)
	}
	if got := fsp.Vfs.GetPath(); got != tmp {
		t.Fatalf("hotkey Ctrl+Enter entered %q, want to stay in %q", got, tmp)
	}

	// Exercise the frame-level fallback independently of macro.MacroManager.Filter.
	pf.CmdLine.Clear()
	pf.ProcessKey(mainCtrlEnter)
	if got := pf.CmdLine.Edit.GetText(); got != "subdir" {
		t.Fatalf("direct Ctrl+Enter inserted %q, want subdir", got)
	}
	if got := fsp.Vfs.GetPath(); got != tmp {
		t.Fatalf("direct Ctrl+Enter entered %q, want to stay in %q", got, tmp)
	}
}

func TestPanelsFrame_CD_QuotedParsing(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)

	// Мокаем VFS, чтобы не ходить на реальный диск
	tmp := t.TempDir()
	targetDir := filepath.Join(tmp, "dir with space's")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Симулируем ввод команды cd в одинарных кавычках (Unix-style)
	// Для Windows этот тест тоже должен работать, так как мы добавили поддержку '' и там.
	pf.CmdLine.Edit.SetText("cd 'dir with space'\\''s'")

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	gotPath := fsp.Vfs.GetPath()
	if gotPath != targetDir {
		t.Errorf("CD parsing failed. Expected path %q, but VFS is at %q", targetDir, gotPath)
	}

	if !pf.CmdLine.IsEmpty() {
		t.Error("Command line should be cleared after successful CD")
	}
}

func TestPanelsFrame_PTY_SyncEscaping(t *testing.T) {
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pty := pf.Pty.(*paneltest.MockPty)

	tmp := t.TempDir()
	dirName := "space 'n' quotes"
	targetDir := filepath.Join(tmp, dirName)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}

	// Вводим команду перехода
	pf.CmdLine.Edit.SetText("cd \"" + dirName + "\"")
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	written := string(pty.Written)
	if runtime.GOOS == "windows" {
		if !strings.Contains(written, "cd /d") {
			t.Errorf("Windows term.PTY sync failed. Expected 'cd /d', got: %q", written)
		}
	} else {
		// Проверяем, что в term.PTY ушла команда с одинарными кавычками и экранированием.
		// Так как путь абсолютный, проверяем наличие экранированного фрагмента имени.
		expectedPiece := "space '\\''n'\\'' quotes'"
		if !strings.Contains(written, " cd '") || !strings.Contains(written, expectedPiece) {
			t.Errorf("Unix term.PTY sync escaping failed.\nExpected to contain escaped name: %q\nFull output: %q", expectedPiece, written)
		}
	}
}

func TestPanelsFrame_LocalUnixCommandKeepsPersistentShellDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix term.PTY command composition")
	}

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pty := pf.Pty.(*paneltest.MockPty)

	tmp := t.TempDir()
	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	if err := fsp.Vfs.SetPath(tmp); err != nil {
		t.Fatal(err)
	}
	// Pretend the normal frame refresh has already synchronized this panel.
	// The command itself must not re-impose the panel path on the persistent
	// shell: an alias such as `cd:home` is allowed to change that shell's cwd.
	pf.LastPtyPath = tmp
	pf.LastPtyVFS = fsp.Vfs

	pf.CmdLine.Edit.SetText("cd:home")
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	written := pty.String()
	panelSync := "cd '" + strings.ReplaceAll(tmp, "'", "'\\''") + "' &&"
	if strings.Contains(written, panelSync) {
		t.Fatalf("local Unix command re-imposed panel directory %q: %q", tmp, written)
	}
	if !strings.Contains(written, "cd:home") {
		t.Fatalf("alias command did not reach the persistent term.PTY shell: %q", written)
	}
}

// TestPanelsFrame_LocalUnixCommandKeepsCdWhenPanelNeedsSudo is the regression
// test for f4#1255's fifth report: a command typed into f4's command line ran
// in the wrong directory whenever the active panel's folder needed sudo to
// even be looked at, regardless of whether the typed command itself used
// sudo.
//
// The persistent local Unix shell is optimized so a plain command does not
// re-impose "cd '<panel path>' &&" once the panel and the shell are known to
// agree (the test above, cd:home): syncPTYDirectory sends the cd once and the
// caller then blanks the panel path out of every command that follows, on the
// assumption that cd succeeded. That assumption does not hold for a folder
// that needs sudo to list: the persistent shell is this same unprivileged
// process's child, so its own plain cd is refused exactly where OSVFS.Stat
// would have to fall back to the sudo helper, and there is no way to read the
// shell's reply back to know that happened. Before the fix, the caller
// trusted the refused cd anyway, blanked the path, and every command after
// that ran wherever the shell's cd had *actually* left it -- typically the
// previous directory -- with no visible error. The fix makes syncPTYDirectory
// report the sync as unconfirmed for such a path (OSVFS.NeedsElevation, the
// same fast permission probe #1261 already uses for panel navigation), so the
// caller keeps sending the command with its own explicit cd every time
// instead of silently trusting one that may not have landed.
func TestPanelsFrame_LocalUnixCommandKeepsCdWhenPanelNeedsSudo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix term.PTY command composition")
	}
	if os.Geteuid() == 0 {
		t.Skip("root is refused nothing, so the folder cannot be made to need sudo")
	}

	// NeedsElevation only asks whether the sudo helper is available, never
	// whether it can actually do anything -- a bare non-nil SudoClient
	// answers that without a real dispatcher process. Restore whatever was
	// there (nothing, in a normal test run) so this does not leak into
	// unrelated tests that run afterwards in the same binary.
	prevSudo := vfs.GetSudoClient()
	vfs.InitSudoClient("f4", "")
	t.Cleanup(func() { vfs.SetSudoClientForTest(prevSudo) })

	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pty := pf.Pty.(*paneltest.MockPty)

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })

	fsp := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel)
	// Stands in for the panel having already navigated here: post-f4#1411,
	// a real Enter keypress resolves and commits exactly this real absolute
	// path via the sudo-aware OSVFS.ResolveElevated/CommitPath, so GetPath()
	// is correct going in -- this bug is not about that path being wrong.
	fsp.Vfs = vfs.NewOSVFS(locked)
	// The persistent shell last agreed with the panel somewhere else, so the
	// command about to run has to (re)sync first.
	pf.LastPtyPath = filepath.Dir(locked)
	pf.LastPtyVFS = fsp.Vfs

	pf.CmdLine.Edit.SetText("pwd")
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	written := pty.String()
	wantCd := "cd '" + strings.ReplaceAll(locked, "'", "'\\''") + "'"
	if !strings.Contains(written, wantCd) {
		t.Fatalf("command ran without an explicit cd into the sudo-only folder %q: %q", locked, written)
	}
	if !strings.Contains(written, "pwd") {
		t.Fatalf("the typed command itself is missing from the wire text: %q", written)
	}
	if pf.LastPtyPath == locked {
		t.Fatalf("panel recorded the persistent shell as having reached %q, but a plain cd there cannot be confirmed to have succeeded", locked)
	}
}
