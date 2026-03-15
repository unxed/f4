package main

import (
	"testing"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestCommandLine_Input(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 10, 0)

	// Simulate typing 'f'
	cl.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'f',
	})

	if cl.Edit.GetText() != "f" {
		t.Errorf("Expected cmdline text 'f', got '%s'", cl.Edit.GetText())
	}

	// Simulate Backspace
	cl.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_BACK,
	})

	if len(cl.Edit.GetText()) != 0 {
		t.Error("CommandLine should be empty after backspace")
	}
}