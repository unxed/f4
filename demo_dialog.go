package main

import (
	"github.com/unxed/vtui"
)

func ShowDemoDialog() {
	scrWidth := vtui.FrameManager.GetScreenSize()
	dlgWidth, dlgHeight := 64, 18
	x1 := (scrWidth - dlgWidth) / 2
	y1 := 4

	dlg := vtui.NewDialog(x1, y1, x1+dlgWidth-1, y1+dlgHeight-1, " vtui Components Demo ")

	// 1. Радиокнопки
	dlg.AddItem(vtui.NewText(x1+2, y1+2, "Select &mode:", vtui.Palette[vtui.ColDialogText]))
	rb1 := vtui.NewRadioButton(x1+4, y1+3, "&Fast and Dangerous")
	rb1.Selected = true
	dlg.AddItem(rb1)
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+4, "Slow and &Stable"))
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+5, "Mental &Health Mode"))

	// Вертикальный разделитель/метка в центре
	dlg.AddItem(vtui.NewVText(x1+30, y1+2, "│CORE│", vtui.Palette[vtui.ColDialogText]))

	// 2. Чекбоксы
	dlg.AddItem(vtui.NewText(x1+34, y1+2, "S&ettings:", vtui.Palette[vtui.ColDialogText]))
	dlg.AddItem(vtui.NewCheckbox(x1+36, y1+3, "Enable &AI", false))
	dlg.AddItem(vtui.NewCheckbox(x1+36, y1+4, "A&uto-update", true))
	dlg.AddItem(vtui.NewCheckbox(x1+36, y1+5, "F&orce Legacy", false))

	// 3. ComboBox
	comboLabel := vtui.NewText(x1+2, y1+8, "&Encoding:", vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(comboLabel)
	items := []string{"UTF-8", "CP866 (OEM)", "Windows-1251", "KOI8-R"}
	for i := 0; i < 15; i++ {
		items = append(items, "Item "+string(rune('A'+i)))
	}
	combo := vtui.NewComboBox(x1+13, y1+8, 16, items)
	combo.Edit.SetText("UTF-8")
	comboLabel.FocusLink = combo // Привязываем метку к выпадающему списку!
	dlg.AddItem(combo)

	// 4. Password
	passLabel := vtui.NewText(x1+2, y1+10, "&Password:", vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(passLabel)
	pass := vtui.NewEdit(x1+13, y1+10, 16, "")
	pass.PasswordMode = true
	passLabel.FocusLink = pass // Привязываем метку к текстовому полю!
	dlg.AddItem(pass)

	// 5. Вертикальное меню (VMenu)
	menu := vtui.NewVMenu(" Operations ")
	menu.SetHelp("MenuOperationsTopic")
	menu.SetPosition(x1+34, y1+8, x1+58, y1+13)
	menu.AddItem("&Copy File")
	menu.AddItem("&Move File")
	menu.AddSeparator()
	menu.AddItem("&Delete File")
	dlg.AddItem(menu)

	// 6. Кнопки
	btnOk := vtui.NewButton(x1+dlgWidth/2-10, y1+15, "&Ok")
	btnOk.OnClick = func() { dlg.SetExitCode(0) }
	dlg.AddItem(btnOk)

	btnCancel := vtui.NewButton(x1+dlgWidth/2+2, y1+15, "&Close")
	btnCancel.OnClick = func() { dlg.SetExitCode(1) }
	dlg.AddItem(btnCancel)

	vtui.FrameManager.Push(dlg)
}