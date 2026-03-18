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

	// 1. Radio buttons
	dlg.AddItem(vtui.NewText(x1+2, y1+2, "Select &mode:", vtui.Palette[vtui.ColDialogText]))
	rb1 := vtui.NewRadioButton(x1+4, y1+3, "&Fast and Dangerous")
	rb1.Selected = true
	dlg.AddItem(rb1)
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+4, "Slow and &Stable"))
	dlg.AddItem(vtui.NewRadioButton(x1+4, y1+5, "Mental &Health Mode"))

	// Vertical separator/label in the center
	dlg.AddItem(vtui.NewVText(x1+30, y1+2, "│CORE│", vtui.Palette[vtui.ColDialogText]))

	// 2. Checkboxes
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
	comboLabel.FocusLink = combo // Link label to the combo box!
	dlg.AddItem(combo)

	// 4. Password
	passLabel := vtui.NewText(x1+2, y1+10, "&Password:", vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(passLabel)
	pass := vtui.NewEdit(x1+13, y1+10, 16, "")
	pass.PasswordMode = true
	passLabel.FocusLink = pass // Link label to the edit field!
	dlg.AddItem(pass)

	// 5. Vertical menu (VMenu)
	menu := vtui.NewVMenu(" Operations ")
	menu.SetHelp("MenuOperationsTopic")
	menu.SetPosition(x1+34, y1+8, x1+58, y1+10) // Height of 3 lines for the menu
	menu.AddItem("&Copy File")
	menu.AddItem("&Move File")
	menu.AddSeparator()
	menu.AddItem("&Delete File")
	dlg.AddItem(menu)

	// 5. ListBox (list of recently opened files)
	recentLabel := vtui.NewText(x1+34, y1+11, "&Recently used:", vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(recentLabel)

	recentFiles := []string{
		"config.go", "main.go", "utils.go", "README.md",
		"LICENSE", "go.mod", "ansi_parser.go", "vfs.go",
	}
	// ListBox at y1+12, height of 2 lines (occupies 12, 13)
	lb := vtui.NewListBox(x1+34, y1+12, 24, 2, recentFiles)
	lb.ColorTextIdx = vtui.ColDialogEdit // Text like in an edit field (black on cyan)
	lb.ColorSelectedTextIdx = vtui.ColDialogEditSelected // Selection (white on black/cyan)
	recentLabel.FocusLink = lb
	dlg.AddItem(lb)

	// 6. Buttons
	// Buttons at y1+15. Now there is an empty line (y1+14) between the ListBox (y1+13) and the buttons.
	btnOk := vtui.NewButton(x1+dlgWidth/2-10, y1+15, "&Ok")
	btnOk.OnClick = func() { dlg.SetExitCode(0) }
	dlg.AddItem(btnOk)

	btnCancel := vtui.NewButton(x1+dlgWidth/2+2, y1+15, "&Close")
	btnCancel.OnClick = func() { dlg.SetExitCode(1) }
	dlg.AddItem(btnCancel)

	vtui.FrameManager.Push(dlg)
}