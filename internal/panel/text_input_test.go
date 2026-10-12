package panel

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestConfirmedTextDoesNotSelectByMask(t *testing.T) {
	oldConfig, oldRun := config.App, RunAction
	t.Cleanup(func() { config.App, RunAction = oldConfig, oldRun })
	config.App.NavigationMode = config.NavigationClassic
	var action string
	RunAction = func(name string) bool { action = name; return true }
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShowPanels = true
	for _, ch := range "+-*" {
		pf.CmdLine.Edit.SetText("")
		event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
			Char: ch, VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
		original := event
		if !pf.ProcessKey(&event) || action != "" || pf.CmdLine.Edit.GetText() != string(ch) {
			t.Fatalf("text %q triggered action %q, command line %q", ch, action, pf.CmdLine.Edit.GetText())
		}
		if !reflect.DeepEqual(event, original) {
			t.Fatal("source event changed")
		}
	}
	pf.CmdLine.Edit.SetText("")
	pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '*'})
	if action != "Panel.InvertSelection" {
		t.Fatalf("ordinary selection shortcut changed: %q", action)
	}
}

func TestConfirmedTextDoesNotInsertPanelPath(t *testing.T) {
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	if err := pf.Panels[0].(*FileSystemPanel).Vfs.SetPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Push(pf)
	dialog := vtui.NewCenteredDialog(50, 9, "Path")
	edit := vtui.NewEdit(0, 0, 30, "original")
	dialog.AddItem(edit)
	dialog.SetFocusedItem(edit)
	vtui.FrameManager.Push(dialog)
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '[', VirtualKeyCode: vtinput.VK_OEM_4, ControlKeyState: vtinput.LeftCtrlPressed}
	shortcut := event
	shortcut.TextInput = false
	if !HandlePanelPathEditHotkey(&shortcut) {
		t.Fatal("ordinary Ctrl+[ must insert a panel path")
	}
	edit.SetText("original")
	if HandlePanelPathEditHotkey(&event) || edit.GetText() != "original" {
		t.Fatalf("committed text inserted a panel path: %q", edit.GetText())
	}
}

func TestConfirmedTextDoesNotControlPlayer(t *testing.T) {
	player := newTestPlayerPanel()
	player.focused = true
	player.Engine.SetVolume(0.5)
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true, Char: '+'}
	if player.ProcessKey(&event) || player.Engine.Volume() != 0.5 {
		t.Fatal("committed text changed player volume")
	}
	event.TextInput = false
	if !player.ProcessKey(&event) || player.Engine.Volume() <= 0.5 {
		t.Fatal("ordinary volume shortcut stopped working")
	}
}

func TestConfirmedTextPluginHotkeyField(t *testing.T) {
	edit := &pluginHotkeyEdit{vtui.NewEdit(0, 0, 1, "A")}
	for _, ch := range "@q" {
		event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
			Char: ch, VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
		original := event
		edit.ProcessKey(&event)
		want := "A"
		if ch == 'q' {
			want = "Q"
		}
		if edit.GetText() != want {
			t.Fatalf("text %q bypassed one-character field validation: got %q, want %q", ch, edit.GetText(), want)
		}
		if !reflect.DeepEqual(event, original) {
			t.Fatal("field changed source event")
		}
	}
}

func TestConfirmedTextDoesNotAddMenuItem(t *testing.T) {
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: 'ń', VirtualKeyCode: vtinput.VK_N, ControlKeyState: vtinput.LeftCtrlPressed}
	if isAddItemKey(&event) {
		t.Fatal("committed text interpreted as Ctrl+N")
	}
}

func TestConfirmedTextForwardedToChild(t *testing.T) {
	for _, altScreen := range []bool{false, true} {
		for _, win32 := range []bool{false, true} {
			t.Run(fmt.Sprintf("alt=%t/win32=%t", altScreen, win32), func(t *testing.T) {
				pf := NewPanelsFrame()
				defer pf.Close()
				pf.ShowPanels = false
				pf.TermView.UseAltScreen = altScreen
				pf.TermView.Win32InputMode = win32
				pty := &busyMockPty{}
				pf.Pty = pty
				e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
					Char: '@', VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
					ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed, RepeatCount: 1,
					InputSource: "win32"}
				original := e
				if !pf.ProcessKey(&e) {
					t.Fatal("child input was not handled")
				}
				want := "@"
				if win32 {
					want = "\x1b[81;16;64;1;9;1_"
				}
				if got := pty.String(); got != want {
					t.Fatalf("child received %q, want %q", got, want)
				}
				if !reflect.DeepEqual(e, original) {
					t.Fatal("forwarding changed event")
				}
			})
		}
	}
}
