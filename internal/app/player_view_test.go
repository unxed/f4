package app

import (
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// screenRows is what the player shows, row by row, on the fixture's screen.
func (f *playerFixture) screenRows() []string {
	f.pf.Show(f.scr)
	x1, y1, x2, y2 := f.player.GetPosition()
	var rows []string
	for y := y1; y <= y2; y++ {
		var sb strings.Builder
		for x := x1; x <= x2; x++ {
			ch := rune(f.scr.GetCell(x, y).Char) // #nosec G115 -- a cell holds one character
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		rows = append(rows, sb.String())
	}
	return rows
}

// The player drew a spectrum and nothing that showed how far the track had
// got (#2185). The bar between the two clocks does. Both are settings, on by
// default: with the bar off the clock shares a row with the facts about the
// track, as it did, and the spectrum takes two rows, as it did.
func TestPlayerDrawsThePlaybackBar(t *testing.T) {
	oldBar, oldSpectrum := config.App.PlayerBar, config.App.PlayerSpectrum
	t.Cleanup(func() { config.App.PlayerBar, config.App.PlayerSpectrum = oldBar, oldSpectrum })
	config.App.PlayerBar, config.App.PlayerSpectrum = true, false

	f := newPlayerFixture(t, "a.mp3")
	f.press(playerKey(vtinput.VK_RETURN, 0))
	f.audio.Seek(90 * time.Second) // half of the fake's three minutes

	rows := f.screenRows()
	bar := ""
	for _, row := range rows {
		if strings.Contains(row, "01:30") {
			bar = row
		}
	}
	if !strings.Contains(bar, "03:00") || !strings.Contains(bar, "●") {
		t.Fatalf("no playback bar with 01:30 and 03:00 in the player:\n%s", strings.Join(rows, "\n"))
	}
	filled, rest := strings.Count(bar, "━"), strings.Count(bar, "─")
	if filled == 0 || rest == 0 || filled-rest > 1 || rest-filled > 1 {
		t.Errorf("at half the track the bar is %q: %d cells played, %d to go", bar, filled, rest)
	}

	separator := func(rows []string) int {
		for i, row := range rows {
			if strings.HasPrefix(row, "├") {
				return i
			}
		}
		t.Fatalf("no separator above the playlist:\n%s", strings.Join(rows, "\n"))
		return -1
	}
	withBar := separator(rows)
	config.App.PlayerSpectrum = true
	if got := separator(f.screenRows()); got != withBar+2 {
		t.Errorf("with the spectrum on the playlist starts at row %d, want %d: two rows of spectrum", got, withBar+2)
	}

	config.App.PlayerBar = false
	rows = f.screenRows()
	if got := separator(rows); got != withBar+1 {
		t.Errorf("with the bar off and the spectrum on the playlist starts at row %d, want %d", got, withBar+1)
	}
	if strings.Contains(strings.Join(rows, "\n"), "●") || !strings.Contains(strings.Join(rows, "\n"), "01:30 / 03:00") {
		t.Errorf("with the bar off the player shows no clock alone:\n%s", strings.Join(rows, "\n"))
	}
}

// The letters of the player were nowhere to be seen (#2185). They stay out of
// the way until H shows them: the bottom border says so, H puts the letters
// on the buttons and the other keys in the border, H again takes them off.
func TestPlayerShowsItsKeysOnH(t *testing.T) {
	oldHints, oldKeys := config.App.PlayerKeyHints, config.App.PlayerKeys
	t.Cleanup(func() { config.App.PlayerKeyHints, config.App.PlayerKeys = oldHints, oldKeys })
	config.App.PlayerKeyHints, config.App.PlayerKeys = false, config.PlayerKeysBoth

	f := newPlayerFixture(t)
	f.press(playerKey(vtinput.VK_TAB, 0))
	shows := func(text string) bool { return strings.Contains(strings.Join(f.screenRows(), "\n"), text) }
	if !shows(i18n.Msg("Player.KeysHint")) || !shows("[|<]") || shows("[Z |<]") {
		t.Fatalf("with the keys hidden the player shows:\n%s", strings.Join(f.screenRows(), "\n"))
	}

	f.press(playerChar(0, 'р')) // H with the Russian layout on
	if !config.App.PlayerKeyHints {
		t.Fatal("р (H) did not show the keys")
	}
	for _, want := range []string{"[Z |<]", "[X >]", "[C ||]", "[V #]", "[B >|]", "K play", "H hide"} {
		if !shows(want) {
			t.Errorf("with the keys shown the player has no %q:\n%s", want, strings.Join(f.screenRows(), "\n"))
		}
	}

	f.press(playerChar(vtinput.VK_H, 'h'))
	if config.App.PlayerKeyHints || shows("[Z |<]") {
		t.Errorf("H again did not hide the keys:\n%s", strings.Join(f.screenRows(), "\n"))
	}
	if !f.pf.CmdLine.IsEmpty() {
		t.Errorf("H went to the command line too: %q", f.pf.CmdLine.Edit.GetText())
	}
}

// The player had no help of its own: F1 in it opened the panels' (#2185).
// It opens the player's topic now, and F1 in the file panel still the
// panels'. F1 is the frame manager's own key, which a test cannot press;
// App.Help asks the frame for its topic the same way.
func TestPlayerF1OpensItsHelp(t *testing.T) {
	previous := vtui.GlobalHelpEngine
	t.Cleanup(func() { vtui.GlobalHelpEngine = previous })
	f := newPlayerFixture(t)
	f.pf.SetHelp("Panels") // as NewPanelsFrame does; the mock frame is built without it
	InitHelpSystem()

	topic := func() string {
		t.Helper()
		help, ok := vtui.FrameManager.GetTopFrame().(*vtui.HelpView)
		if !ok {
			t.Fatalf("F1 opened %T, want the help", vtui.FrameManager.GetTopFrame())
		}
		name := help.CurrentTopic().Name
		vtui.FrameManager.Pop()
		f.pf.Show(f.scr)
		return name
	}
	RunAction("App.Help")
	if got := topic(); got != "Panels" {
		t.Errorf("F1 in the file panel opened %q, want the panels' help", got)
	}
	f.press(playerKey(vtinput.VK_TAB, 0))
	RunAction("App.Help")
	if got := topic(); got != "Player" {
		t.Errorf("F1 in the player opened %q, want Player", got)
	}
}

// The key bar over a focused player named the file panel's keys, which do
// nothing there. It names F7 and F8, which make a playlist folder and remove
// an item, and leaves F3 to F6 blank.
func TestPlayerKeyBar(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3", "b.mp3")
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_F5, 0))
	f.press(playerKey(vtinput.VK_TAB, 0))

	labels := f.pf.GetKeyLabels().Normal
	if labels[6] != i18n.Msg("KeyBar.Player.F7") || labels[7] != i18n.Msg("KeyBar.Player.F8") {
		t.Errorf("F7 and F8 over the player read %q and %q", labels[6], labels[7])
	}
	for i := 2; i <= 5; i++ {
		if labels[i] != "" {
			t.Errorf("F%d over the player reads %q, a key that does nothing there", i+1, labels[i])
		}
	}

	f.press(playerKey(vtinput.VK_F8, 0))
	if got := f.player.GetSelectedName(); got != "b" {
		t.Errorf("after F8 on a the cursor is on %q, want b, the one left", got)
	}

	f.press(playerKey(vtinput.VK_TAB, 0))
	if got := f.pf.GetKeyLabels().Normal[4]; got == "" {
		t.Error("back in the file panel F5 has no caption")
	}
}
