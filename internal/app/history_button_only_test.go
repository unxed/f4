package app

import (
	"testing"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// closeHistoryAbove pops whatever the history field put over the dialog.
func closeHistoryAbove(dlg vtui.Frame) {
	for vtui.FrameManager.GetTopFrame() != dlg {
		vtui.FrameManager.Pop()
	}
}

// checkHistoryButtonOnly drives the dialog on top of the frame stack the way
// the mouse does (f4#2124): a click in the body of the history field only
// places the cursor, the arrow at its right edge opens the list, and so does
// the wheel, wherever the pointer is inside the dialog.
func checkHistoryButtonOnly(t *testing.T, edit *vtui.Edit) {
	t.Helper()
	dlg := vtui.FrameManager.GetTopFrame()
	if !edit.HistoryButtonOnly {
		t.Fatal("history field must open its list only on the arrow and the wheel")
	}
	mouse := func(down bool, x, y int, wheel bool) {
		ev := &vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: down,
			MouseX: testutil.Int16(x), MouseY: testutil.Int16(y)}
		if wheel {
			ev.WheelDirection = -1
		} else if down {
			ev.ButtonState = vtinput.FromLeft1stButtonPressed
		}
		dlg.ProcessMouse(ev)
	}

	mouse(true, edit.X1+1, edit.Y1, false)
	mouse(false, edit.X1+1, edit.Y1, false)
	if vtui.FrameManager.GetTopFrame() != dlg {
		closeHistoryAbove(dlg)
		t.Fatal("a click in the body of the field opened the history list")
	}

	mouse(true, edit.X2, edit.Y1, false)
	mouse(false, edit.X2, edit.Y1, false)
	if vtui.FrameManager.GetTopFrame() == dlg {
		t.Fatal("a click on the arrow did not open the history list")
	}
	closeHistoryAbove(dlg)

	_, _, x2, y2 := dlg.GetPosition()
	mouse(true, x2-1, y2-1, true)
	if vtui.FrameManager.GetTopFrame() == dlg {
		t.Fatal("the wheel away from the field did not open the history list")
	}
	closeHistoryAbove(dlg)
}

func TestMkDirDialogHistoryButtonOnly(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	theme.SetDefaultF4Palette()
	store := useStubHistory(t)
	store[history.NewFolderHistoryID] = []string{"build", "docs"}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)
	defer vtui.FrameManager.Pop()

	actionMkDir(pf)
	defer vtui.FrameManager.Pop()
	edit := findHistoryEdit(t, vtui.FrameManager.GetTopFrame().(vtui.Container), history.NewFolderHistoryID)
	checkHistoryButtonOnly(t, edit)
}

func TestNewFileDialogHistoryButtonOnly(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	theme.SetDefaultF4Palette()
	store := useStubHistory(t)
	store[history.NewEditHistoryID] = []string{"a.txt", "b.txt"}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionNewFile(pf)
	defer vtui.FrameManager.Pop()
	edit := findHistoryEdit(t, vtui.FrameManager.GetTopFrame().(vtui.Container), history.NewEditHistoryID)
	checkHistoryButtonOnly(t, edit)
}

func TestCopyMoveDialogHistoryButtonOnly(t *testing.T) {
	for _, isMove := range []bool{false, true} {
		name := "copy"
		if isMove {
			name = "move"
		}
		t.Run(name, func(t *testing.T) {
			t.Cleanup(paneltest.SwapFrameManager(t))
			screen := vtui.NewSilentScreenBuf()
			screen.AllocBuf(80, 25)
			vtui.FrameManager.Init(screen)
			theme.SetDefaultF4Palette()
			store := useStubHistory(t)
			store[history.CopyDestHistoryID] = []string{"/tmp/one", "/tmp/two"}

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
			defer vtui.FrameManager.Pop()
			edit := findHistoryEdit(t, vtui.FrameManager.GetTopFrame().(vtui.Container), history.CopyDestHistoryID)
			checkHistoryButtonOnly(t, edit)
		})
	}
}
