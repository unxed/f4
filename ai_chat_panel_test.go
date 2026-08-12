package main

import (
	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vtvibe"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"strings"
	"testing"
)

func TestAIChatPanel_Resize(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewNullVFS(0))
	cp := NewAIChatPanel(fp)
	cp.SetPosition(0, 0, 79, 23)

	if cp.input.Y1 != 19 {
		t.Errorf("Expected input Y1 to be 19, got %d", cp.input.Y1)
	}
	if cp.input.Y2 != 22 {
		t.Errorf("Expected input Y2 to be 22, got %d", cp.input.Y2)
	}
	if cp.Kind() != "ai_chat" {
		t.Errorf("Expected Kind 'ai_chat', got '%s'", cp.Kind())
	}
}

func TestAIChatPanel_CellCutChat(t *testing.T) {
	if cut := cellCutChat("hello", 10); cut != 5 {
		t.Errorf("cellCutChat expected 5, got %d", cut)
	}
	if cut := cellCutChat("hello", 2); cut != 2 {
		t.Errorf("cellCutChat expected 2, got %d", cut)
	}
}
func TestFormatAttachedFilesLabel(t *testing.T) {
	files := []string{"app.go", "file_panel.go", "file_panel_test.go", "vtvibe.go"}

	labelShort := formatAttachedFilesLabel(files, 80)
	if !strings.Contains(labelShort, "app.go, file_panel.go") || strings.Contains(labelShort, "...") {
		t.Errorf("expected full label, got %q", labelShort)
	}

	labelTruncated := formatAttachedFilesLabel(files, 25)
	if !strings.Contains(labelTruncated, "...") {
		t.Errorf("expected truncated label with '...', got %q", labelTruncated)
	}
	if runewidth.StringWidth(labelTruncated) > 25 {
		t.Errorf("label width %d exceeds maxW 25: %q", runewidth.StringWidth(labelTruncated), labelTruncated)
	}
}

func TestAIChatPanel_AttachedFilesBarFocusAndNavigation(t *testing.T) {
	session := vtvibe.NewSession()
	fp := NewFileSystemPanel(0, 0, 80, 24, &aiVFSWrapper{AIVFS: vtvibe.NewVFS(session)})
	cp := NewAIChatPanel(fp)
	cp.SetFocus(true)

	// Add file to ctx
	vfsInst := vtvibe.NewVFS(session)
	w, err := vfsInst.Create(nil, "/ctx/main.go")
	if err == nil {
		_, _ = w.Write([]byte("package main"))
		_ = w.Close()
	}

	// Press Up arrow from input row 0 -> should focus Attached Files Bar (focusedLinkIdx = -2)
	upEvent := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_UP,
	}

	if !cp.ProcessKey(upEvent) {
		t.Fatal("Up arrow from input box should be handled")
	}
	if cp.focusedLinkIdx != -2 {
		t.Fatalf("expected focusedLinkIdx = -2 (Attached Files Bar), got %d", cp.focusedLinkIdx)
	}

	// Press Down arrow -> should return focus to input box (focusedLinkIdx = -1)
	downEvent := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_DOWN,
	}
	if !cp.ProcessKey(downEvent) {
		t.Fatal("Down arrow from Attached Files Bar should be handled")
	}
	if cp.focusedLinkIdx != -1 {
		t.Fatalf("expected focusedLinkIdx = -1 (Input Box), got %d", cp.focusedLinkIdx)
	}
}
func TestAIChatPanel_TabPassesThroughForPanelSwitching(t *testing.T) {
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewNullVFS(0))
	cp := NewAIChatPanel(fp)
	cp.SetFocus(true)

	// Tab key in ProcessKey must return false so PanelsFrame can switch active panel
	tabEvent := &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_TAB,
	}

	if cp.ProcessKey(tabEvent) {
		t.Error("AIChatPanel should NOT consume Tab key, so panel switching works")
	}
}
func TestAIChatPanel_RCtrlC_CopiesLastResponse(t *testing.T) {
	session := vtvibe.NewSession()
	fp := NewFileSystemPanel(0, 0, 80, 24, &aiVFSWrapper{AIVFS: vtvibe.NewVFS(session)})
	cp := NewAIChatPanel(fp)
	cp.SetFocus(true)

	rCtrlCEvent := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_C,
		ControlKeyState: vtinput.RightCtrlPressed,
	}

	// Should be handled and return true
	if !cp.ProcessKey(rCtrlCEvent) {
		t.Error("AIChatPanel should consume RCtrl+C")
	}
}

func TestAIChatPanel_ContextFilesRenderingAndLinkNavigation(t *testing.T) {
	session := vtvibe.NewSession()
	_ = session.Ask // compile check

	fp := NewFileSystemPanel(0, 0, 80, 24, &aiVFSWrapper{AIVFS: vtvibe.NewVFS(session)})
	cp := NewAIChatPanel(fp)
	cp.SetFocus(true)

	// Add file to ctx
	vfsInst := vtvibe.NewVFS(session)
	w, err := vfsInst.Create(nil, "/ctx/app.go")
	if err == nil {
		_, _ = w.Write([]byte("package main"))
		_ = w.Close()
	}

	files := session.ContextFiles()
	if len(files) != 1 || files[0] != "app.go" {
		t.Fatalf("expected ['app.go'], got %v", files)
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	cp.Show(scr)

	// Check that app.go is rendered on screen
	foundAttached := false
	for y := 0; y < 25; y++ {
		str := ScreenRow(scr, y, 0, 79)
		if strings.Contains(str, "app.go") {
			foundAttached = true
			break
		}
	}
	if !foundAttached {
		t.Error("Attached file app.go not rendered in AIChatPanel")
	}
}
