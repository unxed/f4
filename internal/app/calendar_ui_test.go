package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// calendarMonthLabel mirrors the "%s %d" template showCalendarDialog's own
// refresh closure feeds lblMonth, so tests can predict the label text without
// hardcoding a locale's month names.
func calendarMonthLabel(year int, month time.Month) string {
	return fmt.Sprintf("%s %d", i18n.Msg(fmt.Sprintf("Group.Month%d", int(month))), year)
}

// TestBuildCalendarWeeksLayout pins buildCalendarWeeks's month-grid math
// (f4#1601) against weekdays independently confirmed with `date -d`, not
// re-derived from time.Weekday: January 2026 starts on a Thursday, February
// 2026 and March 2026 on a Sunday, April 2026 on a Wednesday, and February
// 2024 (a leap year) on a Thursday.
func TestBuildCalendarWeeksLayout(t *testing.T) {
	tests := []struct {
		name        string
		year        int
		month       time.Month
		startOffset int // blank cells before day 1, Monday-first
		daysInMonth int
	}{
		{"January 2026, Thursday start", 2026, time.January, 3, 31},
		{"February 2026, Sunday start", 2026, time.February, 6, 28},
		{"March 2026, Sunday start, 31 days (max fill)", 2026, time.March, 6, 31},
		{"April 2026, Wednesday start", 2026, time.April, 2, 30},
		{"February 2024, leap year, Thursday start", 2024, time.February, 3, 29},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := buildCalendarWeeks(tc.year, tc.month, vtui.ColMenuHighlight)
			if len(rows) != calendarWeeks {
				t.Fatalf("len(rows) = %d, want %d", len(rows), calendarWeeks)
			}

			var flat []calendarDay
			for _, r := range rows {
				row, ok := r.(calendarWeekRow)
				if !ok {
					t.Fatalf("row type = %T, want calendarWeekRow", r)
				}
				flat = append(flat, row.days[:]...)
			}
			if len(flat) != calendarWeeks*7 {
				t.Fatalf("flattened cell count = %d, want %d", len(flat), calendarWeeks*7)
			}

			nonZero := 0
			firstIdx, lastIdx := -1, -1
			for i, d := range flat {
				if d.isToday {
					t.Fatalf("cell %d marked isToday for a non-current month", i)
				}
				if d.day == 0 {
					continue
				}
				nonZero++
				if firstIdx == -1 {
					firstIdx = i
				}
				lastIdx = i
			}
			if nonZero != tc.daysInMonth {
				t.Fatalf("non-blank cell count = %d, want %d", nonZero, tc.daysInMonth)
			}
			if firstIdx != tc.startOffset {
				t.Fatalf("first non-blank cell at %d, want startOffset %d", firstIdx, tc.startOffset)
			}
			if flat[firstIdx].day != 1 {
				t.Fatalf("first non-blank cell day = %d, want 1", flat[firstIdx].day)
			}
			wantLastIdx := tc.startOffset + tc.daysInMonth - 1
			if lastIdx != wantLastIdx {
				t.Fatalf("last non-blank cell at %d, want %d", lastIdx, wantLastIdx)
			}
			if flat[lastIdx].day != tc.daysInMonth {
				t.Fatalf("last non-blank cell day = %d, want %d", flat[lastIdx].day, tc.daysInMonth)
			}
			// Every day in between increments by exactly one, with nothing
			// past the month's last day.
			for i := firstIdx; i <= lastIdx; i++ {
				if flat[i].day != i-firstIdx+1 {
					t.Fatalf("cell %d day = %d, want %d", i, flat[i].day, i-firstIdx+1)
				}
			}
			for i := lastIdx + 1; i < len(flat); i++ {
				if flat[i].day != 0 {
					t.Fatalf("cell %d after the month's last day = %d, want 0 (blank)", i, flat[i].day)
				}
			}
		})
	}
}

