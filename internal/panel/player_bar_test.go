package panel

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

// The playback bar is the clock at both ends and a bar between them that
// fills as the track plays (#2185): the player had a spectrum and no way to
// see how much of the track was left.
func TestPlaybackBarShowsWhereTheTrackIs(t *testing.T) {
	const w = 40
	const barW = w - len("00:00 ") - len(" 03:00") // 28 cells, the knob one of them
	knobAt := func(cell int) string {
		return strings.Repeat("━", cell) + "●" + strings.Repeat("─", barW-cell-1)
	}
	empty := strings.Repeat("─", barW)
	for _, c := range []struct {
		name     string
		pos, dur time.Duration
		loaded   bool
		want     string
	}{
		{"start", 0, 3 * time.Minute, true, "00:00 " + knobAt(0) + " 03:00"},
		{"half", 90 * time.Second, 3 * time.Minute, true, "01:30 " + knobAt(14) + " 03:00"},
		{"end", 3 * time.Minute, 3 * time.Minute, true, "03:00 " + knobAt(barW-1) + " 03:00"},
		{"past the end", 4 * time.Minute, 3 * time.Minute, true, "04:00 " + knobAt(barW-1) + " 03:00"},
		{"unknown length", 10 * time.Second, 0, true, "00:10 " + empty + " --:--"},
		{"nothing loaded", 0, 0, false, "--:-- " + empty + " --:--"},
	} {
		got := playbackBar(c.pos, c.dur, c.loaded, w)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if width := runewidth.StringWidth(got); width != w {
			t.Errorf("%s: the bar is %d cells wide, want %d", c.name, width, w)
		}
	}
	if got := playbackBar(time.Second, time.Minute, true, 12); strings.Contains(got, "●") {
		t.Errorf("a bar too narrow to draw is %q, want just the clocks", got)
	}
}
