package app

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/theme"
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
	return keymap.FormatKeyForUI(raw.String())
}

func configureHotkeyTableSearch(table *vtui.Table) {
	table.QuickSearch = true
	table.SearchExactOnHit = config.App.SearchExactOnHit
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
func buildHotkeyRows(draft *keymap.HotkeyManager) []hotkeyRow {
	if draft == nil {
		return nil
	}

	var hkRows []hotkeyRow
	activeBinds := draft.GetActiveBindings()
	actions := action.AllSorted()
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
				act = action.Action{Name: actName, Label: actName, Description: "Unknown action"}
			}
			hkRows = append(hkRows, hotkeyRow{
				Action:    act.Name,
				Label:     action.PlainLabel(act.DisplayLabel()),
				Area:      area,
				Key:       keymap.FormatKeyForUI(key),
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
	// Dispatch already honors such a binding -- macro.MacroManager.Filter resolves
	// it through keymap.ConfiguredHotkeyAction and RunAction, with Common as the
	// fallback area -- so only this inventory stood in the way. The read-only
	// native row stays alongside the editable one.
	for _, act := range actions {
		seenNative := make(map[string]bool)
		for _, spec := range act.NativeKeys {
			key, cond, _ := strings.Cut(spec, ":")
			key = strings.TrimSpace(key)
			displayKey := keymap.FormatKeyForUI(key)
			if key == "" || seenNative[displayKey] {
				continue
			}
			if draft.GetAction(act.Area, key) != "" {
				continue
			}
			seenNative[displayKey] = true
			hkRows = append(hkRows, hotkeyRow{
				Action:    act.Name,
				Label:     action.PlainLabel(act.DisplayLabel()),
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
				Label:     action.PlainLabel(act.DisplayLabel()),
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
	for _, act := range panel.PluginHotkeyActionsSnapshot() {
		if assignedActions[strings.ToLower(act.Name)] {
			continue
		}
		key := panel.PluginActionShortcut(act.Name)
		if key == "" {
			key = panel.PluginActionDefaultShortcut(act.Name)
		}
		rawKey := ""
		if configured := panel.PluginActionConfiguredKey(act.Name); configured != "" {
			rawKey = configured
		}
		hkRows = append(hkRows, hotkeyRow{
			Action:   act.Name,
			Label:    action.PlainLabel(act.DisplayLabel()),
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

func actionHotkeyConfig(pf *panel.PanelsFrame) {
	w, h := 120, 48
	if vtui.FrameManager != nil {
		w, h = hotkeyDialogSizeForScreen(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	}

	btnAssign := vtui.NewButton(0, 0, i18n.Msg("Hotkeys.BtnAssign"))
	btnUnbind := vtui.NewButton(0, 0, i18n.Msg("Hotkeys.BtnUnbind"))
	btnSave := vtui.NewButton(0, 0, i18n.Msg("vtui.Save"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	btnSave.IsDefault = true

	if keymap.GlobalHotkeysMgr == nil {
		return
	}
	original := keymap.GlobalHotkeysMgr
	draft := original.CloneForEdit()

	dlg, table := vtui.NewTableDialog(w, h, i18n.Msg("Hotkeys.Title"), []vtui.TableColumn{
		{Title: i18n.Msg("Hotkeys.ColCommand"), Width: 23},
		{Title: i18n.Msg("Hotkeys.ColKey"), Width: 14},
		{Title: i18n.Msg("Hotkeys.ColArea"), Width: 10},
		{Title: i18n.Msg("Hotkeys.ColWhen"), Width: 17},
		{Title: i18n.Msg("Hotkeys.ColDescription"), Width: 0},
	}, btnAssign, btnUnbind, btnSave, btnCancel)
	refresh := configureHotkeyEditor(dlg, table, btnAssign, btnUnbind, draft, nil, w)

	btnSave.OnClick = func() {
		original.ReplaceBindingsFrom(draft)
		original.Save()
		dlg.Close()
	}

	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
	refresh()
}

func showAreaSelectDialog(hm *keymap.HotkeyManager, actionName, defaultArea, defaultCond string, onComplete func()) {
	dlg := vtui.NewCenteredDialog(40, 11, i18n.Msg("Hotkeys.SelectTitle"))
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

	conds := keymap.GetConditions()
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

	btnOk := vtui.NewButton(0, 0, i18n.Msg("Hotkeys.BtnNext"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	lbl := vtui.NewLabel(0, 0, i18n.Msg("Hotkeys.LabelArea"), combo)
	lblCond := vtui.NewLabel(0, 0, i18n.Msg("Hotkeys.LabelWhen"), comboCond)

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
			vtui.FrameManager.Push(dialog.NewHotkeyAssignFrame(hm, fullName, area, onComplete))
		})
	}
	vtui.FrameManager.Push(dlg)
}

// configureHotkeyEditor is shared by the legacy dialog and its embedded Settings tab.
func configureHotkeyEditor(dlg *vtui.Window, table *vtui.Table, btnAssign, btnUnbind *vtui.Button, draft *keymap.HotkeyManager, onChange func(*keymap.HotkeyManager), w int) func() {
	theme.UseTableColors(table)
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
		width := w
		if table.X2 > table.X1 {
			width = table.X2 - table.X1 + 5
		}
		table.Columns = hotkeyTableColumns(hkRows, width)
		table.SetRows(rows)
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}

	changed := func() {
		if onChange != nil {
			onChange(draft)
		}
		refresh()
	}
	btnAssign.OnClick = func() {
		if row, ok := selectedHotkeyRow(table, hkRows); ok && row.Editable {
			showAreaSelectDialog(draft, row.Action, row.Area, row.Condition, changed)
		}
	}

	btnUnbind.OnClick = func() {
		if row, ok := selectedHotkeyRow(table, hkRows); ok && row.Editable {
			if row.RawKey != "" && row.Area != "" {
				question := fmt.Sprintf("%s %s?", action.PlainLabel(i18n.Msg("Hotkeys.BtnUnbind")), row.Key)
				vtui.ShowMessageOn(dlg, i18n.Msg("Hotkeys.Title"), question, []string{i18n.Msg("vtui.Ok"), i18n.Msg("vtui.Cancel")}).OnResult = func(choice int) {
					if choice != 0 {
						return
					}
					draft.Bind(row.Area, row.RawKey, "None")
					changed()
				}
			}
		}
	}

	table.OnAction = func(idx int) {
		// idx is the table's display position. Use it directly: the table may
		// be sorted or QuickSearch-filtered, and SelectPos can be observed
		// after the dispatcher's state has moved on.
		if row, ok := selectedHotkeyRowAt(table, hkRows, idx); ok && row.Editable {
			showAreaSelectDialog(draft, row.Action, row.Area, row.Condition, changed)
		}
	}

	return refresh
}

type hotkeyPage struct {
	*vtui.Group
	table          *vtui.Table
	assign, unbind *vtui.Button
}

// Keep vertical navigation inside the configurator, while Tab can still leave it.
func (p *hotkeyPage) ProcessKey(e *vtinput.InputEvent) bool {
	previous := p.WrapFocus
	p.WrapFocus = e.KeyDown && (e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_DOWN)
	defer func() { p.WrapFocus = previous }()
	return p.Group.ProcessKey(e)
}

func (p *hotkeyPage) SetPosition(x1, y1, x2, y2 int) {
	p.Group.SetPosition(x1, y1, x2, y2)
	p.table.SetPosition(x1, y1, x2, y2-2)
	rows := make([]hotkeyRow, 0, len(p.table.Rows))
	for _, row := range p.table.Rows {
		if r, ok := row.(hotkeyRow); ok {
			rows = append(rows, r)
		}
	}
	p.table.Columns = hotkeyTableColumns(rows, x2-x1+5)
	x := x1
	for _, button := range []*vtui.Button{p.assign, p.unbind} {
		width := vtui.StringWidth(button.GetCaption()) + 4
		button.SetPosition(x, y2, min(x2, x+width-1), y2)
		x += width + 1
	}
}
func (settingsHost) HotkeyPage(owner *vtui.Window, onChange func(*keymap.HotkeyManager)) vtui.UIElement {
	p := &hotkeyPage{Group: vtui.NewGroup(0, 0, 40, 10)}
	p.SetId("hotkey-configurator")
	p.table = vtui.NewTable(0, 0, 40, 7, hotkeyTableColumns(nil, 44))
	p.table.SetId("hotkey-table")
	p.assign = vtui.NewButton(0, 0, i18n.Msg("Hotkeys.BtnAssign"))
	p.assign.SetId("hotkey-assign")
	p.unbind = vtui.NewButton(0, 0, i18n.Msg("Hotkeys.BtnUnbind"))
	p.unbind.SetId("hotkey-unbind")
	p.AddItem(p.table)
	p.AddItem(p.assign)
	p.AddItem(p.unbind)
	if keymap.GlobalHotkeysMgr == nil {
		p.SetDisabled(true)
		return p
	}
	refresh := configureHotkeyEditor(owner, p.table, p.assign, p.unbind, keymap.GlobalHotkeysMgr.CloneForEdit(), onChange, 44)
	refresh()
	p.SetFocusedItem(p.table)
	return p
}
