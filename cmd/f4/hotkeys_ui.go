package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type hotkeyRow struct {
	Action    string
	Label     string
	Area      string
	Key       string
	RawKey    string
	Editable  bool
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
	if r.Key == "" || !r.Editable {
		return vtui.DimColor(def)
	}
	return def
}

func hotkeyDialogSizeForScreen(screenWidth, screenHeight int) (int, int) {
	if screenWidth <= 0 {
		screenWidth = 120
	}
	if screenHeight <= 0 {
		screenHeight = 48
	}
	width := screenWidth - 4
	if width < 40 {
		width = 40
	}
	height := screenHeight - 2
	if height < 10 {
		height = 10
	}
	return width, height
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func fitHotkeyColumnWidths(widths []int, budget int) []int {
	if budget < 0 {
		budget = 0
	}
	for sumInts(widths) > budget {
		changed := false
		for i := range widths {
			if widths[i] <= 1 {
				continue
			}
			widths[i]--
			changed = true
			if sumInts(widths) <= budget {
				break
			}
		}
		if !changed {
			break
		}
	}
	return widths
}

func hotkeyTableColumns(rows []hotkeyRow, dialogWidth int) []vtui.TableColumn {
	titles := []string{"Command", "Key", "Area", "When", "Description"}
	widths := make([]int, len(titles))
	for i, title := range titles {
		widths[i] = vtui.StringWidth(title)
	}
	for _, row := range rows {
		for col := range widths {
			if cellWidth := vtui.StringWidth(row.GetCellText(col)); cellWidth > widths[col] {
				widths[col] = cellWidth
			}
		}
	}

	// The description column remains elastic. Reserve its header, the four
	// column separators, and the table's side padding before fitting the other
	// columns to the actual dialog width.
	fixedBudget := dialogWidth - 4 - (len(widths) - 1) - vtui.StringWidth(titles[len(titles)-1])
	fixed := fitHotkeyColumnWidths(append([]int(nil), widths[:len(widths)-1]...), fixedBudget)
	columns := make([]vtui.TableColumn, 0, len(widths))
	for i, width := range fixed {
		columns = append(columns, vtui.TableColumn{Title: titles[i], Width: width})
	}
	columns = append(columns, vtui.TableColumn{Title: titles[len(titles)-1], Width: 0})
	return columns
}

func selectedHotkeyRowAt(table *vtui.Table, rows []hotkeyRow, displayPos int) (hotkeyRow, bool) {
	if table == nil {
		return hotkeyRow{}, false
	}
	idx := table.RowAt(displayPos)
	if idx < 0 || idx >= len(rows) {
		return hotkeyRow{}, false
	}
	return rows[idx], true
}

func normalizeHotkeySearchQuery(query string) string {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '+' {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(query))
	if compact == "" {
		return query
	}

	var raw strings.Builder
	remaining := compact
	for {
		matched := false
		for _, modifier := range []struct {
			name  string
			canon string
		}{
			{name: "rctrl", canon: "RCtrl"},
			{name: "ctrl", canon: "Ctrl"},
			{name: "alt", canon: "Alt"},
			{name: "shift", canon: "Shift"},
		} {
			if strings.HasPrefix(remaining, modifier.name) && len(remaining) > len(modifier.name) {
				raw.WriteString(modifier.canon)
				remaining = remaining[len(modifier.name):]
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	if raw.Len() == 0 || remaining == "" {
		return query
	}

	key := map[string]string{
		"up": "Up", "down": "Down", "left": "Left", "right": "Right",
		"home": "Home", "end": "End", "pgup": "PgUp", "pgdn": "PgDn",
		"ins": "Ins", "del": "Del", "enter": "Enter", "esc": "Esc",
		"tab": "Tab", "back": "Back", "space": "Space",
	}[remaining]
	if key == "" {
		if len(remaining) == 1 || (remaining[0] == 'f' && len(remaining) <= 3) {
			key = strings.ToUpper(remaining)
		} else {
			return query
		}
	}
	raw.WriteString(key)
	if strings.HasPrefix(raw.String(), "RCtrl") {
		return raw.String()
	}
	return FormatKeyForUI(raw.String())
}

func configureHotkeyTableSearch(table *vtui.Table) {
	table.QuickSearch = true
	table.SearchExactOnHit = AppConfig.SearchExactOnHit
	normalizing := false
	table.OnSearchChange = func(text string) {
		if normalizing {
			return
		}
		normalized := normalizeHotkeySearchQuery(text)
		if normalized == text {
			return
		}
		normalizing = true
		table.SetSearchText(normalized)
		normalizing = false
	}
}

func selectedHotkeyRow(table *vtui.Table, rows []hotkeyRow) (hotkeyRow, bool) {
	if table == nil {
		return hotkeyRow{}, false
	}
	return selectedHotkeyRowAt(table, rows, table.SelectPos)
}

// buildHotkeyRows assembles the shortcut inventory the hotkey settings dialog
// shows: one editable row per configurable binding, one read-only row per
// framework-owned native chord, and one editable row for every action that has
// no configurable binding yet.
func buildHotkeyRows(draft *HotkeyManager) []hotkeyRow {
	if draft == nil {
		return nil
	}

	var hkRows []hotkeyRow
	activeBinds := draft.GetActiveBindings()
	actions := GetActions()
	// Only configurable bindings count as assigned here. Native chords are
	// documented below but deliberately stay out of this set.
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
				Key:       FormatKeyForUI(key),
				RawKey:    key,
				Editable:  true,
				Condition: cond,
				Desc:      act.DisplayDescription(),
			})
			assignedActions[strings.ToLower(act.Name)] = true
		}
	}

	// Native shortcuts are handled by the focused frame rather than the
	// configurable hotkey manager. They still belong in this inventory so
	// the dialog describes every shortcut the user can press. Keep these
	// rows read-only: assigning them would create a misleading binding that
	// cannot replace the frame-owned behavior.
	//
	// They must not mark the action as assigned, though. An action whose only
	// shortcut is native -- Next Workspace on Ctrl+Tab -- would otherwise get
	// no editable row at all from the loop below, and both Assign and Enter
	// gate on hotkeyRow.Editable, so the dialog offered no way to give it a
	// second key. That is exactly the case a user hits when the host swallows
	// the native chord: Ctrl+Tab switches browser tabs when f4 runs in a
	// browser, and iTerm2 over ssh does not deliver it either (issue #72).
	// Dispatch already honors such a binding -- MacroManager.Filter resolves
	// it through configuredHotkeyAction and RunAction, with Common as the
	// fallback area -- so only this inventory stood in the way. The read-only
	// native row stays alongside the editable one.
	for _, act := range actions {
		seenNative := make(map[string]bool)
		for _, spec := range act.NativeKeys {
			key, cond, _ := strings.Cut(spec, ":")
			key = strings.TrimSpace(key)
			displayKey := FormatKeyForUI(key)
			if key == "" || seenNative[displayKey] {
				continue
			}
			if draft.GetAction(act.Area, key) != "" {
				continue
			}
			seenNative[displayKey] = true
			hkRows = append(hkRows, hotkeyRow{
				Action:    act.Name,
				Label:     plainLabel(act.DisplayLabel()),
				Area:      act.Area,
				Key:       displayKey,
				RawKey:    key,
				Condition: cond,
				Desc:      act.DisplayDescription(),
			})
		}
	}

	for _, act := range actions {
		if !assignedActions[strings.ToLower(act.Name)] {
			hkRows = append(hkRows, hotkeyRow{
				Action:    act.Name,
				Label:     plainLabel(act.DisplayLabel()),
				Area:      act.Area, // unassigned, shown under the action's native area
				Key:       "",
				Editable:  true,
				Condition: "",
				Desc:      act.DisplayDescription(),
			})
		}
	}

	// Plugin menu commands live outside the built-in action registry, but they
	// use the same persisted binding format. Show every loaded command so a
	// shortcut can be prepared even while its context-sensitive menu item is
	// currently hidden.
	for _, act := range pluginHotkeyActionsSnapshot() {
		if assignedActions[strings.ToLower(act.Name)] {
			continue
		}
		key := pluginActionShortcut(act.Name)
		if key == "" {
			key = pluginActionDefaultShortcut(act.Name)
		}
		rawKey := ""
		if configured := pluginActionConfiguredKey(act.Name); configured != "" {
			rawKey = configured
		}
		hkRows = append(hkRows, hotkeyRow{
			Action:   act.Name,
			Label:    plainLabel(act.DisplayLabel()),
			Area:     act.Area,
			Key:      key,
			RawKey:   rawKey,
			Editable: true,
			Desc:     act.DisplayDescription(),
		})
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
		if hkRows[i].Label != hkRows[j].Label {
			return hkRows[i].Label < hkRows[j].Label
		}
		// One action can now contribute both a native row and the empty
		// row that offers an extra key. Order that pair deterministically,
		// chord first, so the empty row reads as "and you may add one here"
		// instead of appearing above the shortcut it complements.
		return hkRows[i].Key > hkRows[j].Key
	})

	return hkRows
}

