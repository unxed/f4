package panel

import (
	"time"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The player under the mouse (#2185). A click presses a button, sets the
// volume where it lands on the slider and moves the track where it lands on
// the playback bar; the slider and the bar follow a drag. A click on a
// playlist row puts the cursor there, a click on ▸ or ▾ folds or unfolds the
// folder, and a double click plays the track or folds the folder. The wheel
// scrolls the playlist, and over the slider turns the volume.

// playerLayout is where Show put what the mouse can hit.
type playerLayout struct {
	barY       int                     // the row of the playback bar, or of the clock with it off
	barX, barW int                     // the cells of the bar; barW is 0 with no bar to click
	buttonsY   int                     // the row of the buttons
	buttons    [playerBtnVolume][2]int // the first and the last cell of each button
	volX, volW int                     // the cells of the volume slider; volW is 0 with none
	listY      int                     // the first row of the playlist
	listRows   int                     // how many rows of it there are
}

// playerDrag is what the left button drags since it went down on the player.
type playerDrag int

const (
	playerDragNone playerDrag = iota
	playerDragVolume
	playerDragBar
)

// playerWheelRows is how far a notch of the wheel scrolls the playlist.
const playerWheelRows = 3

// ProcessMouse takes every mouse event on the player: the panels give it
// the press, and then the moves and the release of the same gesture. A
// wheel it did not take would scroll the file panel behind it.
func (pp *PlayerPanel) ProcessMouse(e *vtinput.InputEvent) bool {
	if pp.playerSession == nil || e == nil || e.Type != vtinput.MouseEventType {
		return false
	}
	mx, my := int(e.MouseX), int(e.MouseY)
	switch {
	case e.WheelDirection != 0:
		pp.mouseWheel(e.WheelDirection, mx, my)
	case e.MouseEventFlags&vtinput.MouseMoved != 0:
		// Some backends report the moves of a drag with no button held.
		pp.dragTo(mx)
	case e.ButtonState == 0 || !e.KeyDown:
		pp.endDrag()
	case e.ButtonState&vtinput.FromLeft1stButtonPressed != 0:
		pp.mousePress(mx, my, e.MouseEventFlags&vtinput.DoubleClick != 0)
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
	return true
}

func (pp *PlayerPanel) mousePress(mx, my int, double bool) {
	l := &pp.layout
	switch {
	case my == l.barY:
		pp.cursor = playerCursorBar
		if l.barW > 0 && mx >= l.barX && mx < l.barX+l.barW && pp.Engine.IsLoaded() {
			pp.drag = playerDragBar
			pp.dragSeek = pp.barFraction(mx)
		}
	case my == l.buttonsY:
		pp.cursor = playerCursorButtons
		for i, b := range l.buttons {
			if mx >= b[0] && mx <= b[1] {
				pp.button = i
				pp.pressButton(i)
				return
			}
		}
		if l.volW > 0 && mx >= l.volX && mx < l.volX+l.volW {
			pp.button = playerBtnVolume
			pp.drag = playerDragVolume
			pp.volumeAt(mx)
		}
	case my >= l.listY && my < l.listY+l.listRows:
		pp.rebuildRows()
		ri := pp.top + my - l.listY
		if ri >= len(pp.rows) {
			return
		}
		pp.cursor = ri
		r := pp.rows[ri]
		mark := pp.X1 + 1 + 2*r.depth // the ▸ or ▾ of a folder
		switch {
		case r.item.Folder && (double || mx == mark):
			r.item.Expanded = !r.item.Expanded
			pp.savePlaylist()
		case double:
			pp.playItem(r.item)
		}
	}
}

// dragTo follows a drag: the volume at once, the knob of the bar on screen.
func (pp *PlayerPanel) dragTo(mx int) {
	switch pp.drag {
	case playerDragVolume:
		pp.volumeAt(mx)
	case playerDragBar:
		pp.dragSeek = pp.barFraction(mx)
	}
}

// endDrag is the button let go: the track goes where the knob of the bar
// was dragged. Seeking on every move would load the track again each time.
func (pp *PlayerPanel) endDrag() {
	if pp.drag == playerDragBar && pp.Engine.IsLoaded() {
		target := time.Duration(pp.dragSeek * float64(pp.Engine.Duration()))
		pp.SeekBy(target - pp.Engine.Position())
	}
	pp.drag = playerDragNone
}

// volumeAt sets the volume to how far along the slider mx is, the last cell
// being all of it.
func (pp *PlayerPanel) volumeAt(mx int) {
	l := &pp.layout
	cells := max(1, min(l.volW, mx-l.volX+1))
	pp.Engine.SetVolume(float64(cells) / float64(l.volW))
}

// barFraction is how far along the playback bar mx is, 0 to 1, as
// playbackBar puts the knob.
func (pp *PlayerPanel) barFraction(mx int) float64 {
	l := &pp.layout
	if l.barW < 2 {
		return 0
	}
	return max(0, min(1, float64(mx-l.barX)/float64(l.barW-1)))
}

// mouseWheel turns the volume over the slider and scrolls the playlist
// anywhere else; a positive direction is up.
func (pp *PlayerPanel) mouseWheel(direction, mx, my int) {
	l := &pp.layout
	if my == l.buttonsY && l.volW > 0 && mx >= l.volX && mx < l.volX+l.volW {
		if direction > 0 {
			pp.adjustVolume(+0.05)
		} else {
			pp.adjustVolume(-0.05)
		}
		return
	}
	if direction > 0 {
		pp.top = max(0, pp.top-playerWheelRows)
	} else {
		pp.top += playerWheelRows // Show keeps the last row at the bottom
	}
}
