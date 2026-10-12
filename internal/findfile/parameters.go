package findfile

import (
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// ParametersWindow keeps the search form aligned as its window changes size.
type ParametersWindow struct {
	*vtui.Window
	controls   []requestControl
	buttons    *vtui.HBoxLayout
	navigation []vtui.UIElement
}

func NewParametersWindow() *ParametersWindow {
	w := &ParametersWindow{Window: vtui.NewCenteredDialog(78, 20, i18n.Msg("FindFile.Title"))}
	w.ShowClose, w.ShowZoom = true, true
	w.MinW = 78
	return w
}

// SetNavigation supplies the primary fields in their vertical focus order.
func (w *ParametersWindow) SetNavigation(mask, text *vtui.Edit, find *vtui.Button) {
	w.navigation = []vtui.UIElement{mask, text, find}
}

func (w *ParametersWindow) ProcessKey(event *vtinput.InputEvent) bool {
	modifiers := vtinput.ShiftPressed | vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed | vtinput.LeftAltPressed | vtinput.RightAltPressed
	if event.ControlKeyState&modifiers == 0 && (event.VirtualKeyCode == vtinput.VK_UP || event.VirtualKeyCode == vtinput.VK_DOWN) {
		for i, element := range w.navigation {
			if element != w.GetFocusedItem() {
				continue
			}
			if event.KeyDown {
				direction := 1
				if event.VirtualKeyCode == vtinput.VK_UP {
					direction = -1
				}
				w.SetFocusedItem(w.navigation[(i+direction+len(w.navigation))%len(w.navigation)])
			}
			return true
		}
	}
	return w.Window.ProcessKey(event)
}

// SetLayout captures the form's rows after its initial language-aware layout.
func (w *ParametersWindow) SetLayout(buttons *vtui.HBoxLayout) {
	w.buttons = buttons
	w.controls = nil
	bottom := 2
	for _, element := range w.GetChildren() {
		if _, button := element.(*vtui.Button); button {
			continue
		}
		x1, y1, x2, y2 := element.GetPosition()
		w.controls = append(w.controls, requestControl{
			element: element,
			x1:      x1 - w.X1, y1: y1 - w.Y1,
			x2: x2 - w.X1, y2: y2 - w.Y1,
		})
		bottom = max(bottom, y2-w.Y1)
	}
	w.MinH = bottom + 5
	w.ChangeSize(w.X2-w.X1+1, w.MinH)
	w.Center(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	w.layout()
}

func (w *ParametersWindow) layout() {
	for _, control := range w.controls {
		right := w.X1 + control.x2
		switch control.element.(type) {
		case *vtui.Edit, *vtui.Separator:
			right = w.X2 - 2
		}
		control.element.SetPosition(w.X1+control.x1, w.Y1+control.y1, right, w.Y1+control.y2)
	}
	if w.buttons != nil {
		w.buttons.SetPosition(w.X1+2, w.Y2-2, w.X2-2, w.Y2-2)
		w.buttons.Apply()
	}
}

func (w *ParametersWindow) Show(scr *vtui.ScreenBuf) {
	w.layout()
	w.Window.Show(scr)
}

func (w *ParametersWindow) ResizeConsole(width, height int) {
	w.ChangeSize(min(w.X2-w.X1+1, width), min(w.Y2-w.Y1+1, height-1))
	w.BaseWindow.ResizeConsole(width, height-1)
	w.layout()
}

// Expand hands the same window and its submitted controls to the search.
func (w *ParametersWindow) Expand(controls []vtui.UIElement, v vfs.VFS, root, mask, text string, options Options, host Host) *SearchResultsWindow {
	w.layout()
	vtui.FrameManager.RemoveFrame(w)
	return Expand(w.Window, controls, v, root, mask, text, options, host)
}
