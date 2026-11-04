package app

import (
	"fmt"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Built-in calendar (f4#1601, another idea carried over from DOS Navigator,
// like f4#384's calculator right above it): a small popup with a month
// grid, PgUp/PgDn to change month, Ctrl+PgUp/PgDn to change year, Home to
// jump back to today, and an Insert button that writes the highlighted date
// into the active panel's command line the same way the calculator's own
// Insert button writes its result (insertTextIntoCommandLine, defined in
// calculator_ui.go and reused here as-is).

const calendarDialogWidth = 44

// calendarDialogHeight follows the same vbox math calculator_ui.go's own
// comment documents (and calcDialogHeight's fix, f4#384, got right): the
// vbox starts at Y1+2, each stacked item consumes
// Margins.Top + height + Margins.Bottom, and the dialog needs 2 more rows
// above (border + clearance) and 2 below (clearance + border). Stack here
// is: month/year label (h=1, Margins{}) + the day grid (h=1+calendarWeeks,
// Margins{Top:1}) + a key hint label (h=1, Margins{Top:1}) + the button row
// (h=1, Margins{Top:1}) = 1 + (1+1+calendarWeeks) + 2 + 2 = 7+calendarWeeks
// rows of content, so height = 4 + 7 + calendarWeeks.
const calendarDialogHeight = 4 + 7 + calendarWeeks

// calendarWeeks is how many Monday-first weeks the grid always shows.
// Six covers every possible month (including a 31-day month starting on a
// Sunday), so switching months never has to resize the dialog.
const calendarWeeks = 6

// calendarColumnWidth is the day grid's per-column width: 2 digits plus a
// blank column so single-digit days ("1".."9") do not visually crowd the
// weekday header above them.
const calendarColumnWidth = 4

// calendarDay is one grid cell: which day of the displayed month it shows,
// if any (0 means the cell is blank padding before day 1 or after the
// month's last day), and whether that day is today in local time.
type calendarDay struct {
	day     int
	isToday bool
}

// calendarWeekRow is one row (week) of the grid. It implements
// vtui.TableRow (GetCellText) and vtui.CellColorableRow (GetCellAttr, for
// the "today" highlight). todayIdx is the palette index used for today's
// cell, taken from the embedding table's ColorHighlightIdx so the colour
// follows the active theme instead of a hardcoded one.
type calendarWeekRow struct {
	days     [7]calendarDay
	todayIdx int
}

func (r calendarWeekRow) GetCellText(col int) string {
	d := r.days[col]
	if d.day == 0 {
		return ""
	}
	return fmt.Sprintf("%d", d.day)
}

// GetCellAttr highlights today's cell with the table's own highlight colour
// (ColorHighlightIdx), which the active theme can override, so the calendar
// never paints a colour the theme did not choose. It does not special-case
// the cursor already sitting on today -- defaultAttr already reflects
// cursor/selection state, and the highlight simply wins there too.
func (r calendarWeekRow) GetCellAttr(col int, defaultAttr uint64) uint64 {
	if r.days[col].isToday {
		return vtui.Palette[r.todayIdx]
	}
	return defaultAttr
}

// calendarWeekdayKeys are the i18n keys for the grid's Monday-first column
// headers.
var calendarWeekdayKeys = [7]string{
	"Calendar.Mon", "Calendar.Tue", "Calendar.Wed", "Calendar.Thu",
	"Calendar.Fri", "Calendar.Sat", "Calendar.Sun",
}

func calendarColumns() []vtui.TableColumn {
	cols := make([]vtui.TableColumn, 7)
	for i, key := range calendarWeekdayKeys {
		cols[i] = vtui.TableColumn{Title: i18n.Msg(key), Width: calendarColumnWidth, Alignment: vtui.AlignRight}
	}
	return cols
}

// buildCalendarWeeks lays year/month out into calendarWeeks Monday-first
// rows, blank-padding the days before the 1st and after the month's last
// day so every month always produces exactly calendarWeeks rows.
func buildCalendarWeeks(year int, month time.Month, todayIdx int) []vtui.TableRow {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.Local)
	// time.Weekday is Sunday=0..Saturday=6; +6 %7 rotates it to
	// Monday=0..Sunday=6, how many blank cells precede day 1.
	startOffset := (int(first.Weekday()) + 6) % 7
	daysInMonth := time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
	now := time.Now()
	isCurrentMonth := now.Year() == year && now.Month() == month

	rows := make([]vtui.TableRow, calendarWeeks)
	day := 1 - startOffset
	for w := 0; w < calendarWeeks; w++ {
		row := calendarWeekRow{todayIdx: todayIdx}
		for c := 0; c < 7; c++ {
			if day >= 1 && day <= daysInMonth {
				row.days[c] = calendarDay{day: day, isToday: isCurrentMonth && day == now.Day()}
			}
			day++
		}
		rows[w] = row
	}
	return rows
}

// calendarTable embeds *vtui.Table and overrides only ProcessKey, so the
// grid gets a Table's whole existing arrow/PgUp/PgDn/quick-search machinery
// for free (via Go's method promotion) while PgUp/PgDn/Ctrl+PgUp/Ctrl+PgDn/
// Home get repurposed for month/year/today navigation before they ever
// reach the embedded Table's own paging.
type calendarTable struct {
	*vtui.Table
	onMonth func(delta int)
	onYear  func(delta int)
	onToday func()
}

func (c *calendarTable) ProcessKey(e *vtinput.InputEvent) bool {
	if e != nil && e.KeyDown {
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		switch e.VirtualKeyCode {
		case vtinput.VK_PRIOR:
			if ctrl {
				c.onYear(-1)
			} else {
				c.onMonth(-1)
			}
			return true
		case vtinput.VK_NEXT:
			if ctrl {
				c.onYear(1)
			} else {
				c.onMonth(1)
			}
			return true
		case vtinput.VK_HOME:
			if !ctrl {
				c.onToday()
				return true
			}
		}
	}
	return c.Table.ProcessKey(e)
}

func showCalendarDialog() {
	dlg := vtui.NewCenteredDialog(calendarDialogWidth, calendarDialogHeight, i18n.Msg("Calendar.Title"))
	dlg.ShowClose = true

	now := time.Now()
	year, month := now.Year(), now.Month()

	lblMonth := vtui.NewLabel(0, 0, "", nil)
	lblHint := vtui.NewLabel(0, 0, i18n.Msg("Calendar.Hint"), nil)

	gridWidth := 7*calendarColumnWidth + 6 // 6 one-cell separators between columns
	table := &calendarTable{Table: vtui.NewTable(0, 0, gridWidth, 1+calendarWeeks, calendarColumns())}
	table.ShowHeader = true
	table.CellSelection = true
	// Today's cell is a highlight inside a table, not a menu match, so it must
	// follow the table selection colour: themes tune table colours separately
	// from menu colours (ColMenuHighlight, the vtui.NewTable default, leaks the
	// menu's cyan background into the grid).
	table.ColorHighlightIdx = vtui.ColTableSelectedText

	btnInsert := vtui.NewButton(0, 0, i18n.Msg("Calendar.BtnInsert"))
	btnInsert.IsDefault = true
	btnClose := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	refresh := func() {
		lblMonth.SetText(fmt.Sprintf("%s %d", i18n.Msg(fmt.Sprintf("Group.Month%d", int(month))), year))
		table.SetRows(buildCalendarWeeks(year, month, table.ColorHighlightIdx))
	}

	// clampSelection walks the cursor backward (same weekday column, one
	// week at a time) off a blank padding cell -- reachable after PgUp/PgDn
	// rebuilds the grid under an unmoved cursor position, e.g. day 31
	// selected and the new month has 30 days or fewer.
	clampSelection := func() {
		for tries := 0; tries < calendarWeeks*7; tries++ {
			idx := table.RowAt(table.SelectPos)
			if idx < 0 || idx >= len(table.Rows) {
				return
			}
			row, ok := table.Rows[idx].(calendarWeekRow)
			if !ok || row.days[table.SelectCol].day != 0 {
				return
			}
			if !table.MoveSelection(-1) {
				return
			}
		}
	}

	selectToday := func() {
		for w, r := range table.Rows {
			row, ok := r.(calendarWeekRow)
			if !ok {
				continue
			}
			for c := 0; c < 7; c++ {
				if row.days[c].isToday {
					table.SelectPos, table.SelectCol = w, c
					return
				}
			}
		}
	}

	table.onMonth = func(delta int) {
		total := int(month) - 1 + delta
		year += total / 12
		total %= 12
		if total < 0 {
			total += 12
			year--
		}
		month = time.Month(total + 1)
		refresh()
		clampSelection()
	}
	table.onYear = func(delta int) {
		year += delta
		refresh()
		clampSelection()
	}
	table.onToday = func() {
		today := time.Now()
		year, month = today.Year(), today.Month()
		refresh()
		selectToday()
	}

	refresh()
	selectToday()

	dlg.AddItem(lblMonth)
	dlg.AddItem(table)
	dlg.AddItem(lblHint)
	dlg.AddItem(btnInsert)
	dlg.AddItem(btnClose)
	dlg.SetFocusedItem(table)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, calendarDialogWidth-4, calendarDialogHeight-4)
	vbox.Add(lblMonth, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(table, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(lblHint, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, calendarDialogWidth-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnInsert, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnClose, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnInsert.OnClick = func() {
		idx := table.RowAt(table.SelectPos)
		var day int
		if idx >= 0 && idx < len(table.Rows) {
			if row, ok := table.Rows[idx].(calendarWeekRow); ok {
				day = row.days[table.SelectCol].day
			}
		}
		dlg.Close()
		if day == 0 {
			return
		}
		text := time.Date(year, month, day, 0, 0, 0, 0, time.Local).Format("2006-01-02")
		insertTextIntoCommandLine(text)
	}
	btnClose.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}
