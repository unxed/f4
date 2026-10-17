package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// browserWindow is the dialog with one key of its own.
type browserWindow struct {
	*vtui.Window
	browser *browser
}

// ProcessKey makes F9 do what the label above the SQL box has always claimed
// it does. Nothing handled the key here, so it fell through to the frame
// manager, which has its own use for F9 and no idea about this dialog: the
// statement stayed where it was typed and the only way to run it was to tab
// to the button.
func (w *browserWindow) ProcessKey(e *vtinput.InputEvent) bool {
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_F1 {
		vfs.ShowPanelHelp(browserHelpTitle(), browserHelpText())
		return true
	}
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_F9 {
		w.browser.runQuery()
		return true
	}
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_F4 {
		w.browser.editCell()
		return true
	}
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_INSERT {
		w.browser.insertRow()
		return true
	}
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_DELETE {
		w.browser.deleteRow()
		return true
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	if e.KeyDown && ctrl && e.VirtualKeyCode == vtinput.VK_NEXT {
		w.browser.turnPage(browsePageSize)
		return true
	}
	if e.KeyDown && ctrl && e.VirtualKeyCode == vtinput.VK_PRIOR {
		w.browser.turnPage(-browsePageSize)
		return true
	}
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	if e.KeyDown && !ctrl && !alt && !shift && e.VirtualKeyCode == vtinput.VK_RETURN &&
		w.browser.dialog.GetFocusedItem() == vtui.UIElement(w.browser.resultTable) {
		// Enter on the grid is the same gesture as F4. Before this it fell
		// through to the dialog's default button, which is Run, and Run on
		// the prefilled SELECT re-read the same rows as a hand written query
		// -- one without rowids -- so the table quietly stopped being
		// editable until Refresh put the identities back.
		w.browser.editCell()
		return true
	}
	return w.Window.ProcessKey(e)
}

type browser struct {
	app          vfs.App
	session      *databaseSession
	path         string
	tables       []string
	currentTable string
	columns      []string
	rowIDs       []int64
	writable     bool
	offset       int64
	total        int64
	// selectRowID moves the cursor onto a row once the table it belongs to
	// has been read again, which is how a newly inserted row is found.
	selectRowID int64
	dialog      *vtui.Window
	frame       *browserWindow
	tableList   *vtui.ListBox
	resultTable *vtui.Table
	query       *vtui.MultiLineEdit
	status      *vtui.Text
	closed      bool
	// onClose runs once the client has closed, when whoever opened it wants
	// to know. The database panel reads its tables again then.
	onClose func()
}

func newBrowser(app vfs.App, session *databaseSession, tables []string) *browser {
	return newBrowserAt(app, session, tables, "")
}

// newBrowserAt opens the client on initialTable when the database has it, and
// on the first table otherwise. Enter on a table in the database panel lands
// here with the table's name.
func newBrowserAt(app vfs.App, session *databaseSession, tables []string, initialTable string) *browser {
	width, height := 110, 32
	if vtui.FrameManager != nil {
		if maxWidth := vtui.FrameManager.GetScreenSize() - 2; maxWidth > 20 && width > maxWidth {
			width = maxWidth
		}
		if maxHeight := vtui.FrameManager.GetScreenHeight() - 2; maxHeight > 18 && height > maxHeight {
			height = maxHeight
		}
	}
	if width < 72 {
		width = 72
	}
	if height < 20 {
		height = 20
	}

	b := &browser{app: app, session: session, path: session.path, tables: append([]string(nil), tables...)}
	b.dialog = vtui.NewCenteredDialog(width, height, " "+sqliteText("SQLite.Title", "SQLite", "SQLite")+": "+filepath.Base(session.path)+" ")
	b.dialog.ShowClose = true

	leftX := b.dialog.X1 + 2
	topY := b.dialog.Y1 + 2
	leftWidth := 24
	rightX := leftX + leftWidth + 2
	rightWidth := b.dialog.X2 - rightX - 1
	dataHeight := height - 13
	if dataHeight < 3 {
		dataHeight = 3
	}

	b.tableList = vtui.NewListBox(leftX, topY+1, leftWidth, dataHeight, b.tables)
	useDialogListColors(b.tableList)
	b.resultTable = vtui.NewTable(rightX, topY+1, rightWidth, dataHeight, nil)
	b.resultTable.CellSelection = true
	b.resultTable.ShowHeader = true
	b.resultTable.ShowSeparators = true
	b.resultTable.ColorTextIdx = vtui.ColDialogText
	b.resultTable.ColorSelectedTextIdx = vtui.ColDialogSelectedButton
	b.resultTable.ColorTitleIdx = vtui.ColDialogHighlightText
	b.resultTable.ColorBoxIdx = vtui.ColDialogBox

	b.dialog.AddItem(vtui.NewText(leftX, topY, sqliteText("SQLite.TablesViews", "Tables / views", "Таблицы / представления"), 0))
	b.dialog.AddItem(vtui.NewText(rightX, topY, sqliteText("SQLite.ResultLimit", "Result (maximum 100 rows)", "Результат (не более 100 строк)"), 0))
	b.dialog.AddItem(b.tableList)
	b.dialog.AddItem(b.resultTable)

	queryY := b.dialog.Y2 - 7
	b.dialog.AddItem(vtui.NewText(leftX, queryY-1, sqliteText("SQLite.SQLHint", "SQL (F9 or Run)", "SQL (F9 или Выполнить)"), 0))
	b.query = vtui.NewMultiLineEdit(leftX, queryY, b.dialog.X2-leftX-1, 3, "SELECT 1")
	b.query.SetGrowMode(vtui.GrowHiX | vtui.GrowHiY)
	b.dialog.AddItem(b.query)

	b.status = vtui.NewText(leftX, b.dialog.Y2-4, "", 0)
	b.dialog.AddItem(b.status)

	runButton := vtui.NewButton(0, 0, sqliteText("SQLite.Run", "&Run", "&Выполнить"))
	refreshButton := vtui.NewButton(0, 0, sqliteText("SQLite.Refresh", "&Refresh", "&Обновить"))
	closeButton := vtui.NewButton(0, 0, sqliteText("SQLite.Close", "&Close", "&Закрыть"))
	runButton.IsDefault = true
	runButton.OnClick = b.runQuery
	refreshButton.OnClick = b.refresh
	closeButton.OnClick = b.dialog.Close
	b.dialog.AddItem(runButton)
	b.dialog.AddItem(refreshButton)
	b.dialog.AddItem(closeButton)
	buttonLayout := vtui.NewHBoxLayout(b.dialog.X1+2, b.dialog.Y2-2, width-4, 1)
	buttonLayout.HorizontalAlign = vtui.AlignCenter
	buttonLayout.Spacing = 2
	buttonLayout.Add(runButton, vtui.Margins{}, vtui.AlignTop)
	buttonLayout.Add(refreshButton, vtui.Margins{}, vtui.AlignTop)
	buttonLayout.Add(closeButton, vtui.Margins{}, vtui.AlignTop)
	buttonLayout.Apply()
	runButton.SetGrowMode(vtui.GrowAll)
	refreshButton.SetGrowMode(vtui.GrowAll)
	closeButton.SetGrowMode(vtui.GrowAll)

	b.tableList.OnSelect = func(index int) {
		if index >= 0 && index < len(b.tables) {
			b.loadTable(b.tables[index])
		}
	}
	b.tableList.OnAction = func(index int) {
		if index >= 0 && index < len(b.tables) {
			b.loadTable(b.tables[index])
		}
	}
	b.dialog.OnResult = func(int) {
		if b.closed {
			return
		}
		b.closed = true
		b.session.Close()
		if b.onClose != nil {
			b.onClose()
		}
	}

	b.frame = &browserWindow{Window: b.dialog, browser: b}

	if len(b.tables) > 0 {
		first := 0
		for index, table := range b.tables {
			if table == initialTable {
				first = index
				break
			}
		}
		b.tableList.SetSelectPos(first)
		b.loadTable(b.tables[first])
	} else {
		b.setStatus(sqliteText("SQLite.NoTables", "The database has no user tables or views.", "В базе нет пользовательских таблиц или представлений."))
	}
	return b
}

// loadTable reads the first page of a table into the right hand side.
func (b *browser) loadTable(table string) {
	b.loadPage(table, 0)
}

// turnPage moves the browse window by whole pages. A table shorter than one
// page has nowhere to go, and the last page stops at the end rather than
// running off it.
func (b *browser) turnPage(by int64) {
	if b.closed || b.currentTable == "" {
		return
	}
	offset := clampOffset(b.offset+by, b.total)
	if offset == b.offset {
		return
	}
	b.loadPage(b.currentTable, offset)
}

// loadPage reads one page of a table.
func (b *browser) loadPage(table string, offset int64) {
	if b.closed {
		return
	}
	b.currentTable = table
	b.query.SetText(tableSelect(table, offset))
	var browse tableBrowse
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), fmt.Sprintf(sqliteText("SQLite.ReadingTable", "Reading %s...", "Чтение %s..."), table), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.ReadingTableProgress", "Reading table...", "Чтение таблицы..."), -1)
			var err error
			browse, err = b.session.browseTable(ctx, table, offset)
			return err
		},
		func(err error) {
			if b.closed || b.currentTable != table {
				return
			}
			if err != nil {
				b.setStatus(fmt.Sprintf(sqliteText("SQLite.ReadFailed", "Could not read %s: %v", "Не удалось прочитать %s: %v"), table, err))
				return
			}
			b.applyBrowse(table, browse)
			b.setStatus(b.browseStatus(table, browse))
			b.takePendingSelection()
		})
}

