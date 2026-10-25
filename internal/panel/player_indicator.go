package panel

import (
	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The mark in the top right corner of the screen while music plays and no
// player is on it: ♪ playing, ‖ paused (#2185). The top left corner has the
// mark of a macro recording. A click on the mark opens the player.

// playerIndicator is where the mark was drawn last, for the click.
var playerIndicator struct {
	x, y, width int
	shown       bool
}

// DrawPlayerIndicator draws the mark over whatever the screen shows; the
// application calls it after each frame.
func DrawPlayerIndicator(scr *vtui.ScreenBuf) {
	playerIndicator.shown = false
	s := activePlayerSession
	if s == nil || vtui.FrameManager == nil || !s.Engine.IsLoaded() || playerOnScreen() {
		return
	}
	mark := " ♪ "
	if !s.Engine.IsPlaying() {
		mark = " ‖ "
	}
	width := runewidth.StringWidth(mark)
	// One cell in from the edge, so the corner of a frame stays a corner.
	x, y := scr.Width()-width-1, vtui.FrameManager.WorkspaceTopInset()
	if x < 0 {
		return
	}
	scr.Write(x, y, vtui.StringToCharInfo(mark, vtui.Palette[theme.ColPanelSelectedTitle]))
	playerIndicator.x, playerIndicator.y, playerIndicator.width, playerIndicator.shown = x, y, width, true
}

// playerOnScreen is whether the panels on the screen show a player: no mark
// then, the player says what plays.
func playerOnScreen() bool {
	pf, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame)
	if !ok || !pf.ShowPanels {
		return false
	}
	for _, a := range pf.AltPanels {
		if _, ok := a.(*PlayerPanel); ok {
			return true
		}
	}
	return false
}

// PlayerIndicatorClick opens the player in the panels when e is a click on
// the mark, and says whether it was. Over an editor or the terminal the
// panels are not there to open it in, and the click goes on.
func PlayerIndicatorClick(e *vtinput.InputEvent) bool {
	if !playerIndicator.shown || e == nil || e.Type != vtinput.MouseEventType || !e.KeyDown ||
		e.MouseEventFlags&vtinput.MouseMoved != 0 || e.ButtonState != vtinput.FromLeft1stButtonPressed {
		return false
	}
	x, y := int(e.MouseX), int(e.MouseY)
	if y != playerIndicator.y || x < playerIndicator.x || x >= playerIndicator.x+playerIndicator.width {
		return false
	}
	pf, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame)
	if !ok || !pf.ShowPanels {
		return false
	}
	pf.ToggleAltPanel("player", func(src *FileSystemPanel) AltPanel { return NewPlayerPanel(src) })
	return true
}
