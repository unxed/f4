package cmdline

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// CompletionMenu applies command-line shortcuts to the shared suggestion menu.
type CompletionMenu struct {
	*vtui.AutoCompleteMenu
}

func NewCompletionMenu(edit *vtui.Edit) *CompletionMenu {
	return &CompletionMenu{AutoCompleteMenu: vtui.NewAutoCompleteMenu(edit)}
}

func (menu *CompletionMenu) ProcessKey(event *vtinput.InputEvent) bool {
	ctrl := event.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	if event.Type == vtinput.KeyEventType && event.VirtualKeyCode == vtinput.VK_RETURN && ctrl {
		if !event.KeyDown {
			return true
		}
		// Tab accepts the chosen suggestion (or the first match) without
		// queuing Enter for the command executor. Keep the original event intact.
		accept := *event
		accept.VirtualKeyCode = vtinput.VK_TAB
		accept.Char = 0
		accept.ControlKeyState = 0
		return menu.AutoCompleteMenu.ProcessKey(&accept)
	}
	return menu.AutoCompleteMenu.ProcessKey(event)
}

// AsCompletionMenu exposes the shared popup data for console overlay rendering.
func AsCompletionMenu(frame vtui.Frame) (*vtui.AutoCompleteMenu, bool) {
	switch menu := frame.(type) {
	case *CompletionMenu:
		return menu.AutoCompleteMenu, true
	case *vtui.AutoCompleteMenu:
		return menu, true
	default:
		return nil, false
	}
}
