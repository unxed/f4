package app

import (
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtinput"
)

// The player had no mouse at all (#2185). Clicks, drags and the wheel, given
// to the panels as the frame manager gives them.

// playerRows is the player on the screen, row by row, as runes, so that an
// index is a column.
func (f *playerFixture) playerRows() (x1, y1 int, rows [][]rune) {
	f.pf.Show(f.scr)
	x1, y1, x2, y2 := f.player.GetPosition()
	for y := y1; y <= y2; y++ {
		var row []rune
		for x := x1; x <= x2; x++ {
			row = append(row, rune(f.scr.GetCell(x, y).Char)) // #nosec G115 -- a cell holds one character
		}
		rows = append(rows, row)
	}
	return x1, y1, rows
}

// find is where text is on the player's screen.
func (f *playerFixture) find(text string) (x, y int) {
	f.t.Helper()
	x1, y1, rows := f.playerRows()
	for i, row := range rows {
		if at := strings.Index(string(row), text); at >= 0 {
			return x1 + len([]rune(string(row)[:at])), y1 + i
		}
	}
	f.t.Fatalf("%q is not on the player:\n%s", text, strings.Join(runeRows(rows), "\n"))
	return 0, 0
}

// span is the first and the last column of the run of any of chars on the
// row y of the player.
func (f *playerFixture) span(y int, chars string) (first, last int) {
	f.t.Helper()
	x1, y1, rows := f.playerRows()
	first, last = -1, -1
	for i, r := range rows[y-y1] {
		if strings.ContainsRune(chars, r) {
			if first < 0 {
				first = x1 + i
			}
			last = x1 + i
		}
	}
	if first < 0 {
		f.t.Fatalf("no %q on row %d of the player", chars, y)
	}
	return first, last
}

func runeRows(rows [][]rune) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = string(r)
	}
	return out
}

// mouse is a mouse event in the panels, as the frame manager hands it to
// the frame.
func (f *playerFixture) mouse(e *vtinput.InputEvent) {
	f.t.Helper()
	e.Type = vtinput.MouseEventType
	f.pf.ProcessMouse(e)
	f.settle()
	f.pf.Show(f.scr)
}

func at(x, y int) (int16, int16) { return int16(x), int16(y) } // #nosec G115 -- screen coordinates

func (f *playerFixture) click(x, y int) {
	mx, my := at(x, y)
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: mx, MouseY: my})
	f.mouse(&vtinput.InputEvent{MouseX: mx, MouseY: my})
}

func (f *playerFixture) doubleClick(x, y int) {
	mx, my := at(x, y)
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.DoubleClick, MouseX: mx, MouseY: my})
	f.mouse(&vtinput.InputEvent{MouseX: mx, MouseY: my})
}

func (f *playerFixture) wheel(x, y, direction int) {
	mx, my := at(x, y)
	f.mouse(&vtinput.InputEvent{KeyDown: true, WheelDirection: direction, MouseX: mx, MouseY: my})
}

func TestPlayerButtonsAndVolumeUnderTheMouse(t *testing.T) {
	f := playingFixture(t)

	f.click(f.find("[>|]"))
	if got := f.audio.Path(); got != f.path("b.mp3") {
		t.Errorf("a click on >| played %q, want b.mp3", got)
	}
	f.click(f.find("[ # ]"))
	if got := f.audio.Path(); got != "" {
		t.Errorf("a click on # left %q loaded", got)
	}

	_, y := f.find("[>|]")
	first, last := f.span(y, "█░")
	f.click(last, y)
	if got := f.audio.Volume(); got != 1 {
		t.Errorf("a click on the last cell of the slider set the volume to %v, want 1", got)
	}
	mx, my := at(last, y)
	fx, _ := at(first, y)
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: mx, MouseY: my})
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved, MouseX: fx, MouseY: my})
	f.mouse(&vtinput.InputEvent{MouseX: fx, MouseY: my})
	low := f.audio.Volume()
	if low <= 0 || low > 0.25 {
		t.Errorf("dragged to the first cell of the slider the volume is %v, want a little", low)
	}
	f.wheel(first, y, +1)
	if got := f.audio.Volume(); got < low+0.04 {
		t.Errorf("the wheel up over the slider left the volume at %v, from %v", got, low)
	}
}

