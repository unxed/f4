package main

import (
	"testing"

	"github.com/unxed/f4/piecetable"
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
		{name: "decimal suffix overrides checkbox", text: "255d", hex: true, want: 255},
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