// browseStatus says which rows of how many are on screen, because a hundred
// rows out of a thousand look exactly like all of them otherwise.
func (b *browser) browseStatus(table string, browse tableBrowse) string {
	if browse.total == 0 || len(browse.result.Rows) == 0 {
		return fmt.Sprintf(sqliteText("SQLite.TableRows", "%s: %d row(s)", "%s: %d строк(и)"), table, len(browse.result.Rows))
	}
	first := browse.offset + 1
	last := browse.offset + int64(len(browse.result.Rows))
	return fmt.Sprintf(sqliteText("SQLite.TableRange", "%s: rows %d-%d of %d", "%s: строки %d-%d из %d"), table, first, last, browse.total)
}

// applyBrowse shows a reading of a table that a worker has already done.
//
// Everything that changes a table -- an edit, an insert, a statement from the
// SQL box -- reads it again on its own worker and ends here, rather than
// starting a second progress task from inside the first one's completion. A
// nested task left the rows on screen out of step with the rowids behind them
// until the user pressed Refresh, and one gesture is one task.
func (b *browser) applyBrowse(table string, browse tableBrowse) {
	b.currentTable = table
	b.rowIDs = browse.rowIDs
	b.writable = browse.writable
	b.offset = browse.offset
	b.total = browse.total
	b.applyResult(browse.result)
}

