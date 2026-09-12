package dialog

import (
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// File dialogs (F5/F6 copy/move, Shift+F5 copy in place, Shift+F6 rename)
// used to be 50 and 40 columns wide regardless of the terminal, which cut
// long file names and destination paths and made the four dialogs visibly
// different sizes (#891). They now share one width that follows the f4
// window: half the screen by default, with the same width also used by the
// text field in the rename and copy-in-place dialogs.
const (
	fileDialogMinWidth = 40

	// Heights are deliberately fixed: only the width follows the window.
	fileInputBoxHeight = 9

	// CopyBoxHeight holds the prompt, the destination field, the button row
	// and the two selectors of the copy/move dialog: access rights (#722)
	// and the operation mode.
	CopyBoxHeight = 13
)

// FileDialogWidth returns the dialog width for the given screen width.
func FileDialogWidth(screenWidth int) int {
	if screenWidth <= 0 {
		return 50
	}
	w := screenWidth / 2
	if w < fileDialogMinWidth {
		w = fileDialogMinWidth
	}
	if w > screenWidth {
		w = screenWidth
	}
	return w
}

func currentFileDialogWidth() int {
	screenW := 0
	if vtui.FrameManager != nil {
		screenW = vtui.FrameManager.GetScreenSize()
	}
	return FileDialogWidth(screenW)
}

// FileDialog keeps a copy/move/rename dialog in step with the f4 window for
// as long as it stays open. A modal window inherits only the vtui behaviour
// of re-centering its old rectangle and clipping whatever no longer fits, so
// a dialog opened in a wide terminal stayed wide: after the window had been
// narrowed it hung over the right border with the name field cut in half,
// and after it had been widened the field stopped short of the new border
// (#891). This dialog takes half of the new width instead and lays its
// controls out again, so the name field is always stretched to the dialog.
type FileDialog struct {
	*vtui.Window

	height   int
	relayout func()
}

// NewFileDialog opens a centered dialog of the shared width and the given
// fixed height.
func NewFileDialog(title string, height int) *FileDialog {
	dlg := &FileDialog{
		Window: vtui.NewCenteredDialog(currentFileDialogWidth(), height, title),
		height: height,
	}
	dlg.ShowClose = true
	return dlg
}

// SetLayout stores the layout pass and runs it once for the initial size.
// The pass has to place the controls against the dialog rectangle as it is
// at the time of the call, because the same closure runs again on a resize.
func (d *FileDialog) SetLayout(relayout func()) {
	d.relayout = relayout
	if relayout != nil {
		relayout()
	}
}

// size returns the current outer size of the dialog.
func (d *FileDialog) Size() (int, int) {
	return d.X2 - d.X1 + 1, d.Y2 - d.Y1 + 1
}

// ResizeConsole re-sizes the dialog for the new terminal size, keeps it
// centered and re-runs the layout so the text field follows the new width.
func (d *FileDialog) ResizeConsole(screenWidth, screenHeight int) {
	if d == nil || d.Window == nil {
		return
	}
	width := FileDialogWidth(screenWidth)
	x1 := (screenWidth - width) / 2
	y1 := (screenHeight - d.height) / 2
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	d.SetPosition(x1, y1, x1+width-1, y1+d.height-1)
	if d.relayout != nil {
		d.relayout()
	}
}

// FileInputBox is vtui.InputBox with the shared file dialog width instead of
// the library's fixed 40 columns, so a long name is visible while editing.
func FileInputBox(title, prompt, defaultText string, onOk func(string)) *FileDialog {
	dlg := NewFileDialog(title, fileInputBoxHeight)

	edit := vtui.NewEdit(0, 0, 10, defaultText)
	lbl := vtui.NewLabel(0, 0, prompt, edit)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	btnOk.OnClick = func() {
		if onOk != nil {
			onOk(edit.GetText())
		}
		dlg.SetExitCode(1)
	}
	btnCancel.OnClick = func() { dlg.SetExitCode(-1) }

	dlg.AddItem(lbl)
	dlg.AddItem(edit)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	width, height := dlg.Size()
	layout := vtui.NewAutoLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	layout.
		PinTop(lbl, 0).PinLeft(lbl, 0).
		StackVertical(1, lbl, edit).FillWidth(edit, 0, 0).
		PinBottom(btnOk, 0).PinBottom(btnCancel, 0).
		StackHorizontal(2, btnOk, btnCancel).
		CenterHorizontalGroup(btnOk, btnCancel)

	// AutoLayout.SetPosition re-suggests its bounds and solves again, so the
	// one constraint set serves every window width. The layout keeps the
	// minimum sizes it took from the freshly created controls, so a later
	// resize can also make the dialog narrower again.
	dlg.SetLayout(func() {
		layout.SetPosition(dlg.X1+2, dlg.Y1+2, dlg.X2-2, dlg.Y2-2)
	})

	vtui.FrameManager.Push(dlg)
	return dlg
}