// TestBuildCalendarWeeksMarksToday covers the isToday flag buildCalendarWeeks
// sets only when the requested year/month is the real current one.
func TestBuildCalendarWeeksMarksToday(t *testing.T) {
	now := time.Now()

	rows := buildCalendarWeeks(now.Year(), now.Month(), vtui.ColMenuHighlight)
	todayCells := 0
	for _, r := range rows {
		row := r.(calendarWeekRow)
		for _, d := range row.days {
			if d.isToday {
				todayCells++
				if d.day != now.Day() {
					t.Fatalf("isToday cell day = %d, want %d", d.day, now.Day())
				}
			}
		}
	}
	if todayCells != 1 {
		t.Fatalf("current month has %d isToday cells, want exactly 1", todayCells)
	}

	// A neighboring month never gets a today cell, even though the numeric
	// day may coincide.
	prevMonth := now.Month() - 1
	prevYear := now.Year()
	if prevMonth < time.January {
		prevMonth = time.December
		prevYear--
	}
	rows = buildCalendarWeeks(prevYear, prevMonth, vtui.ColMenuHighlight)
	for _, r := range rows {
		row := r.(calendarWeekRow)
		for _, d := range row.days {
			if d.isToday {
				t.Fatalf("previous month has an isToday cell (day %d), want none", d.day)
			}
		}
	}
}

func TestCalendarWeekRowGetCellText(t *testing.T) {
	var row calendarWeekRow
	row.days[0] = calendarDay{day: 0}
	row.days[1] = calendarDay{day: 7}
	row.days[6] = calendarDay{day: 31}

	if got := row.GetCellText(0); got != "" {
		t.Fatalf("blank cell text = %q, want empty", got)
	}
	if got := row.GetCellText(1); got != "7" {
		t.Fatalf("day 7 cell text = %q, want %q", got, "7")
	}
	if got := row.GetCellText(6); got != "31" {
		t.Fatalf("day 31 cell text = %q, want %q", got, "31")
	}
}

func TestCalendarWeekRowGetCellAttr(t *testing.T) {
	row := calendarWeekRow{todayIdx: vtui.ColTableSelectedText}
	row.days[0] = calendarDay{day: 5, isToday: true}
	row.days[1] = calendarDay{day: 6, isToday: false}

	const sentinel = uint64(0xdeadbeef)
	if got := row.GetCellAttr(0, sentinel); got != vtui.Palette[vtui.ColTableSelectedText] {
		t.Fatalf("today cell attr = %#x, want the highlight color %#x", got, vtui.Palette[vtui.ColTableSelectedText])
	}
	if got := row.GetCellAttr(1, sentinel); got != sentinel {
		t.Fatalf("non-today cell attr = %#x, want the passed-through default %#x", got, sentinel)
	}
}

func TestCalendarDialogUsesThemeHighlight(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	defer vtui.SetDefaultPalette()

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	// A theme that overrides the table selection colour: the today cell must
	// render in this colour, proving the calendar takes the palette index from
	// the table rather than hardcoding one. Set after Init, which may reset the
	// palette.
	custom := vtui.SetRGBBoth(0, 0xFF00FF, 0x00FF00)
	vtui.Palette[vtui.ColTableSelectedText] = custom

	now := time.Now()
	dlg, table, _, _, _ := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	if table.ColorHighlightIdx != vtui.ColTableSelectedText {
		t.Fatalf("table.ColorHighlightIdx = %d, want %d", table.ColorHighlightIdx, vtui.ColTableSelectedText)
	}
	if got := vtui.Palette[table.ColorHighlightIdx]; got != custom {
		t.Fatalf("theme highlight = %#x, want the custom colour %#x", got, custom)
	}

	rows := buildCalendarWeeks(now.Year(), now.Month(), table.ColorHighlightIdx)
	for _, r := range rows {
		row := r.(calendarWeekRow)
		for c, d := range row.days {
			if !d.isToday {
				continue
			}
			if got := row.GetCellAttr(c, 0); got != custom {
				t.Fatalf("today cell attr = %#x, want the theme highlight %#x", got, custom)
			}
			return
		}
	}
	t.Fatal("calendar grid has no today cell")
}

func TestCalendarColumns(t *testing.T) {
	cols := calendarColumns()
	if len(cols) != 7 {
		t.Fatalf("len(cols) = %d, want 7", len(cols))
	}
	for i, key := range calendarWeekdayKeys {
		if cols[i].Title != i18n.Msg(key) {
			t.Fatalf("column %d title = %q, want %q", i, cols[i].Title, i18n.Msg(key))
		}
		if cols[i].Width != calendarColumnWidth {
			t.Fatalf("column %d width = %d, want %d", i, cols[i].Width, calendarColumnWidth)
		}
		if cols[i].Alignment != vtui.AlignRight {
			t.Fatalf("column %d alignment = %v, want AlignRight", i, cols[i].Alignment)
		}
	}
}

