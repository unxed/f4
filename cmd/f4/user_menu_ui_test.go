package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestParseFunctionKey(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
	}{
		{"F1", 1},
		{"F2", 2},
		{"F12", 12},
		{"F24", 24},
		{"f3", 3}, // case insensitive on the leading F
		{"F0", 0},
		{"F25", 0},
		{"F", 0},
		{"FF", 0},
		{"a", 0},
		{"", 0},
		{"--", 0},
		{"F1a", 0},
	}
	for _, c := range cases {
		if got := parseFunctionKey(c.in); got != c.want {
			t.Errorf("parseFunctionKey(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEscapeAmpersand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo", "foo"},
		{"R&D", "R&&D"},
		{"&start", "&&start"},
		{"a&b&c", "a&&b&&c"},
	}
	for _, c := range cases {
		if got := escapeAmpersand(c.in); got != c.want {
			t.Errorf("escapeAmpersand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripAmpersand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo", "foo"},
		{"R&&D", "R&D"},
		{"&Open", "Open"},
		{"a&b&c", "abc"},
		{"foo&&bar", "foo&bar"},
	}
	for _, c := range cases {
		if got := stripAmpersand(c.in); got != c.want {
			t.Errorf("stripAmpersand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsMenuComment(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"REM this is a comment", true},
		{"rem lowercase too", true},
		{"REM", true},
		{"REM\tcomment with tab", true},
		{"REMOVE", false}, // no separator after REM
		{":: shell-style comment", true},
		{":single colon", false},
		{"normal command", false},
		{"  REM indented", false}, // caller strips spaces first
	}
	for _, c := range cases {
		if got := isMenuComment(c.in); got != c.want {
			t.Errorf("isMenuComment(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFormatMenuItemText_SingleChar(t *testing.T) {
	got := formatMenuItemText(UserMenuItem{HotKey: "a", Label: "Apple"})
	// Hotkey marker '&a' plus enough padding to bring the label to column 6
	if !strings.HasPrefix(got, "&a") {
		t.Errorf("missing & marker for single-char hotkey: %q", got)
	}
	if !strings.HasSuffix(got, "Apple") {
		t.Errorf("label missing: %q", got)
	}
}

func TestFormatMenuItemText_FunctionKey(t *testing.T) {
	got := formatMenuItemText(UserMenuItem{HotKey: "F3", Label: "Build"})
	// F-keys must not use the '&' marker (would underline 'F').
	if strings.HasPrefix(got, "&") {
		t.Errorf("F-key should not have & marker: %q", got)
	}
	if !strings.HasPrefix(got, "F3") || !strings.HasSuffix(got, "Build") {
		t.Errorf("unexpected layout: %q", got)
	}
}

func TestFormatMenuItemText_NoHotkey(t *testing.T) {
	got := formatMenuItemText(UserMenuItem{HotKey: "", Label: "Plain"})
	if !strings.HasSuffix(got, "Plain") {
		t.Errorf("missing label: %q", got)
	}
}

func TestFormatMenuItemText_AmpersandInLabel(t *testing.T) {
	got := formatMenuItemText(UserMenuItem{HotKey: "x", Label: "R&D"})
	// The '&' in label must be doubled so vtui doesn't underline 'D'.
	if !strings.Contains(got, "R&&D") {
		t.Errorf("label ampersand not escaped: %q", got)
	}
}

func TestFindLocalFarMenu_WalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	wanted := filepath.Join(root, "a", farMenuFileName)
	if err := os.WriteFile(wanted, []byte("x:  X\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found := findLocalFarMenu(deep)
	if !found {
		t.Fatalf("expected to find FarMenu.ini at %q from %q", wanted, deep)
	}
	// Resolve to absolute to avoid /var vs /private/var on macOS.
	gotAbs, _ := filepath.EvalSymlinks(got)
	wantAbs, _ := filepath.EvalSymlinks(wanted)
	if gotAbs != wantAbs {
		t.Errorf("found %q, want %q", gotAbs, wantAbs)
	}
}

func TestFindLocalFarMenu_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, found := findLocalFarMenu(dir)
	if found {
		t.Errorf("expected no FarMenu.ini in empty tree")
	}
}

func TestFindLocalFarMenu_PicksClosest(t *testing.T) {
	// When two ancestors have FarMenu.ini, the closer one wins.
	root := t.TempDir()
	mid := filepath.Join(root, "mid")
	leaf := filepath.Join(mid, "leaf")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	rootMenu := filepath.Join(root, farMenuFileName)
	midMenu := filepath.Join(mid, farMenuFileName)
	if err := os.WriteFile(rootMenu, []byte("r:  R\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(midMenu, []byte("m:  M\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := findLocalFarMenu(leaf)
	gotAbs, _ := filepath.EvalSymlinks(got)
	midAbs, _ := filepath.EvalSymlinks(midMenu)
	if gotAbs != midAbs {
		t.Errorf("closest wins: got %q, want %q", gotAbs, midAbs)
	}
}

func TestMainMenuFilePath_HasExpectedSuffix(t *testing.T) {
	p := MainMenuFilePath()
	want := filepath.Join("f4", "settings", "user_menu.ini")
	if !strings.HasSuffix(p, want) {
		t.Errorf("MainMenuFilePath()=%q, want suffix %q", p, want)
	}
}

func TestFindMenuItemByUserData(t *testing.T) {
	menu := vtui.NewVMenu("test")
	menu.AddItem(vtui.MenuItem{Text: "a", UserData: 3})
	menu.AddItem(vtui.MenuItem{Text: "b", UserData: 7})
	menu.AddItem(vtui.MenuItem{Text: "c", UserData: 1})

	if idx, ok := findMenuItemByUserData(menu, 7); !ok || idx != 1 {
		t.Errorf("got idx=%d ok=%v, want 1/true", idx, ok)
	}
	if _, ok := findMenuItemByUserData(menu, 99); ok {
		t.Errorf("expected not found for unknown UserData")
	}
}

func TestUserMenu_ExecuteCommands(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pty := pf.pty.(*mockPty)

	// Очищаем буфер вывода в PTY
	pty.written = nil

	// Создаем временную папку и файл на панели
	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmpDir := t.TempDir()
	if err := fsp.vfs.SetPath(tmpDir); err != nil {
		t.Fatal(err)
	}
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "file.go"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1) // Курсор на "file.go"

	// Тестовый набор команд с комментариями и заменой токена !.! (имя текущего файла)
	commands := []string{
		"REM This is a comment and should be ignored",
		":: Another comment to be ignored",
		"cat !.!",
	}

	executeMenuCommands(pf, commands)

	written := string(pty.written)

	// Проверяем, что в PTY ушла сформированная команда c "cat file.go"
	if !strings.Contains(written, "cat file.go") {
		t.Errorf("executeMenuCommands failed to translate or dispatch. Expected to contain %q, got: %q", "cat file.go", written)
	}

	// Комментарии не должны уйти в выполнение
	if strings.Contains(written, "ignored") {
		t.Error("Comments (REM / ::) were erroneously sent to PTY execution")
	}
}
func TestUserMenu_ExecuteMultipleCommands(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pty := pf.pty.(*mockPty)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmpDir := t.TempDir()
	if err := fsp.vfs.SetPath(tmpDir); err != nil {
		t.Fatal(err)
	}
	fsp.entries = []*fileEntry{
		{VFSItem: vfs.VFSItem{Name: ".."}},
		{VFSItem: vfs.VFSItem{Name: "file.go"}},
	}
	fsp.Refresh()
	fsp.SetCursorIndex(1)

	commands := []string{
		"echo 1",
		"echo 2",
	}

	executeMenuCommands(pf, commands)

	written := string(pty.written)
	if runtime.GOOS == "windows" {
		if !strings.Contains(written, "echo 1 & echo 2") {
			t.Errorf("Expected Windows commands to be joined with ' & ', got: %q", written)
		}
	} else {
		if !strings.Contains(written, "echo 1; echo 2") {
			t.Errorf("Expected Unix commands to be joined with '; ', got: %q", written)
		}
	}
}

func TestUserMenu_InterpreterDirectiveUsesScriptMode(t *testing.T) {
	interpreter, ok := userMenuInterpreter([]string{
		"#!/usr/bin/env python3",
		"print('hello')",
	})
	if !ok || interpreter != "/usr/bin/env python3" {
		t.Fatalf("userMenuInterpreter() = %q, %v", interpreter, ok)
	}

	if _, ok := userMenuInterpreter([]string{"echo plain", "#!/bin/sh", "echo script"}); ok {
		t.Fatal("a shebang below the first command must not switch a legacy menu item to script mode")
	}
}

func TestUserMenu_ScriptCommandUsesInterpreterAndQuotedBody(t *testing.T) {
	body := "printf '%s\\n' hello\nprintf \"quoted\\\" world\\n\""
	command, err := buildUserMenuScriptCommand("/usr/bin/env bash", body, vfs.CommandDialectPOSIX)
	if err != nil {
		t.Fatal(err)
	}
	quotedBody, err := QuoteCommandArgument(vfs.CommandDialectPOSIX, body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "printf '%s' "+quotedBody+" | /usr/bin/env bash -") {
		t.Fatalf("script command = %q", command)
	}
}

func TestUserMenu_InteractiveEdit(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	s := &userMenuState{
		pf:         pf,
		mode:       MenuModeLocal,
		sourcePath: filepath.Join(t.TempDir(), farMenuFileName),
		rootTitle:  "Local Menu",
		rootItems: []UserMenuItem{
			{HotKey: "1", Label: "old label", Commands: []string{"echo 1"}},
		},
	}

	showEditItemDialog(s, vtui.NewVMenu("dummy"), s.rootItems, 0, false, false)

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("UserMenu.EditTitle") {
		t.Fatalf("Expected Edit User Menu dialog, got %v", top)
	}

	dlg := top.(vtui.Container)
	vtui.AssertLayout(t, dlg)

	var editLabel *vtui.Edit
	var btnSave *vtui.Button
	for _, child := range dlg.GetChildren() {
		if e, ok := child.(*vtui.Edit); ok && e.GetText() == "old label" {
			editLabel = e
		}
		if b, ok := child.(*vtui.Button); ok && strings.Contains(b.GetText(), "Save") {
			btnSave = b
		}
	}

	if editLabel == nil || btnSave == nil {
		t.Fatal("Required dialog controls not found")
	}

	editLabel.SetText("new label")
	btnSave.OnClick()

	if s.rootItems[0].Label != "new label" {
		t.Errorf("Expected updated label 'new label', got %q", s.rootItems[0].Label)
	}
}

// TestUserMenu_EditItemMultilineCommand covers issue #342: the Command
// field is a MultiLineEdit, so editing an item with several commands
// preserves them one-per-line on save and lets the user append more.
func TestUserMenu_EditItemMultilineCommand(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	s := &userMenuState{
		pf:         pf,
		mode:       MenuModeLocal,
		sourcePath: filepath.Join(t.TempDir(), farMenuFileName),
		rootTitle:  "Local Menu",
		rootItems: []UserMenuItem{
			{HotKey: "1", Label: "build", Commands: []string{"go build ./...", "go vet ./..."}},
		},
	}

	showEditItemDialog(s, vtui.NewVMenu("dummy"), s.rootItems, 0, false, false)

	dlg := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var editCmd *vtui.MultiLineEdit
	var btnSave *vtui.Button
	for _, child := range dlg.GetChildren() {
		if mle, ok := child.(*vtui.MultiLineEdit); ok {
			editCmd = mle
		}
		if b, ok := child.(*vtui.Button); ok && strings.Contains(b.GetText(), "Save") {
			btnSave = b
		}
	}
	if editCmd == nil {
		t.Fatal("MultiLineEdit for Command field not found")
	}
	if btnSave == nil {
		t.Fatal("Save button not found")
	}

	// The dialog opened with the existing commands rendered on separate
	// rows (no "; " join), so the user immediately sees the full script.
	if got := editCmd.GetLines(); len(got) != 2 || got[0] != "go build ./..." || got[1] != "go vet ./..." {
		t.Errorf("initial lines = %v, want [go build ./..., go vet ./...]", got)
	}

	// Append a third command via SetLines (simulating typing).
	editCmd.SetLines([]string{"go build ./...", "go vet ./...", "go test ./..."})
	btnSave.OnClick()

	got := s.rootItems[0].Commands
	want := []string{"go build ./...", "go vet ./...", "go test ./..."}
	if len(got) != len(want) {
		t.Fatalf("commands after save = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("commands[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestUserMenu_EditItemStripsBlankLines guards the save-path helper:
// blank rows at the top/bottom of the multi-line box shouldn't reach
// the ini file as empty Command entries; interior blank lines survive
// (visual grouping inside a shell script).
func TestUserMenu_EditItemStripsBlankLines(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)

	s := &userMenuState{
		pf:         pf,
		mode:       MenuModeLocal,
		sourcePath: filepath.Join(t.TempDir(), farMenuFileName),
		rootTitle:  "Local Menu",
		rootItems: []UserMenuItem{
			{HotKey: "1", Label: "cmd", Commands: []string{"echo a"}},
		},
	}

	showEditItemDialog(s, vtui.NewVMenu("dummy"), s.rootItems, 0, false, false)
	dlg := vtui.FrameManager.GetTopFrame().(vtui.Container)

	var editCmd *vtui.MultiLineEdit
	var btnSave *vtui.Button
	for _, child := range dlg.GetChildren() {
		if mle, ok := child.(*vtui.MultiLineEdit); ok {
			editCmd = mle
		}
		if b, ok := child.(*vtui.Button); ok && strings.Contains(b.GetText(), "Save") {
			btnSave = b
		}
	}

	editCmd.SetLines([]string{"", "", "echo a", "", "echo b", "", ""})
	btnSave.OnClick()

	got := s.rootItems[0].Commands
	want := []string{"echo a", "", "echo b"}
	if len(got) != len(want) {
		t.Fatalf("commands after save = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("commands[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitMenuCommandSteps(t *testing.T) {
	sep := "; "
	cases := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"shell only", []string{"echo 1", "echo 2"}, []string{"echo 1; echo 2"}},
		{"trailing cd", []string{"rm -rf _build", "mkdir -p _build", "cd _build/"}, []string{"rm -rf _build; mkdir -p _build", "cd _build/"}},
		{"cd in the middle", []string{"mkdir -p out", "cd out", "touch a", "touch b"}, []string{"mkdir -p out", "cd out", "touch a; touch b"}},
		{"leading cd", []string{"cd /tmp", "ls"}, []string{"cd /tmp", "ls"}},
		{"cd dotdot", []string{"cd ..", "cd..", "chdir /tmp"}, []string{"cd ..", "cd..", "chdir /tmp"}},
		{"cd only", []string{"cd /tmp"}, []string{"cd /tmp"}},
		{"not a cd", []string{"cdparanoia -B", "echo cd x"}, []string{"cdparanoia -B; echo cd x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitMenuCommandSteps(tc.lines, sep)
			if len(got) != len(tc.want) {
				t.Fatalf("splitMenuCommandSteps(%q) = %q, want %q", tc.lines, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitMenuCommandSteps(%q) = %q, want %q", tc.lines, got, tc.want)
				}
			}
		})
	}
}

// Issue #893: the last "cd" line of a multi-command menu item must move the
// panel, and it must do so only after the shell lines before it finished.
func TestUserMenu_TrailingCdFollowsPanelAfterShellCommands(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pty := pf.pty.(*mockPty)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmpDir := t.TempDir()
	if err := fsp.vfs.SetPath(tmpDir); err != nil {
		t.Fatal(err)
	}
	pty.written = nil

	executeMenuCommands(pf, []string{
		"rm -rf _build",
		"mkdir -p _build",
		"cd _build/",
	})

	written := string(pty.written)
	wantJoined := "rm -rf _build; mkdir -p _build"
	if runtime.GOOS == "windows" {
		wantJoined = "rm -rf _build & mkdir -p _build"
	}
	if !strings.Contains(written, wantJoined) {
		t.Fatalf("shell lines before cd must be sent as one joined command %q, got: %q", wantJoined, written)
	}
	if strings.Contains(written, "cd _build/") {
		t.Fatalf("the cd line must not be sent to the shell as part of the joined command: %q", written)
	}
	if !pf.executing {
		t.Fatal("the joined shell command must be running before the cd step is applied")
	}
	if pf.afterExecution == nil {
		t.Fatal("the cd step must be queued behind the running shell command")
	}
	if got := fsp.vfs.GetPath(); got != tmpDir {
		t.Fatalf("panel must not move before the shell command completes; path = %q", got)
	}

	// The shell finishes and, by then, mkdir has created the directory.
	buildDir := filepath.Join(tmpDir, "_build")
	if err := os.Mkdir(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pf.endExecution()

	if pf.afterExecution != nil {
		t.Fatal("no step must remain queued after the chain finished")
	}
	if got := fsp.vfs.GetPath(); filepath.Clean(got) != filepath.Clean(buildDir) {
		t.Fatalf("panel must follow the trailing cd; path = %q, want %q", got, buildDir)
	}
}

// A "cd" in the middle of an item runs the lines after it in the new
// directory, like far2l does when it feeds every line to the command line.
func TestUserMenu_MiddleCdRunsRemainingCommandsInNewDir(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pty := pf.pty.(*mockPty)

	fsp := pf.panels[pf.activeIdx].(*FileSystemPanel)
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsp.vfs.SetPath(tmpDir); err != nil {
		t.Fatal(err)
	}
	pty.written = nil

	executeMenuCommands(pf, []string{
		"cd sub",
		"echo one",
		"echo two",
	})

	// The cd completes synchronously, so the shell lines follow at once,
	// with the PTY synced to the new panel directory first.
	if got := fsp.vfs.GetPath(); filepath.Clean(got) != filepath.Clean(subDir) {
		t.Fatalf("panel must follow the leading cd; path = %q, want %q", got, subDir)
	}
	written := string(pty.written)
	wantJoined := "echo one; echo two"
	if runtime.GOOS == "windows" {
		wantJoined = "echo one & echo two"
	}
	if !strings.Contains(written, wantJoined) {
		t.Fatalf("shell lines after cd must be sent joined %q, got: %q", wantJoined, written)
	}
	if !strings.Contains(written, "sub") {
		t.Fatalf("PTY must be moved to the new directory before the shell lines run, got: %q", written)
	}
	if syncIdx, cmdIdx := strings.Index(written, "sub"), strings.Index(written, wantJoined); syncIdx > cmdIdx {
		t.Fatalf("directory sync must precede the shell command, got: %q", written)
	}
	if pf.afterExecution != nil {
		t.Fatal("no step must remain queued after the last shell command was sent")
	}
}