// refresh re-reads the schema and then the table being shown.
//
// It used to reload the current table and nothing else, so on a database whose
// tables had changed -- and on a new one, where there is no current table at
// all -- the button did nothing whatsoever, and the only way to see a table
// that had just been created was to close the client and open it again.
func (b *browser) refresh() {
	if b.closed {
		return
	}
	current := b.currentTable
	// Read on the UI goroutine, before the worker starts: the worker must not
	// touch the browser's fields.
	offset := b.offset
	var (
		tables []string
		table  string
		browse tableBrowse
	)
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), sqliteText("SQLite.ReadingSchema", "Reading database schema...", "Чтение схемы базы данных..."), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.ReadingSchema", "Reading database schema...", "Чтение схемы базы данных..."), -1)
			var err error
			if tables, err = b.session.listTables(ctx); err != nil {
				return err
			}
			table = tableToShow(tables, current)
			if table == "" {
				return nil
			}
			if table != current {
				offset = 0
			}
			browse, err = b.session.browseTable(ctx, table, offset)
			return err
		},
		func(err error) {
			if b.closed {
				return
			}
			if err != nil {
				b.setStatus(fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err))
				return
			}
			b.setTables(tables)
			if table == "" {
				b.applyResult(queryResult{})
				b.setStatus(sqliteText("SQLite.NoTables", "The database has no user tables or views.", "В базе нет пользовательских таблиц или представлений."))
				return
			}
			b.selectTableInList(table)
			b.applyBrowse(table, browse)
			b.setStatus(b.browseStatus(table, browse))
		})
}

