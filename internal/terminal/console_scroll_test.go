package terminal

import "testing"

// The numbers in the first two cases are the ones measured on live ReactOS
// 0.4.16 (issue #513): an 80x300 buffer, a 25-row window, and a cursor that
// walked off the bottom while the window stayed put.
func TestWindowFollowingCursor(t *testing.T) {
	tests := []struct {
		name     string
		win      ConsoleWindowRect
		cursorY  int16
		bufferH  int16
		wantMove bool
		wantTop  int16
		wantBot  int16
	}{
		{
			name:     "measured on reactos: cursor 12 rows below the window",
			win:      ConsoleWindowRect{Left: 0, Top: 12, Right: 79, Bottom: 36},
			cursorY:  48,
			bufferH:  300,
			wantMove: true,
			wantTop:  24,
			wantBot:  48,
		},
		{
			name:     "measured on reactos: second run, deeper in the buffer",
			win:      ConsoleWindowRect{Left: 0, Top: 26, Right: 79, Bottom: 50},
			cursorY:  61,
			bufferH:  300,
			wantMove: true,
			wantTop:  37,
			wantBot:  61,
		},
		{
			name:     "cursor on the last visible row: nothing to do",
			win:      ConsoleWindowRect{Left: 0, Top: 12, Right: 79, Bottom: 36},
			cursorY:  36,
			bufferH:  300,
			wantMove: false,
		},
		{
			name:     "cursor inside the window: nothing to do",
			win:      ConsoleWindowRect{Left: 0, Top: 12, Right: 79, Bottom: 36},
			cursorY:  20,
			bufferH:  300,
			wantMove: false,
		},
		{
			name:     "cursor above the window is not chased upward",
			win:      ConsoleWindowRect{Left: 0, Top: 40, Right: 79, Bottom: 64},
			cursorY:  3,
			bufferH:  300,
			wantMove: false,
		},
		{
			name:     "clamped to the buffer at the very bottom",
			win:      ConsoleWindowRect{Left: 0, Top: 270, Right: 79, Bottom: 294},
			cursorY:  299,
			bufferH:  300,
			wantMove: true,
			// Rows are 0-based, so a 300-row buffer ends at 299 and a
			// 25-row window sitting flush against the end starts at 275.
			wantTop: 275,
			wantBot: 299,
		},
		{
			name:     "no scrollback at all (window == buffer): nothing to do",
			win:      ConsoleWindowRect{Left: 0, Top: 0, Right: 79, Bottom: 24},
			cursorY:  24,
			bufferH:  25,
			wantMove: false,
		},
		{
			name:     "degenerate window is left alone",
			win:      ConsoleWindowRect{Left: 0, Top: 10, Right: 79, Bottom: 5},
			cursorY:  40,
			bufferH:  300,
			wantMove: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := WindowFollowingCursor(tc.win, tc.cursorY, tc.bufferH)
			if ok != tc.wantMove {
				t.Fatalf("moved = %v, want %v (got %+v)", ok, tc.wantMove, got)
			}
			if !tc.wantMove {
				if got != tc.win {
					t.Errorf("no move wanted but rect changed: %+v -> %+v", tc.win, got)
				}
				return
			}
			if got.Top != tc.wantTop || got.Bottom != tc.wantBot {
				t.Errorf("Top/Bottom = %d/%d, want %d/%d", got.Top, got.Bottom, tc.wantTop, tc.wantBot)
			}
			if got.Left != tc.win.Left || got.Right != tc.win.Right {
				t.Errorf("columns changed: %d..%d, want %d..%d",
					got.Left, got.Right, tc.win.Left, tc.win.Right)
			}
			if h := got.Bottom - got.Top; h != tc.win.Bottom-tc.win.Top {
				t.Errorf("height changed: %d, want %d", h, tc.win.Bottom-tc.win.Top)
			}
			if got.Bottom > tc.bufferH-1 {
				t.Errorf("window runs past the buffer: Bottom=%d, buffer height %d",
					got.Bottom, tc.bufferH)
			}
		})
	}
}

// The cursor normally lands one row below the last line printed, so a window
// moved to follow it must still show that line.
func TestWindowFollowingCursorKeepsTheLastPrintedRowVisible(t *testing.T) {
	win := ConsoleWindowRect{Left: 0, Top: 12, Right: 79, Bottom: 36}
	lastPrinted := int16(47)
	got, ok := WindowFollowingCursor(win, lastPrinted+1, 300)
	if !ok {
		t.Fatal("expected the window to move")
	}
	if lastPrinted < got.Top || lastPrinted > got.Bottom {
		t.Errorf("last printed row %d not visible in %d..%d", lastPrinted, got.Top, got.Bottom)
	}
}
