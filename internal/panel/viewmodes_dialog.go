package panel

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// panelViewModeName is the menu name of the mode Ctrl+key selects: the name
// the user gave it in the mode dialog, else the built-in name of the slot.
func panelViewModeName(key int) string {
	if key < 0 || key >= PanelViewModeCount {
		return ""
	}
	if mode, ok := ViewModeForKey(key); ok {
		if name := PanelViewModeCustomName(mode); name != "" {
			return name
		}
	}
	return i18n.Msg(panelViewModeNameKeys[key])
}

var panelViewModeNameKeys = [PanelViewModeCount]string{
	"Panel.Modes.Mode0", "Panel.Modes.Mode1", "Panel.Modes.Mode2", "Panel.Modes.Mode3", "Panel.Modes.Mode4",
	"Panel.Modes.Mode5", "Panel.Modes.Mode6", "Panel.Modes.Mode7", "Panel.Modes.Mode8", "Panel.Modes.Mode9",
}

// panelModesMenuPos is the list row of a mode: far2l lists Ctrl+1 .. Ctrl+9
// first and Ctrl+0 last (flmodes.cpp, IndexToMenuPos).
func panelModesMenuPos(mode ViewMode) int {
	return (mode.Key() + PanelViewModeCount - 1) % PanelViewModeCount
}

func panelModesMenuKey(pos int) int {
	return (pos + 1) % PanelViewModeCount
}

// ShowPanelModesMenu is far2l's Options -> File panel modes
// (FileList::SetFilePanelModes): the ten modes, Enter edits the one under
// the cursor and returns to the list.
func ShowPanelModesMenu(pf *PanelsFrame) {
	pos := 0
	if pf != nil {
		if fsp := pf.GetActivePanel(); fsp != nil {
			pos = panelModesMenuPos(fsp.EffectiveViewMode())
		}
	}
	openPanelModesMenu(pf, pos)
}

func openPanelModesMenu(pf *PanelsFrame, pos int) {
	if vtui.FrameManager == nil {
		return
	}
	menu := vtui.NewVMenu(i18n.Msg("Panel.Modes.Title"))
	for p := 0; p < PanelViewModeCount; p++ {
		key := panelModesMenuKey(p)
		menu.AddItem(vtui.MenuItem{Text: panelViewModeName(key), Shortcut: "Ctrl+" + strconv.Itoa(key)})
	}
	menu.SetSelectPos(pos)
	menu.OnAction = func(idx int) {
		if idx < 0 || idx >= PanelViewModeCount {
			return
		}
		vtui.FrameManager.PostTask(func() { editPanelViewMode(pf, idx) })
	}
	screenW, screenH := vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight()
	w := min(46, screenW)
	h := min(PanelViewModeCount+2, screenH)
	x := max(0, (screenW-w)/2)
	y := max(0, (screenH-h)/2)
	menu.SetPosition(x, y, x+w-1, y+h-1)
	vtui.FrameManager.Push(menu)
}

// panelModeErrorText turns a TextToViewSettings error into the message the
// dialog shows.
func panelModeErrorText(err error) string {
	var typeErr *PanelColumnTypeError
	var widthErr *PanelColumnWidthError
	switch {
	case errors.As(err, &typeErr):
		return fmt.Sprintf(i18n.Msg("Panel.Modes.BadColumnType"), typeErr.Token)
	case errors.As(err, &widthErr):
		return fmt.Sprintf(i18n.Msg("Panel.Modes.BadColumnWidth"), widthErr.Token)
	case errors.Is(err, ErrNoPanelColumns):
		return i18n.Msg("Panel.Modes.NoColumns")
	}
	return err.Error()
}

// editPanelViewMode is far2l's mode dialog: column types and widths, the
// full screen switch, and Reset back to the built-in definition. Closing it
// returns to the list, as in far2l.
// editModeColumns opens the column list editor; a variable so tests can see
// which fields a button hands it.
var editModeColumns = showModeColumnsEditor

