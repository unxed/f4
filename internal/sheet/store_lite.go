//go:build lite

package sheet

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
)

// jsonDocument is the on-disk shape of a lite-build sheet file. It mirrors
// the sqlite schema's three tables (f4_sheet_meta, f4_sheet_columns,
// f4_sheet_cells) one field at a time rather than as a literal translation,
// so the JSON reads naturally on its own.
//
// Field order and names are part of the format once a sheet has been saved
// by a released lite build, so treat them the same way the sqlite schema is
// treated: additive changes only, guarded by bumping Schema and branching in
// Load.
type jsonDocument struct {
	// Schema lets a future reader tell an old file from a new one, exactly
	// like the sqlite store's own f4_sheet_meta "schema" row. It also
	// doubles as the content-sniffing marker IsSheetFile looks for: any
	// object with this key at the top level is treated as a candidate sheet
	// file before the rest of its shape is even checked.
	Schema       int            `json:"schema"`
	Title        string         `json:"title"`
	Separators   bool           `json:"separators"`
	CSVSeparator rune           `json:"csv_separator"`
	Columns      map[string]int `json:"columns"`
	Cells        []jsonCell     `json:"cells"`
}

type jsonCell struct {
	Col       int    `json:"col"`
	Row       int    `json:"row"`
	Text      string `json:"text"`
	Display   int    `json:"display"`
	Justify   int    `json:"justify"`
	Decimals  int    `json:"decimals"`
	Protected bool   `json:"protected"`
}

// Save writes the sheet as a single JSON document, replacing whatever the
// file held before.
//
// This is the lite-build store: it exists so "-tags lite" avoids linking
// github.com/ncruces/go-sqlite3 (the whole SQLite engine, translated to
// Go) for a single feature that has nothing to do with either the SQLite
// plugin or archive indexing (f4#1552). A regular build keeps using store.go's
// SQLite-backed format instead, behind the same Save/Load/IsSheetFile API
// but writing a physically different, incompatible file (see the package
// doc comment in cell.go): a file saved by one build is not recognised as a
// sheet by the other.
func (s *Sheet) Save(_ context.Context, path string) error {
	doc := jsonDocument{
		Schema:       SchemaVersion,
		Title:        s.Title,
		Separators:   s.Separators,
		CSVSeparator: s.CSVSeparator,
		Columns:      make(map[string]int),
	}
	for col, width := range s.ColumnWidths() {
		doc.Columns[strconv.Itoa(col)] = width
	}
	s.Cells(func(point Point, cell *Cell) {
		if cell.IsEmpty() {
			return
		}
		doc.Cells = append(doc.Cells, jsonCell{
			Col:       point.Col,
			Row:       point.Row,
			Text:      cell.Text,
			Display:   int(cell.Display),
			Justify:   int(cell.Justify),
			Decimals:  int(cell.Decimals),
			Protected: cell.Protected,
		})
	})

	data, err := json.MarshalIndent(&doc, "", "\t")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	s.Modified = false
	return nil
}

// Load reads a sheet previously written by Save.
func Load(_ context.Context, path string) (*Sheet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc jsonDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("sheet file is not valid JSON: %w", err)
	}
	if doc.Schema > SchemaVersion {
		return nil, fmt.Errorf("sheet was written by a newer version (schema %d)", doc.Schema)
	}

	sheet := New()
	sheet.SuspendUndo()
	sheet.AutoRecalc = false

	sheet.Title = doc.Title
	sheet.Separators = doc.Separators
	if doc.CSVSeparator != 0 {
		sheet.CSVSeparator = doc.CSVSeparator
	}

	for key, width := range doc.Columns {
		col, err := strconv.Atoi(key)
		if err != nil || col < 0 || col >= MaxColumns {
			continue
		}
		sheet.widths[col] = width
	}

	for _, cell := range doc.Cells {
		if cell.Col < 0 || cell.Col >= MaxColumns || cell.Row < 0 || cell.Row >= MaxRows {
			continue
		}
		if cell.Display < int(DisplayAsIs) || cell.Display > int(DisplayHidden) ||
			cell.Justify < int(JustifyLeft) || cell.Justify > int(JustifyCenter) ||
			cell.Decimals < 0 || cell.Decimals > math.MaxUint8 {
			return nil, fmt.Errorf("invalid cell formatting at column %d row %d", cell.Col, cell.Row)
		}
		loaded := NewCell(cell.Text)
		loaded.Display = Display(cell.Display) // #nosec G115 -- checked against the Display enum range above.
		loaded.Justify = Justify(cell.Justify) // #nosec G115 -- checked against the Justify enum range above.
		loaded.Decimals = uint8(cell.Decimals) // #nosec G115 -- checked against the uint8 range above.
		loaded.Protected = cell.Protected
		sheet.cells[Point{Col: cell.Col, Row: cell.Row}] = loaded
	}

	sheet.AutoRecalc = true
	sheet.Recalc()
	sheet.ResumeUndo()
	sheet.Modified = false
	return sheet, nil
}

// IsSheetFile reports whether the file looks like a sheet written by Save.
// Unlike the sqlite store's table-sniffing, this parses the file as JSON and
// checks for the "schema" key every jsonDocument carries; any other JSON
// document, or non-JSON content (including a sqlite-backed sheet from a
// regular build), is reported as not a sheet file.
func IsSheetFile(_ context.Context, path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var probe struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Schema != nil
}
