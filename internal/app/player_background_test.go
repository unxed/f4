package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Closing the player stopped the music: playback belonged to the panel
// (#2185). Closing it while something plays asks now whether to go on in the
// background, Esc keeps it open, a mark in the top right corner shows what
// goes on, a click on the mark opens the player again on the track, and the
// Music commands work with the player closed.

// toggle is Ctrl+Shift+M, which opens and closes the player.
func (f *playerFixture) toggle() {
	f.t.Helper()
	f.press(playerKey(vtinput.VK_M, vtinput.LeftCtrlPressed|vtinput.ShiftPressed))
}

// playerShown is whether the panels have a player.
func (f *playerFixture) playerShown() bool {
	for _, a := range f.pf.AltPanels {
		if _, ok := a.(*panel.PlayerPanel); ok {
			return true
		}
	}
	return false
}

// question is the dialog the player put up, or a failure.
func (f *playerFixture) question() vtui.Frame {
	f.t.Helper()
	dlg := vtui.FrameManager.GetTopFrame()
	if dlg == f.pf {
		f.t.Fatal("closing the player while it plays asked nothing")
	}
	return dlg
}

// indicator is the top right corner of the screen as drawn after the frame.
func (f *playerFixture) indicator() string {
	f.pf.Show(f.scr)
	panel.DrawPlayerIndicator(f.scr)
	var sb strings.Builder
	for x := f.scr.Width() - 5; x < f.scr.Width(); x++ {
		sb.WriteRune(rune(f.scr.GetCell(x, vtui.FrameManager.WorkspaceTopInset()).Char)) // #nosec G115 -- a cell holds one character
	}
	return sb.String()
}

func playingFixture(t *testing.T) *playerFixture {
	f := newPlayerFixture(t, "a.mp3", "b.mp3")
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_F5, 0)) // a and b into the playlist
	f.press(playerKey(vtinput.VK_TAB, 0))
	f.press(playerKey(vtinput.VK_RETURN, 0)) // a plays
	if f.audio.Path() != f.path("a.mp3") {
		t.Fatalf("Enter in the playlist loaded %q", f.audio.Path())
	}
	return f
}

func TestPlayerGoesOnPlayingInTheBackground(t *testing.T) {
	f := playingFixture(t)

	f.toggle()
	f.pressIn(f.question(), playerKey(vtinput.VK_ESCAPE, 0))
	if !f.playerShown() || !f.audio.IsPlaying() {
		t.Fatalf("Esc to the question: player shown=%v, playing=%v; want both", f.playerShown(), f.audio.IsPlaying())
	}
	if strings.Contains(f.indicator(), "♪") {
		t.Error("the mark shows while the player is on the screen")
	}

	f.toggle()
	f.pressIn(f.question(), playerKey(vtinput.VK_RETURN, 0)) // In the background
	if f.playerShown() {
		t.Fatal("In the background left the player open")
	}
	if f.audio.Path() != f.path("a.mp3") || !f.audio.IsPlaying() {
		t.Fatalf("In the background: engine has %q, playing=%v", f.audio.Path(), f.audio.IsPlaying())
	}
	if got := f.indicator(); !strings.Contains(got, "♪") {
		t.Errorf("with the music in the background the top right corner is %q, want the ♪ mark", got)
	}

	// The Music commands, with no player.
	if !RunAction("Music.PlayPause") || f.audio.IsPlaying() {
		t.Error("Music: play or pause did not pause the music in the background")
	}
	if got := f.indicator(); !strings.Contains(got, "‖") {
		t.Errorf("paused, the corner is %q, want ‖", got)
	}
	if !RunAction("Music.Next") || f.audio.Path() != f.path("b.mp3") {
		t.Errorf("Music: next track loaded %q, want b.mp3", f.audio.Path())
	}

	// A click on the mark opens the player on what plays.
	x := f.scr.Width() - 3
	click := &vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseX: int16(x), MouseY: int16(vtui.FrameManager.WorkspaceTopInset())} // #nosec G115 -- a screen column
	f.press(click)
	if !f.playerShown() {
		t.Fatal("a click on the mark did not open the player")
	}
	var player *panel.PlayerPanel
	for _, a := range f.pf.AltPanels {
		if p, ok := a.(*panel.PlayerPanel); ok {
			player = p
		}
	}
	if got := player.GetSelectedName(); got != "b" {
		t.Errorf("the player opened over the music has the cursor on %q, want the track that plays, b", got)
	}
}

