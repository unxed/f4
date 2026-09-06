package main

import (
	"github.com/unxed/vtui"
)

// File dialogs (F5/F6 copy/move, Shift+F5 copy in place, Shift+F6 rename)
// used to be 50 and 40 columns wide regardless of the terminal, which cut
// long file names and destination paths and made the four dialogs visibly
// different sizes (#891). They now share one width that follows the f4
// window: as wide as the screen allows, leaving a small margin, but never
// narrower than the old copy dialog.
const (
	fileDialogMinWidth    = 50
	fileDialogScreenInset = 4
)

// fileDialogWidth returns the dialog width for the given screen width.
func fileDialogWidth(screenWidth int) int {
	w := screenWidth - fileDialogScreenInset
	if w < fileDialogMinWidth {
		w = fileDialogMinWidth
	}
	if screenWidth > 0 && w > screenWidth {
		w = screenWidth
	}
	return w
}

func currentFileDialogWidth() int {
	screenW := 0
	if vtui.FrameManager != nil {
		screenW = vtui.FrameManager.GetScreenSize()
	}
	return fileDialogWidth(screenW)
}

// fileInputBox is vtui.InputBox with the shared file dialog width instead of
// the library's fixed 40 columns, so a long name is visible while editing.
func fileInputBox(title, prompt, defaultText string, onOk func(string)) *vtui.Window {
	width := currentFileDialogWidth()
	height := 9
	dlg := vtui.NewCenteredDialog(width, height, title)
	dlg.ShowClose = true

	edit := vtui.NewEdit(0, 0, 10, defaultText)
	lbl := vtui.NewLabel(0, 0, prompt, edit)
	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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

	layout := vtui.NewAutoLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	layout.
		PinTop(lbl, 0).PinLeft(lbl, 0).
		StackVertical(1, lbl, edit).FillWidth(edit, 0, 0).
		PinBottom(btnOk, 0).PinBottom(btnCancel, 0).
		StackHorizontal(2, btnOk, btnCancel).
		CenterHorizontalGroup(btnOk, btnCancel)
	layout.Apply()

	vtui.FrameManager.Push(dlg)
	return dlg
}
