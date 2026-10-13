package sheet

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// TruncationMarker replaces the tail of a value that does not fit its column.
const TruncationMarker = '\u25ba'

// ColumnSeparator is drawn between columns when separators are switched on.
const ColumnSeparator = '\u2502'

const maxUndoDepth = 64

// Sheet is a grid of cells plus the per-column layout. The zero value is not
// usable; call New.
type Sheet struct {
	cells      map[Point]*Cell
	widths     map[int]int
	Separators bool
	Title      string
	Modified   bool
	// AutoRecalc re-evaluates formulas after every edit. Turning it off is
	// useful while loading or importing many cells at once.
	AutoRecalc bool
	// CSVSeparator is the field delimiter used by ExportCSV/ImportCSV.
	// Default is ',' (comma). Can be changed to '\t' (tab), ';' (semicolon), etc.
	CSVSeparator rune

	lastError   Point
	hasError    bool
	undo        []snapshot
	undoEnabled bool
}

type snapshot struct {
	cells      map[Point]*Cell
	widths     map[int]int
	separators bool
}

// New returns an empty sheet with default column widths.
func New() *Sheet {
	return &Sheet{
		cells:        make(map[Point]*Cell),
		widths:       make(map[int]int),
		AutoRecalc:   true,
		undoEnabled:  true,
		lastError:    Point{Col: -1, Row: -1},
		CSVSeparator: ',',
	}
}

// Cell returns the cell at the position, or nil when it was never filled in.
func (s *Sheet) Cell(col, row int) *Cell { return s.cells[Point{Col: col, Row: row}] }

// Cells calls fn for every non-empty cell, in column-major sorted order so
// that callers producing files get deterministic output.
func (s *Sheet) Cells(fn func(point Point, cell *Cell)) {
	for _, point := range s.sortedPoints() {
		fn(point, s.cells[point])
	}
}

// Count returns the number of stored cells.
func (s *Sheet) Count() int { return len(s.cells) }

func (s *Sheet) sortedPoints() []Point {
	points := make([]Point, 0, len(s.cells))
	for point := range s.cells {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Row != points[j].Row {
			return points[i].Row < points[j].Row
		}
		return points[i].Col < points[j].Col
	})
	return points
}

// Bounds returns the last used column and row, or (-1, -1) for an empty sheet.
func (s *Sheet) Bounds() (int, int) {
	maxCol, maxRow := -1, -1
	for point, cell := range s.cells {
		if cell.IsEmpty() {
			continue
		}
		if point.Col > maxCol {
			maxCol = point.Col
		}
		if point.Row > maxRow {
			maxRow = point.Row
		}
	}
	return maxCol, maxRow
}

// ColumnWidth returns the display width of a column.
func (s *Sheet) ColumnWidth(col int) int {
	if width, ok := s.widths[col]; ok {
		return width
	}
	return DefaultColumnWidth
}

// SetColumnWidth changes the width of a column, clamped to sane bounds.
func (s *Sheet) SetColumnWidth(col, width int) {
	if col < 0 || col >= MaxColumns {
		return
	}
	if width < MinColumnWidth {
		width = MinColumnWidth
	}
	if width > MaxColumnWidth {
		width = MaxColumnWidth
	}
	s.pushUndo()
	if width == DefaultColumnWidth {
		delete(s.widths, col)
	} else {
		s.widths[col] = width
	}
	s.Modified = true
}

// ColumnWidths returns the explicitly configured widths.
func (s *Sheet) ColumnWidths() map[int]int {
	out := make(map[int]int, len(s.widths))
	for col, width := range s.widths {
		out[col] = width
	}
	return out
}

