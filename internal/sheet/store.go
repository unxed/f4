//go:build !lite

package sheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/ncruces/go-sqlite3/driver"
)

const createSchema = `
CREATE TABLE IF NOT EXISTS f4_sheet_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS f4_sheet_columns (
	"col"  INTEGER PRIMARY KEY,
	width  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS f4_sheet_cells (
	"col"     INTEGER NOT NULL,
	"row"     INTEGER NOT NULL,
	text      TEXT NOT NULL,
	display   INTEGER NOT NULL DEFAULT 0,
	justify   INTEGER NOT NULL DEFAULT 0,
	decimals  INTEGER NOT NULL DEFAULT 0,
	protected INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY ("col", "row")
);`

// openDatabase opens a database file for the spreadsheet, limited to a single
// connection because the whole document is written inside one transaction.
func openDatabase(path string) (*sql.DB, error) {
	db, err := driver.Open(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// Save writes the sheet into a SQLite database, replacing whatever the file
// held before. The native format is a plain database, so the very same file
// can be inspected with the SQLite client built into f4.
//
// This is the regular-build store; a "-tags lite" build uses store_lite.go's
// JSON-backed implementation instead, behind the same API but writing a
// physically different, incompatible file format (see the package doc
// comment in cell.go).
func (s *Sheet) Save(ctx context.Context, path string) error {
	db, err := openDatabase(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, createSchema); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM f4_sheet_cells`,
		`DELETE FROM f4_sheet_columns`,
		`DELETE FROM f4_sheet_meta`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	meta := map[string]string{
		"schema":        strconv.Itoa(SchemaVersion),
		"title":         s.Title,
		"separators":    strconv.FormatBool(s.Separators),
		"csv_separator": string(s.CSVSeparator),
	}
	for key, value := range meta {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO f4_sheet_meta (key, value) VALUES (?, ?)`, key, value); err != nil {
			return err
		}
	}

	for col, width := range s.ColumnWidths() {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO f4_sheet_columns ("col", width) VALUES (?, ?)`, col, width); err != nil {
			return err
		}
	}

	var saveErr error
	s.Cells(func(point Point, cell *Cell) {
		if saveErr != nil || cell.IsEmpty() {
			return
		}
		_, saveErr = tx.ExecContext(ctx,
			`INSERT INTO f4_sheet_cells ("col", "row", text, display, justify, decimals, protected)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			point.Col, point.Row, cell.Text,
			int(cell.Display), int(cell.Justify), int(cell.Decimals), boolToInt(cell.Protected))
	})
	if saveErr != nil {
		return saveErr
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.Modified = false
	return nil
}

// Load reads a sheet previously written by Save.
func Load(ctx context.Context, path string) (*Sheet, error) {
	db, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, createSchema); err != nil {
		return nil, err
	}

	sheet := New()
	sheet.SuspendUndo()
	sheet.AutoRecalc = false

	metaRows, err := db.QueryContext(ctx, `SELECT key, value FROM f4_sheet_meta`)
	if err != nil {
		return nil, err
	}
	for metaRows.Next() {
		var key, value string
		if err := metaRows.Scan(&key, &value); err != nil {
			_ = metaRows.Close()
			return nil, err
		}
		switch key {
		case "title":
			sheet.Title = value
		case "separators":
			sheet.Separators = value == "true"
		case "csv_separator":
			if len(value) == 1 {
				sheet.CSVSeparator = rune(value[0])
			}
		case "schema":
			if version, convErr := strconv.Atoi(value); convErr == nil && version > SchemaVersion {
				_ = metaRows.Close()
				return nil, fmt.Errorf("sheet was written by a newer version (schema %d)", version)
			}
		}
	}
	if err := metaRows.Err(); err != nil {
		_ = metaRows.Close()
		return nil, err
	}
	_ = metaRows.Close()

	columnRows, err := db.QueryContext(ctx, `SELECT "col", width FROM f4_sheet_columns`)
	if err != nil {
		return nil, err
	}
	for columnRows.Next() {
		var col, width int
		if err := columnRows.Scan(&col, &width); err != nil {
			_ = columnRows.Close()
			return nil, err
		}
		sheet.widths[col] = width
	}
	if err := columnRows.Err(); err != nil {
		_ = columnRows.Close()
		return nil, err
	}
	_ = columnRows.Close()

	cellRows, err := db.QueryContext(ctx,
		`SELECT "col", "row", text, display, justify, decimals, protected
		 FROM f4_sheet_cells ORDER BY "row", "col"`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cellRows.Close() }()
	for cellRows.Next() {
		var (
			col, row                              int
			text                                  string
			display, justify, decimals, protected int
		)
		if err := cellRows.Scan(&col, &row, &text, &display, &justify, &decimals, &protected); err != nil {
			return nil, err
		}
		if col < 0 || col >= MaxColumns || row < 0 || row >= MaxRows {
			continue
		}
		if display < int(DisplayAsIs) || display > int(DisplayHidden) ||
			justify < int(JustifyLeft) || justify > int(JustifyCenter) ||
			decimals < 0 || decimals > math.MaxUint8 {
			return nil, fmt.Errorf("invalid cell formatting at column %d row %d", col, row)
		}
		cell := NewCell(text)
		cell.Display = Display(display) // #nosec G115 -- display was checked against the Display enum range above.
		cell.Justify = Justify(justify) // #nosec G115 -- justify was checked against the Justify enum range above.
		cell.Decimals = uint8(decimals) // #nosec G115 -- decimals was checked against the uint8 range above.
		cell.Protected = protected != 0
		sheet.cells[Point{Col: col, Row: row}] = cell
	}
	if err := cellRows.Err(); err != nil {
		return nil, err
	}

	sheet.AutoRecalc = true
	sheet.Recalc()
	sheet.ResumeUndo()
	sheet.Modified = false
	return sheet, nil
}

// IsSheetFile reports whether the file looks like a sheet written by Save.
// It is used to decide whether a database on the panel can be opened as a
// spreadsheet rather than in the generic SQLite client.
func IsSheetFile(ctx context.Context, path string) bool {
	db, err := openDatabase(path)
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()

	var name string
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'f4_sheet_cells'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return err == nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