// openCalendarDialogForTest opens the calendar and locates its month/year
// label, its day-grid table and its two buttons, the way TestCalculator...
// in calculator_ui_test.go locates the calculator's own controls.
func openCalendarDialogForTest(t *testing.T) (dlg *vtui.Window, table *calendarTable, lblMonth *vtui.Text, btnInsert, btnClose *vtui.Button) {
	t.Helper()
	showCalendarDialog()
	var ok bool
	dlg, ok = vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want the calendar dialog", vtui.FrameManager.GetTopFrame())
	}
	vtui.AssertLayout(t, dlg)

	hint := i18n.Msg("Calendar.Hint")
	// Button.GetCaption strips the "&" hotkey marker Far-style captions carry
	// (e.g. "&Insert"), so the button text is compared against the same
	// cleaned form.
	insertLabel, _, _ := vtui.ParseAmpersandString(i18n.Msg("Calendar.BtnInsert"))
	closeLabel, _, _ := vtui.ParseAmpersandString(i18n.Msg("vtui.Cancel"))
	walkUI(dlg, func(el vtui.UIElement) bool {
		switch v := el.(type) {
		case *calendarTable:
			table = v
		case *vtui.Text:
			if v.GetText() != hint {
				lblMonth = v
			}
		case *vtui.Button:
			switch v.GetCaption() {
			case insertLabel:
				btnInsert = v
			case closeLabel:
				btnClose = v
			}
		}
		return true
	})
	if table == nil || lblMonth == nil || btnInsert == nil || btnClose == nil {
		t.Fatalf("calendar dialog is missing expected elements: table=%v lblMonth=%v btnInsert=%v btnClose=%v",
			table, lblMonth, btnInsert, btnClose)
	}
	return dlg, table, lblMonth, btnInsert, btnClose
}

func calendarPress(table *calendarTable, code uint16, ctrl bool) bool {
	e := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: code}
	if ctrl {
		e.ControlKeyState = vtinput.LeftCtrlPressed
	}
	return table.ProcessKey(e)
}

