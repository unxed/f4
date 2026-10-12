package panel

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The mode dialog's column list (f4#410, part 2): the same columns the
// "Column types" and "Column widths" strings hold, shown one per row the way
// Far3's file panel modes are thought of -- a set of columns in an order,
// the same type allowed more than once. The list edits the two strings and
// nothing else: OK in the mode dialog still parses and validates them, so
// the list never needs its own copy of the column syntax. A token keeps its
// modifiers (SC, NM, DMB) as typed; the list only moves, adds and removes
// whole tokens and edits their widths.

// modeColumnRow is one column of the list: its type token and width text.
type modeColumnRow struct {
	token string
	width string
}

// panelColumnTypeChoices are the types Insert offers, in the order of the
// column syntax table in docs/ISSUES/ISSUE_1400_PANEL_MODES.md.
var panelColumnTypeChoices = []string{"N", "S", "P", "D", "T", "DM", "DC", "DA", "DE", "A", "O", "U", "LN"}

// modeColumnModifierChoices are the modifiers Insert offers after a type,
// one at a time, with what each does (montoner0, f4#410: the list showed S,
// not that SC works too). The ones kept only for far2l compatibility (NO, NR,
// SA, OL) change nothing and are left out; several at once are still typed
// by hand in the Column types string.
var modeColumnModifierChoices = map[string]string{
	"N": "M", "S": "CEFT", "P": "CEFT",
	"DM": "BM", "DC": "BM", "DA": "BM", "DE": "BM",
}

// modeColumnVariants is a type token followed by the token with each of its
// modifiers; a type without modifiers is only itself.
func modeColumnVariants(token string) []string {
	out := []string{token}
	for _, m := range modeColumnModifierChoices[token] {
		out = append(out, token+string(m))
	}
	return out
}

// modeColumnVariantLabel names a variant in the second Insert menu: the
// column's title, and for a modifier what it changes.
func modeColumnVariantLabel(base, token string) string {
	label := fmt.Sprintf("%-4s %s", token, modeColumnTitle(token))
	if mod := strings.TrimPrefix(token, base); mod != "" {
		kind := "Size"
		switch {
		case base == "N":
			kind = "Name"
		case strings.HasPrefix(base, "D"):
			kind = "Date"
		}
		label += " — " + i18n.Msg("Panel.Modes.Modifier."+kind+mod)
	}
	return label
}

// splitModeColumns pairs the comma separated types with their widths; a
// missing width is 0, the type's default, as TextToViewSettings reads it.
func splitModeColumns(types, widths string) []modeColumnRow {
	var rows []modeColumnRow
	ws := strings.Split(widths, ",")
	for i, token := range strings.Split(types, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		width := "0"
		if i < len(ws) && strings.TrimSpace(ws[i]) != "" {
			width = strings.TrimSpace(ws[i])
		}
		rows = append(rows, modeColumnRow{token: token, width: width})
	}
	return rows
}

// joinModeColumns is splitModeColumns backwards.
func joinModeColumns(rows []modeColumnRow) (types, widths string) {
	t := make([]string, len(rows))
	w := make([]string, len(rows))
	for i, row := range rows {
		t[i], w[i] = row.token, row.width
	}
	return strings.Join(t, ","), strings.Join(w, ",")
}

// modeColumnTitle names a column type token the way the panel header does.
func modeColumnTitle(token string) string {
	columns, err := TextToViewSettings(token, "0")
	if err != nil || len(columns) != 1 {
		return "?"
	}
	return panelColumnTitle(columns[0].Type)
}

func modeColumnLabel(row modeColumnRow) string {
	return fmt.Sprintf("%-5s %-24s %s", row.token, modeColumnTitle(row.token), row.width)
}

