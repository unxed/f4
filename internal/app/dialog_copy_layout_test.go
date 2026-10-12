package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// copyDialogRows opens the F5 (or F6) dialog on an 80x25 screen and returns
// the text of its rows, from the top frame row to the bottom one, cut out of
// vtui's screen dump (the format Ctrl+Shift+P writes).
func copyDialogRows(t *testing.T, isMove bool) []string {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
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

	actionCopyMove(pf, isMove)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*dialog.FileDialog)
	if !ok {
		t.Fatal("copy dialog not found on top")
	}
	t.Cleanup(func() { vtui.FrameManager.Pop() })
	assertFileDialogLayout(t, dlg)

	dlg.Show(screen)
	var buf bytes.Buffer
	screen.Dump(&buf)
	lines := strings.Split(buf.String(), "\n")
	var rows []string
	for y := dlg.Y1; y <= dlg.Y2; y++ {
		cells := []rune(lines[2+y])
		rows = append(rows, string(cells[dlg.X1:dlg.X2+1]))
	}
	return rows
}

// assertFileDialogLayout is vtui.AssertLayout without the blank row it asks
// for between the frame and the first and last controls: the file dialogs
// start right under the title and end right over the bottom frame, as
// far2l's do (#891). Everything else the validator checks still applies.
func assertFileDialogLayout(t *testing.T, dlg vtui.Container) {
	t.Helper()
	rules := vtui.DefaultLayoutRules
	rules.FrameClearanceY = 0
	vtui.AssertLayoutWithRules(t, dlg, rules)
}

// isRuleRow reports whether row is one horizontal line between the two cells
// of the frame. vtui's modal dialogs clip their controls one cell inside the
// frame, so a rule does not paint its junctions with the frame yet: the cells
// at both ends are still the plain vertical frame.
func isRuleRow(row string) bool {
	cells := []rune(row)
	if cells[1] == ' ' {
		return false
	}
	for _, c := range cells[1 : len(cells)-1] {
		if c != cells[1] {
			return false
		}
	}
	return true
}

// The Copy dialog (f4#891) has no blank rows but one: the destination field is
// divided from the options by a rule, the buttons stand under a second rule
// and are the last row inside the frame, and both rules run from one side of the
// frame to the other. The blank row left is the air over the advanced options button.
//
// Screen Dump (80x25, the middle 40 columns; the field shows the end of the CI checkout path):
//
//	╔════════════════ Copy ════════════[×]═╗
//	║ Copy "test.txt" to:                   ║
//	║ me/runner/work/f4/f4/internal/app/ ↓ ║
//	║──────────────────────────────────────║
//	║ Access rights:           Default   ↓ ║
//	║ Already existing files:  Ask       ↓ ║
//	║ [x] Copy symlink contents            ║
//	║   Queue                          ↓   ║
//	║                                      ║
//	║ [ Advanced options... ]              ║
//	║──────────────────────────────────────║
//	║         [ Copy ]  [ Cancel ]         ║
//	╚══════════════════════════════════════╝
func TestCopyDialogLayoutHasRulesAndNoBlankRows(t *testing.T) {
	rows := copyDialogRows(t, false)
	if len(rows) != dialog.CopyBoxHeight {
		t.Fatalf("dialog has %d rows, want %d:\n%s", len(rows), dialog.CopyBoxHeight, strings.Join(rows, "\n"))
	}
	dump := strings.Join(rows, "\n")
	frame := []rune(rows[1])[0]

	const airOverAdvanced = 8
	for i := 1; i < len(rows)-1; i++ {
		if i != airOverAdvanced && !isRuleRow(rows[i]) && strings.TrimSpace(strings.Trim(rows[i], string(frame))) == "" {
			t.Errorf("row %d of the dialog is blank:\n%s", i, dump)
		}
	}
	for i, want := range map[int]string{1: `Copy "test.txt" to:`, 4: "Access rights:", 5: "Already existing files:", 6: "symlink contents", 7: "Queue", 9: "Advanced options"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d should hold %q:\n%s", i, want, dump)
		}
	}
	if !isRuleRow(rows[3]) {
		t.Errorf("no rule between the destination and the options:\n%s", dump)
	}
	last := len(rows) - 2
	if !isRuleRow(rows[last-1]) {
		t.Errorf("no rule over the buttons:\n%s", dump)
	}
	if !strings.Contains(rows[last], "Copy") || !strings.Contains(rows[last], "Cancel") {
		t.Errorf("the buttons are not the last row inside the frame:\n%s", dump)
	}
}

func TestCopyDialogMarksPrimaryButtonAsDefault(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
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
	dlg, ok := vtui.FrameManager.GetTopFrame().(*dialog.FileDialog)
	if !ok {
		t.Fatal("copy dialog not found on top")
	}
	t.Cleanup(func() { vtui.FrameManager.Pop() })

	var defaults []*vtui.Button
	for _, item := range dlg.GetChildren() {
		if button, ok := item.(*vtui.Button); ok && button.IsDefault {
			defaults = append(defaults, button)
		}
	}
	if len(defaults) != 1 || defaults[0].GetCaption() != "Copy" {
		t.Fatalf("copy dialog default buttons = %v, want only Copy", defaults)
	}
}

// A move has no symlink option, so its dialog is one row shorter and is
// otherwise laid out the same way.
//
// Screen Dump (80x25, the middle 40 columns; the field shows the end of the CI checkout path):
//
//	╔════════════════ Move ════════════[×]═╗
//	║ Rename or move "test.txt" to:         ║
//	║ me/runner/work/f4/f4/internal/app/ ↓ ║
//	║──────────────────────────────────────║
//	║ Access rights:           Default   ↓ ║
//	║ Already existing files:  Ask       ↓ ║
//	║   Queue                          ↓   ║
//	║                                      ║
//	║ [ Advanced options... ]              ║
//	║──────────────────────────────────────║
//	║        [ Rename ]  [ Cancel ]        ║
//	╚══════════════════════════════════════╝
func TestMoveDialogLayoutHasRulesAndNoBlankRows(t *testing.T) {
	rows := copyDialogRows(t, true)
	if len(rows) != dialog.MoveBoxHeight {
		t.Fatalf("dialog has %d rows, want %d:\n%s", len(rows), dialog.MoveBoxHeight, strings.Join(rows, "\n"))
	}
	dump := strings.Join(rows, "\n")
	frame := []rune(rows[1])[0]
	if !isRuleRow(rows[3]) || !isRuleRow(rows[len(rows)-3]) {
		t.Errorf("the move dialog lacks a rule:\n%s", dump)
	}
	const airOverAdvanced = 7
	for i := 1; i < len(rows)-1; i++ {
		if i != airOverAdvanced && !isRuleRow(rows[i]) && strings.TrimSpace(strings.Trim(rows[i], string(frame))) == "" {
			t.Errorf("row %d of the dialog is blank:\n%s", i, dump)
		}
	}
}
