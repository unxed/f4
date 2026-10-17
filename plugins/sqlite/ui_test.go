package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type sqliteBrowserApp struct {
	sqliteTestApp
	progressCalls int
}

func (a *sqliteBrowserApp) RunProgressTask(_ string, _ string, _ bool, worker func(context.Context, func(string, int)) error, onComplete func(error)) {
	a.progressCalls++
	err := worker(context.Background(), func(string, int) {})
	onComplete(err)
}

func (a *sqliteBrowserApp) Message(string, string, []string) int { return 0 }

func newSQLiteBrowserTest(t *testing.T, withRows bool) (*browser, *sqliteBrowserApp, *vtui.FrameManagerType) {
	t.Helper()
	oldFrameManager := vtui.FrameManager
	fm := vtui.NewFrameManager()
	fm.Init(vtui.NewSilentScreenBuf())
	vtui.FrameManager = fm

	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")
	db, err := driver.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, note TEXT, size INTEGER)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if withRows {
		if _, err := db.Exec(`INSERT INTO notes VALUES (1, 'first', 10), (2, NULL, 20)`); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	session, tables, err := openDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	app := &sqliteBrowserApp{sqliteTestApp: sqliteTestApp{fs: vfs.NewOSVFS(dir)}}
	b := newBrowser(app, session, tables)
	fm.Push(b.frame)
	t.Cleanup(func() {
		for fm.GetTopFrame() != nil && fm.GetTopFrame() != b.frame {
			fm.GetTopFrame().SetExitCode(-1)
			fm.Pop()
		}
		if fm.GetTopFrame() == b.frame {
			b.dialog.Close()
			fm.Pop()
		}
		fm.Shutdown()
		vtui.FrameManager = oldFrameManager
	})
	return b, app, fm
}

func dismissSQLiteTopFrame(fm *vtui.FrameManagerType) {
	if frame := fm.GetTopFrame(); frame != nil {
		frame.SetExitCode(-1)
		fm.Pop()
	}
}

func TestBrowserRunsQueriesAndRefreshesSchema(t *testing.T) {
	b, app, fm := newSQLiteBrowserTest(t, true)
	if b.currentTable != "notes" || !b.writable || len(b.rowIDs) != 2 {
		t.Fatalf("initial browse = table %q, writable %t, rowIDs %#v", b.currentTable, b.writable, b.rowIDs)
	}

	b.query.SetText("SELECT note FROM notes")
	b.runQuery()
	if b.writable || !strings.EqualFold(b.columns[0], "note") || len(b.rowIDs) != 0 {
		t.Fatalf("SELECT result = columns %#v, writable %t, rowIDs %#v", b.columns, b.writable, b.rowIDs)
	}

	b.query.SetText("UPDATE notes SET note = 'changed' WHERE id = 1")
	b.runQuery()
	value, err := b.session.cellValue(context.Background(), "notes", "note", 1)
	if err != nil || value != "changed" {
		t.Fatalf("updated note = %#v, err = %v", value, err)
	}
	if !b.writable || len(b.rowIDs) != 2 {
		t.Fatalf("browse after UPDATE = writable %t, rowIDs %#v", b.writable, b.rowIDs)
	}

	b.query.SetText("CREATE TABLE added (id INTEGER)")
	b.runQuery()
	if !containsString(b.tables, "added") {
		t.Fatalf("tables after CREATE TABLE = %#v", b.tables)
	}
	b.refresh()
	if !containsString(b.tables, "added") {
		t.Fatalf("tables after refresh = %#v", b.tables)
	}

	b.query.SetText("SELECT")
	b.runQuery()
	if fm.GetTopFrame() == b.frame {
		t.Fatal("invalid SQL did not show an error dialog")
	}
	dismissSQLiteTopFrame(fm)
	if app.progressCalls < 5 {
		t.Errorf("progress calls = %d, want at least 5", app.progressCalls)
	}
}

