package dialog

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// OpenHistoryOnWheel opens the history list of edit when e is a mouse wheel
// event and the field is a history field that opens its list only on the
// arrow and the wheel (vtui.Edit.HistoryButtonOnly). The wheel works anywhere
// inside the dialog, not only over the field: the dialog that owns the field
// calls this before it hands the event to its controls (f4#2124). It reports
// whether the event was taken.
func OpenHistoryOnWheel(e *vtinput.InputEvent, edit *vtui.Edit) bool {
	if e == nil || e.WheelDirection == 0 || edit == nil {
		return false
	}
	if !edit.HistoryButtonOnly || !edit.ShowHistoryButton || edit.IsDisabled() {
		return false
	}
	edit.OpenHistory()
	return true
}

// HistoryWheelWindow is a dialog window whose Edit opens its history list on
// the mouse wheel wherever the pointer is inside the dialog (f4#2124).
type HistoryWheelWindow struct {
	*vtui.Window

	// Edit is the history field; nil leaves the wheel to the controls.
	Edit *vtui.Edit
}

// ProcessMouse takes the wheel for the history field and passes everything
// else to the window.
func (w *HistoryWheelWindow) ProcessMouse(e *vtinput.InputEvent) bool {
	if OpenHistoryOnWheel(e, w.Edit) {
		return true
	}
	return w.Window.ProcessMouse(e)
}
