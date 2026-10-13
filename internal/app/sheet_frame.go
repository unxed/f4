package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sheet"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// rowHeaderWidth is the width of the left gutter holding row numbers. Four
// digits plus a blank is enough for the 4096 row grid.
const rowHeaderWidth = 5

// SheetFrame is the spreadsheet workspace: an input line, the grid itself and
// a status line, wrapped in a movable window.
type SheetFrame struct {
	vtui.BaseWindow

	doc  *sheet.Sheet
	path string

	cur     sheet.Point
	mark    sheet.Point
	marking bool
	delta   sheet.Point

	editing  bool
	editText []rune
	editPos  int

	status string

	clipboard *sheet.Block
	search    sheet.SearchOptions
	replace   string
	replacing bool
}

// NewSheetFrame creates an empty spreadsheet filling the screen.
func NewSheetFrame() *SheetFrame {
	width, height := 80, 25
	if vtui.FrameManager != nil {
		width = vtui.FrameManager.GetScreenSize()
		height = vtui.FrameManager.GetScreenHeight() - 1
	}
	if width < 40 {
		width = 40
	}
	if height < 10 {
		height = 10
	}
	sf := &SheetFrame{
		BaseWindow: *vtui.NewBaseWindow(0, 0, width-1, height-1, i18n.Msg("Sheet.Title")),
		doc:        sheet.New(),
	}
	sf.ShowClose = true
	sf.ShowZoom = true
	sf.MinW = 40
	sf.MinH = 10
	sf.status = i18n.Msg("Sheet.StatusReady")
	return sf
}

// Document exposes the model, which the dialogs and the tests work on.
func (sf *SheetFrame) Document() *sheet.Sheet { return sf.doc }

// Cursor reports the current cell.
func (sf *SheetFrame) Cursor() sheet.Point { return sf.cur }

// Block returns the marked rectangle, which degenerates to the cursor cell
// when no block is active.
func (sf *SheetFrame) Block() sheet.Rect {
	if !sf.marking {
		return sheet.Rect{Left: sf.cur.Col, Top: sf.cur.Row, Right: sf.cur.Col, Bottom: sf.cur.Row}
	}
	return sheet.Rect{Left: sf.mark.Col, Top: sf.mark.Row, Right: sf.cur.Col, Bottom: sf.cur.Row}.Normalized()
}

// SetStatus shows a message on the bottom line of the window.
func (sf *SheetFrame) SetStatus(text string) { sf.status = text }

// Path is the file the sheet was loaded from or last saved to.
func (sf *SheetFrame) Path() string { return sf.path }

// SetPath records the file backing this sheet.
func (sf *SheetFrame) SetPath(path string) { sf.path = path }

func (sf *SheetFrame) GetType() vtui.FrameType { return vtui.TypeUser + 7 }

func (sf *SheetFrame) GetTitle() string {
	name := i18n.Msg("Sheet.Untitled")
	if sf.path != "" {
		name = filepath.Base(sf.path)
	}
	if sf.doc.Modified {
		name = "*" + name
	}
	return i18n.Msg("Sheet.Title") + ": " + name
}

func (sf *SheetFrame) GetHelp() string { return "Spreadsheet" }

// GetKeyLabels mirrors the key bar of the original spreadsheet.
func (sf *SheetFrame) GetKeyLabels() *vtui.KeySet {
	return &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			i18n.Msg("KeyBar.SheetF1"), i18n.Msg("KeyBar.SheetF2"), i18n.Msg("KeyBar.SheetF3"), i18n.Msg("KeyBar.SheetF4"),
			i18n.Msg("KeyBar.SheetF5"), i18n.Msg("KeyBar.SheetF6"), i18n.Msg("KeyBar.SheetF7"), "",
			"", i18n.Msg("KeyBar.SheetF10"), "", "",
		},
		Shift: vtui.KeyBarLabels{
			"", i18n.Msg("KeyBar.SheetShiftF2"), i18n.Msg("KeyBar.SheetShiftF3"), "", "", "", "", "", "", "", "", "",
		},
		Alt: vtui.KeyBarLabels{
			"", "", "", "", "", "", "", "", "", "", "", "",
		},
	}
}

// --- geometry -------------------------------------------------------------

func (sf *SheetFrame) inputLineY() int  { return sf.Y1 + 1 }
func (sf *SheetFrame) headerY() int     { return sf.Y1 + 2 }
func (sf *SheetFrame) gridTopY() int    { return sf.Y1 + 3 }
func (sf *SheetFrame) statusY() int     { return sf.Y2 - 1 }
func (sf *SheetFrame) gridBottomY() int { return sf.statusY() - 1 }