func TestBrowserWritesWithAffinityAndMutatesRows(t *testing.T) {
	b, _, fm := newSQLiteBrowserTest(t, true)

	b.writeCell("note", 1, "edited")
	value, err := b.session.cellValue(context.Background(), "notes", "note", 1)
	if err != nil || value != "edited" {
		t.Fatalf("written note = %#v, err = %v", value, err)
	}

	b.confirmAffinity("size", 1, "words")
	if fm.GetTopFrame() == b.frame {
		t.Fatal("mismatched numeric value did not ask for confirmation")
	}
	fm.GetTopFrame().SetExitCode(0)
	fm.Pop()
	value, err = b.session.cellValue(context.Background(), "notes", "size", 1)
	if err != nil || value != "words" {
		t.Fatalf("affinity-confirmed value = %#v, err = %v", value, err)
	}

	b.deleteRow()
	if fm.GetTopFrame() == b.frame {
		t.Fatal("delete did not ask for confirmation")
	}
	fm.GetTopFrame().SetExitCode(0)
	fm.Pop()
	browse, err := b.session.browseTable(context.Background(), "notes", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(browse.rowIDs) != 1 || browse.rowIDs[0] != 2 {
		t.Fatalf("rows after delete = %#v", browse.rowIDs)
	}

	b.insertRow()
	if len(b.rowIDs) != 2 || b.selectRowID != 0 {
		t.Fatalf("rows after insert = %#v, pending row = %d", b.rowIDs, b.selectRowID)
	}
	if b.resultTable.SelectPos != 1 {
		t.Errorf("selected inserted row = %d, want 1", b.resultTable.SelectPos)
	}
}

func TestBrowserWindowRoutesDatabaseGestures(t *testing.T) {
	b, _, fm := newSQLiteBrowserTest(t, true)

	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F1}) {
		t.Fatal("F1 was not handled")
	}
	if fm.GetTopFrame() == b.frame {
		t.Fatal("F1 did not open the SQLite browser help")
	}
	dismissSQLiteTopFrame(fm)

	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F9}) {
		t.Fatal("F9 was not handled")
	}
	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT, ControlKeyState: vtinput.LeftCtrlPressed}) {
		t.Fatal("Ctrl+PageDown was not handled")
	}
	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR, ControlKeyState: vtinput.RightCtrlPressed}) {
		t.Fatal("Ctrl+PageUp was not handled")
	}
	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_INSERT}) {
		t.Fatal("Insert was not handled")
	}
	if len(b.rowIDs) != 3 {
		t.Fatalf("rows after Insert = %#v", b.rowIDs)
	}
	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE}) {
		t.Fatal("Delete was not handled")
	}
	dismissSQLiteTopFrame(fm)

	b.dialog.SetFocusedItem(b.resultTable)
	if !b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}) {
		t.Fatal("Enter on the result table was not handled")
	}
	dismissSQLiteTopFrame(fm)
	if b.frame.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: 0xffff}) {
		t.Fatal("unrelated key was unexpectedly handled")
	}
}

func TestSQLiteFilenameAndTableHelpers(t *testing.T) {
	for _, name := range []string{"one.db", "two.sqlite", "three.sqlite3", "four.db3", "UPPER.DB"} {
		if !isSQLiteFilename(name) {
			t.Errorf("isSQLiteFilename(%q) = false", name)
		}
	}
	for _, name := range []string{"notes.txt", "database", "folder.db/child"} {
		if isSQLiteFilename(name) {
			t.Errorf("isSQLiteFilename(%q) = true", name)
		}
	}
	if got := tableToShow([]string{"first", "second"}, "missing"); got != "first" {
		t.Errorf("tableToShow fallback = %q, want first", got)
	}
	if got := tableToShow([]string{"first", "second"}, "second"); got != "second" {
		t.Errorf("tableToShow retained = %q, want second", got)
	}
	if got := tableToShow(nil, "missing"); got != "" {
		t.Errorf("tableToShow empty = %q, want empty", got)
	}
	row := resultRow{cells: []string{"one"}}
	if row.GetCellText(-1) != "" || row.GetCellText(1) != "" || row.GetCellText(0) != "one" {
		t.Fatalf("resultRow bounds/text are incorrect")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
