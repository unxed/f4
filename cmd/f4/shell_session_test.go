package main

import (
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestPanelsFrame_ExitResetsLocalShell(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	oldSpawn := spawnLocalShellPTY
	oldFactory := newLocalPTY
	t.Cleanup(func() {
		spawnLocalShellPTY = oldSpawn
		newLocalPTY = oldFactory
	})

	oldPTY := pf.localPTY().(*mockPty)
	spawnLocalShellPTY = true
	created := make(chan *mockPty, 1)
	newLocalPTY = func() (PtyBackend, error) {
		pty := &mockPty{}
		created <- pty
		return pty, nil
	}

	pf.cmdLine.Edit.SetText("exit")
	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	if !oldPTY.IsClosed() {
		t.Fatal("exit did not close the old local shell")
	}

	var replacement *mockPty
	select {
	case replacement = <-created:
	case <-time.After(time.Second):
		t.Fatal("exit did not start a replacement local shell")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pf.localPTY() == replacement {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("replacement local shell was not published to the panels frame")
}

func TestPanelsFrame_ExitF4RequestsApplicationQuit(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldConfirm := AppConfig.ConfirmExit
	AppConfig.ConfirmExit = true
	t.Cleanup(func() { AppConfig.ConfirmExit = oldConfirm })

	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	pty := pf.localPTY().(*mockPty)
	pf.cmdLine.Edit.SetText("exit f4")

	pressKey(pf, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_RETURN,
	})

	top := vtui.FrameManager.GetTopFrame()
	if top == nil || top.GetTitle() != Msg("Quit.Title") {
		t.Fatalf("exit f4 did not open the application quit confirmation: top=%T title=%q", top, func() string {
			if top == nil {
				return ""
			}
			return top.GetTitle()
		}())
	}
	if got := pty.String(); got != "" {
		t.Fatalf("exit f4 was sent to the shell instead of requesting f4 quit: %q", got)
	}

	if dialog, ok := top.(*vtui.Window); ok && dialog.OnResult != nil {
		dialog.OnResult(1)
	}
}

// A local shell that exits on its own -- on Windows, `exit` inside a batch
// file ends cmd.exe itself (issue #409) -- must not strand the command it was
// running: the panels come back, the shell is replaced, and the output the
// shell left on screen survives the replacement.
func TestPanelsFrame_LocalShellExitReturnsPanelsAndRestartsShell(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	defer pf.Close()
	oldSpawn := spawnLocalShellPTY
	oldFactory := newLocalPTY
	t.Cleanup(func() {
		spawnLocalShellPTY = oldSpawn
		newLocalPTY = oldFactory
	})

	pf.ResizeConsole(80, 25)
	oldPTY := pf.localPTY().(*mockPty)
	spawnLocalShellPTY = true
	// Start the read loop on the existing mock shell; TestMain leaves it off.
	pf.initPTY()
	created := make(chan *mockPty, 1)
	newLocalPTY = func() (PtyBackend, error) {
		pty := &mockPty{}
		created <- pty
		return pty, nil
	}

	// A batch is running with the panels hidden for it, and it has printed
	// something the person will want to read afterwards.
	pf.parser.Process([]byte("batch output\r\nC:\\test>exit"))
	pf.beginPromptDrivenExecution()
	pf.returnToPanels = true
	pf.showPanels = false

	// The shell process ends: the read loop sees EOF.
	oldPTY.Close()

	var replacement *mockPty
	deadline := time.Now().Add(2 * time.Second)
	for replacement == nil || !pf.showPanels || pf.localPTY() != replacement {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case replacement = <-created:
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell exit was not recovered from: showPanels=%v executing=%v replacement=%v current=%T",
				pf.showPanels, pf.executing, replacement != nil, pf.localPTY())
		}
	}
	if pf.executing {
		t.Fatal("execution still marked running after the shell exited")
	}
	if pf.isPtyBusy() {
		t.Fatal("terminal still reported busy after the shell exited")
	}
	var screen strings.Builder
	for _, row := range pf.termView.getBuffer() {
		screen.WriteString(strings.TrimRight(cellsText(row), " "))
		screen.WriteString("\n")
	}
	if !strings.Contains(screen.String(), "batch output") {
		t.Fatalf("shell output was cleared when the shell was replaced:\n%s", screen.String())
	}
	if pf.termView.CursorX != 0 {
		t.Fatalf("cursor left mid-line for the new shell: x=%d", pf.termView.CursorX)
	}
}