func (sf *SheetFrame) visibleRows() int {
	rows := sf.gridBottomY() - sf.gridTopY() + 1
	if rows < 1 {
		return 1
	}
	return rows
}

func (sf *SheetFrame) gridWidth() int {
	width := sf.X2 - sf.X1 - 1 - rowHeaderWidth
	if width < 1 {
		return 1
	}
	return width
}

// visibleColumns lists the columns that fit starting at the scroll offset,
// together with the screen x and the width each one occupies.
func (sf *SheetFrame) visibleColumns() []columnLayout {
	var layout []columnLayout
	x := sf.X1 + 1 + rowHeaderWidth
	limit := sf.X2
	for col := sf.delta.Col; col < sheet.MaxColumns && x < limit; col++ {
		width := sf.doc.ColumnWidth(col)
		if x+width > limit {
			width = limit - x
		}
		if width <= 0 {
			break
		}
		layout = append(layout, columnLayout{col: col, x: x, width: width})
		x += width
	}
	return layout
}

type columnLayout struct {
	col   int
	x     int
	width int
}

// ensureVisible scrolls the view so that the cursor stays inside the window.
func (sf *SheetFrame) ensureVisible() {
	if sf.cur.Row < sf.delta.Row {
		sf.delta.Row = sf.cur.Row
	}
	if rows := sf.visibleRows(); sf.cur.Row >= sf.delta.Row+rows {
		sf.delta.Row = sf.cur.Row - rows + 1
	}
	if sf.cur.Col < sf.delta.Col {
		sf.delta.Col = sf.cur.Col
	}
	for {
		width := 0
		fits := false
		for col := sf.delta.Col; col <= sf.cur.Col; col++ {
			width += sf.doc.ColumnWidth(col)
		}
		if width <= sf.gridWidth() || sf.delta.Col >= sf.cur.Col {
			fits = true
		}
		if fits {
			break
		}
		sf.delta.Col++
	}
	if sf.delta.Row < 0 {
		sf.delta.Row = 0
	}
	if sf.delta.Col < 0 {
		sf.delta.Col = 0
	}
}

// --- rendering ------------------------------------------------------------

func (sf *SheetFrame) Show(scr *vtui.ScreenBuf) {
	sf.BaseWindow.Show(scr)
	sf.ensureVisible()

	p := vtui.NewPainter(scr)
	textAttr := vtui.Palette[vtui.ColTableText]
	headerAttr := vtui.Palette[vtui.ColTableColumnTitle]
	cursorAttr := vtui.Palette[vtui.ColTableSelectedText]
	blockAttr := vtui.Palette[vtui.ColTableTitle]
	editAttr := vtui.Palette[vtui.ColDialogEdit]

	inner := sf.X2 - sf.X1 - 1
	p.Fill(sf.X1+1, sf.Y1+1, sf.X2-1, sf.Y2-1, ' ', textAttr)

	// Input line: the address of the current cell plus its raw text.
	address := sheet.CellName(sf.cur.Col, sf.cur.Row)
	p.DrawString(sf.X1+1, sf.inputLineY(), sheet.FitText(address, rowHeaderWidth+1, sheet.JustifyLeft), headerAttr)
	content := sf.currentText()
	fieldX := sf.X1 + 2 + rowHeaderWidth
	fieldWidth := sf.X2 - fieldX
	if fieldWidth > 0 {
		p.DrawString(fieldX, sf.inputLineY(), sheet.FitText(content, fieldWidth, sheet.JustifyLeft), editAttr)
	}

	// Column headers.
	p.DrawString(sf.X1+1, sf.headerY(), strings.Repeat(" ", rowHeaderWidth), headerAttr)
	columns := sf.visibleColumns()
	for _, layout := range columns {
		attr := headerAttr
		if layout.col == sf.cur.Col {
			attr = cursorAttr
		}
		nameWidth := layout.width
		headerSeparator := sf.doc.Separators && layout.x+layout.width-1 < sf.X2-1
		if headerSeparator {
			nameWidth--
		}
		p.DrawString(layout.x, sf.headerY(), sheet.FitText(sheet.ColumnName(layout.col), nameWidth, sheet.JustifyCenter), attr)
		if headerSeparator {
			p.DrawString(layout.x+layout.width-1, sf.headerY(), string(sheet.ColumnSeparator), attr)
		}
	}

	// Grid body.
	block := sf.Block()
	for screenRow := 0; screenRow < sf.visibleRows(); screenRow++ {
		row := sf.delta.Row + screenRow
		if row >= sheet.MaxRows {
			break
		}
		y := sf.gridTopY() + screenRow
		p.DrawString(sf.X1+1, y, sheet.FitText(sheet.RowName(row), rowHeaderWidth-1, sheet.JustifyRight)+" ", headerAttr)
		for _, layout := range columns {
			attr := textAttr
			switch {
			case layout.col == sf.cur.Col && row == sf.cur.Row:
				attr = cursorAttr
			case sf.marking && block.Contains(layout.col, row):
				attr = blockAttr
			}
			textWidth := layout.width
			separator := sf.doc.Separators && layout.x+layout.width-1 < sf.X2-1
			if separator {
				textWidth--
			}
			p.DrawString(layout.x, y, sf.doc.FitCellWidth(layout.col, row, textWidth), attr)
			if separator {
				p.DrawString(layout.x+layout.width-1, y, string(sheet.ColumnSeparator), attr)
			}
		}
	}

	// Status line.
	status := sf.status
	if sf.doc.Modified {
		status = i18n.Msg("Sheet.StatusModified") + " " + status
	}
	p.DrawString(sf.X1+1, sf.statusY(), sheet.FitText(status, inner, sheet.JustifyLeft), headerAttr)

	if sf.editing {
		cursorX := fieldX + sf.editPos
		if cursorX > sf.X2-1 {
			cursorX = sf.X2 - 1
		}
		scr.SetCursorPos(cursorX, sf.inputLineY())
		scr.SetCursorVisible(true)
	}
}