// TestCalendarDialogMonthYearNavigation exercises calendarTable.ProcessKey's
// month/year navigation (f4#1601): PgUp/PgDn change the month and wrap the
// year at the Dec/Jan boundary in both directions, Ctrl+PgUp/PgDn change only
// the year, Home without Ctrl jumps back to today, Ctrl+Home and an unrelated
// key both fall through untouched to the embedded vtui.Table.
func TestCalendarDialogMonthYearNavigation(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	now := time.Now()
	startYear, startMonth := now.Year(), now.Month()

	dlg, table, lblMonth, _, _ := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	if got, want := lblMonth.GetText(), calendarMonthLabel(startYear, startMonth); got != want {
		t.Fatalf("initial month label = %q, want %q", got, want)
	}

	// Ctrl+PgDn/Ctrl+PgUp change only the year.
	if !calendarPress(table, vtinput.VK_NEXT, true) {
		t.Fatal("Ctrl+PgDn was not handled")
	}
	if got, want := lblMonth.GetText(), calendarMonthLabel(startYear+1, startMonth); got != want {
		t.Fatalf("after Ctrl+PgDn label = %q, want %q", got, want)
	}
	if !calendarPress(table, vtinput.VK_PRIOR, true) {
		t.Fatal("Ctrl+PgUp was not handled")
	}
	if got, want := lblMonth.GetText(), calendarMonthLabel(startYear, startMonth); got != want {
		t.Fatalf("after Ctrl+PgUp label = %q, want %q", got, want)
	}

	// PgDn steps through 13 months, crossing at least one December->January
	// boundary and confirming the year advances there.
	year, month := startYear, startMonth
	for i := 0; i < 13; i++ {
		if !calendarPress(table, vtinput.VK_NEXT, false) {
			t.Fatalf("PgDn step %d was not handled", i)
		}
		month++
		if month > time.December {
			month = time.January
			year++
		}
		if got, want := lblMonth.GetText(), calendarMonthLabel(year, month); got != want {
			t.Fatalf("PgDn step %d label = %q, want %q", i, got, want)
		}
	}

	// PgUp undoes exactly those 13 months, crossing the January->December
	// boundary backward, and lands back where it started.
	for i := 0; i < 13; i++ {
		if !calendarPress(table, vtinput.VK_PRIOR, false) {
			t.Fatalf("PgUp step %d was not handled", i)
		}
		month--
		if month < time.January {
			month = time.December
			year--
		}
		if got, want := lblMonth.GetText(), calendarMonthLabel(year, month); got != want {
			t.Fatalf("PgUp step %d label = %q, want %q", i, got, want)
		}
	}
	if year != startYear || month != startMonth {
		t.Fatalf("round trip ended at %d-%d, want %d-%d", year, month, startYear, startMonth)
	}

	// Move one month away from today, then confirm Ctrl+Home and an
	// unrelated key both leave the label untouched (they are not the
	// "jump to today" shortcut and fall through to the embedded Table).
	if !calendarPress(table, vtinput.VK_NEXT, false) {
		t.Fatal("PgDn (setup) was not handled")
	}
	awayFromToday := lblMonth.GetText()

	calendarPress(table, vtinput.VK_HOME, true)
	if got := lblMonth.GetText(); got != awayFromToday {
		t.Fatalf("Ctrl+Home changed the month label to %q, want unchanged %q", got, awayFromToday)
	}
	calendarPress(table, vtinput.VK_DOWN, false)
	if got := lblMonth.GetText(); got != awayFromToday {
		t.Fatalf("an unrelated key changed the month label to %q, want unchanged %q", got, awayFromToday)
	}

	// Home (without Ctrl) jumps back to today and re-marks it in the grid.
	if !calendarPress(table, vtinput.VK_HOME, false) {
		t.Fatal("Home was not handled")
	}
	if got, want := lblMonth.GetText(), calendarMonthLabel(startYear, startMonth); got != want {
		t.Fatalf("after Home label = %q, want %q", got, want)
	}
	todayCount := 0
	for _, r := range table.Rows {
		row, ok := r.(calendarWeekRow)
		if !ok {
			continue
		}
		for _, d := range row.days {
			if d.isToday {
				todayCount++
				if d.day != now.Day() {
					t.Fatalf("today cell day = %d, want %d", d.day, now.Day())
				}
			}
		}
	}
	if todayCount != 1 {
		t.Fatalf("today cell count after Home = %d, want 1", todayCount)
	}
}