// SetText replaces the content of a cell, re-deriving its kind. Formatting
// already applied to the cell is preserved.
func (s *Sheet) SetText(col, row int, text string) {
	if col < 0 || col >= MaxColumns || row < 0 || row >= MaxRows {
		return
	}
	s.pushUndo()
	s.setTextNoUndo(col, row, text)
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

func (s *Sheet) setTextNoUndo(col, row int, text string) {
	point := Point{Col: col, Row: row}
	fresh := NewCell(text)
	if existing, ok := s.cells[point]; ok {
		fresh.Display = existing.Display
		fresh.Decimals = existing.Decimals
		fresh.Protected = existing.Protected
		if existing.Kind == fresh.Kind {
			fresh.Justify = existing.Justify
		}
	}
	if strings.TrimSpace(text) == "" {
		delete(s.cells, point)
		return
	}
	s.cells[point] = fresh
}

// SetCell stores a prepared cell, taking ownership of it.
func (s *Sheet) SetCell(col, row int, cell *Cell) {
	if col < 0 || col >= MaxColumns || row < 0 || row >= MaxRows {
		return
	}
	s.pushUndo()
	point := Point{Col: col, Row: row}
	if cell == nil || cell.IsEmpty() {
		delete(s.cells, point)
	} else {
		s.cells[point] = cell
	}
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

// Format applies display options to every cell of a block, creating cells for
// empty positions only when they already exist.
func (s *Sheet) Format(area Rect, display Display, justify Justify, decimals uint8, protected bool) {
	area = area.Normalized()
	s.pushUndo()
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			cell := s.cells[Point{Col: col, Row: row}]
			if cell == nil {
				continue
			}
			cell.Display = display
			cell.Justify = justify
			cell.Decimals = decimals
			cell.Protected = protected
		}
	}
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

// ClearBlock removes every cell of a block that is not write protected.
func (s *Sheet) ClearBlock(area Rect) {
	area = area.Normalized()
	s.pushUndo()
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			point := Point{Col: col, Row: row}
			if cell, ok := s.cells[point]; ok && !cell.Protected {
				delete(s.cells, point)
			}
		}
	}
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

// Recalc re-evaluates every formula cell and records the position of the first
// failing one, so the UI can offer "go to last error".
func (s *Sheet) Recalc() {
	state := &recalcState{sheet: s, status: make(map[Point]int, len(s.cells))}
	s.hasError = false
	s.lastError = Point{Col: -1, Row: -1}
	for _, point := range s.sortedPoints() {
		cell := s.cells[point]
		if cell.Kind != KindFormula {
			continue
		}
		_, _ = state.evaluate(point) // evaluate records formula errors on the cell for the UI below.
		if cell.Err != "" && !s.hasError {
			s.hasError = true
			s.lastError = point
		}
	}
}

// LastError reports the position of the most recent failing formula.
func (s *Sheet) LastError() (Point, bool) { return s.lastError, s.hasError }

type recalcState struct {
	sheet  *Sheet
	status map[Point]int // 0 pending, 1 in progress, 2 evaluated
}

func (r *recalcState) evaluate(point Point) (float64, error) {
	cell := r.sheet.cells[point]
	if cell == nil {
		return 0, nil
	}
	if cell.Kind != KindFormula {
		return cell.NumericValue()
	}
	switch r.status[point] {
	case 1:
		cell.Err = "circular reference"
		cell.Value = 0
		return 0, fmt.Errorf("circular reference")
	case 2:
		if cell.Err != "" {
			return 0, fmt.Errorf("%s", cell.Err)
		}
		return cell.Value, nil
	}

	r.status[point] = 1
	tree, err := Parse(cell.Formula())
	if err == nil {
		var value float64
		value, err = Eval(tree, r)
		if err == nil {
			cell.Value, cell.Err = value, ""
		}
	}
	if err != nil {
		cell.Value = 0
		if cell.Err == "" {
			cell.Err = err.Error()
		}
	}
	r.status[point] = 2
	if cell.Err != "" {
		return 0, fmt.Errorf("%s", cell.Err)
	}
	return cell.Value, nil
}

