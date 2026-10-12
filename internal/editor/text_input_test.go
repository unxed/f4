package editor

import (
	"testing"

	"github.com/unxed/vtinput"
)

func TestConfirmedTextAtMultipleCarets(t *testing.T) {
	ev := multiCursorEditor(t, "a\nb")
	setExtraCaretsForTest(ev, 2)
	e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: 'Ą', VirtualKeyCode: vtinput.VK_A, RepeatCount: 2,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed | vtinput.ShiftPressed}
	if !ev.ProcessKey(&e) || ev.Pt.String() != "ĄĄa\nĄĄb" {
		t.Fatalf("multicursor text = %q", ev.Pt.String())
	}
	if !ev.MultiCursor() {
		t.Fatal("text collapsed the extra carets")
	}
}

func TestConfirmedTextRepeatOvertypesEachCharacter(t *testing.T) {
	ev := multiCursorEditor(t, "abcd")
	ev.Overtype = true
	e := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '€', VirtualKeyCode: vtinput.VK_E, RepeatCount: 3,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
	if !ev.ProcessKey(&e) || ev.Pt.String() != "€€€d" {
		t.Fatalf("text = %q", ev.Pt.String())
	}
	if e.RepeatCount != 3 {
		t.Fatal("repeat count was lost")
	}
}
