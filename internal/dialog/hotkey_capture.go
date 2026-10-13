package dialog

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type HotkeyAssignFrame struct {
	*vtui.Window
	hm         *keymap.HotkeyManager
	actionName string
	area       string
	onComplete func()
	shiftHeld  bool
}

func NewHotkeyAssignFrame(hm *keymap.HotkeyManager, actionName, area string, onComplete func()) *HotkeyAssignFrame {
	width, height := 42, 9
	base := vtui.NewCenteredDialog(width, height, i18n.Msg("Hotkeys.AssignTitle"))
	f := &HotkeyAssignFrame{
		Window:     base,
		hm:         hm,
		actionName: actionName,
		area:       area,
		onComplete: onComplete,
	}

	lblAction := vtui.NewText(0, 0, fmt.Sprintf(i18n.Msg("Hotkeys.AssignAction"), actionName), vtui.Palette[vtui.ColDialogText])
	lblArea := vtui.NewText(0, 0, fmt.Sprintf(i18n.Msg("Hotkeys.AssignArea"), area), vtui.Palette[vtui.ColDialogText])
	currentText := fmt.Sprintf(i18n.Msg("Hotkeys.AssignCurrent"), i18n.Msg("Hotkeys.AssignNone"))
	if _, currentKey := keymap.ConfiguredHotkeyBinding(hm, strings.SplitN(actionName, ":", 2)[0]); currentKey != "" {
		currentText = fmt.Sprintf(i18n.Msg("Hotkeys.AssignCurrent"), keymap.FormatKeyForUI(currentKey))
	}
	lblCurrent := vtui.NewText(0, 0, currentText, vtui.Palette[vtui.ColDialogText])
	prompt := vtui.NewText(0, 0, i18n.Msg("Hotkeys.AssignPrompt"), vtui.Palette[vtui.ColDialogText])
	cancelPrompt := vtui.NewText(0, 0, i18n.Msg("Hotkeys.AssignCancel"), vtui.Palette[vtui.ColDialogText])

	f.AddItem(lblAction)
	f.AddItem(lblArea)
	f.AddItem(lblCurrent)
	f.AddItem(prompt)
	f.AddItem(cancelPrompt)

	vbox := vtui.NewVBoxLayout(f.X1+2, f.Y1+2, width-4, height-4)
	vbox.Add(lblAction, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(lblArea, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(lblCurrent, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(prompt, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(cancelPrompt, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	return f
}

func (f *HotkeyAssignFrame) ProcessKey(e *vtinput.InputEvent) bool {
	// Text commits cannot name a physical shortcut; keep waiting for a chord.
	if e.TextInput {
		return true
	}
	if e.Type == vtinput.FocusEventType {
		if !e.SetFocus {
			f.shiftHeld = false
		}
		return f.Window.ProcessKey(e)
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT:
		// Some GUI hosts omit ShiftPressed from the following key event. Keep
		// the modifier state across the two events while the assignment dialog
		// owns the keyboard (the same rule used by the grabber).
		f.shiftHeld = e.KeyDown
		return true
	}

	if !e.KeyDown {
		return false
	}

	if e.VirtualKeyCode == vtinput.VK_ESCAPE {
		f.Close()
		vtui.FrameManager.Redraw()
		return true
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return true
	}

	keyEvent := *e
	if f.shiftHeld {
		keyEvent.ControlKeyState |= vtinput.ShiftPressed
	}
	keyStr := keymap.EventToHotkeyString(&keyEvent)

	if f.hm != nil {
		f.hm.Bind(f.area, keyStr, f.actionName)
	}

	f.Close()
	if f.onComplete != nil {
		f.onComplete()
	}
	vtui.FrameManager.Redraw()
	return true
}

func (f *HotkeyAssignFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	return true
}
func (f *HotkeyAssignFrame) GetType() vtui.FrameType { return vtui.TypeDialog }
func (f *HotkeyAssignFrame) IsModal() bool           { return true }
