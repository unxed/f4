package settings

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Unavailable text values remain inspectable without permitting draft edits.
type settingsEdit struct {
	*vtui.Edit
	readOnly bool
}

func (e *settingsEdit) SetDisabled(disabled bool) {
	e.readOnly = disabled
	e.Edit.SetDisabled(false)
}

func (e *settingsEdit) ProcessKey(event *vtinput.InputEvent) bool {
	if !e.readOnly {
		return e.Edit.ProcessKey(event)
	}
	if event.Type == vtinput.PasteEventType || event.TextInput {
		return true
	}
	if !event.KeyDown {
		return false
	}
	ctrl := event.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := event.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := event.ControlKeyState&vtinput.ShiftPressed != 0
	if !alt {
		switch event.VirtualKeyCode {
		case vtinput.VK_LEFT, vtinput.VK_RIGHT, vtinput.VK_HOME, vtinput.VK_END:
			return e.Edit.ProcessKey(event)
		case vtinput.VK_A, vtinput.VK_C, vtinput.VK_INSERT:
			if ctrl && !shift {
				return e.Edit.ProcessKey(event)
			}
		}
	}
	switch event.VirtualKeyCode {
	case vtinput.VK_TAB, vtinput.VK_ESCAPE, vtinput.VK_RETURN, vtinput.VK_UP, vtinput.VK_DOWN:
		return false
	}
	return true
}

func (e *settingsEdit) ProcessMouse(event *vtinput.InputEvent) bool {
	if e.readOnly && (event.ButtonState != 0 && event.ButtonState != vtinput.FromLeft1stButtonPressed) {
		return true
	}
	return e.Edit.ProcessMouse(event)
}

func (e *settingsEdit) Show(scr *vtui.ScreenBuf) {
	if !e.readOnly {
		e.Edit.Show(scr)
		return
	}
	selected, unchanged := e.ColorSelectedIdx, e.ColorUnchangedIdx
	// Selection is useful only in the active field; otherwise show its value
	// with the ordinary input background and dimmed text.
	e.ColorUnchangedIdx = selected
	if !e.IsFocused() {
		e.ColorSelectedIdx = e.ColorTextIdx
		e.ColorUnchangedIdx = e.ColorTextIdx
	}
	e.Edit.Show(scr)
	e.ColorSelectedIdx, e.ColorUnchangedIdx = selected, unchanged
	normal := vtui.Palette[e.ColorTextIdx]
	for x := e.X1; x <= e.X2; x++ {
		cell := scr.GetCell(x, e.Y1)
		if cell.Attributes == normal {
			cell.Attributes = vtui.DimColor(normal)
			scr.Write(x, e.Y1, []vtui.CharInfo{cell})
		}
	}
}
