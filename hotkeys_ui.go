package main

import (
	"sort"
	"strings"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type hotkeyRow struct {
	Action    string
	Label     string
	Area      string
	Key       string
	Condition string
	Desc      string
}

func (r hotkeyRow) GetCellText(col int) string {
	switch col {
	case 0:
		return r.Label
	case 1:
		return r.Key
	case 2:
		return r.Area
	case 3:
		return r.Condition
	case 4:
		return r.Desc
	}
	return ""
}

func (r hotkeyRow) GetCellAttr(col int, def uint64) uint64 {
	if r.Key == "" {
		return vtui.DimColor(def)
	}
	return def
}

func actionHotkeyConfig(pf *PanelsFrame) {
	w, h := 120, 48

	btnAssign := vtui.NewButton(0, 0, Msg("Hotkeys.BtnAssign"))
	btnUnbind := vtui.NewButton(0, 0, Msg("Hotkeys.BtnUnbind"))
	btnClose := vtui.NewButton(0, 0, Msg("Hotkeys.BtnClose"))

	dlg, table := vtui.NewTableDialog(w, h, Msg("Hotkeys.Title"), []vtui.TableColumn{
		{Title: "Command", Width: 23},
		{Title: "Key", Width: 14},
		{Title: "Area", Width: 10},
		{Title: "When", Width: 17},
		{Title: "Description", Width: 0},
	}, btnAssign, btnUnbind, btnClose)
	useDialogTableColors(table)
	table.ShowScrollBar = true
	table.Sortable = true    // click a column header to sort, again to reverse
	table.QuickSearch = true // type to fuzzy-filter (Myers bit-vector)

	var rows []vtui.TableRow
	var hkRows []hotkeyRow

	refresh := func() {
		hkRows = nil
		rows = nil

		activeBinds := GlobalHotkeysMgr.GetActiveBindings()
		actions := GetActions()
		assignedActions := make(map[string]bool)

		for area, binds := range activeBinds {
			for key, binding := range binds {
				parts := strings.SplitN(binding, ":", 2)
				actName := parts[0]
				cond := ""
				if len(parts) == 2 {
					cond = parts[1]
				}
				act, ok := GetAction(actName)
				if !ok {
					act = Action{Name: actName, Label: actName, Description: "Unknown action"}
				}
				hkRows = append(hkRows, hotkeyRow{
					Action:    act.Name,
					Label:     plainLabel(act.DisplayLabel()),
					Area:      area,
					Key:       key,
					Condition: cond,
					Desc:      act.DisplayDescription(),
				})
				assignedActions[strings.ToLower(act.Name)] = true
			}
		}

		for _, act := range actions {
			if !assignedActions[strings.ToLower(act.Name)] {
				hkRows = append(hkRows, hotkeyRow{
					Action:    act.Name,
					Label:     plainLabel(act.DisplayLabel()),
					Area:      act.Area, // unassigned, shown under the action's native area
					Key:       "",
					Condition: "",
					Desc:      act.DisplayDescription(),
				})
			}
		}

		sort.Slice(hkRows, func(i, j int) bool {
			if hkRows[i].Area != hkRows[j].Area {
				// Rows without an area (shouldn't happen) go last
				if hkRows[i].Area == "" {
					return false
				}
				if hkRows[j].Area == "" {
					return true
				}
				return hkRows[i].Area < hkRows[j].Area
			}
			return hkRows[i].Label < hkRows[j].Label
		})

		for _, r := range hkRows {
			rows = append(rows, r)
		}
		table.SetRows(rows)
		vtui.FrameManager.Redraw()
	}

	btnAssign.OnClick = func() {
		idx := table.SelectPos
		if idx >= 0 && idx < len(hkRows) {
			row := hkRows[idx]
			showAreaSelectDialog(row.Action, row.Area, row.Condition, refresh)
		}
	}

	btnUnbind.OnClick = func() {
		idx := table.SelectPos
		if idx >= 0 && idx < len(hkRows) {
			row := hkRows[idx]
			if row.Key != "" && row.Area != "" {
				GlobalHotkeysMgr.Bind(row.Area, row.Key, "None")
				GlobalHotkeysMgr.Save()
				refresh()
			}
		}
	}

	btnClose.OnClick = func() { dlg.Close() }

	table.OnAction = func(idx int) {
		btnAssign.OnClick()
	}

	vtui.FrameManager.Push(dlg)
	refresh()
}

func showAreaSelectDialog(actionName, defaultArea, defaultCond string, onComplete func()) {
	dlg := vtui.NewCenteredDialog(40, 11, Msg("Hotkeys.SelectTitle"))
	dlg.ShowClose = true

	if defaultArea == "" {
		defaultArea = "Shell"
	}

	areas := []string{"Shell", "Terminal", "Editor", "Viewer", "Dialog", "Menu", "Disks", "Common"}

	combo := vtui.NewComboBox(0, 0, 20, areas)
	combo.DropdownOnly = true
	idx := 0
	for i, a := range areas {
		if a == defaultArea {
			idx = i
		}
	}
	combo.Menu.SetSelectPos(idx)
	combo.Edit.SetText(areas[idx])

	conds := GetConditions()
	comboCond := vtui.NewComboBox(0, 0, 20, conds)
	comboCond.DropdownOnly = true
	cIdx := 0
	for i, c := range conds {
		if strings.EqualFold(c, defaultCond) || (c == "None" && defaultCond == "") {
			cIdx = i
		}
	}
	comboCond.Menu.SetSelectPos(cIdx)
	comboCond.Edit.SetText(conds[cIdx])

	btnOk := vtui.NewButton(0, 0, Msg("Hotkeys.BtnNext"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	lbl := vtui.NewLabel(0, 0, Msg("Hotkeys.LabelArea"), combo)
	lblCond := vtui.NewLabel(0, 0, Msg("Hotkeys.LabelWhen"), comboCond)

	dlg.AddItem(lbl)
	dlg.AddItem(combo)
	dlg.AddItem(lblCond)
	dlg.AddItem(comboCond)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 36, 7)
	row := vtui.NewHBoxLayout(0, 0, 36, 1)
	row.Add(lbl, vtui.Margins{Right: 1}, vtui.AlignLeft)
	row.Add(combo, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(row, vtui.Margins{}, vtui.AlignFill)

	rowCond := vtui.NewHBoxLayout(0, 0, 36, 1)
	rowCond.Add(lblCond, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowCond.Add(comboCond, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowCond, vtui.Margins{Top: 1}, vtui.AlignFill)

	btns := vtui.NewHBoxLayout(0, 0, 36, 1)
	btns.HorizontalAlign = vtui.AlignCenter
	btns.Spacing = 2
	btns.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btns.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(btns, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		area := combo.Menu.Items[combo.Menu.SelectPos].Text
		cond := comboCond.Menu.Items[comboCond.Menu.SelectPos].Text
		if cond == "None" {
			cond = ""
		}

		fullName := actionName
		if cond != "" {
			fullName = actionName + ":" + cond
		}

		dlg.Close()
		vtui.FrameManager.PostTask(func() {
			vtui.FrameManager.Push(NewHotkeyAssignFrame(fullName, area, onComplete))
		})
	}
	vtui.FrameManager.Push(dlg)
}

type HotkeyAssignFrame struct {
	vtui.Window
	actionName string
	area       string
	onComplete func()
}

func NewHotkeyAssignFrame(actionName, area string, onComplete func()) *HotkeyAssignFrame {
	width, height := 42, 9
	base := vtui.NewCenteredDialog(width, height, Msg("Hotkeys.AssignTitle"))
	f := &HotkeyAssignFrame{
		Window:     *base,
		actionName: actionName,
		area:       area,
		onComplete: onComplete,
	}

	lblAction := vtui.NewText(0, 0, "Action: "+actionName, vtui.Palette[vtui.ColDialogText])
	lblArea := vtui.NewText(0, 0, "Area: "+area, vtui.Palette[vtui.ColDialogText])
	prompt := vtui.NewText(0, 0, "Press the desired key combination...", vtui.Palette[vtui.ColDialogText])
	cancelPrompt := vtui.NewText(0, 0, "Press Esc to cancel", vtui.Palette[vtui.ColDialogText])

	f.AddItem(lblAction)
	f.AddItem(lblArea)
	f.AddItem(prompt)
	f.AddItem(cancelPrompt)

	vbox := vtui.NewVBoxLayout(f.X1+2, f.Y1+2, width-4, height-4)
	vbox.Add(lblAction, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(lblArea, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(prompt, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(cancelPrompt, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	return f
}

func (f *HotkeyAssignFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e.Type == vtinput.FocusEventType {
		return f.Window.ProcessKey(e)
	}

	if !e.KeyDown {
		return false
	}

	if e.VirtualKeyCode == vtinput.VK_ESCAPE {
		f.Close()
		vtui.FrameManager.Redraw()
		return true
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
		vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
		vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
		vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
		return false
	}

	keyStr := EventToFarString(e)

	GlobalHotkeysMgr.Bind(f.area, keyStr, f.actionName)
	GlobalHotkeysMgr.Save()

	f.Close()
	if f.onComplete != nil {
		f.onComplete()
	}
	vtui.FrameManager.Redraw()
	return true
}

func (f *HotkeyAssignFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	return true
}
func (f *HotkeyAssignFrame) GetType() vtui.FrameType { return vtui.TypeDialog }
func (f *HotkeyAssignFrame) IsModal() bool           { return true }