func actionHotkeyConfig(pf *PanelsFrame) {
	w, h := 120, 48
	if vtui.FrameManager != nil {
		w, h = hotkeyDialogSizeForScreen(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	}

	btnAssign := vtui.NewButton(0, 0, Msg("Hotkeys.BtnAssign"))
	btnUnbind := vtui.NewButton(0, 0, Msg("Hotkeys.BtnUnbind"))
	btnSave := vtui.NewButton(0, 0, Msg("vtui.Save"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	btnSave.IsDefault = true

	if GlobalHotkeysMgr == nil {
		return
	}
	original := GlobalHotkeysMgr
	draft := original.CloneForEdit()

	dlg, table := vtui.NewTableDialog(w, h, Msg("Hotkeys.Title"), []vtui.TableColumn{
		{Title: "Command", Width: 23},
		{Title: "Key", Width: 14},
		{Title: "Area", Width: 10},
		{Title: "When", Width: 17},
		{Title: "Description", Width: 0},
	}, btnAssign, btnUnbind, btnSave, btnCancel)
	useDialogTableColors(table)
	table.ShowScrollBar = true
	table.Sortable = true // click a column header to sort, again to reverse
	//table.QuickSearch = true // type to fuzzy-filter (Myers bit-vector)
	configureHotkeyTableSearch(table)

	var rows []vtui.TableRow
	var hkRows []hotkeyRow

	refresh := func() {
		rows = nil
		hkRows = buildHotkeyRows(draft)

		for _, r := range hkRows {
			rows = append(rows, r)
		}
		table.Columns = hotkeyTableColumns(hkRows, w)
		table.SetRows(rows)
		vtui.FrameManager.Redraw()
	}

	btnAssign.OnClick = func() {
		if row, ok := selectedHotkeyRow(table, hkRows); ok && row.Editable {
			showAreaSelectDialog(draft, row.Action, row.Area, row.Condition, refresh)
		}
	}

	btnUnbind.OnClick = func() {
		if row, ok := selectedHotkeyRow(table, hkRows); ok && row.Editable {
			if row.RawKey != "" && row.Area != "" {
				question := fmt.Sprintf("%s %s?", plainLabel(Msg("Hotkeys.BtnUnbind")), row.Key)
				vtui.ShowMessageOn(dlg, Msg("Hotkeys.Title"), question, []string{Msg("vtui.Ok"), Msg("vtui.Cancel")}).OnResult = func(choice int) {
					if choice != 0 {
						return
					}
					draft.Bind(row.Area, row.RawKey, "None")
					refresh()
				}
			}
		}
	}

	btnSave.OnClick = func() {
		original.ReplaceBindingsFrom(draft)
		original.Save()
		dlg.Close()
	}

	btnCancel.OnClick = func() { dlg.Close() }

	table.OnAction = func(idx int) {
		// idx is the table's display position. Use it directly: the table may
		// be sorted or QuickSearch-filtered, and SelectPos can be observed
		// after the dispatcher's state has moved on.
		if row, ok := selectedHotkeyRowAt(table, hkRows, idx); ok && row.Editable {
			showAreaSelectDialog(draft, row.Action, row.Area, row.Condition, refresh)
		}
	}

	vtui.FrameManager.Push(dlg)
	refresh()
}

func showAreaSelectDialog(hm *HotkeyManager, actionName, defaultArea, defaultCond string, onComplete func()) {
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
			vtui.FrameManager.Push(NewHotkeyAssignFrame(hm, fullName, area, onComplete))
		})
	}
	vtui.FrameManager.Push(dlg)
}

type HotkeyAssignFrame struct {
	*vtui.Window
	hm         *HotkeyManager
	actionName string
	area       string
	onComplete func()
}

func NewHotkeyAssignFrame(hm *HotkeyManager, actionName, area string, onComplete func()) *HotkeyAssignFrame {
	width, height := 42, 9
	base := vtui.NewCenteredDialog(width, height, Msg("Hotkeys.AssignTitle"))
	f := &HotkeyAssignFrame{
		Window:     base,
		hm:         hm,
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

	keyStr := EventToHotkeyString(e)

	if f.hm != nil {
		f.hm.Bind(f.area, keyStr, f.actionName)
	}

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