// tableToShow keeps the table the user was looking at when it is still there,
// and otherwise falls back to the first one.
func tableToShow(tables []string, current string) string {
	for _, table := range tables {
		if table == current {
			return current
		}
	}
	if len(tables) > 0 {
		return tables[0]
	}
	return ""
}

// selectTableInList moves the cursor of the list on the left onto table.
func (b *browser) selectTableInList(table string) {
	for index, candidate := range b.tables {
		if candidate == table {
			b.tableList.SetSelectPos(index)
			return
		}
	}
}

func (b *browser) runQuery() {
	if b.closed {
		return
	}
	statement := strings.TrimSpace(b.query.GetText())
	if statement == "" {
		b.setStatus(sqliteText("SQLite.EmptySQL", "SQL statement is empty", "SQL-запрос пуст."))
		return
	}
	if b.currentTable != "" && statement == tableSelect(b.currentTable, b.offset) {
		// The box still holds exactly what loadPage put there. Running it as
		// a hand written query would return the same rows stripped of their
		// rowids and turn editing off; reading the page keeps them.
		b.loadPage(b.currentTable, b.offset)
		return
	}
	current := b.currentTable
	offset := b.offset
	var (
		result queryResult
		tables []string
		table  string
		browse tableBrowse
	)
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), sqliteText("SQLite.ExecutingSQL", "Executing SQL...", "Выполнение SQL..."), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.ExecutingSQL", "Executing SQL...", "Выполнение SQL..."), -1)
			var err error
			if result, err = b.session.execute(ctx, statement); err != nil {
				return err
			}
			if result.ReturnsRows {
				return nil
			}
			// CREATE, DROP and ALTER change what the list on the left is
			// showing, and an INSERT or an UPDATE changes what the right hand
			// side is showing. Both are re-read here, on the worker that ran
			// the statement.
			if tables, err = b.session.listTables(ctx); err != nil {
				return err
			}
			if table = tableToShow(tables, current); table == "" {
				return nil
			}
			if table != current {
				offset = 0
			}
			browse, err = b.session.browseTable(ctx, table, offset)
			return err
		},
		func(err error) {
			if b.closed {
				return
			}
			if err != nil {
				message := fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err)
				b.setStatus(message)
				// A rejected statement has to say so. Truncated into the
				// status line it reads like nothing happened at all, which is
				// exactly what a typo in the SQL box looked like: the file
				// stays empty, the list stays empty, and nobody is told why.
				// SQLite names what it did not like; put that in front of the
				// user and leave the statement in the box to be fixed.
				vtui.ShowMessageOn(b.frame,
					sqliteText("SQLite.Title", " SQLite ", " SQLite "),
					message,
					[]string{sqliteText("SQLite.OK", "&OK", "&ОК")})
				return
			}
			if result.ReturnsRows {
				// The rows of a hand written query are not tied to any one
				// table, so nothing here can be written back.
				b.rowIDs = nil
				b.writable = false
				b.applyResult(result)
				b.setStatus(fmt.Sprintf(sqliteText("SQLite.Rows", "%d row(s)", "%d строк(и)"), len(result.Rows)))
				return
			}
			b.setTables(tables)
			b.setStatus(fmt.Sprintf(sqliteText("SQLite.StatementCompleted", "Statement completed; %d row(s) affected", "Запрос выполнен; затронуто строк: %d"), result.RowsAffected))
			if table == "" {
				return
			}
			// A CREATE TABLE on a database that had nothing in it shows what
			// was just made instead of an empty right hand side.
			b.selectTableInList(table)
			b.applyBrowse(table, browse)
		})
}
func (b *browser) setTables(tables []string) {
	b.tables = tables
	b.tableList.Items = tables
	b.tableList.UpdateRows()
	if len(tables) == 0 {
		b.currentTable = ""
		return
	}
	if b.tableList.SelectPos >= len(tables) {
		b.tableList.SetSelectPos(0)
	}
	for _, table := range tables {
		if table == b.currentTable {
			return
		}
	}
	b.currentTable = ""
}

