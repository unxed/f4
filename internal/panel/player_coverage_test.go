package panel

import (
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestPlayerShowRendersPlaylistStates(t *testing.T) {
	pp := newTestPlayerPanel()
	t.Cleanup(pp.Close)
	pp.SetPosition(0, 0, 60, 20)
	current := &playlistItem{Name: "current.mp3", Path: "/current.mp3"}
	open := &playlistItem{Name: "open", Folder: true, Expanded: true}
	open.insertChild(-1, current)
	closed := &playlistItem{Name: "closed", Folder: true}
	pp.root.insertChild(-1, open)
	pp.root.insertChild(-1, closed)
	pp.status = "ready"
	pp.focused = true
	pp.cursor = 1
	pp.rebuildRows()
	pp.Show(vtui.NewSilentScreenBuf())
	pp.status = ""
	pp.current = current
	pp.cursor = -1
	pp.button = playerBtnVolume
	pp.Show(vtui.NewSilentScreenBuf())

	pp.SetPosition(0, 0, 5, 3)
	pp.Show(vtui.NewSilentScreenBuf())
}

func TestPlayerGlobalAndControlKeys(t *testing.T) {
	pp := newTestPlayerPanel()
	t.Cleanup(pp.Close)
	for _, ch := range []rune{'z', 'x', 'c', 'v', 'b', '+', '=', '-', '_', 'я', 'Ч', 'с', 'м', 'и'} {
		if !pp.globalKey(&vtinput.InputEvent{KeyDown: true, Char: ch}) {
			t.Errorf("globalKey(%q) was not handled", ch)
		}
	}
	if pp.globalKey(&vtinput.InputEvent{KeyDown: true, Char: '?'}) {
		t.Error("unknown global character was handled")
	}
	for _, vk := range []uint16{vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_HOME, vtinput.VK_END, vtinput.VK_DOWN, vtinput.VK_NEXT, vtinput.VK_RETURN, vtinput.VK_SPACE, vtinput.VK_UP} {
		if !pp.controlKey(&vtinput.InputEvent{VirtualKeyCode: vk}) {
			t.Errorf("control key %d was not handled", vk)
		}
	}
	if pp.controlKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_F1}) {
		t.Error("unrelated control key was handled")
	}
	for _, vk := range []uint16{vtinput.VK_F3, vtinput.VK_F4, vtinput.VK_F5, vtinput.VK_F6, vtinput.VK_F7, vtinput.VK_F8, vtinput.VK_INSERT, vtinput.VK_DELETE} {
		if !isFilePanelOnlyKey(vk) {
			t.Errorf("file-panel key %d was not recognized", vk)
		}
	}
	if isFilePanelOnlyKey(vtinput.VK_F1) {
		t.Error("F1 is not file-panel-only")
	}
}

func TestPlayerVetoesFilePanelHotkeysBeforeGlobalActions(t *testing.T) {
	player := &PlayerPanel{focused: true}
	p := &PanelsFrame{ShowPanels: true, ActiveIdx: 0}
	p.AltPanels[0] = player

	for _, vk := range []uint16{
		vtinput.VK_F3, vtinput.VK_F4, vtinput.VK_F5, vtinput.VK_F6,
		vtinput.VK_F7, vtinput.VK_F8, vtinput.VK_INSERT, vtinput.VK_DELETE,
	} {
		e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk}
		if !p.VetoActionKey(e) {
			t.Errorf("VetoActionKey(%d) = false, want true with focused player", vk)
		}
	}

	for _, e := range []*vtinput.InputEvent{
		{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F4, ControlKeyState: vtinput.LeftCtrlPressed},
		{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F4, ControlKeyState: vtinput.LeftAltPressed},
	} {
		if p.VetoActionKey(e) {
			t.Errorf("VetoActionKey(%d) = true with modifier 0x%x, want false", e.VirtualKeyCode, e.ControlKeyState)
		}
	}
}