// currentText is what the input line shows: the pending edit while typing, the
// stored text otherwise.
func (sf *SheetFrame) currentText() string {
	if sf.editing {
		return string(sf.editText)
	}
	if cell := sf.doc.Cell(sf.cur.Col, sf.cur.Row); cell != nil {
		return cell.Text
	}
	return ""
}

// --- editing --------------------------------------------------------------

func (sf *SheetFrame) beginEdit(initial string) {
	sf.editing = true
	sf.editText = []rune(initial)
	sf.editPos = len(sf.editText)
	sf.marking = false
}

func (sf *SheetFrame) commitEdit() {
	if !sf.editing {
		return
	}
	text := string(sf.editText)
	sf.editing = false
	sf.editText = nil
	sf.editPos = 0
	if cell := sf.doc.Cell(sf.cur.Col, sf.cur.Row); cell != nil && cell.Protected {
		sf.status = i18n.Msg("Sheet.CellProtected")
		return
	}
	sf.doc.SetText(sf.cur.Col, sf.cur.Row, text)
	sf.reportError()
}

func (sf *SheetFrame) cancelEdit() {
	sf.editing = false
	sf.editText = nil
	sf.editPos = 0
}

// reportError surfaces the first failing formula on the status line.
func (sf *SheetFrame) reportError() {
	if point, ok := sf.doc.LastError(); ok {
		sf.status = fmt.Sprintf(i18n.Msg("Sheet.FormulaError"), sheet.CellName(point.Col, point.Row))
		return
	}
	sf.status = i18n.Msg("Sheet.StatusReady")
}

func (sf *SheetFrame) processEditKey(e *vtinput.InputEvent, ctrl, alt bool) bool {
	switch e.VirtualKeyCode {
	case vtinput.VK_RETURN:
		sf.commitEdit()
		sf.moveCursor(0, 1, false)
		return true
	case vtinput.VK_ESCAPE:
		sf.cancelEdit()
		return true
	case vtinput.VK_BACK:
		if sf.editPos > 0 {
			sf.editText = append(sf.editText[:sf.editPos-1], sf.editText[sf.editPos:]...)
			sf.editPos--
		}
		return true
	case vtinput.VK_DELETE:
		if sf.editPos < len(sf.editText) {
			sf.editText = append(sf.editText[:sf.editPos], sf.editText[sf.editPos+1:]...)
		}
		return true
	case vtinput.VK_LEFT:
		if sf.editPos > 0 {
			sf.editPos--
		}
		return true
	case vtinput.VK_RIGHT:
		if sf.editPos < len(sf.editText) {
			sf.editPos++
		}
		return true
	case vtinput.VK_HOME:
		sf.editPos = 0
		return true
	case vtinput.VK_END:
		sf.editPos = len(sf.editText)
		return true
	case vtinput.VK_UP:
		sf.commitEdit()
		sf.moveCursor(0, -1, false)
		return true
	case vtinput.VK_DOWN:
		sf.commitEdit()
		sf.moveCursor(0, 1, false)
		return true
	case vtinput.VK_TAB:
		sf.commitEdit()
		sf.moveCursor(1, 0, false)
		return true
	}
	if e.Char != 0 && !ctrl && !alt && unicode.IsPrint(e.Char) {
		sf.editText = append(sf.editText, 0)
		copy(sf.editText[sf.editPos+1:], sf.editText[sf.editPos:])
		sf.editText[sf.editPos] = e.Char
		sf.editPos++
		return true
	}
	return false
}

