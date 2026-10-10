package panel

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestHiddenPanelsCursorSurvivesWorkspaceSwitch(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	previousConfig := config.App
	t.Cleanup(func() { config.App = previousConfig })
	previousBidiMode := vtui.DefaultBidiMode
	vtui.DefaultBidiMode = vtui.BidiFull
	t.Cleanup(func() { vtui.DefaultBidiMode = previousBidiMode })
	config.App.NavigationMode = config.NavigationSearchFirst
	config.App.SearchCommandStayFocused = false
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 25)
	vtui.FrameManager.Init(scr)
	first := NewPanelsFrame()
	defer first.Close()
	vtui.FrameManager.AddScreen(first)
	firstIdx := vtui.FrameManager.ActiveIdx
	second := NewPanelsFrame()
	defer second.Close()
	second.ShellMode = terminal.ShellModeOwn
	second.Pty = &mockPty{}
	second.LastAutoRefresh = time.Now()
	vtui.FrameManager.AddScreen(second)
	secondIdx := vtui.FrameManager.ActiveIdx
	second.ResizeConsole(120, 25)
	second.CmdLine.Edit.SetText("echo test")
	second.CmdLine.Edit.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	// Give the terminal a different caret, as a shell's prompt commonly does.
	second.TermView.SetCursor(2, second.TermView.Height-1)
	second.TogglePanelsVisibility()

	checkCursor := func(stage string) {
		t.Helper()
		scr.SetCursorVisible(false)
		second.Show(scr)
		x, y, visible, _ := scr.GetCursorStateForTesting()
		wantX := second.CmdLine.Edit.X1 + len(second.CmdLine.Edit.GetText())
		if !visible || x != wantX || y != second.CmdLine.Edit.Y1 {
			t.Errorf("%s: cursor=(%d,%d) visible=%v, want command edit (%d,%d)", stage, x, y, visible, wantX, second.CmdLine.Edit.Y1)
		}
		if second.CommandLineFocused {
			t.Error("temporary console focus changed the saved panel navigation target")
		}
	}
	checkCursor("hide panels")
	vtui.FrameManager.SwitchScreen(firstIdx)
	if second.CmdLine.IsFocused() || second.CmdLine.Edit.IsFocused() {
		t.Error("inactive workspace retained command-line focus")
	}
	vtui.FrameManager.SwitchScreen(secondIdx)
	checkCursor("return to non-first workspace")
	for i := 0; i < 2; i++ {
		second.CmdLine.Edit.SetText("echo test")
		if !second.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}) || !second.Executing {
			t.Fatal("command did not start")
		}
		// Completion is simulated here to test UI focus policy, rather than
		// ConPTY protocol or timing. The native shell caret differs from the
		// f4 prompt and must not win when the empty command edit reappears.
		second.endExecution()
		second.TermView.SetCursor(2, second.TermView.Height-1)
		checkCursor("command completion")
	}
	second.CmdLine.Edit.SetText("abc")
	second.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT,
	})
	second.Show(scr)
	x, y, _, _ := scr.GetCursorStateForTesting()
	second.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'X'})
	second.Show(scr)
	if got := vtui.CellString(scr.GetCell(x, y).Char); got != "X" {
		t.Errorf("caret cell (%d,%d) contains %q after insertion", x, y, got)
	}
	second.TogglePanelsVisibility()
	if second.CmdLine.IsFocused() || second.CmdLine.Edit.IsFocused() {
		t.Error("showing panels did not restore search-first panel focus")
	}
}
