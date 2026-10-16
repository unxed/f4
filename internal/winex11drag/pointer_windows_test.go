//go:build windows

package winex11drag

import "testing"

func TestPointerPosition(t *testing.T) {
	point := winex11Point{X: -17, Y: 42}
	for _, tt := range []struct {
		name     string
		cursorOK bool
		keyState uintptr
		wantX    int
		wantY    int
		wantDown bool
	}{
		{name: "button held", cursorOK: true, keyState: winex11KeyDown, wantX: -17, wantY: 42, wantDown: true},
		{name: "button up", cursorOK: true, keyState: 1, wantX: -17, wantY: 42},
		{name: "cursor unavailable", keyState: winex11KeyDown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x, y, down := pointerPosition(point, tt.cursorOK, tt.keyState)
			if x != tt.wantX || y != tt.wantY || down != tt.wantDown {
				t.Fatalf("pointerPosition() = (%d, %d, %v), want (%d, %d, %v)", x, y, down, tt.wantX, tt.wantY, tt.wantDown)
			}
		})
	}
}