func (b *browser) applyResult(result queryResult) {
	b.columns = result.Columns
	if b.resultTable.SelectCol >= len(result.Columns) {
		b.resultTable.SelectCol = 0
	}
	columns := make([]vtui.TableColumn, len(result.Columns))
	for index, column := range result.Columns {
		columns[index] = vtui.TableColumn{Title: column, Width: 0, MinWidth: 8}
	}
	rows := make([]vtui.TableRow, len(result.Rows))
	for index, cells := range result.Rows {
		rows[index] = resultRow{cells: cells}
	}
	b.resultTable.Columns = columns
	b.resultTable.SetRows(rows)
}

// editCell edits the cell under the cursor and writes it back.
//
// Only a table browse can be edited: the rows of a hand written query are not
// tied to a table, and a view or a WITHOUT ROWID table has no rowid to write
// against. The value is read again before editing rather than taken off the
// screen, because what is on screen is escaped and cut at 512 characters.
//
// That read is done here rather than on a progress task. One row by rowid is
// an indexed lookup on a database that is already open, and a task for it puts
// a progress dialog on a workspace of its own, which is then torn down before
// the input box opens: the box ends up on whichever workspace that leaves
// active, and after it closes the client is no longer the frame the keyboard
// goes to. Pressing Enter without changing anything is where it showed --
// nothing was written, so nothing put the client back, and the next F4 went
// nowhere until something was clicked.
func (b *browser) editCell() {
	if b.closed {
		return
	}
	column, rowID, ok := b.cellUnderCursor()
	if !ok {
		b.setStatus(sqliteText("SQLite.NotEditable",
			"Only a table from the list on the left can be edited, and only one with rowids.",
			"Редактировать можно только таблицу из списка слева, и только имеющую rowid."))
		return
	}

	value, err := b.session.cellValue(context.Background(), b.currentTable, column, rowID)
	if err != nil {
		b.setStatus(fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err))
		return
	}
	text, editable := editableText(value)
	if !editable {
		b.setStatus(sqliteText("SQLite.CellNotEditable",
			"This cell holds binary data or line breaks; change it with SQL.",
			"В ячейке двоичные данные или переводы строк; измените её запросом SQL."))
		return
	}
	// Anchored to the client's own window, so the box opens over it and hands
	// the keyboard back to it.
	vtui.InputBoxOn(b.frame,
		sqliteText("SQLite.Title", " SQLite ", " SQLite "),
		fmt.Sprintf(sqliteText("SQLite.EditPrompt", "New value for %s:", "Новое значение для %s:"), column),
		text,
		func(answer string) {
			// Unchanged means unchanged. Pressing Enter over a NULL cell must
			// not quietly turn it into an empty string.
			if answer == text {
				return
			}
			b.confirmAffinity(column, rowID, answer)
		})
}

// deleteRow removes the row under the cursor, after asking.
func (b *browser) deleteRow() {
	if b.closed {
		return
	}
	_, rowID, ok := b.cellUnderCursor()
	if !ok {
		b.setStatus(sqliteText("SQLite.NotEditable",
			"Only a table from the list on the left can be edited, and only one with rowids.",
			"Редактировать можно только таблицу из списка слева, и только имеющую rowid."))
		return
	}
	table := b.currentTable
	confirm := vtui.ShowMessageOn(b.frame,
		sqliteText("SQLite.Title", " SQLite ", " SQLite "),
		fmt.Sprintf(sqliteText("SQLite.DeletePrompt", "Delete this row from %s?", "Удалить эту строку из %s?"), table),
		[]string{
			sqliteText("SQLite.Delete", "&Delete", "&Удалить"),
			sqliteText("SQLite.Cancel", "&Cancel", "О&тмена"),
		})
	if confirm == nil {
		return
	}
	// A row is gone for good, and no undo follows it, so the first button is
	// the one that deletes and Esc is the one that does nothing.
	confirm.OnResult = func(code int) {
		if code == 0 {
			b.removeRow(table, rowID)
		}
	}
}