func TestPlayerSeeksWithTheMouseOnTheBar(t *testing.T) {
	f := playingFixture(t) // a.mp3, three minutes in the fake
	x0, y := f.find("●")   // at the start of the bar
	_, last := f.span(y, "●─━")
	middle := (x0 + last) / 2

	mx, my := at(x0, y)
	ex, _ := at(middle, y)
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: mx, MouseY: my})
	f.mouse(&vtinput.InputEvent{KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved, MouseX: ex, MouseY: my})
	if got := f.audio.Position(); got != 0 {
		t.Errorf("a drag on the bar moved the track to %s before the button was let go", got)
	}
	if knob, _ := f.find("●"); knob != middle {
		t.Errorf("while dragged the knob is at column %d, want %d under the mouse", knob, middle)
	}
	f.mouse(&vtinput.InputEvent{MouseX: ex, MouseY: my})
	cell := 3 * time.Minute / time.Duration(last-x0)
	if got := f.audio.Position(); got < 90*time.Second-cell || got > 90*time.Second+cell {
		t.Errorf("let go in the middle of the bar, the track is at %s, want about 1:30", got)
	}

	f.click(last, y)
	if got := f.audio.Position(); got != 3*time.Minute {
		t.Errorf("a click on the end of the bar left the track at %s", got)
	}
}

func TestPlayerPlaylistUnderTheMouse(t *testing.T) {
	f := playingFixture(t) // a and b in the playlist, a playing
	f.press(playerKey(vtinput.VK_HOME, vtinput.LeftCtrlPressed))

	_, yb := f.find("  b ")
	xb, _ := f.find("♪ a")
	f.click(xb, yb)
	if got := f.player.GetSelectedName(); got != "b" {
		t.Errorf("a click on the row of b put the cursor on %q", got)
	}
	f.doubleClick(xb, yb)
	if got := f.audio.Path(); got != f.path("b.mp3") {
		t.Errorf("a double click on b played %q", got)
	}

	// A folder F holding a: a click on its ▾ folds it, and a is not shown.
	f.press(playerKey(vtinput.VK_HOME, vtinput.LeftCtrlPressed))
	f.newFolder("F") // a, b, F
	f.press(playerKey(vtinput.VK_UP, vtinput.ShiftPressed))
	f.press(playerKey(vtinput.VK_UP, vtinput.ShiftPressed)) // F, a, b
	f.press(playerKey(vtinput.VK_DOWN, 0))
	f.press(playerKey(vtinput.VK_RIGHT, vtinput.ShiftPressed)) // a into F
	f.click(f.find("▾ F"))
	if x1, _, rows := f.playerRows(); strings.Contains(strings.Join(runeRows(rows), "\n"), "  a") {
		t.Errorf("a click on ▾ did not fold F (player at column %d):\n%s", x1, strings.Join(runeRows(rows), "\n"))
	}
	f.find("▸ F")
}

// The wheel over the player scrolls its playlist, not the file panel behind it.
func TestPlayerWheelScrollsThePlaylist(t *testing.T) {
	names := []string{"a.mp3"}
	for c := 'b'; c <= 'z'; c++ {
		names = append(names, string(c)+".mp3")
	}
	f := newPlayerFixture(t, names...)
	f.player.AddPaths([]string{f.dir}) // a folder of 26 recordings
	f.press(playerKey(vtinput.VK_TAB, 0))
	behind := f.pf.GetActivePanel().GetCursorIndex()

	x, y := f.find("▾ ")
	f.wheel(x, y, -1)
	if _, y1, rows := f.playerRows(); strings.Contains(string(rows[y-y1]), "▾") {
		t.Errorf("the wheel down did not scroll the playlist: its first row still shows the folder")
	}
	if got := f.pf.GetActivePanel().GetCursorIndex(); got != behind {
		t.Errorf("the wheel over the player moved the cursor of the file panel behind it from %d to %d", behind, got)
	}
	f.wheel(x, y, +1)
	if _, y1, rows := f.playerRows(); !strings.Contains(string(rows[y-y1]), "▾") {
		t.Errorf("the wheel up did not scroll the playlist back to the folder")
	}
}
