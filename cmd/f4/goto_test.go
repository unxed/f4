package main

import (
	"testing"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestParseGotoOffset(t *testing.T) {
	tests := []struct {
		name string
		text string
		hex  bool
		want int64
	}{
		{name: "decimal", text: "255", want: 255},
		{name: "checked hexadecimal", text: "ff", hex: true, want: 255},
		{name: "0x prefix", text: "0xff", want: 255},
		{name: "dollar prefix", text: "$ff", want: 255},
		{name: "h suffix", text: "ffh", want: 255},
		{name: "decimal suffix in decimal mode", text: "255d", want: 255},
		{name: "hexadecimal value ending in d", text: "dead", hex: true, want: 0xdead},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := parseGotoOffset(test.text, test.hex); err != nil || got != test.want {
				t.Fatalf("parseGotoOffset(%q, %v) = %d, %v; want %d", test.text, test.hex, got, err, test.want)
			}
		})
	}
	for _, text := range []string{"", "-1", "nope", "0x"} {
		if _, err := parseGotoOffset(text, false); err == nil {
			t.Errorf("parseGotoOffset(%q) accepted invalid input", text)
		}
	}
}

func TestGotoOffsetDialogCheckboxSelectsHexadecimalInput(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	anchor := vtui.NewWindow(0, 0, 80, 24, "anchor")
	vtui.FrameManager.Push(anchor)

	var got int64 = -1
	dlg := showGotoOffsetDialog(anchor, "Go to offset", "Byte offset:", 0, func(offset int64) {
		got = offset
	})
	var edit *vtui.Edit
	var hex *vtui.Checkbox
	var ok *vtui.Button
	for _, item := range dlg.GetChildren() {
		switch item := item.(type) {
		case *vtui.Edit:
			edit = item
		case *vtui.Checkbox:
			hex = item
		case *vtui.Button:
			if ok == nil {
				ok = item
			}
		}
	}
	if edit == nil || hex == nil || ok == nil {
		t.Fatalf("goto dialog controls = edit %v, hex %v, ok %v", edit != nil, hex != nil, ok != nil)
	}
	edit.SetText("dead")
	dlg.SetFocusedItem(hex)
	if !dlg.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_SPACE}) {
		t.Fatal("space did not toggle hexadecimal checkbox")
	}
	if hex.State != 1 {
		t.Fatalf("hexadecimal checkbox state = %d, want checked", hex.State)
	}
	dlg.SetFocusedItem(ok)
	if !dlg.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}) {
		t.Fatal("enter did not activate OK")
	}
	if got != 0xdead {
		t.Fatalf("dialog parsed hexadecimal input as %d, want %d", got, 0xdead)
	}
}

func TestEditorGotoOffsetUsesByteCoordinates(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := NewEditorView(piecetable.New([]byte("0123456789abcdefXYZ")), nil, "test.bin")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)
	ev.HexMode = true
	ev.gotoOffset(0x12)
	if ev.CursorLine != 0 || ev.CursorPos != 0x12 || ev.HexTopOffset != 0x10 {
		t.Fatalf("gotoOffset coordinates = line %d, pos %d, top %d; want 0, 18, 16", ev.CursorLine, ev.CursorPos, ev.HexTopOffset)
	}
}

func TestEditorGotoLinePositionUsesOneBasedInput(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := NewEditorView(piecetable.New([]byte("first\nsecond\nthird")), nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)
	ev.gotoLinePosition(2, 3)
	if ev.targetLine != -1 || ev.CursorLine != 1 || ev.CursorPos != 2 {
		t.Fatalf("gotoLinePosition state = target %d, line %d, pos %d; want -1, 1, 2", ev.targetLine, ev.CursorLine, ev.CursorPos)
	}
}