// CellValue implements Env.
func (r *recalcState) CellValue(ref Ref) (float64, error) {
	point := ref.Point()
	cell := r.sheet.cells[point]
	if cell == nil || cell.IsEmpty() {
		return 0, nil
	}
	if cell.Kind == KindText {
		return 0, fmt.Errorf("reference to a text cell")
	}
	return r.evaluate(point)
}

// RangeValues implements Env, skipping empty and text cells.
func (r *recalcState) RangeValues(from, to Ref) ([]float64, error) {
	area := Rect{Left: from.Col, Top: from.Row, Right: to.Col, Bottom: to.Row}.Normalized()
	var values []float64
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			point := Point{Col: col, Row: row}
			cell := r.sheet.cells[point]
			if cell == nil || cell.IsEmpty() || cell.Kind == KindText {
				continue
			}
			value, err := r.evaluate(point)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	}
	return values, nil
}

// Evaluate computes a stand-alone expression against the sheet, which is what
// the status line uses to preview the value of the cell being edited.
func (s *Sheet) Evaluate(expression string) (float64, error) {
	tree, err := Parse(strings.TrimPrefix(strings.TrimSpace(expression), "="))
	if err != nil {
		return 0, err
	}
	return Eval(tree, &recalcState{sheet: s, status: make(map[Point]int)})
}

// InsertRow makes room at the given row, moving everything below it down and
// repairing the references of every formula.
func (s *Sheet) InsertRow(row int) { s.shiftRows(row, 1) }

// DeleteRow removes a row and pulls the rows below it up.
func (s *Sheet) DeleteRow(row int) { s.shiftRows(row, -1) }

// InsertColumn makes room at the given column.
func (s *Sheet) InsertColumn(col int) { s.shiftColumns(col, 1) }

// DeleteColumn removes a column.
func (s *Sheet) DeleteColumn(col int) { s.shiftColumns(col, -1) }

func (s *Sheet) shiftRows(at, delta int) {
	if at < 0 || at >= MaxRows {
		return
	}
	s.pushUndo()
	moved := make(map[Point]*Cell, len(s.cells))
	for point, cell := range s.cells {
		if point.Row < at {
			moved[point] = cell
			continue
		}
		if delta < 0 && point.Row == at {
			continue // deleted row
		}
		target := Point{Col: point.Col, Row: point.Row + delta}
		if target.Row < 0 || target.Row >= MaxRows {
			continue
		}
		moved[target] = cell
	}
	s.cells = moved
	s.adjustFormulas(func(ref Ref) (Ref, bool) {
		if ref.Row < at {
			return ref, true
		}
		if delta < 0 && ref.Row == at {
			return ref, false
		}
		ref.Row += delta
		if ref.Row < 0 || ref.Row >= MaxRows {
			return ref, false
		}
		return ref, true
	})
	s.finishStructuralChange()
}

func (s *Sheet) shiftColumns(at, delta int) {
	if at < 0 || at >= MaxColumns {
		return
	}
	s.pushUndo()
	moved := make(map[Point]*Cell, len(s.cells))
	for point, cell := range s.cells {
		if point.Col < at {
			moved[point] = cell
			continue
		}
		if delta < 0 && point.Col == at {
			continue
		}
		target := Point{Col: point.Col + delta, Row: point.Row}
		if target.Col < 0 || target.Col >= MaxColumns {
			continue
		}
		moved[target] = cell
	}
	s.cells = moved

	widths := make(map[int]int, len(s.widths))
	for col, width := range s.widths {
		switch {
		case col < at:
			widths[col] = width
		case delta < 0 && col == at:
		default:
			if shifted := col + delta; shifted >= 0 && shifted < MaxColumns {
				widths[shifted] = width
			}
		}
	}
	s.widths = widths

	s.adjustFormulas(func(ref Ref) (Ref, bool) {
		if ref.Col < at {
			return ref, true
		}
		if delta < 0 && ref.Col == at {
			return ref, false
		}
		ref.Col += delta
		if ref.Col < 0 || ref.Col >= MaxColumns {
			return ref, false
		}
		return ref, true
	})
	s.finishStructuralChange()
}

