package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestCommandHistoryEnterRunsSelectedEntry(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	previousHistory := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = stubHistoryProvider{}
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previousHistory })

	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	pf.ActiveIdx = 0
	target := t.TempDir()
	pf.CmdLine.Edit.History = []string{"cd " + target}

	actionCommandHistory(pf)
	menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	if !ok {
		t.Fatalf("top frame = %T, want *vtui.VMenu", vtui.FrameManager.GetTopFrame())
	}

	menu.SetSelectPos(0)
	menu.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	got := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel).Vfs.GetPath()
	if filepath.Clean(got) != filepath.Clean(target) {
		t.Fatalf("Enter left the active panel at %q, want command to change it to %q", got, target)
	}
}

func TestCommandHistoryCtrlEnterPastesSelectedEntry(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	previousHistory := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = stubHistoryProvider{}
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previousHistory })

	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	const command = "echo selected-command"
	pf.CmdLine.Edit.History = []string{command}

	actionCommandHistory(pf)
	menu := vtui.FrameManager.GetTopFrame().(*vtui.VMenu)
	menu.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RETURN,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if got := pf.CmdLine.Edit.GetText(); got != command {
		t.Fatalf("Ctrl+Enter inserted %q, want %q", got, command)
	}
}

func TestIssue821CommandHistoryKeepsLongEntryInsideDialog(t *testing.T) {
	const (
		screenWidth  = 100
		screenHeight = 20
	)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(screenWidth, screenHeight)
	vtui.FrameManager.Init(scr)
	theme.SetDefaultF4Palette()

	menu := vtui.NewVMenu("History")
	search := newHistorySearch(menu, []history.HistoryRecord{{Name: strings.Repeat("x", 200)}}, "")
	t.Cleanup(search.cleanup)
	const sentinel = '~'
	scr.FillRect(0, 0, screenWidth-1, screenHeight-1, sentinel, 0)
	menu.Show(scr)
	search.draw(scr)

	for x := 0; x < screenWidth; x++ {
		if x >= menu.X1 && x <= menu.X2 {
			continue
		}
		if got := testutil.Rune(scr.GetCell(x, menu.Y1+1).Char); got != sentinel {
			t.Fatalf("long history entry escaped dialog at column %d as %q", x, got)
		}
	}
}