// showModeColumnsEditor opens the column list over the mode dialog. Insert
// adds a column after the cursor, Delete removes one, Ctrl+Up/Ctrl+Down move
// one, F4 or Enter edits its width; OK hands the result back as the two
// strings.
func showModeColumnsEditor(types, widths string, onOk func(types, widths string)) {
	if vtui.FrameManager == nil {
		return
	}
	rows := splitModeColumns(types, widths)

	const width = 64
	const height = 18
	dlg := vtui.NewCenteredDialog(width, height, " "+i18n.Msg("Panel.Modes.ColumnsTitle")+" ")
	dlg.ShowClose = true
	dlg.SetHelp("PanelModes")

	lb := vtui.NewListBox(dlg.X1+3, dlg.Y1+2, width-6, height-7, nil)
	hint := vtui.NewText(dlg.X1+3, dlg.Y2-4, i18n.Msg("Panel.Modes.ColumnsHint"), 0)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	refresh := func(pos int) {
		labels := make([]string, len(rows))
		for i, row := range rows {
			labels[i] = modeColumnLabel(row)
		}
		lb.Items = labels
		lb.UpdateRows()
		if pos >= 0 && pos < len(rows) {
			lb.SetSelectPos(pos)
		}
		vtui.FrameManager.Redraw()
	}

	insertColumn := func(token string) {
		at := 0
		if len(rows) > 0 {
			at = lb.SelectPos + 1
		}
		row := modeColumnRow{token: token, width: "0"}
		rows = append(rows[:at], append([]modeColumnRow{row}, rows[at:]...)...)
		refresh(at)
	}
	addColumn := func() {
		menu := vtui.NewVMenu(i18n.Msg("Panel.Modes.AddColumn"))
		for _, token := range panelColumnTypeChoices {
			menu.AddItem(vtui.MenuItem{Text: fmt.Sprintf("%-3s %s", token, modeColumnTitle(token))})
		}
		menu.OnAction = func(idx int) {
			if idx < 0 || idx >= len(panelColumnTypeChoices) {
				return
			}
			base := panelColumnTypeChoices[idx]
			variants := modeColumnVariants(base)
			if len(variants) == 1 {
				insertColumn(base)
				return
			}
			// A type with modifiers asks which variant (f4#410).
			vtui.FrameManager.PostTask(func() {
				sub := vtui.NewVMenu(base)
				for _, token := range variants {
					sub.AddItem(vtui.MenuItem{Text: modeColumnVariantLabel(base, token)})
				}
				sub.OnAction = func(i int) {
					if i >= 0 && i < len(variants) {
						insertColumn(variants[i])
					}
				}
				showModeColumnsMenu(sub, 56, len(variants))
			})
		}
		showModeColumnsMenu(menu, 36, len(panelColumnTypeChoices))
	}
	editWidth := func() {
		pos := lb.SelectPos
		if pos < 0 || pos >= len(rows) {
			return
		}
		vtui.InputBox(" "+rows[pos].token+" ", i18n.Msg("Panel.Modes.ColumnWidthPrompt"), rows[pos].width, func(text string) {
			text = strings.TrimSpace(text)
			if text == "" {
				text = "0"
			}
			rows[pos].width = text
			refresh(pos)
		})
	}

	lb.OnKeyDown = func(e *vtinput.InputEvent) bool {
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		pos := lb.SelectPos
		switch {
		case e.VirtualKeyCode == vtinput.VK_INSERT:
			addColumn()
		case e.VirtualKeyCode == vtinput.VK_DELETE:
			if pos >= 0 && pos < len(rows) {
				rows = append(rows[:pos], rows[pos+1:]...)
				refresh(min(pos, len(rows)-1))
			}
		case ctrl && e.VirtualKeyCode == vtinput.VK_UP:
			if pos > 0 && pos < len(rows) {
				rows[pos-1], rows[pos] = rows[pos], rows[pos-1]
				refresh(pos - 1)
			}
		case ctrl && e.VirtualKeyCode == vtinput.VK_DOWN:
			if pos >= 0 && pos+1 < len(rows) {
				rows[pos], rows[pos+1] = rows[pos+1], rows[pos]
				refresh(pos + 1)
			}
		case e.VirtualKeyCode == vtinput.VK_F4 || e.VirtualKeyCode == vtinput.VK_RETURN:
			editWidth()
		default:
			return false
		}
		return true
	}

	dlg.AddItem(lb)
	dlg.AddItem(hint)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	btnRow := vtui.NewHBoxLayout(dlg.X1+3, dlg.Y2-2, width-6, 1)
	btnRow.HorizontalAlign = vtui.AlignCenter
	btnRow.Spacing = 2
	btnRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	btnRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	btnRow.Apply()

	btnOk.OnClick = func() {
		if onOk != nil {
			onOk(joinModeColumns(rows))
		}
		dlg.SetExitCode(1)
	}
	btnCancel.OnClick = func() { dlg.SetExitCode(-1) }

	refresh(0)
	dlg.SetFocusedItem(lb)
	vtui.FrameManager.Push(dlg)
}

// showModeColumnsMenu centres an Insert menu of n rows, w columns wide.
func showModeColumnsMenu(menu *vtui.VMenu, w, n int) {
	screenW, screenH := vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight()
	w = min(w, screenW)
	h := min(n+2, screenH)
	x := max(0, (screenW-w)/2)
	y := max(0, (screenH-h)/2)
	menu.SetPosition(x, y, x+w-1, y+h-1)
	vtui.FrameManager.Push(menu)
}
