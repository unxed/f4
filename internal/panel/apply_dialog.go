package panel

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const (
	applyDialogWidth        = 72
	applyDialogHeight       = 10
	applyDialogModeWidth    = 23
	applyDialogWorkersWidth = 4
)

// applyCommandDialog owns the compact form's geometry, including corner drags
// that vtui handles through BaseWindow rather than the ChangeSize override.
type applyCommandDialog struct {
	*vtui.Window
	commandLabel, targets, modeLabel, workersLabel *vtui.Text
	command, workers                               *vtui.Edit
	mode                                           *vtui.ComboBox
	unlimited                                      *vtui.Checkbox
	parametersRule, buttonsRule                    *vtui.Separator
	buttons                                        *vtui.HBoxLayout
}

// Measure natural control widths before layout stretches the command caption
// and target count. Four cells account for the frame and side margins.
func (d *applyCommandDialog) minimumWidth() int {
	widthOf := func(item vtui.UIElement) int {
		x, _, x2, _ := item.GetPosition()
		return x2 - x + 1
	}
	modeWidth := widthOf(d.modeLabel) + 1 + applyDialogModeWidth
	workersWidth := widthOf(d.workersLabel) + 1 + applyDialogWorkersWidth + 2 + widthOf(d.unlimited)
	buttonsWidth := d.buttons.Spacing
	for _, item := range d.buttons.Items {
		buttonsWidth += widthOf(item.Element)
	}
	return max(modeWidth+4, workersWidth+4, buttonsWidth+4,
		widthOf(d.commandLabel)+4, widthOf(d.targets)+4,
		vtui.StringWidth(d.GetTitle())+8)
}

func (d *applyCommandDialog) layoutControls() {
	left, right := d.X1+2, d.X2-2
	d.commandLabel.SetPosition(left, d.Y1+1, right, d.Y1+1)
	d.command.SetPosition(left, d.Y1+2, right, d.Y1+2)
	d.targets.SetPosition(left, d.Y1+3, right, d.Y1+3)
	d.parametersRule.SetPosition(d.X1, d.Y1+4, d.X2, d.Y1+4)

	x, _, x2, _ := d.modeLabel.GetPosition()
	labelWidth := x2 - x + 1
	d.modeLabel.SetPosition(left, d.Y1+5, left+labelWidth-1, d.Y1+5)
	modeLeft := left + labelWidth + 1
	d.mode.SetPosition(modeLeft, d.Y1+5, modeLeft+applyDialogModeWidth-1, d.Y1+5)
	x, _, x2, _ = d.workersLabel.GetPosition()
	labelWidth = x2 - x + 1
	d.workersLabel.SetPosition(left, d.Y1+6, left+labelWidth-1, d.Y1+6)
	x, _, x2, _ = d.unlimited.GetPosition()
	checkboxWidth := x2 - x + 1
	workersLeft := left + labelWidth + 1
	workersRight := workersLeft + applyDialogWorkersWidth - 1
	d.workers.SetPosition(workersLeft, d.Y1+6, workersRight, d.Y1+6)
	checkboxLeft := workersRight + 3
	d.unlimited.SetPosition(checkboxLeft, d.Y1+6, checkboxLeft+checkboxWidth-1, d.Y1+6)
	d.buttonsRule.SetPosition(d.X1, d.Y2-2, d.X2, d.Y2-2)
	d.buttons.SetPosition(left, d.Y2-1, right, d.Y2-1)
}

func (d *applyCommandDialog) ChangeSize(width, _ int) {
	d.Window.ChangeSize(width, applyDialogHeight)
	d.layoutControls()
	vtui.DebugLog("[FIX] Apply command layout: width=%d height=%d", d.X2-d.X1+1, d.Y2-d.Y1+1)
}

func (d *applyCommandDialog) ResizeConsole(width, height int) {
	dialogWidth := min(width, max(d.MinW, width/2))
	x, y := max(0, (width-dialogWidth)/2), max(0, (height-applyDialogHeight)/2)
	d.SetPosition(x, y, x+dialogWidth-1, y+applyDialogHeight-1)
	d.layoutControls()
}

func (d *applyCommandDialog) ProcessMouse(e *vtinput.InputEvent) bool {
	handled := d.Window.ProcessMouse(e)
	if d.Y2-d.Y1+1 != applyDialogHeight {
		d.ChangeSize(d.X2-d.X1+1, applyDialogHeight)
	}
	d.layoutControls()
	return handled
}

func (d *applyCommandDialog) Show(screen *vtui.ScreenBuf) {
	d.layoutControls()
	d.Window.Show(screen)
}
