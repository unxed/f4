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

// With nothing playing there is nothing to ask.
func TestPlayerClosesWithoutAQuestionWhenSilent(t *testing.T) {
	f := newPlayerFixture(t)
	f.toggle()
	if top := vtui.FrameManager.GetTopFrame(); top != f.pf || f.playerShown() {
		t.Errorf("closing a silent player: top frame %T, player shown=%v", top, f.playerShown())
	}
}
