package main

import (
	"github.com/unxed/vtui"
)

func ShowDemoDialog() {
	scrWidth := vtui.FrameManager.GetScreenSize()
	dlgWidth, dlgHeight := 60, 16
	x1 := (scrWidth - dlgWidth) / 2
	y1 := 4

	dlg := vtui.NewDialog(x1, y1, x1+dlgWidth-1, y1+dlgHeight-1, " vtui Components Demo ")

	// 1. Радиокнопки
	dlg.AddItem(vtui.NewText(x1+2, y1+2, "Select mode:", vtui.Palette[vtui.ColDialogText]))
	rb1 := vtui.NewRadioButton(x1+4, y1+3, "Fast and Dangerous")
	rb1.Selected = true
	dlg.AddItem(rb1)
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+4, "Slow and Stable"))
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+5, "Mental Health Mode"))

	// 2. Чекбоксы
	dlg.AddItem(vtui.NewText(x1+30, y1+2, "Settings:", vtui.Palette[vtui.ColDialogText]))
	dlg.AddItem(vtui.NewCheckbox(x1+32, y1+3, "Enable AI", false))
	dlg.AddItem(vtui.NewCheckbox(x1+32, y1+4, "Auto-update", true))
	dlg.AddItem(vtui.NewCheckbox(x1+32, y1+5, "Force Legacy", false))

	// 3. ComboBox
	dlg.AddItem(vtui.NewText(x1+2, y1+7, "Encoding:", vtui.Palette[vtui.ColDialogText]))
	items := []string{"UTF-8", "CP866 (OEM)", "Windows-1251", "KOI8-R"}
	combo := vtui.NewComboBox(x1+12, y1+7, 20, items)
	combo.Edit.SetText("UTF-8")
	dlg.AddItem(combo)

	// 4. Password
	dlg.AddItem(vtui.NewText(x1+2, y1+9, "Password:", vtui.Palette[vtui.ColDialogText]))
	pass := vtui.NewEdit(x1+12, y1+9, 20, "")
	pass.PasswordMode = true
	dlg.AddItem(pass)

	// 5. Кнопка
	btnOk := vtui.NewButton(x1+dlgWidth/2-5, y1+12, "Close")
	btnOk.OnClick = func() { dlg.SetExitCode(0) }
	dlg.AddItem(btnOk)

	vtui.FrameManager.Push(dlg)
}