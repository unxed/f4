package dialog

import (
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// MaskDialog keeps the mask field and button row aligned with its frame,
// both when the screen changes size and when the corner is dragged.
type MaskDialog struct {
	*vtui.Window
	label   *vtui.Text
	edit    *vtui.Edit
	rule    *vtui.Separator
	buttons *vtui.HBoxLayout

	// WheelHistory makes the mouse wheel open the history list of the field
	// anywhere in the dialog (f4#2124); it needs edit.HistoryButtonOnly.
	WheelHistory bool
}

const (
	maskDialogMinWidth = 30
	maskDialogHeight   = 6
)

func compactInputWidth(screenWidth int) int {
	return min(screenWidth, max(maskDialogMinWidth, screenWidth/2))
}

func (d *MaskDialog) layoutControls() {
	d.label.SetPosition(d.X1+2, d.Y1+1, d.X2-2, d.Y1+1)
	d.edit.SetPosition(d.X1+2, d.Y1+2, d.X2-2, d.Y1+2)
	d.rule.SetPosition(d.X1, d.Y2-2, d.X2, d.Y2-2)
	d.buttons.SetPosition(d.X1+2, d.Y2-1, d.X2-2, d.Y2-1)
}

func (d *MaskDialog) ChangeSize(width, _ int) {
	d.Window.ChangeSize(width, maskDialogHeight)
	d.layoutControls()
	vtui.DebugLog("[FIX] Compact input dialog %q layout: width=%d height=%d", d.GetTitle(), d.X2-d.X1+1, d.Y2-d.Y1+1)
}

func (d *MaskDialog) ResizeConsole(width, height int) {
	// Like the file dialogs, use half of the available screen width. Keep
	// the smaller minimum local to mask selection, not copy/move dialogs.
	dialogWidth := compactInputWidth(width)
	x, y := max(0, (width-dialogWidth)/2), max(0, (height-maskDialogHeight)/2)
	d.SetPosition(x, y, x+dialogWidth-1, y+maskDialogHeight-1)
	d.layoutControls()
}

func (d *MaskDialog) ProcessMouse(e *vtinput.InputEvent) bool {
	if d.WheelHistory && OpenHistoryOnWheel(e, d.edit) {
		return true
	}
	handled := d.Window.ProcessMouse(e)
	// Window's corner handler calls BaseWindow.ChangeSize directly, so our
	// ChangeSize override cannot constrain its vertical drag. Restore the
	// fixed height before laying out or drawing any controls.
	if d.Y2-d.Y1+1 != maskDialogHeight {
		d.ChangeSize(d.X2-d.X1+1, maskDialogHeight)
	}
	d.layoutControls()
	return handled
}

func (d *MaskDialog) Show(screen *vtui.ScreenBuf) {
	d.layoutControls()
	d.Window.Show(screen)
}

// MaskInputBox opens the select/deselect group mask prompt. Its minimum is
// ten columns narrower than vtui.InputBox's original 40-column window.
func MaskInputBox(title, prompt, defaultText string, onOK func(string)) *MaskDialog {
	return compactInputBox(40, title, prompt, defaultText, onOK)
}

func compactInputBox(width int, title, prompt, defaultText string, onOK func(string)) *MaskDialog {
	d := &MaskDialog{Window: vtui.NewCenteredDialog(width, maskDialogHeight, title)}
	d.ShowClose = true
	d.edit = vtui.NewEdit(0, 0, 10, defaultText)
	d.label = vtui.NewLabel(0, 0, prompt, d.edit)
	d.rule = vtui.NewSeparator(0, 0, width, true, true)
	ok := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	cancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	ok.IsDefault = true
	ok.OnClick = func() {
		if onOK != nil {
			onOK(d.edit.GetText())
		}
		d.SetExitCode(1)
	}
	cancel.OnClick = func() { d.SetExitCode(-1) }
	d.AddItem(d.label)
	d.AddItem(d.edit)
	d.AddItem(ok)
	d.AddItem(cancel)
	d.AddItem(d.rule)
	d.buttons = vtui.NewHBoxLayout(0, 0, width-4, 1)
	d.buttons.HorizontalAlign = vtui.AlignCenter
	d.buttons.Spacing = 2
	d.buttons.Add(ok, vtui.Margins{}, vtui.AlignTop)
	d.buttons.Add(cancel, vtui.Margins{}, vtui.AlignTop)
	d.MinW = maskDialogMinWidth
	d.layoutControls()
	vtui.FrameManager.Push(d)
	return d
}