func editPanelViewMode(pf *PanelsFrame, pos int) {
	key := panelModesMenuKey(pos)
	mode, ok := ViewModeForKey(key)
	if !ok || vtui.FrameManager == nil {
		return
	}
	settings := PanelViewModeSettings(mode)
	types, widths := ViewSettingsToText(settings.Columns)

	const width = 64
	// Rows the vbox stacks: five label/edit pairs, the status line's
	// Columns... button, two checkboxes and the button row (14) plus the
	// blank row before each group after the first and before that button
	// (7). The frame, the row under the title and the row over the bottom
	// frame add four more. The dialog used to be 20 rows tall, three short
	// of that, so the button row landed below the bottom frame and only
	// the modal clip kept it on screen.
	const contentRows = 14 + 7
	height := 4 + contentRows
	// On a console shorter than the dialog the frame would be drawn past
	// its bottom row; the modal viewport then scrolls the controls instead.
	if scrH := vtui.FrameManager.GetScreenHeight(); scrH > 0 && height > scrH {
		height = scrH
	}
	dlg := vtui.NewCenteredDialog(width, height, " "+panelViewModeName(key)+" ")
	dlg.ShowClose = true
	dlg.SetHelp("PanelModes")

	editName := vtui.NewEdit(0, 0, width-6, settings.Name)
	if settings.Name == "" {
		editName.SetText(i18n.Msg(panelViewModeNameKeys[key]))
	}
	builtinName := i18n.Msg(panelViewModeNameKeys[key])
	editTypes := vtui.NewEdit(0, 0, width-6, types)
	editWidths := vtui.NewEdit(0, 0, width-6, widths)
	statusTypes, statusWidths := ViewSettingsToText(settings.StatusColumns)
	editStatusTypes := vtui.NewEdit(0, 0, width-6, statusTypes)
	editStatusWidths := vtui.NewEdit(0, 0, width-6, statusWidths)
	chkFullScreen := vtui.NewCheckbox(0, 0, i18n.Msg("Panel.Modes.FullScreen"), false)
	if settings.FullScreen {
		chkFullScreen.State = 1
	}
	chkUppercaseDirs := vtui.NewCheckbox(0, 0, i18n.Msg("Panel.Modes.UppercaseDirs"), false)
	if settings.UppercaseDirs {
		chkUppercaseDirs.State = 1
	}
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnReset := vtui.NewButton(0, 0, i18n.Msg("Panel.Modes.Reset"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	btnColumns := vtui.NewButton(0, 0, i18n.Msg("Panel.Modes.EditColumns"))
	// The status line's columns get the same list editor as the panel's,
	// under their own fields (montoner0, f4#410).
	btnStatusColumns := vtui.NewButton(0, 0, i18n.Msg("Panel.Modes.EditStatusColumns"))
	lblName := vtui.NewLabel(0, 0, i18n.Msg("Panel.Modes.Name"), editName)
	lblTypes := vtui.NewLabel(0, 0, i18n.Msg("Panel.Modes.ColumnTypes"), editTypes)
	lblStatusTypes := vtui.NewLabel(0, 0, i18n.Msg("Panel.Modes.StatusColumnTypes"), editStatusTypes)
	lblStatusWidths := vtui.NewLabel(0, 0, i18n.Msg("Panel.Modes.StatusColumnWidths"), editStatusWidths)
	lblWidths := vtui.NewLabel(0, 0, i18n.Msg("Panel.Modes.ColumnWidths"), editWidths)

	dlg.AddItem(lblName)
	dlg.AddItem(editName)
	dlg.AddItem(lblTypes)
	dlg.AddItem(editTypes)
	dlg.AddItem(lblWidths)
	dlg.AddItem(editWidths)
	dlg.AddItem(lblStatusTypes)
	dlg.AddItem(editStatusTypes)
	dlg.AddItem(lblStatusWidths)
	dlg.AddItem(editStatusWidths)
	dlg.AddItem(btnStatusColumns)
	dlg.AddItem(chkFullScreen)
	dlg.AddItem(chkUppercaseDirs)
	// Tab walks the controls in the order they are added, so the buttons go in
	// the order they stand on the row: Columns... used to come after Cancel
	// (f4#410).
	dlg.AddItem(btnOk)
	dlg.AddItem(btnColumns)
	dlg.AddItem(btnReset)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, width-6, height-4)
	vbox.Add(lblName, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editName, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblTypes, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editTypes, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblWidths, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editWidths, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblStatusTypes, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editStatusTypes, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblStatusWidths, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editStatusWidths, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(btnStatusColumns, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkFullScreen, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkUppercaseDirs, vtui.Margins{}, vtui.AlignLeft)
	btnRow := vtui.NewHBoxLayout(0, 0, width-6, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnColumns, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnReset, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(btnRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	finish := func(changed *PanelViewSettings, reset bool) {
		if changed != nil || reset {
			if err := SetPanelViewModeSettings(mode, changed); err != nil {
				vtui.ShowMessageOn(dlg, " "+i18n.Msg("Panel.Modes.Title")+" ", err.Error(), []string{"&Ok"})
				return
			}
			applyPanelViewModes(pf)
		}
		dlg.Close()
		vtui.FrameManager.PostTask(func() { openPanelModesMenu(pf, pos) })
	}
	btnCancel.OnClick = func() { finish(nil, false) }
	// The column list edits the two strings above in place (f4#410).
	btnColumns.OnClick = func() {
		editModeColumns(editTypes.GetText(), editWidths.GetText(), func(types, widths string) {
			editTypes.SetText(types)
			editWidths.SetText(widths)
			vtui.FrameManager.Redraw()
		})
	}
	btnStatusColumns.OnClick = func() {
		editModeColumns(editStatusTypes.GetText(), editStatusWidths.GetText(), func(types, widths string) {
			editStatusTypes.SetText(types)
			editStatusWidths.SetText(widths)
			vtui.FrameManager.Redraw()
		})
	}
	btnReset.OnClick = func() { finish(nil, true) }
	btnOk.OnClick = func() {
		columns, err := TextToViewSettings(editTypes.GetText(), editWidths.GetText())
		if err != nil {
			vtui.ShowMessageOn(dlg, " "+i18n.Msg("Panel.Modes.Title")+" ", panelModeErrorText(err), []string{"&Ok"})
			return
		}
		// A name left as the built-in one stays unnamed, so it keeps following
		// the interface language.
		name := strings.TrimSpace(editName.GetText())
		if name == builtinName {
			name = ""
		}
		// Empty status columns keep the built-in status line (f4#410).
		var status []PanelColumn
		if strings.TrimSpace(editStatusTypes.GetText()) != "" {
			status, err = TextToViewSettings(editStatusTypes.GetText(), editStatusWidths.GetText())
			if err != nil {
				vtui.ShowMessageOn(dlg, " "+i18n.Msg("Panel.Modes.Title")+" ", panelModeErrorText(err), []string{"&Ok"})
				return
			}
		}
		changed := &PanelViewSettings{
			Name:          name,
			Columns:       columns,
			FullScreen:    chkFullScreen.State != 0,
			UppercaseDirs: chkUppercaseDirs.State != 0,
			StatusColumns: status,
		}
		finish(changed, false)
	}

	dlg.SetFocusedItem(editName)
	vtui.FrameManager.Push(dlg)
}

// applyPanelViewModes lays the panels out again after a mode changed.
func applyPanelViewModes(pf *PanelsFrame) {
	if pf == nil {
		return
	}
	if pf.LastW > 0 && pf.LastH > 0 {
		pf.ResizeConsole(pf.LastW, pf.LastH)
	}
	pf.UpdateMenuCheckmarks()
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}