// TestCalendarDialogClampsSelectionAcrossMonths covers clampSelection: moving
// from a day that does not exist in the newly displayed month walks the
// cursor backward (same weekday column) until it lands on a real day.
//
// The dialog always navigates to a fixed year (2026) first via Ctrl+PgUp/
// Ctrl+PgDn, then steps month-by-month to March without ever crossing a
// Dec/Jan boundary, so the scenario is reproducible regardless of the date
// the test actually runs on. March 2026 starts on a Sunday (startOffset 6,
// confirmed independently with `date -d 2026-03-01 +%A`) and has 31 days, so
// day 31 sits at row 5, column 1 -- the grid's last possible cell. April 2026
// starts on a Wednesday (startOffset 2) and has only 30 days, so that same
// cell (row 5, column 1) is blank padding and clampSelection must walk back
// exactly one row, to row 4 column 1, which April displays as day 28.
func TestCalendarDialogClampsSelectionAcrossMonths(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	dlg, table, lblMonth, _, _ := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	now := time.Now()
	const targetYear = 2026

	if yearDelta := targetYear - now.Year(); yearDelta != 0 {
		code := uint16(vtinput.VK_NEXT)
		n := yearDelta
		if yearDelta < 0 {
			code, n = vtinput.VK_PRIOR, -yearDelta
		}
		for i := 0; i < n; i++ {
			if !calendarPress(table, code, true) {
				t.Fatalf("year-alignment step %d was not handled", i)
			}
		}
	}
	if monthDelta := int(time.March) - int(now.Month()); monthDelta != 0 {
		code := uint16(vtinput.VK_NEXT)
		n := monthDelta
		if monthDelta < 0 {
			code, n = vtinput.VK_PRIOR, -monthDelta
		}
		for i := 0; i < n; i++ {
			if !calendarPress(table, code, false) {
				t.Fatalf("month-alignment step %d was not handled", i)
			}
		}
	}
	if got, want := lblMonth.GetText(), calendarMonthLabel(targetYear, time.March); got != want {
		t.Fatalf("month label after alignment = %q, want %q", got, want)
	}

	// Find day 31 in the March grid and select it explicitly.
	targetRow, targetCol := -1, -1
	for r, item := range table.Rows {
		row := item.(calendarWeekRow)
		for c, d := range row.days {
			if d.day == 31 {
				targetRow, targetCol = r, c
			}
		}
	}
	if targetRow == -1 {
		t.Fatal("March 2026 grid has no day 31 cell")
	}
	if targetRow != 5 || targetCol != 1 {
		t.Fatalf("day 31 landed at row %d col %d, want row 5 col 1", targetRow, targetCol)
	}
	table.SelectPos, table.SelectCol = targetRow, targetCol

	if !calendarPress(table, vtinput.VK_NEXT, false) {
		t.Fatal("PgDn to April was not handled")
	}
	if got, want := lblMonth.GetText(), calendarMonthLabel(targetYear, time.April); got != want {
		t.Fatalf("month label after PgDn = %q, want %q", got, want)
	}

	if table.SelectCol != targetCol {
		t.Fatalf("clampSelection changed the column to %d, want it to stay %d", table.SelectCol, targetCol)
	}
	if table.SelectPos >= targetRow {
		t.Fatalf("clampSelection left SelectPos at %d, want it to move up from %d", table.SelectPos, targetRow)
	}
	idx := table.RowAt(table.SelectPos)
	row := table.Rows[idx].(calendarWeekRow)
	if row.days[table.SelectCol].day == 0 {
		t.Fatalf("clampSelection left the cursor on a blank cell (row %d col %d)", table.SelectPos, table.SelectCol)
	}
	if table.SelectPos != 4 || row.days[table.SelectCol].day != 28 {
		t.Fatalf("clamped selection = row %d day %d, want row 4 day 28", table.SelectPos, row.days[table.SelectCol].day)
	}
}

// findBlankCell locates a padding cell (day == 0) in the table's current
// grid. Every month leaves at least 5 blank cells (the grid always has 42
// cells and no month fills more than 37), so one is always available.
func findBlankCell(t *testing.T, table *calendarTable) (int, int) {
	t.Helper()
	for r, item := range table.Rows {
		row, ok := item.(calendarWeekRow)
		if !ok {
			continue
		}
		for c, d := range row.days {
			if d.day == 0 {
				return r, c
			}
		}
	}
	t.Fatal("calendar grid has no blank padding cell")
	return 0, 0
}

func TestCalendarDialogInsertWritesSelectedDate(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	dlg, _, _, btnInsert, _ := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	// showCalendarDialog's own selectToday() already put the cursor on
	// today's cell before the dialog was ever shown.
	btnInsert.OnClick()

	if !dlg.IsDone() {
		t.Fatal("Insert did not close the calendar dialog")
	}
	want := time.Now().Format("2006-01-02")
	if got := pf.CmdLine.Edit.GetText(); got != want {
		t.Fatalf("command line = %q, want %q", got, want)
	}
}

func TestCalendarDialogInsertOnBlankCellDoesNothing(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	dlg, table, _, btnInsert, _ := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	blankRow, blankCol := findBlankCell(t, table)
	table.SelectPos, table.SelectCol = blankRow, blankCol

	btnInsert.OnClick()

	if !dlg.IsDone() {
		t.Fatal("Insert did not close the calendar dialog even on a blank cell")
	}
	if got := pf.CmdLine.Edit.GetText(); got != "" {
		t.Fatalf("command line = %q, want empty (a blank cell must not insert anything)", got)
	}
}

func TestCalendarDialogCloseButtonClosesWithoutInserting(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	dlg, _, _, _, btnClose := openCalendarDialogForTest(t)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(dlg) })

	btnClose.OnClick()

	if !dlg.IsDone() {
		t.Fatal("Close did not close the calendar dialog")
	}
	if got := pf.CmdLine.Edit.GetText(); got != "" {
		t.Fatalf("command line = %q, want empty", got)
	}
}
