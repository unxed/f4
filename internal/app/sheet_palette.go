package app

import (
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// commandPaletteSheetEntries publishes the spreadsheet commands that live in
// SheetFrame.ProcessKey rather than in the action registry. Cursor movement,
// block marking and text entry stay local primitives, exactly as they do for
// the editor.
func commandPaletteSheetEntries(frame *SheetFrame) []commandPaletteEntry {
	if frame == nil {
		return nil
	}
	newEntry := func(id, labelKey, englishLabel, descKey, englishDescription, shortcut string, run func(*SheetFrame)) commandPaletteEntry {
		return commandPaletteLocalizedDirectEntry(
			"sheet:"+strings.ToLower(id), "Sheet."+id,
			labelKey, englishLabel, descKey, englishDescription,
			"CommandPalette.CategorySpreadsheet", "Spreadsheet", shortcut,
			func() bool {
				if vtui.FrameManager == nil || vtui.FrameManager.GetTopFrame() != frame {
					return false
				}
				run(frame)
				vtui.FrameManager.Redraw()
				return true
			},
			"Action.App.Spreadsheet", "Action.App.Spreadsheet.Desc",
		)
	}

	separators := newEntry(
		"ToggleSeparators", "CommandPalette.Sheet.ToggleSeparators", "Toggle column separators",
		"CommandPalette.Sheet.ToggleSeparators.Desc", "Show or hide the vertical lines between columns", "Alt+S",
		func(sf *SheetFrame) { sf.doc.Separators = !sf.doc.Separators },
	)
	separators.Checked = frame.doc.Separators

	csvSeparator := newEntry(
		"CSVSeparator", "CommandPalette.Sheet.CSVSeparator", "CSV separator",
		"CommandPalette.Sheet.CSVSeparator.Desc", "Change the field delimiter for CSV import/export", "",
		func(sf *SheetFrame) { showSheetCSVSeparatorDialog(sf) },
	)

	return []commandPaletteEntry{
		newEntry("Save", "CommandPalette.Sheet.Save", "Save spreadsheet",
			"CommandPalette.Sheet.Save.Desc", "Write the spreadsheet back to its file", "F2",
			func(sf *SheetFrame) { sheetSave(sf, sf.path) }),
		newEntry("SaveAs", "CommandPalette.Sheet.SaveAs", "Save spreadsheet as",
			"CommandPalette.Sheet.SaveAs.Desc", "Write the spreadsheet to a new file", "Shift+F2",
			func(sf *SheetFrame) { showSheetSaveAsDialog(sf) }),
		newEntry("Open", "CommandPalette.Sheet.Open", "Open spreadsheet",
			"CommandPalette.Sheet.Open.Desc", "Load a spreadsheet, workbook or CSV file", "F3",
			func(sf *SheetFrame) { showSheetOpenDialog(sf) }),
		newEntry("Export", "CommandPalette.Sheet.Export", "Export spreadsheet",
			"CommandPalette.Sheet.Export.Desc", "Write the sheet out as text, CSV or a workbook", "Shift+F3",
			func(sf *SheetFrame) { showSheetExportDialog(sf) }),
		newEntry("Recalc", "CommandPalette.Sheet.Recalc", "Recalculate",
			"CommandPalette.Sheet.Recalc.Desc", "Re-evaluate every formula in the sheet", "F5",
			func(sf *SheetFrame) { sf.doc.Recalc() }),
		newEntry("Goto", "CommandPalette.Sheet.Goto", "Go to cell",
			"CommandPalette.Sheet.Goto.Desc", "Jump to a cell by its name", "F6",
			func(sf *SheetFrame) { showSheetGotoDialog(sf) }),
		newEntry("Find", "CommandPalette.Sheet.Find", "Find in spreadsheet",
			"CommandPalette.Sheet.Find.Desc", "Search cells by text or by value", "F7",
			func(sf *SheetFrame) { showSheetFindDialog(sf, false) }),
		newEntry("Replace", "CommandPalette.Sheet.Replace", "Replace in spreadsheet",
			"CommandPalette.Sheet.Replace.Desc", "Search cells and replace what is found", "Ctrl+F7",
			func(sf *SheetFrame) { showSheetFindDialog(sf, true) }),
		newEntry("SearchAgain", "CommandPalette.Sheet.SearchAgain", "Search again",
			"CommandPalette.Sheet.SearchAgain.Desc", "Repeat the last spreadsheet search", "Shift+F7",
			func(sf *SheetFrame) { sheetSearchAgain(sf) }),
		newEntry("CellFormat", "CommandPalette.Sheet.CellFormat", "Cell format",
			"CommandPalette.Sheet.CellFormat.Desc", "Change display format, alignment and protection of the block", "Alt+O",
			func(sf *SheetFrame) { showSheetFormatDialog(sf) }),
		newEntry("ColumnWidth", "CommandPalette.Sheet.ColumnWidth", "Column width",
			"CommandPalette.Sheet.ColumnWidth.Desc", "Set the width of the current column", "Alt+Left, Alt+Right",
			func(sf *SheetFrame) { showSheetWidthDialog(sf) }),
		separators,
		csvSeparator,
		newEntry("InsertRow", "CommandPalette.Sheet.InsertRow", "Insert row",
			"CommandPalette.Sheet.InsertRow.Desc", "Insert a row above the cursor and repair formulas", "Alt+I",
			func(sf *SheetFrame) { sf.doc.InsertRow(sf.cur.Row) }),
		newEntry("InsertColumn", "CommandPalette.Sheet.InsertColumn", "Insert column",
			"CommandPalette.Sheet.InsertColumn.Desc", "Insert a column before the cursor and repair formulas", "Alt+C",
			func(sf *SheetFrame) { sf.doc.InsertColumn(sf.cur.Col) }),
		newEntry("DeleteRow", "CommandPalette.Sheet.DeleteRow", "Delete row",
			"CommandPalette.Sheet.DeleteRow.Desc", "Remove the current row and repair formulas", "Alt+L",
			func(sf *SheetFrame) { sf.doc.DeleteRow(sf.cur.Row) }),
		newEntry("DeleteColumn", "CommandPalette.Sheet.DeleteColumn", "Delete column",
			"CommandPalette.Sheet.DeleteColumn.Desc", "Remove the current column and repair formulas", "Alt+D",
			func(sf *SheetFrame) { sf.doc.DeleteColumn(sf.cur.Col) }),
		newEntry("Undo", "CommandPalette.Sheet.Undo", "Undo",
			"CommandPalette.Sheet.Undo.Desc", "Undo the last spreadsheet change", "Alt+BackSpace",
			func(sf *SheetFrame) {
				if !sf.doc.Undo() {
					sf.status = i18n.Msg("Sheet.NothingToUndo")
				}
			}),
		newEntry("Menu", "CommandPalette.Sheet.Menu", "Spreadsheet menu",
			"CommandPalette.Sheet.Menu.Desc", "Open the File/Edit/Search/Table menu", "F10",
			func(sf *SheetFrame) { showSheetMenu(sf) }),
	}
}
