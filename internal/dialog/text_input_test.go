package dialog

import (
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
)

func TestConfirmedTextDoesNotAssignHotkey(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	completed := false
	frame := newTestHotkeyAssignFrame(hm, func() { completed = true })
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '@', VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
	if !frame.ProcessKey(&event) || frame.IsDone() || completed || len(hm.Bindings["Shell"]) != 0 {
		t.Fatalf("text assigned a shortcut or closed the dialog: done=%v completed=%v bindings=%v", frame.IsDone(), completed, hm.Bindings["Shell"])
	}
}

func TestConfirmedTextHelpSearch(t *testing.T) {
	newSearchableHelpForTest(t, []string{"@@"})
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '@', VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed,
		RepeatCount: 2}
	original := event
	if !HandleHelpSearchHotkey(&event) || CurrentHelpSearch == nil || string(CurrentHelpSearch.Query) != "@@" {
		t.Fatalf("committed text did not reach help search: %+v", CurrentHelpSearch)
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("help search changed source event")
	}
}