func TestPlayerStopsWhenAskedOnClose(t *testing.T) {
	f := playingFixture(t)

	f.toggle()
	dlg := f.question()
	f.pressIn(dlg, playerKey(vtinput.VK_TAB, 0))
	f.pressIn(dlg, playerKey(vtinput.VK_RETURN, 0)) // Stop
	if f.playerShown() || f.audio.Path() != "" {
		t.Errorf("Stop: player shown=%v, engine has %q; want it closed and silent", f.playerShown(), f.audio.Path())
	}
	if panel.MusicLoaded() {
		t.Error("after Stop the player still has music")
	}
	if got := f.indicator(); strings.ContainsAny(got, "♪‖") {
		t.Errorf("after Stop the corner is %q, want no mark", got)
	}
}

// The question on closing came every time the player was closed while it
// played (#2185). I in the player, YouTube's key of its miniplayer, [-] in its
// top right corner and the palette close it without asking and leave the
// music playing. The corner has the button only while something is loaded.
func TestPlayerGoesToTheBackgroundWithoutAsking(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(t *testing.T, f *playerFixture)
	}{
		{"ш, I with the Russian layout on", func(t *testing.T, f *playerFixture) { f.press(playerChar(0, 'ш')) }},
		{"a click on [-]", func(t *testing.T, f *playerFixture) {
			x, y := f.find("[-]")
			if y != vtui.FrameManager.WorkspaceTopInset() {
				// That row is where a click opens the main menu.
				t.Fatalf("[-] is on row %d, not on the top row of the panels, %d", y, vtui.FrameManager.WorkspaceTopInset())
			}
			f.click(x+1, y)
		}},
		{"the palette", func(t *testing.T, f *playerFixture) {
			if !RunAction("Panel.Player.ToBackground") {
				t.Error("Player: close, keep playing did nothing")
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := playingFixture(t)
			c.do(t, f)
			if top := vtui.FrameManager.GetTopFrame(); top != f.pf {
				t.Fatalf("closing the player for the background put up %T", top)
			}
			if f.playerShown() {
				t.Fatal("the player is still open")
			}
			if f.audio.Path() != f.path("a.mp3") || !f.audio.IsPlaying() {
				t.Errorf("in the background the engine has %q, playing=%v; want a.mp3 playing", f.audio.Path(), f.audio.IsPlaying())
			}
			if got := f.indicator(); !strings.Contains(got, "♪") {
				t.Errorf("the top right corner is %q, want the ♪ mark", got)
			}
			if strings.Contains(strings.Join(f.screenRows(), "\n"), "[-]") {
				t.Error("the button stayed on the screen")
			}
		})
	}

	f := newPlayerFixture(t)
	f.press(playerKey(vtinput.VK_TAB, 0))
	if rows := f.screenRows(); strings.Contains(strings.Join(rows, "\n"), "[-]") {
		t.Errorf("with nothing loaded the player has the button:\n%s", strings.Join(rows, "\n"))
	}
}

// With the player on the right its button is where the ♪ comes then: a click
// there sends the player to the background, and a click on the same cell
// brings it back.
func TestPlayerButtonAndTheMarkShareTheCorner(t *testing.T) {
	f := playingFixture(t) // the player on the left, with the focus
	f.press(playerChar(vtinput.VK_I, 'i'))
	f.toggle() // from the left panel: the player opens on the right
	player, ok := f.pf.AltPanels[1].(*panel.PlayerPanel)
	if !ok {
		t.Fatalf("Ctrl+Shift+M from the left panel opened %T on the right", f.pf.AltPanels[1])
	}
	f.player = player
	x, y := f.find("[-]")
	if mark := f.scr.Width() - 3; x+1 != mark {
		t.Fatalf("the _ of the player on the right is in column %d, the ♪ in %d", x+1, mark)
	}

	f.click(x+1, y)
	if f.playerShown() {
		t.Fatal("a click on _ left the player open")
	}
	if got := f.indicator(); !strings.Contains(got, "♪") {
		t.Fatalf("the top right corner is %q, want the ♪ mark", got)
	}
	mx, my := at(x+1, y)
	f.press(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed, MouseX: mx, MouseY: my})
	if !f.playerShown() {
		t.Error("a click on the ♪ in the same cell did not bring the player back")
	}
}

// With nothing playing there is nothing to ask.
func TestPlayerClosesWithoutAQuestionWhenSilent(t *testing.T) {
	f := newPlayerFixture(t)
	f.toggle()
	if top := vtui.FrameManager.GetTopFrame(); top != f.pf || f.playerShown() {
		t.Errorf("closing a silent player: top frame %T, player shown=%v", top, f.playerShown())
	}
}