// removeRow deletes one row and shows the table as it is afterwards.
func (b *browser) removeRow(table string, rowID int64) {
	if b.closed {
		return
	}
	offset := b.offset
	var (
		affected int64
		browse   tableBrowse
	)
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), sqliteText("SQLite.DeletingRow", "Deleting the row...", "Удаление строки..."), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.DeletingRow", "Deleting the row...", "Удаление строки..."), -1)
			var err error
			if affected, err = b.session.deleteRow(ctx, table, rowID); err != nil {
				return err
			}
			browse, err = b.session.browseTable(ctx, table, offset)
			return err
		},
		func(err error) {
			if b.closed || b.currentTable != table {
				return
			}
			if err != nil {
				// A foreign key with rows depending on this one refuses the
				// delete, and SQLite says which constraint it was.
				message := fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err)
				b.setStatus(message)
				vtui.ShowMessageOn(b.frame,
					sqliteText("SQLite.Title", " SQLite ", " SQLite "),
					message,
					[]string{sqliteText("SQLite.OK", "&OK", "&ОК")})
				return
			}
			b.applyBrowse(table, browse)
			if affected == 0 {
				b.setStatus(sqliteText("SQLite.RowGone", "That row is no longer there.", "Этой строки больше нет."))
				return
			}
			b.setStatus(sqliteText("SQLite.RowDeleted", "Row deleted.", "Строка удалена."))
		})
}

// confirmAffinity asks before a value goes into a column whose declared type
// it does not match, and writes it when the user says so.
//
// SQLite would take it silently either way: its column types are affinities,
// so an int column converts "42" and stores "тест" as the text it is. The
// database is not wrong and the client does not overrule it -- a STRICT table
// refuses on its own, and that refusal is shown as it stands -- but a typed
// value that misses the declared type is usually a slip worth one question.
func (b *browser) confirmAffinity(column string, rowID int64, value string) {
	declared, err := b.session.columnDeclaredType(context.Background(), b.currentTable, column)
	if err != nil || declared == "" || !storedAsTextInstead(typeAffinity(declared), value) {
		// A pragma that failed is no reason to block an edit; the write
		// itself will say what is wrong with it.
		b.writeCell(column, rowID, value)
		return
	}
	confirm := vtui.ShowMessageOn(b.frame,
		sqliteText("SQLite.Title", " SQLite ", " SQLite "),
		fmt.Sprintf(sqliteText("SQLite.AffinityPrompt",
			"%s is declared %s; %q is not a number and will be stored as text. Write it?",
			"Столбец %s объявлен как %s; %q — не число и будет сохранено как текст. Записать?"),
			column, declared, value),
		[]string{
			sqliteText("SQLite.Write", "&Write", "&Записать"),
			sqliteText("SQLite.Cancel", "&Cancel", "О&тмена"),
		})
	if confirm == nil {
		return
	}
	confirm.OnResult = func(code int) {
		if code == 0 {
			b.writeCell(column, rowID, value)
		}
	}
}

// cellUnderCursor resolves the table cursor to a column and a rowid.
func (b *browser) cellUnderCursor() (column string, rowID int64, ok bool) {
	if !b.writable || b.currentTable == "" || len(b.rowIDs) == 0 {
		return "", 0, false
	}
	row, col := b.resultTable.SelectPos, b.resultTable.SelectCol
	if row < 0 || row >= len(b.rowIDs) || col < 0 || col >= len(b.columns) {
		return "", 0, false
	}
	return b.columns[col], b.rowIDs[row], true
}

// writeCell stores one edited value and shows the table as it is afterwards.
func (b *browser) writeCell(column string, rowID int64, value string) {
	if b.closed {
		return
	}
	table := b.currentTable
	offset := b.offset
	var (
		affected int64
		browse   tableBrowse
	)
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), sqliteText("SQLite.WritingValue", "Writing the value...", "Запись значения..."), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.WritingValue", "Writing the value...", "Запись значения..."), -1)
			var err error
			if affected, err = b.session.updateCell(ctx, table, column, rowID, value); err != nil {
				return err
			}
			// A trigger or a generated column can change more than the cell
			// that was written, so the table is read again -- here, on the
			// same worker, not from a second task started in the completion.
			browse, err = b.session.browseTable(ctx, table, offset)
			return err
		},
		func(err error) {
			if b.closed || b.currentTable != table {
				return
			}
			if err != nil {
				message := fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err)
				b.setStatus(message)
				vtui.ShowMessageOn(b.frame,
					sqliteText("SQLite.Title", " SQLite ", " SQLite "),
					message,
					[]string{sqliteText("SQLite.OK", "&OK", "&ОК")})
				return
			}
			b.applyBrowse(table, browse)
			if affected == 0 {
				b.setStatus(sqliteText("SQLite.RowGone", "That row is no longer there.", "Этой строки больше нет."))
				return
			}
			b.setStatus(fmt.Sprintf(sqliteText("SQLite.CellUpdated", "%s updated", "Поле %s изменено"), column))
		})
}