// adjustFormulas rewrites the references of every formula cell. Structural
// edits move cells around, so both relative and absolute references follow
// their target.
func (s *Sheet) adjustFormulas(fn func(Ref) (Ref, bool)) {
	for _, cell := range s.cells {
		if cell.Kind != KindFormula {
			continue
		}
		cell.Text = "=" + RewriteRefs(cell.Formula(), fn)
	}
}

func (s *Sheet) finishStructuralChange() {
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

// Block is a rectangular piece of a sheet, used as the clipboard payload.
type Block struct {
	Width, Height int
	Origin        Point
	Cells         []*Cell
}

// At returns the cell at the block-relative position.
func (b *Block) At(col, row int) *Cell {
	if b == nil || col < 0 || row < 0 || col >= b.Width || row >= b.Height {
		return nil
	}
	return b.Cells[row*b.Width+col]
}

// IsEmpty reports whether the block holds no cells at all.
func (b *Block) IsEmpty() bool { return b == nil || b.Width == 0 || b.Height == 0 }

// CopyBlock snapshots a rectangle of the sheet.
func (s *Sheet) CopyBlock(area Rect) *Block {
	area = area.Normalized()
	block := &Block{
		Width:  area.Right - area.Left + 1,
		Height: area.Bottom - area.Top + 1,
		Origin: Point{Col: area.Left, Row: area.Top},
	}
	block.Cells = make([]*Cell, block.Width*block.Height)
	for row := 0; row < block.Height; row++ {
		for col := 0; col < block.Width; col++ {
			cell := s.cells[Point{Col: area.Left + col, Row: area.Top + row}]
			block.Cells[row*block.Width+col] = cell.Clone()
		}
	}
	return block
}

// CutBlock copies a rectangle and then clears it.
func (s *Sheet) CutBlock(area Rect) *Block {
	block := s.CopyBlock(area)
	s.ClearBlock(area)
	return block
}

// PasteBlock drops a block at the given position. References inside pasted
// formulas move with the block unless their column or row is pinned with '@'.
func (s *Sheet) PasteBlock(block *Block, col, row int) {
	if block.IsEmpty() {
		return
	}
	s.pushUndo()
	deltaCol := col - block.Origin.Col
	deltaRow := row - block.Origin.Row
	for blockRow := 0; blockRow < block.Height; blockRow++ {
		for blockCol := 0; blockCol < block.Width; blockCol++ {
			targetCol, targetRow := col+blockCol, row+blockRow
			if targetCol >= MaxColumns || targetRow >= MaxRows {
				continue
			}
			target := Point{Col: targetCol, Row: targetRow}
			source := block.At(blockCol, blockRow)
			if source == nil {
				delete(s.cells, target)
				continue
			}
			if existing, ok := s.cells[target]; ok && existing.Protected {
				continue
			}
			pasted := source.Clone()
			if pasted.Kind == KindFormula {
				pasted.Text = "=" + RewriteRefs(pasted.Formula(), func(ref Ref) (Ref, bool) {
					if !ref.AbsCol {
						ref.Col += deltaCol
					}
					if !ref.AbsRow {
						ref.Row += deltaRow
					}
					if ref.Col < 0 || ref.Col >= MaxColumns || ref.Row < 0 || ref.Row >= MaxRows {
						return ref, false
					}
					return ref, true
				})
			}
			s.cells[target] = pasted
		}
	}
	s.Modified = true
	if s.AutoRecalc {
		s.Recalc()
	}
}

// SearchOptions configures Find and Replace.
type SearchOptions struct {
	Pattern       string
	ByValue       bool
	CaseSensitive bool
	WholeWords    bool
}

// Find returns the first cell at or after the start position that matches.
func (s *Sheet) Find(options SearchOptions, start Point) (Point, bool) {
	maxCol, maxRow := s.Bounds()
	for row := start.Row; row <= maxRow; row++ {
		firstCol := 0
		if row == start.Row {
			firstCol = start.Col
		}
		for col := firstCol; col <= maxCol; col++ {
			cell := s.cells[Point{Col: col, Row: row}]
			if cell == nil {
				continue
			}
			if matchCell(cell, options) {
				return Point{Col: col, Row: row}, true
			}
		}
	}
	return Point{}, false
}

// Replace substitutes the pattern in the given cell and returns whether
// anything changed. Value searches replace the whole cell content.
func (s *Sheet) Replace(point Point, options SearchOptions, replacement string) bool {
	cell := s.cells[point]
	if cell == nil || cell.Protected || !matchCell(cell, options) {
		return false
	}
	updated := replacement
	if !options.ByValue {
		updated = replaceOccurrences(cell.Text, options, replacement)
	}
	if updated == cell.Text {
		return false
	}
	s.SetText(point.Col, point.Row, updated)
	return true
}

func matchCell(cell *Cell, options SearchOptions) bool {
	if options.Pattern == "" {
		return false
	}
	if options.ByValue {
		wanted, err := parseNumber(options.Pattern)
		if err != nil {
			return false
		}
		if cell.Kind == KindText {
			return false
		}
		return cell.Value == wanted
	}
	return findOccurrence(cell.Text, options) >= 0
}

func findOccurrence(text string, options SearchOptions) int {
	haystack, needle := text, options.Pattern
	if !options.CaseSensitive {
		haystack, needle = strings.ToLower(haystack), strings.ToLower(needle)
	}
	offset := 0
	for {
		index := strings.Index(haystack[offset:], needle)
		if index < 0 {
			return -1
		}
		index += offset
		if !options.WholeWords || isWholeWord(haystack, index, len(needle)) {
			return index
		}
		offset = index + 1
		if offset >= len(haystack) {
			return -1
		}
	}
}

func replaceOccurrences(text string, options SearchOptions, replacement string) string {
	var out strings.Builder
	rest := text
	consumed := 0
	for {
		index := findOccurrence(rest, options)
		if index < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(rest[:index])
		out.WriteString(replacement)
		rest = rest[index+len(options.Pattern):]
		consumed++
		if rest == "" {
			break
		}
	}
	if consumed == 0 {
		return text
	}
	return out.String()
}

func isWholeWord(text string, index, length int) bool {
	if index > 0 && isWordByte(text[index-1]) {
		return false
	}
	end := index + length
	if end < len(text) && isWordByte(text[end]) {
		return false
	}
	return true
}

func isWordByte(b byte) bool {
	return isLetterByte(b) || (b >= '0' && b <= '9') || b == '_'
}

func parseNumber(text string) (float64, error) {
	tree, err := Parse(strings.TrimSpace(text))
	if err != nil {
		return 0, err
	}
	return Eval(tree, nil)
}

// FitCell renders a cell padded and aligned to its column width. When the
// value does not fit, the tail is replaced with a truncation marker, the way
// the original grid signals clipped content.
func (s *Sheet) FitCell(col, row int) string {
	return s.FitCellWidth(col, row, s.ColumnWidth(col))
}

// FitCellWidth renders a cell into a field narrower than its column. The
// frame uses it when the column separators are switched on: the separator
// takes the last cell of the column, so the text must fit into width-1
// instead of overwriting it.
func (s *Sheet) FitCellWidth(col, row, width int) string {
	cell := s.cells[Point{Col: col, Row: row}]
	text := cell.DisplayText()
	justify := JustifyLeft
	if cell != nil {
		justify = cell.Justify
	}
	return FitText(text, width, justify)
}

// FitText aligns a string inside a field of the given width.
func FitText(text string, width int, justify Justify) string {
	if width <= 0 {
		return ""
	}
	length := utf8.RuneCountInString(text)
	if length > width {
		runes := []rune(text)
		return string(runes[:width-1]) + string(TruncationMarker)
	}
	padding := width - length
	switch justify {
	case JustifyRight:
		return strings.Repeat(" ", padding) + text
	case JustifyCenter:
		left := padding / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
	}
	return text + strings.Repeat(" ", padding)
}

// ExportText writes the sheet as plain text laid out with the current column
// widths, honouring the separator display mode.
func (s *Sheet) ExportText(w io.Writer) error {
	maxCol, maxRow := s.Bounds()
	writer := newLineWriter(w)
	for row := 0; row <= maxRow; row++ {
		var line strings.Builder
		for col := 0; col <= maxCol; col++ {
			if s.Separators && col > 0 {
				line.WriteRune(ColumnSeparator)
			}
			line.WriteString(s.FitCell(col, row))
		}
		writer.writeLine(strings.TrimRight(line.String(), " "))
	}
	return writer.err
}

// ExportCSV writes the displayed values, one record per row.
// The field delimiter is taken from s.CSVSeparator (default ',').
func (s *Sheet) ExportCSV(w io.Writer) error {
	maxCol, maxRow := s.Bounds()
	writer := csv.NewWriter(w)
	writer.Comma = s.CSVSeparator
	for row := 0; row <= maxRow; row++ {
		record := make([]string, maxCol+1)
		for col := 0; col <= maxCol; col++ {
			record[col] = s.Cell(col, row).DisplayText()
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// ImportCSV replaces the sheet content with the records of a CSV stream.
// The field delimiter is taken from s.CSVSeparator (default ',').
func (s *Sheet) ImportCSV(r io.Reader) error {
	reader := csv.NewReader(r)
	reader.Comma = s.CSVSeparator
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}
	s.pushUndo()
	s.cells = make(map[Point]*Cell)
	autoRecalc := s.AutoRecalc
	s.AutoRecalc = false
	for row, record := range records {
		if row >= MaxRows {
			break
		}
		for col, field := range record {
			if col >= MaxColumns {
				break
			}
			s.setTextNoUndo(col, row, field)
		}
	}
	s.AutoRecalc = autoRecalc
	s.Modified = true
	s.Recalc()
	return nil
}

type lineWriter struct {
	w   io.Writer
	err error
}

func newLineWriter(w io.Writer) *lineWriter { return &lineWriter{w: w} }

func (l *lineWriter) writeLine(text string) {
	if l.err != nil {
		return
	}
	_, l.err = io.WriteString(l.w, text+"\n")
}

// pushUndo remembers the current state so the next edit can be reverted.
func (s *Sheet) pushUndo() {
	if !s.undoEnabled {
		return
	}
	state := snapshot{
		cells:      make(map[Point]*Cell, len(s.cells)),
		widths:     make(map[int]int, len(s.widths)),
		separators: s.Separators,
	}
	for point, cell := range s.cells {
		state.cells[point] = cell.Clone()
	}
	for col, width := range s.widths {
		state.widths[col] = width
	}
	s.undo = append(s.undo, state)
	if len(s.undo) > maxUndoDepth {
		s.undo = s.undo[len(s.undo)-maxUndoDepth:]
	}
}

// CanUndo reports whether an undo step is available.
func (s *Sheet) CanUndo() bool { return len(s.undo) > 0 }

// Undo reverts the most recent modification.
func (s *Sheet) Undo() bool {
	if len(s.undo) == 0 {
		return false
	}
	state := s.undo[len(s.undo)-1]
	s.undo = s.undo[:len(s.undo)-1]
	s.cells = state.cells
	s.widths = state.widths
	s.Separators = state.separators
	s.Modified = true
	s.Recalc()
	return true
}

// SuspendUndo disables undo recording, used while loading files.
func (s *Sheet) SuspendUndo() { s.undoEnabled = false }

// ResumeUndo re-enables undo recording and drops the accumulated history.
func (s *Sheet) ResumeUndo() {
	s.undoEnabled = true
	s.undo = nil
}