// --- navigation -----------------------------------------------------------

func (sf *SheetFrame) moveCursor(dx, dy int, extend bool) {
	if extend && !sf.marking {
		sf.mark = sf.cur
		sf.marking = true
	}
	if !extend {
		sf.marking = false
	}
	sf.cur.Col = clampInt(sf.cur.Col+dx, 0, sheet.MaxColumns-1)
	sf.cur.Row = clampInt(sf.cur.Row+dy, 0, sheet.MaxRows-1)
	sf.ensureVisible()
}

func (sf *SheetFrame) gotoCell(col, row int) {
	sf.cur.Col = clampInt(col, 0, sheet.MaxColumns-1)
	sf.cur.Row = clampInt(row, 0, sheet.MaxRows-1)
	sf.marking = false
	sf.ensureVisible()
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// --- key handling ---------------------------------------------------------

func (sf *SheetFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if e == nil || !e.KeyDown {
		return sf.BaseWindow.ProcessKey(e)
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	if sf.editing {
		if sf.processEditKey(e, ctrl, alt) {
			return true
		}
	}

	if alt && !ctrl {
		switch e.VirtualKeyCode {
		case 'I':
			sf.doc.InsertRow(sf.cur.Row)
			sf.status = i18n.Msg("Sheet.RowInserted")
			return true
		case 'C':
			sf.doc.InsertColumn(sf.cur.Col)
			sf.status = i18n.Msg("Sheet.ColumnInserted")
			return true
		case 'L':
			sf.doc.DeleteRow(sf.cur.Row)
			sf.status = i18n.Msg("Sheet.RowDeleted")
			return true
		case 'D':
			sf.doc.DeleteColumn(sf.cur.Col)
			sf.status = i18n.Msg("Sheet.ColumnDeleted")
			return true
		case 'O':
			showSheetFormatDialog(sf)
			return true
		case 'S':
			sf.doc.Separators = !sf.doc.Separators
			return true
		case vtinput.VK_LEFT:
			sf.doc.SetColumnWidth(sf.cur.Col, sf.doc.ColumnWidth(sf.cur.Col)-1)
			return true
		case vtinput.VK_RIGHT:
			sf.doc.SetColumnWidth(sf.cur.Col, sf.doc.ColumnWidth(sf.cur.Col)+1)
			return true
		case vtinput.VK_BACK:
			if !sf.doc.Undo() {
				sf.status = i18n.Msg("Sheet.NothingToUndo")
			}
			return true
		}
	}

	if ctrl && !alt {
		switch e.VirtualKeyCode {
		case vtinput.VK_INSERT:
			sf.clipboard = sf.doc.CopyBlock(sf.Block())
			sf.status = i18n.Msg("Sheet.BlockCopied")
			return true
		case vtinput.VK_DELETE:
			sf.doc.ClearBlock(sf.Block())
			sf.marking = false
			return true
		}
	}

	if shift && !alt && !ctrl {
		switch e.VirtualKeyCode {
		case vtinput.VK_INSERT:
			if sf.clipboard != nil {
				sf.doc.PasteBlock(sf.clipboard, sf.cur.Col, sf.cur.Row)
				sf.reportError()
			}
			return true
		case vtinput.VK_DELETE:
			sf.clipboard = sf.doc.CutBlock(sf.Block())
			sf.marking = false
			sf.status = i18n.Msg("Sheet.BlockCut")
			return true
		case vtinput.VK_F2:
			showSheetSaveAsDialog(sf)
			return true
		case vtinput.VK_F3:
			showSheetExportDialog(sf)
			return true
		}
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_LEFT:
		sf.moveCursor(-1, 0, shift)
		return true
	case vtinput.VK_RIGHT:
		sf.moveCursor(1, 0, shift)
		return true
	case vtinput.VK_UP:
		sf.moveCursor(0, -1, shift)
		return true
	case vtinput.VK_DOWN:
		sf.moveCursor(0, 1, shift)
		return true
	case vtinput.VK_PRIOR:
		sf.moveCursor(0, -sf.visibleRows(), shift)
		return true
	case vtinput.VK_NEXT:
		sf.moveCursor(0, sf.visibleRows(), shift)
		return true
	case vtinput.VK_HOME:
		sf.gotoCell(0, 0)
		return true
	case vtinput.VK_END:
		col, row := sf.doc.Bounds()
		if col < 0 {
			col, row = 0, 0
		}
		sf.gotoCell(col, row)
		return true
	case vtinput.VK_DELETE:
		sf.doc.ClearBlock(sf.Block())
		sf.marking = false
		return true
	case vtinput.VK_RETURN:
		cell := sf.doc.Cell(sf.cur.Col, sf.cur.Row)
		text := ""
		if cell != nil {
			text = cell.Text
		}
		sf.beginEdit(text)
		return true
	case vtinput.VK_F2:
		sheetSave(sf, sf.path)
		return true
	case vtinput.VK_F3:
		showSheetOpenDialog(sf)
		return true
	case vtinput.VK_F4:
		sheetNew(sf)
		return true
	case vtinput.VK_F5:
		sf.doc.Recalc()
		sf.reportError()
		return true
	case vtinput.VK_F6:
		showSheetGotoDialog(sf)
		return true
	case vtinput.VK_F7:
		showSheetFindDialog(sf, false)
		return true
	case vtinput.VK_F10:
		showSheetMenu(sf)
		return true
	case vtinput.VK_ESCAPE:
		sf.Close()
		return true
	}

	if e.Char != 0 && !ctrl && !alt && unicode.IsPrint(e.Char) {
		sf.beginEdit(string(e.Char))
		sf.editPos = len(sf.editText)
		return true
	}
	return sf.BaseWindow.ProcessKey(e)
}

// --- file operations ------------------------------------------------------

func sheetNew(sf *SheetFrame) {
	sf.doc = sheet.New()
	sf.path = ""
	sf.cur = sheet.Point{}
	sf.delta = sheet.Point{}
	sf.marking = false
	sf.status = i18n.Msg("Sheet.StatusReady")
}

// sheetSave writes the sheet, choosing the format from the file extension.
//
// The path is resolved against the active panel before anything is written:
// a name typed without a directory belongs where the user is looking, not in
// the directory f4 happens to have been started from.
func sheetSave(sf *SheetFrame, path string) {
	if path == "" {
		showSheetSaveAsDialog(sf)
		return
	}
	path = sheetResolvePath(path)
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx":
		err = sf.doc.SaveXLSX(path)
	default:
		err = sf.doc.Save(context.Background(), path)
	}
	if err != nil {
		vtui.ShowMessage(i18n.Msg("Sheet.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
		return
	}
	sf.path = path
	sf.doc.Modified = false
	sf.status = fmt.Sprintf(i18n.Msg("Sheet.Saved"), filepath.Base(path))
}

// sheetOpen loads a sheet from disk, picking the reader by extension.
func sheetOpen(sf *SheetFrame, path string) {
	path = sheetResolvePath(path)
	var (
		loaded *sheet.Sheet
		err    error
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xlsx":
		loaded, err = sheet.LoadXLSX(path)
	case ".csv":
		loaded, err = loadSheetCSV(path)
	default:
		loaded, err = sheet.Load(context.Background(), path)
	}
	if err != nil {
		vtui.ShowMessage(i18n.Msg("Sheet.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
		return
	}
	sf.doc = loaded
	sf.path = path
	sf.cur = sheet.Point{}
	sf.delta = sheet.Point{}
	sf.marking = false
	sf.reportError()
}

func (sf *SheetFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.MouseEventType {
		return false
	}
	if e.ButtonState == vtinput.FromLeft1stButtonPressed && e.KeyDown {
		mx, my := int(e.MouseX), int(e.MouseY)
		if my >= sf.gridTopY() && my <= sf.gridBottomY() && mx > sf.X1+rowHeaderWidth && mx < sf.X2 {
			for _, layout := range sf.visibleColumns() {
				if mx >= layout.x && mx < layout.x+layout.width {
					sf.gotoCell(layout.col, sf.delta.Row+my-sf.gridTopY())
					return true
				}
			}
		}
	}
	return sf.BaseWindow.ProcessMouse(e)
}
