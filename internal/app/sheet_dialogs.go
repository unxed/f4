package app

import (
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sheet"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// sheetNativeExtension is the suffix of the native format.
//
// It is a double extension on purpose. The native format has always been an
// ordinary SQLite database, but a file named .f4s said so to nothing: every
// other SQLite tool goes by the extension, f4's own client among them, and
// they all declined to open it. Ending the name in .sqlite makes the file
// what it already was, while .f4s in front of it keeps a sheet recognisable
// as a sheet.
const sheetNativeExtension = ".f4s.sqlite"

// sheetBaseName drops the extension from a sheet path, counting the native
// double extension as one so an export beside book.f4s.sqlite is book.csv
// rather than book.f4s.csv.
func sheetBaseName(path string) string {
	if strings.HasSuffix(strings.ToLower(path), sheetNativeExtension) {
		return path[:len(path)-len(sheetNativeExtension)]
	}
	if dot := strings.LastIndex(path, "."); dot > strings.LastIndexAny(path, `/\`) {
		return path[:dot]
	}
	return path
}

// sheetDirectory is where a name typed without a directory belongs: whatever
// the active file panel is showing.
//
// The frame stack is searched across workspaces rather than only on top,
// because a spreadsheet lives in a workspace of its own and has no panels
// under it, and because a dialog is what sits on top while a path is typed.
// An empty result means there is no local panel to speak of.
func sheetDirectory() string {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return ""
	}
	fs, ok := pf.GetActivePanelVFS().(*vfs.OSVFS)
	if !ok || fs == nil {
		return ""
	}
	dir, err := fs.Abs(fs.GetPath())
	if err != nil {
		return ""
	}
	return dir
}

// sheetPathIn resolves a typed sheet path against dir.
//
// Nothing used to resolve these names at all, so a relative one reached the
// SQLite and XLSX writers as typed and landed in the working directory f4 was
// started from. The sheet reported "Saved as sheet.f4s" and the panel the user
// was looking at never showed a file.
//
// An empty dir keeps the old destination, made explicit: a name with no panel
// behind it resolves against the process working directory.
func sheetPathIn(dir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if dir != "" {
		return filepath.Join(dir, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// sheetResolvePath is sheetPathIn against the directory of the active panel.
func sheetResolvePath(path string) string {
	return sheetPathIn(sheetDirectory(), path)
}

// loadSheetCSV reads a CSV file into a fresh sheet.
func loadSheetCSV(path string) (*sheet.Sheet, error) {
	File, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = File.Close() }()
	target := sheet.New()
	if err := target.ImportCSV(File); err != nil {
		return nil, err
	}
	return target, nil
}

// exportSheetCSV writes the displayed values of a sheet as CSV.
func exportSheetCSV(doc *sheet.Sheet, path string) error {
	File, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := doc.ExportCSV(File); err != nil {
		_ = File.Close()
		return err
	}
	return File.Close()
}

// exportSheetText writes the sheet laid out with its current column widths.
func exportSheetText(doc *sheet.Sheet, path string) error {
	File, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := doc.ExportText(File); err != nil {
		_ = File.Close()
		return err
	}
	return File.Close()
}

// askSheetPath shows a one line file name prompt.
func askSheetPath(title, label, initial string, accept func(string)) {
	dlg := vtui.NewCenteredDialog(64, 8, title)
	dlg.ShowClose = true

	lbl := vtui.NewText(0, 0, label, vtui.Palette[vtui.ColDialogText])
	edit := vtui.NewEdit(0, 0, 56, initial)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lbl)
	dlg.AddItem(edit)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	box := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, 58, 4)
	box.Add(lbl, vtui.Margins{}, vtui.AlignLeft)
	box.Add(edit, vtui.Margins{Top: 1}, vtui.AlignFill)
	buttons := vtui.NewHBoxLayout(0, 0, 58, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	box.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Apply()

	btnOk.OnClick = func() {
		path := strings.TrimSpace(edit.GetText())
		dlg.Close()
		if path != "" {
			accept(path)
		}
	}
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.SetFocusedItem(edit)
	vtui.FrameManager.Push(dlg)
}

func showSheetOpenDialog(sf *SheetFrame) {
	initial := sf.Path()
	if initial == "" {
		if dir := sheetDirectory(); dir != "" {
			initial = dir + string(filepath.Separator)
		}
	}
	askSheetPath(i18n.Msg("Sheet.Title"), i18n.Msg("Sheet.OpenPrompt"), initial, func(path string) {
		sheetOpen(sf, path)
	})
}

func showSheetSaveAsDialog(sf *SheetFrame) {
	initial := sf.Path()
	if initial == "" {
		initial = sheetResolvePath(i18n.Msg("Sheet.DefaultFileName"))
	}
	askSheetPath(i18n.Msg("Sheet.Title"), i18n.Msg("Sheet.SavePrompt"), initial, func(path string) {
		sheetSave(sf, path)
	})
}

// showSheetExportDialog offers the text, CSV and XLSX writers.
func showSheetExportDialog(sf *SheetFrame) {
	dlg := vtui.NewCenteredDialog(60, 12, i18n.Msg("Sheet.ExportTitle"))
	dlg.ShowClose = true

	lblFormat := vtui.NewText(0, 0, i18n.Msg("Sheet.ExportFormat"), vtui.Palette[vtui.ColDialogText])
	format := vtui.NewRadioGroup(0, 0, 1, []string{
		i18n.Msg("Sheet.ExportText"),
		i18n.Msg("Sheet.ExportCSV"),
		i18n.Msg("Sheet.ExportXLSX"),
	})
	lblPath := vtui.NewText(0, 0, i18n.Msg("Sheet.ExportPath"), vtui.Palette[vtui.ColDialogText])
	edit := vtui.NewEdit(0, 0, 52, defaultExportName(sf, ".txt"))
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	format.OnChange = func(selected int) {
		edit.SetText(defaultExportName(sf, exportExtension(selected)))
	}

	for _, item := range []vtui.UIElement{lblFormat, format, lblPath, edit, btnOk, btnCancel} {
		dlg.AddItem(item)
	}
	box := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, 54, 8)
	box.Add(lblFormat, vtui.Margins{}, vtui.AlignLeft)
	box.Add(format, vtui.Margins{}, vtui.AlignFill)
	box.Add(lblPath, vtui.Margins{Top: 1}, vtui.AlignLeft)
	box.Add(edit, vtui.Margins{}, vtui.AlignFill)
	buttons := vtui.NewHBoxLayout(0, 0, 54, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	box.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Apply()

	btnOk.OnClick = func() {
		path := sheetResolvePath(edit.GetText())
		selected := format.Selected
		dlg.Close()
		if path == "" {
			return
		}
		var err error
		switch selected {
		case 1:
			err = exportSheetCSV(sf.Document(), path)
		case 2:
			err = sf.Document().SaveXLSX(path)
		default:
			err = exportSheetText(sf.Document(), path)
		}
		if err != nil {
			vtui.ShowMessage(i18n.Msg("Sheet.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
			return
		}
		sf.SetStatus(fmt.Sprintf(i18n.Msg("Sheet.Exported"), path))
	}
	btnCancel.OnClick = func() { dlg.Close() }
	vtui.FrameManager.Push(dlg)
}

func exportExtension(selected int) string {
	switch selected {
	case 1:
		return ".csv"
	case 2:
		return ".xlsx"
	default:
		return ".txt"
	}
}

func defaultExportName(sf *SheetFrame, extension string) string {
	base := sf.Path()
	if base == "" {
		return sheetResolvePath("sheet" + extension)
	}
	return sheetBaseName(base) + extension
}

// showSheetGotoDialog jumps to a cell entered by name.
func showSheetGotoDialog(sf *SheetFrame) {
	askSheetPathLike(i18n.Msg("Sheet.GotoTitle"), i18n.Msg("Sheet.GotoPrompt"), sheet.CellName(sf.Cursor().Col, sf.Cursor().Row), 16, func(text string) {
		ref, ok := sheet.ParseRef(strings.TrimSpace(text))
		if !ok {
			vtui.ShowMessage(i18n.Msg("Sheet.Title"), i18n.Msg("Sheet.BadReference"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		point := ref.Point()
		sf.gotoCell(point.Col, point.Row)
	})
}

// askSheetPathLike is the short input prompt used for cell names and widths.
func askSheetPathLike(title, label, initial string, width int, accept func(string)) {
	dlg := vtui.NewCenteredDialog(44, 8, title)
	dlg.ShowClose = true

	lbl := vtui.NewText(0, 0, label, vtui.Palette[vtui.ColDialogText])
	edit := vtui.NewEdit(0, 0, width, initial)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{lbl, edit, btnOk, btnCancel} {
		dlg.AddItem(item)
	}
	box := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, 38, 4)
	box.Add(lbl, vtui.Margins{}, vtui.AlignLeft)
	box.Add(edit, vtui.Margins{Top: 1}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, 38, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	box.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Apply()

	btnOk.OnClick = func() {
		text := edit.GetText()
		dlg.Close()
		accept(text)
	}
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.SetFocusedItem(edit)
	vtui.FrameManager.Push(dlg)
}

// showSheetWidthDialog changes the width of the current column.
func showSheetWidthDialog(sf *SheetFrame) {
	col := sf.Cursor().Col
	current := strconv.Itoa(sf.Document().ColumnWidth(col))
	askSheetPathLike(i18n.Msg("Sheet.WidthTitle"), fmt.Sprintf(i18n.Msg("Sheet.WidthPrompt"), sheet.ColumnName(col)), current, 6, func(text string) {
		width, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return
		}
		sf.Document().SetColumnWidth(col, width)
	})
}

// showSheetFormatDialog is the cell/block format editor.
// showSheetCSVSeparatorDialog lets the user change the CSV field delimiter.
func showSheetCSVSeparatorDialog(sf *SheetFrame) {
	sep := sf.Document().CSVSeparator
	var initial string
	switch sep {
	case '\t':
		initial = "tab"
	case ';':
		initial = "semicolon"
	case ' ':
		initial = "space"
	default:
		initial = "comma"
	}
	askSheetPathLike(i18n.Msg("Sheet.CSVSeparatorTitle"),
		i18n.Msg("Sheet.CSVSeparatorPrompt"), initial, 12, func(text string) {
			text = strings.TrimSpace(strings.ToLower(text))
			var r rune
			switch text {
			case "comma", ",":
				r = ','
			case "tab", "\\t", "t":
				r = '\t'
			case "semicolon", ";":
				r = ';'
			case "space", " ":
				r = ' '
			default:
				return
			}
			sf.Document().CSVSeparator = r
		})
}

func showSheetFormatDialog(sf *SheetFrame) {
	dlg := vtui.NewCenteredDialog(50, 18, i18n.Msg("Sheet.FormatTitle"))
	dlg.ShowClose = true

	lblDisplay := vtui.NewText(0, 0, i18n.Msg("Sheet.FormatDisplay"), vtui.Palette[vtui.ColDialogText])
	display := vtui.NewRadioGroup(0, 0, 2, []string{
		i18n.Msg("Sheet.FormatAsIs"), i18n.Msg("Sheet.FormatDecimal"), i18n.Msg("Sheet.FormatComma"),
		i18n.Msg("Sheet.FormatExponent"), i18n.Msg("Sheet.FormatLogical"), i18n.Msg("Sheet.FormatCurrency"),
		i18n.Msg("Sheet.FormatPercent"), i18n.Msg("Sheet.FormatHidden"),
	})
	lblJustify := vtui.NewText(0, 0, i18n.Msg("Sheet.FormatJustify"), vtui.Palette[vtui.ColDialogText])
	justify := vtui.NewRadioGroup(0, 0, 3, []string{
		i18n.Msg("Sheet.JustifyLeft"), i18n.Msg("Sheet.JustifyRight"), i18n.Msg("Sheet.JustifyCenter"),
	})
	lblDecimals := vtui.NewText(0, 0, i18n.Msg("Sheet.FormatDecimals"), vtui.Palette[vtui.ColDialogText])
	decimals := vtui.NewEdit(0, 0, 6, "2")
	protected := vtui.NewCheckbox(0, 0, i18n.Msg("Sheet.FormatProtected"), false)

	if cell := sf.Document().Cell(sf.Cursor().Col, sf.Cursor().Row); cell != nil {
		display.Selected = int(cell.Display)
		justify.Selected = int(cell.Justify)
		decimals.SetText(strconv.Itoa(int(cell.Decimals)))
		if cell.Protected {
			protected.State = 1
		}
	}

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{lblDisplay, display, lblJustify, justify, lblDecimals, decimals, protected, btnOk, btnCancel} {
		dlg.AddItem(item)
	}
	box := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, 44, 14)
	box.Add(lblDisplay, vtui.Margins{}, vtui.AlignLeft)
	box.Add(display, vtui.Margins{}, vtui.AlignFill)
	box.Add(lblJustify, vtui.Margins{Top: 1}, vtui.AlignLeft)
	box.Add(justify, vtui.Margins{}, vtui.AlignFill)
	box.Add(lblDecimals, vtui.Margins{Top: 1}, vtui.AlignLeft)
	box.Add(decimals, vtui.Margins{}, vtui.AlignLeft)
	box.Add(protected, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, 44, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	box.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Apply()

	btnOk.OnClick = func() {
		places, err := strconv.Atoi(strings.TrimSpace(decimals.GetText()))
		if err != nil || places < 0 || places > 20 {
			places = int(sheet.DefaultDecimals)
		}
		selectedDisplay := sheet.DisplayAsIs
		switch display.Selected {
		case 1:
			selectedDisplay = sheet.DisplayDecimal
		case 2:
			selectedDisplay = sheet.DisplayComma
		case 3:
			selectedDisplay = sheet.DisplayExponent
		case 4:
			selectedDisplay = sheet.DisplayLogical
		case 5:
			selectedDisplay = sheet.DisplayCurrency
		case 6:
			selectedDisplay = sheet.DisplayPercent
		case 7:
			selectedDisplay = sheet.DisplayHidden
		}
		selectedJustify := sheet.JustifyLeft
		switch justify.Selected {
		case 1:
			selectedJustify = sheet.JustifyRight
		case 2:
			selectedJustify = sheet.JustifyCenter
		}
		isProtected := protected.State == 1
		dlg.Close()
		sf.Document().Format(sf.Block(), selectedDisplay, selectedJustify, uint8(places), isProtected)
	}
	btnCancel.OnClick = func() { dlg.Close() }
	vtui.FrameManager.Push(dlg)
}

// showSheetFindDialog runs the search, optionally with a replacement.
func showSheetFindDialog(sf *SheetFrame, withReplace bool) {
	height := 13
	title := i18n.Msg("Sheet.FindTitle")
	if withReplace {
		height = 16
		title = i18n.Msg("Sheet.ReplaceTitle")
	}
	dlg := vtui.NewCenteredDialog(56, height, title)
	dlg.ShowClose = true

	lblPattern := vtui.NewText(0, 0, i18n.Msg("Sheet.FindPattern"), vtui.Palette[vtui.ColDialogText])
	pattern := vtui.NewEdit(0, 0, 48, sf.search.Pattern)
	lblReplace := vtui.NewText(0, 0, i18n.Msg("Sheet.ReplaceWith"), vtui.Palette[vtui.ColDialogText])
	replacement := vtui.NewEdit(0, 0, 48, sf.replace)
	searchAs := vtui.NewRadioGroup(0, 0, 2, []string{i18n.Msg("Sheet.FindAsString"), i18n.Msg("Sheet.FindAsValue")})
	caseSensitive := vtui.NewCheckbox(0, 0, i18n.Msg("Sheet.FindCase"), false)
	wholeWords := vtui.NewCheckbox(0, 0, i18n.Msg("Sheet.FindWholeWords"), false)
	if sf.search.ByValue {
		searchAs.Selected = 1
	}
	if sf.search.CaseSensitive {
		caseSensitive.State = 1
	}
	if sf.search.WholeWords {
		wholeWords.State = 1
	}

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	items := []vtui.UIElement{lblPattern, pattern, searchAs, caseSensitive, wholeWords, btnOk, btnCancel}
	if withReplace {
		items = append(items, lblReplace, replacement)
	}
	for _, item := range items {
		dlg.AddItem(item)
	}

	box := vtui.NewVBoxLayout(dlg.X1+3, dlg.Y1+2, 50, height-4)
	box.Add(lblPattern, vtui.Margins{}, vtui.AlignLeft)
	box.Add(pattern, vtui.Margins{}, vtui.AlignFill)
	if withReplace {
		box.Add(lblReplace, vtui.Margins{Top: 1}, vtui.AlignLeft)
		box.Add(replacement, vtui.Margins{}, vtui.AlignFill)
	}
	box.Add(searchAs, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Add(caseSensitive, vtui.Margins{}, vtui.AlignLeft)
	box.Add(wholeWords, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, 50, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	box.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	box.Apply()

	btnOk.OnClick = func() {
		options := sheet.SearchOptions{
			Pattern:       pattern.GetText(),
			ByValue:       searchAs.Selected == 1,
			CaseSensitive: caseSensitive.State == 1,
			WholeWords:    wholeWords.State == 1,
		}
		newReplacement := replacement.GetText()
		dlg.Close()
		sf.search = options
		sf.replace = newReplacement
		sf.replacing = withReplace
		sheetSearchNext(sf, sf.Cursor())
	}
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.SetFocusedItem(pattern)
	vtui.FrameManager.Push(dlg)
}

// sheetSearchNext moves to the next match, replacing it when the last dialog
// was the replace one.
func sheetSearchNext(sf *SheetFrame, from sheet.Point) {
	if sf.search.Pattern == "" {
		return
	}
	point, ok := sf.Document().Find(sf.search, from)
	if !ok {
		sf.SetStatus(i18n.Msg("Sheet.NotFound"))
		return
	}
	sf.gotoCell(point.Col, point.Row)
	if sf.replacing {
		if sf.Document().Replace(point, sf.search, sf.replace) {
			sf.SetStatus(i18n.Msg("Sheet.Replaced"))
			return
		}
	}
	sf.SetStatus(fmt.Sprintf(i18n.Msg("Sheet.Found"), sheet.CellName(point.Col, point.Row)))
}

// sheetSearchAgain repeats the last search from the cell after the cursor.
func sheetSearchAgain(sf *SheetFrame) {
	next := sf.Cursor()
	next.Row++
	sheetSearchNext(sf, next)
}

// sheetMenuEntry is one line of the F10 menu.
type sheetMenuEntry struct {
	label  string
	hotkey string
	run    func(*SheetFrame)
}

func sheetMenuEntries() []sheetMenuEntry {
	return []sheetMenuEntry{
		{i18n.Msg("Sheet.MenuNew"), "F4", func(sf *SheetFrame) { sheetNew(sf) }},
		{i18n.Msg("Sheet.MenuOpen"), "F3", showSheetOpenDialog},
		{i18n.Msg("Sheet.MenuSave"), "F2", func(sf *SheetFrame) { sheetSave(sf, sf.Path()) }},
		{i18n.Msg("Sheet.MenuSaveAs"), "Shift-F2", showSheetSaveAsDialog},
		{i18n.Msg("Sheet.MenuExport"), "Shift-F3", showSheetExportDialog},
		{i18n.Msg("Sheet.MenuUndo"), "Alt-BkSp", func(sf *SheetFrame) {
			if !sf.Document().Undo() {
				sf.SetStatus(i18n.Msg("Sheet.NothingToUndo"))
			}
		}},
		{i18n.Msg("Sheet.MenuCut"), "Shift-Del", func(sf *SheetFrame) {
			sf.clipboard = sf.Document().CutBlock(sf.Block())
			sf.marking = false
		}},
		{i18n.Msg("Sheet.MenuCopy"), "Ctrl-Ins", func(sf *SheetFrame) {
			sf.clipboard = sf.Document().CopyBlock(sf.Block())
		}},
		{i18n.Msg("Sheet.MenuPaste"), "Shift-Ins", func(sf *SheetFrame) {
			if sf.clipboard != nil {
				sf.Document().PasteBlock(sf.clipboard, sf.Cursor().Col, sf.Cursor().Row)
			}
		}},
		{i18n.Msg("Sheet.MenuClear"), "Ctrl-Del", func(sf *SheetFrame) {
			sf.Document().ClearBlock(sf.Block())
			sf.marking = false
		}},
		{i18n.Msg("Sheet.MenuFind"), "F7", func(sf *SheetFrame) { showSheetFindDialog(sf, false) }},
		{i18n.Msg("Sheet.MenuReplace"), "", func(sf *SheetFrame) { showSheetFindDialog(sf, true) }},
		{i18n.Msg("Sheet.MenuSearchAgain"), "", sheetSearchAgain},
		{i18n.Msg("Sheet.MenuGoto"), "F6", showSheetGotoDialog},
		{i18n.Msg("Sheet.MenuGotoError"), "", func(sf *SheetFrame) {
			point, ok := sf.Document().LastError()
			if !ok {
				sf.SetStatus(i18n.Msg("Sheet.NoErrors"))
				return
			}
			sf.gotoCell(point.Col, point.Row)
		}},
		{i18n.Msg("Sheet.MenuCellFormat"), "Alt-O", showSheetFormatDialog},
		{i18n.Msg("Sheet.MenuColumnWidth"), "Alt-Arrows", showSheetWidthDialog},
		{i18n.Msg("Sheet.MenuSeparators"), "Alt-S", func(sf *SheetFrame) {
			sf.Document().Separators = !sf.Document().Separators
		}},
		{i18n.Msg("Sheet.MenuInsertRow"), "Alt-I", func(sf *SheetFrame) { sf.Document().InsertRow(sf.Cursor().Row) }},
		{i18n.Msg("Sheet.MenuInsertColumn"), "Alt-C", func(sf *SheetFrame) { sf.Document().InsertColumn(sf.Cursor().Col) }},
		{i18n.Msg("Sheet.MenuDeleteRow"), "Alt-L", func(sf *SheetFrame) { sf.Document().DeleteRow(sf.Cursor().Row) }},
		{i18n.Msg("Sheet.MenuDeleteColumn"), "Alt-D", func(sf *SheetFrame) { sf.Document().DeleteColumn(sf.Cursor().Col) }},
		{i18n.Msg("Sheet.MenuRecalc"), "F5", func(sf *SheetFrame) {
			sf.Document().Recalc()
			sf.reportError()
		}},
	}
}

// showSheetMenu lists every command with its shortcut.
func showSheetMenu(sf *SheetFrame) {
	entries := sheetMenuEntries()
	labels := make([]string, len(entries))
	widest := 0
	for _, entry := range entries {
		if length := len([]rune(entry.label)); length > widest {
			widest = length
		}
	}
	for index, entry := range entries {
		labels[index] = entry.label + strings.Repeat(" ", widest-len([]rune(entry.label))+2) + entry.hotkey
	}

	height := len(entries) + 4
	if height > 24 {
		height = 24
	}
	dlg := vtui.NewCenteredDialog(52, height, i18n.Msg("Sheet.MenuTitle"))
	dlg.ShowClose = true
	list := vtui.NewListBox(dlg.X1+2, dlg.Y1+1, 48, height-3, labels)
	list.ColorTextIdx = vtui.ColDialogText
	list.ColorSelectedTextIdx = vtui.ColDialogSelectedButton
	list.OnAction = func(index int) {
		if index < 0 || index >= len(entries) {
			return
		}
		action := entries[index].run
		dlg.Close()
		action(sf)
	}
	dlg.AddItem(list)
	dlg.SetFocusedItem(list)
	vtui.FrameManager.Push(dlg)
}
