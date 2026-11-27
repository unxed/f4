package panel

import (
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/vtinput"
)

// K-74 and K-75 of docs/FAR2L_COMPAT.md (unxed/f4#2141): Ctrl+J puts the name
// of the file under the cursor into the command line like Ctrl+Enter, and
// Ctrl+Shift+Enter puts the name from the passive panel -- it never runs the
// command line.

func namesFixture(t *testing.T) *issue2049Fixture {
	t.Helper()
	f := newIssue2049Fixture(t)
	f.fsp.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "active one.txt"}}}
	f.fsp.CursorIdx = 0
	passive, ok := f.pf.Panels[1].(*FileSystemPanel)
	if !ok {
		t.Fatal("the second panel is not a file panel")
	}
	passive.Entries = []*FileEntry{{VFSItem: vfs.VFSItem{Name: "passive.txt"}}}
	passive.CursorIdx = 0
	f.pf.ActiveIdx = 0
	return f
}

func ctrlKey(vk uint16, shift bool) *vtinput.InputEvent {
	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: vtinput.LeftCtrlPressed}
	if shift {
		e.ControlKeyState |= vtinput.ShiftPressed
	}
	return e
}

func TestCtrlJInsertsTheNameOfTheFileUnderTheCursor(t *testing.T) {
	f := namesFixture(t)
	f.pf.CmdLine.Edit.SetText("ls ")
	pressKey(f.pf, ctrlKey(vtinput.VK_J, false))
	wantName := "'active one.txt'"
	if runtime.GOOS == "windows" {
		wantName = `"active one.txt"`
	}
	if got := f.pf.CmdLine.Edit.GetText(); got != "ls "+wantName+" " {
		t.Fatalf("command line after Ctrl+J: %q, want the quoted name of the active file and a space", got)
	}
}

func TestCtrlEnterOnParentInsertsCurrentDirectory(t *testing.T) {
	currentDir := t.TempDir()
	fsp := &FileSystemPanel{
		Vfs:       vfs.NewOSVFS(currentDir),
		Entries:   []*FileEntry{{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}}},
		CursorIdx: 0,
	}
	pf := &PanelsFrame{ActiveIdx: 0, CmdLine: cmdline.NewCommandLine(">"), Panels: [2]Panel{fsp}}

	if !pf.InsertSelectedFileName() {
		t.Fatal("parent entry was not inserted")
	}
	want := currentDir + " "
	if runtime.GOOS == "windows" {
		want = `"` + currentDir + `" `
	}
	if got := pf.CmdLine.Edit.GetText(); got != want {
		t.Fatalf("command line after Ctrl+Enter on ..: %q, want current directory %q", got, want)
	}
}

func TestCtrlShiftEnterInsertsThePassiveNameAndRunsNothing(t *testing.T) {
	f := namesFixture(t)
	f.pf.CmdLine.Edit.SetText("true ")
	pressKey(f.pf, ctrlKey(vtinput.VK_RETURN, true))
	got := f.pf.CmdLine.Edit.GetText()
	if got == "" || !strings.HasPrefix(got, "true ") {
		t.Fatalf("Ctrl+Shift+Enter ran or cleared the command line: %q", got)
	}
	if got != "true passive.txt " {
		t.Fatalf("command line after Ctrl+Shift+Enter: %q, want the name of the passive file", got)
	}
}

// K-76 and K-78 (unxed/f4#2141): the full name of the file of the passive
// panel, and the paths of the active and of the passive panel.
func TestInsertPassiveAndPanelPaths(t *testing.T) {
	f := namesFixture(t)
	passive := f.pf.Panels[1].(*FileSystemPanel)
	passiveDir := t.TempDir()
	passive.Vfs = vfs.NewOSVFS(passiveDir)

	f.pf.CmdLine.Edit.SetText("")
	if !f.pf.InsertPassivePath() {
		t.Fatal("the full name of the passive file was not inserted")
	}
	if got := f.pf.CmdLine.Edit.GetText(); !strings.Contains(got, passiveDir) || !strings.Contains(got, "passive.txt") {
		t.Fatalf("passive full name: %q, want the folder %q and passive.txt", got, passiveDir)
	}

	f.pf.CmdLine.Edit.SetText("")
	f.pf.InsertActiveDirPath()
	if got := f.pf.CmdLine.Edit.GetText(); !strings.Contains(got, f.fsp.Vfs.GetPath()) {
		t.Fatalf("path of the active panel: %q, want it to hold %q", got, f.fsp.Vfs.GetPath())
	}

	f.pf.CmdLine.Edit.SetText("")
	f.pf.InsertPassiveDirPath()
	if got := f.pf.CmdLine.Edit.GetText(); !strings.Contains(got, passiveDir) {
		t.Fatalf("path of the passive panel: %q, want it to hold %q", got, passiveDir)
	}
}