// insertRow adds a row of defaults to the table being browsed and puts the
// cursor on it, ready for F4.
func (b *browser) insertRow() {
	if b.closed {
		return
	}
	if !b.writable || b.currentTable == "" {
		b.setStatus(sqliteText("SQLite.NotEditable",
			"Only a table from the list on the left can be edited, and only one with rowids.",
			"Редактировать можно только таблицу из списка слева, и только имеющую rowid."))
		return
	}
	table := b.currentTable
	var (
		rowID  int64
		browse tableBrowse
	)
	b.app.RunProgressTask(sqliteText("SQLite.Title", " SQLite ", " SQLite "), sqliteText("SQLite.AddingRow", "Adding a row...", "Добавление строки..."), false,
		func(ctx context.Context, update func(string, int)) error {
			update(sqliteText("SQLite.AddingRow", "Adding a row...", "Добавление строки..."), -1)
			var err error
			if rowID, err = b.session.insertRow(ctx, table); err != nil {
				return err
			}
			// A rowid table appends: the new row is on the last page, so that
			// is the page to show, whatever page the browse was on before.
			total, err := b.session.countRows(ctx, table)
			if err != nil {
				return err
			}
			browse, err = b.session.browseTable(ctx, table, lastPageOffset(total))
			return err
		},
		func(err error) {
			if b.closed || b.currentTable != table {
				return
			}
			if err != nil {
				// SQLite names the column that would not take a default, and
				// that is the whole answer to why the row was refused.
				message := fmt.Sprintf(sqliteText("SQLite.SQLError", "SQL error: %v", "Ошибка SQL: %v"), err)
				b.setStatus(message)
				vtui.ShowMessageOn(b.frame,
					sqliteText("SQLite.Title", " SQLite ", " SQLite "),
					message,
					[]string{sqliteText("SQLite.OK", "&OK", "&ОК")})
				return
			}
			b.applyBrowse(table, browse)
			b.selectRowID = rowID
			b.takePendingSelection()
		})
}
func (b *browser) takePendingSelection() {
	rowID := b.selectRowID
	b.selectRowID = 0
	if rowID == 0 {
		return
	}
	for index, candidate := range b.rowIDs {
		if candidate != rowID {
			continue
		}
		b.resultTable.SetSelectPos(index)
		b.resultTable.SelectCol = 0
		b.setStatus(sqliteText("SQLite.RowAdded", "Row added; fill it in with F4.", "Строка добавлена; заполните её по F4."))
		return
	}
	// The insert path lands on the page that holds the new row, so with rowid
	// tables this is not reached; it stays for the day something ends up here
	// without a visible row to stand on.
	b.setStatus(sqliteText("SQLite.RowAdded", "Row added; fill it in with F4.", "Строка добавлена; заполните её по F4."))
}

func (b *browser) setStatus(message string) {
	if b.status != nil {
		b.status.SetText(vtui.TruncateMiddle(message, b.dialog.X2-b.dialog.X1-4))
	}
}

type resultRow struct{ cells []string }

func (row resultRow) GetCellText(column int) string {
	if column < 0 || column >= len(row.cells) {
		return ""
	}
	return row.cells[column]
}

func useDialogListColors(list *vtui.ListBox) {
	list.ColorTextIdx = vtui.ColDialogText
	list.ColorSelectedTextIdx = vtui.ColDialogSelectedButton
	list.ColorItemSelectTextIdx = vtui.ColDialogHighlightText
	list.ColorItemSelectCursorIdx = vtui.ColDialogHighlightSelectedButton
	list.ColorTitleIdx = vtui.ColDialogHighlightText
	list.ColorBoxIdx = vtui.ColDialogBox
	if list.ScrollBar != nil {
		list.ScrollBar.ColorIdx = vtui.ColDialogBox
	}
}
