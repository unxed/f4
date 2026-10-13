package sheet

import (
	"bytes"
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestColumnNames(t *testing.T) {
	cases := map[int]string{0: "A", 25: "Z", 26: "AA", 51: "AZ", 52: "BA", 255: "IV"}
	for col, want := range cases {
		if got := ColumnName(col); got != want {
			t.Errorf("ColumnName(%d) = %q, want %q", col, got, want)
		}
		parsed, ok := ParseColumnName(want)
		if !ok || parsed != col {
			t.Errorf("ParseColumnName(%q) = %d, %v; want %d", want, parsed, ok, col)
		}
	}
	if _, ok := ParseColumnName("IW"); ok {
		t.Error("column IW is outside the grid and must not parse")
	}
}

func TestParseRefAbsoluteMarkers(t *testing.T) {
	cases := []struct {
		token          string
		col, row       int
		absCol, absRow bool
	}{
		{"B7", 1, 6, false, false},
		{"@A@2", 0, 1, true, true},
		{"@A2", 0, 1, true, false},
		{"A@2", 0, 1, false, true},
		{"iv4096", 255, 4095, false, false},
	}
	for _, tc := range cases {
		ref, ok := ParseRef(tc.token)
		if !ok {
			t.Fatalf("ParseRef(%q) failed", tc.token)
		}
		if ref.Col != tc.col || ref.Row != tc.row || ref.AbsCol != tc.absCol || ref.AbsRow != tc.absRow {
			t.Errorf("ParseRef(%q) = %+v", tc.token, ref)
		}
	}
	for _, bad := range []string{"sum", "A", "4", "ABC1", "A0", "A4097"} {
		if _, ok := ParseRef(bad); ok {
			t.Errorf("ParseRef(%q) unexpectedly succeeded", bad)
		}
	}
	if got := (Ref{Col: 0, Row: 1, AbsCol: true}).String(); got != "@A2" {
		t.Errorf("Ref.String() = %q, want %q", got, "@A2")
	}
}

func TestDetectKind(t *testing.T) {
	cases := map[string]Kind{
		"=A1+1": KindFormula,
		"12":    KindValue,
		"-3.5":  KindValue,
		"1e3":   KindValue,
		"total": KindText,
		"":      KindText,
	}
	for text, want := range cases {
		if got := DetectKind(text); got != want {
			t.Errorf("DetectKind(%q) = %v, want %v", text, got, want)
		}
	}
}

func evalString(t *testing.T, expression string) float64 {
	t.Helper()
	tree, err := Parse(expression)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expression, err)
	}
	value, err := Eval(tree, nil)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expression, err)
	}
	return value
}

func TestExpressionSyntax(t *testing.T) {
	cases := []struct {
		expression string
		want       float64
	}{
		{"2+2*2", 6},
		{"(2+2)*2", 8},
		{"01:10", 70},
		{"2^3^2", 64},  // equal priority runs left to right
		{"-2^2", 4},    // prefix minus binds tighter than any infix operator
		{"log 2+1", 0}, // parses as log(2, +1)
		{"log(2,8)", 3},
		{"if 3<5 log 2 8 4", 3},
		{"if(3<5, log(2,8), 4)", 3},
		{"sqr 3", 9},
		{"sqrt 9", 3},
		{"root(2,16)", 4},
		{"$10.1", 16.0625},
		{"0x10.1", 16.0625},
		{"10.1h", 16.0625},
		{"10.1o", 8.125},
		{"1000.001b", 8.125},
		{"1.2k", 1200},
		{"1.2u", 0.0000012},
		{"7 div 2", 3},
		{"7 mod 2", 1},
		{"1 shl 4", 16},
		{"12 and 10", 8},
		{"12 or 3", 15},
		{"~(3<5)", 0},
		{"~(3>5)", TrueValue},
		{"sum(1,2,3)", 6},
		{"mul(2,3,4)", 24},
	}
	for _, tc := range cases {
		got := evalString(t, tc.expression)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%q = %v, want %v", tc.expression, got, tc.want)
		}
	}
	if value := evalString(t, "3<5"); value != TrueValue {
		t.Errorf("comparison returned %v, want %v", value, TrueValue)
	}
}

func TestExpressionErrors(t *testing.T) {
	for _, expression := range []string{"2+", "unknown(1)", "sum", "1/0", "(1+2"} {
		tree, err := Parse(expression)
		if err == nil {
			_, err = Eval(tree, nil)
		}
		if err == nil {
			t.Errorf("%q was expected to fail", expression)
		}
	}
}

func TestRangesSkipEmptyAndTextCells(t *testing.T) {
	s := New()
	s.SetText(1, 1, "2") // B2
	s.SetText(2, 0, "3") // C1
	s.SetText(2, 1, "4") // C2
	s.SetText(5, 10, "5")
	s.SetText(0, 5, "note")

	s.SetText(0, 0, "=mul(B1:C2, F11)")
	if got := s.Cell(0, 0).Value; got != 120 {
		t.Errorf("mul over a range with an empty cell = %v, want 120", got)
	}
	s.SetText(0, 1, "=B1*B2*C1*C2*F11")
	if got := s.Cell(0, 1).Value; got != 0 {
		t.Errorf("multiplication through operators = %v, want 0", got)
	}
	s.SetText(0, 2, "=sum(A6:A6)")
	if cell := s.Cell(0, 2); cell.Err != "" || cell.Value != 0 {
		t.Errorf("a text cell inside a range must be skipped, got value %v err %q", cell.Value, cell.Err)
	}
	s.SetText(0, 3, "=A6")
	if s.Cell(0, 3).Err == "" {
		t.Error("a direct reference to a text cell must fail")
	}
	s.SetText(0, 4, "=Z90")
	if cell := s.Cell(0, 4); cell.Err != "" || cell.Value != 0 {
		t.Errorf("a reference to an empty cell must be zero, got %v err %q", cell.Value, cell.Err)
	}
}

func TestRecalcChainAndCycles(t *testing.T) {
	s := New()
	s.SetText(0, 0, "2")
	s.SetText(0, 1, "=A1*10")
	s.SetText(0, 2, "=A2+5")
	if got := s.Cell(0, 2).Value; got != 25 {
		t.Errorf("chained formulas = %v, want 25", got)
	}
	s.SetText(0, 0, "3")
	if got := s.Cell(0, 2).Value; got != 35 {
		t.Errorf("after editing the source cell = %v, want 35", got)
	}

	s.SetText(2, 0, "=C2")
	s.SetText(2, 1, "=C1")
	if s.Cell(2, 0).Err == "" || s.Cell(2, 1).Err == "" {
		t.Error("a circular reference must be reported")
	}
	if point, ok := s.LastError(); !ok || point.Col != 2 {
		t.Errorf("LastError() = %+v, %v; want the failing cell", point, ok)
	}
}

func TestStructuralEditsRepairFormulas(t *testing.T) {
	s := New()
	s.SetText(0, 0, "=B2+1")
	s.InsertRow(0)
	if got := s.Cell(0, 1).Text; got != "=B3+1" {
		t.Errorf("after inserting a row the formula is %q, want %q", got, "=B3+1")
	}
	s.DeleteRow(0)
	if got := s.Cell(0, 0).Text; got != "=B2+1" {
		t.Errorf("after deleting the row the formula is %q, want %q", got, "=B2+1")
	}

	s.SetText(3, 3, "=D1")
	s.InsertColumn(0)
	if got := s.Cell(4, 3).Text; got != "=E1" {
		t.Errorf("after inserting a column the formula is %q, want %q", got, "=E1")
	}

	s = New()
	s.SetText(0, 0, "=B1")
	s.DeleteColumn(1)
	if cell := s.Cell(0, 0); !strings.Contains(cell.Text, DeadRef) || cell.Err == "" {
		t.Errorf("a reference to a deleted column must go dead, got %q", cell.Text)
	}
}

func TestColumnWidthsFollowInsertedColumns(t *testing.T) {
	s := New()
	s.SetColumnWidth(2, 20)
	s.InsertColumn(0)
	if got := s.ColumnWidth(3); got != 20 {
		t.Errorf("column width after insertion = %d, want 20", got)
	}
	if got := s.ColumnWidth(2); got != DefaultColumnWidth {
		t.Errorf("the fresh column must use the default width, got %d", got)
	}
}

func TestPasteAdjustsRelativeReferencesOnly(t *testing.T) {
	s := New()
	s.SetText(0, 0, "=A2")
	block := s.CopyBlock(Rect{Left: 0, Top: 0, Right: 0, Bottom: 0})
	s.PasteBlock(block, 1, 2) // paste into B3
	if got := s.Cell(1, 2).Text; got != "=B4" {
		t.Errorf("relative reference after paste = %q, want %q", got, "=B4")
	}

	cases := map[string]string{"=@A@2": "=@A@2", "=@A2": "=@A4", "=A@2": "=B@2"}
	for source, want := range cases {
		s := New()
		s.SetText(0, 0, source)
		block := s.CopyBlock(Rect{Left: 0, Top: 0, Right: 0, Bottom: 0})
		s.PasteBlock(block, 1, 2)
		if got := s.Cell(1, 2).Text; got != want {
			t.Errorf("pasting %q produced %q, want %q", source, got, want)
		}
	}
}

func TestCutClearAndUndo(t *testing.T) {
	s := New()
	s.SetText(0, 0, "1")
	s.SetText(1, 0, "2")
	block := s.CutBlock(Rect{Left: 0, Top: 0, Right: 1, Bottom: 0})
	if !s.Cell(0, 0).IsEmpty() || !s.Cell(1, 0).IsEmpty() {
		t.Error("cut must clear the block")
	}
	s.PasteBlock(block, 0, 3)
	if got := s.Cell(1, 3).Text; got != "2" {
		t.Errorf("pasted cell = %q, want %q", got, "2")
	}
	if !s.Undo() {
		t.Fatal("undo must be available")
	}
	if !s.Cell(1, 3).IsEmpty() {
		t.Error("undo must revert the paste")
	}
}

func TestProtectedCellsSurviveClear(t *testing.T) {
	s := New()
	s.SetText(0, 0, "keep")
	s.Format(Rect{Left: 0, Top: 0, Right: 0, Bottom: 0}, DisplayAsIs, JustifyLeft, 0, true)
	s.ClearBlock(Rect{Left: 0, Top: 0, Right: 3, Bottom: 3})
	if s.Cell(0, 0).IsEmpty() {
		t.Error("a protected cell must not be cleared")
	}
}

func TestDisplayFormats(t *testing.T) {
	s := New()
	s.SetText(0, 0, "1234567.891")
	cell := s.Cell(0, 0)

	cell.Display = DisplayDecimal
	cell.Decimals = 2
	if got := cell.DisplayText(); got != "1234567.89" {
		t.Errorf("decimal format = %q", got)
	}
	cell.Display = DisplayComma
	if got := cell.DisplayText(); got != "1,234,567.891" {
		t.Errorf("comma format = %q", got)
	}
	cell.Display = DisplayCurrency
	if got := cell.DisplayText(); got != "$1,234,567.89" {
		t.Errorf("currency format = %q", got)
	}
	cell.Display = DisplayHidden
	if got := cell.DisplayText(); got != "" {
		t.Errorf("hidden format = %q", got)
	}

	s.SetText(1, 0, "0")
	logical := s.Cell(1, 0)
	logical.Display = DisplayLogical
	if got := logical.DisplayText(); got != BoolFalseText {
		t.Errorf("logical format = %q", got)
	}

	s.SetText(2, 0, "0.25")
	percent := s.Cell(2, 0)
	percent.Display = DisplayPercent
	if got := percent.DisplayText(); got != "25.00%" {
		t.Errorf("percent format = %q", got)
	}
}

func TestFitText(t *testing.T) {
	if got := FitText("abc", 6, JustifyRight); got != "   abc" {
		t.Errorf("right aligned = %q", got)
	}
	if got := FitText("abc", 7, JustifyCenter); got != "  abc  " {
		t.Errorf("centered = %q", got)
	}
	if got := FitText("abcdefgh", 4, JustifyLeft); got != "abc"+string(TruncationMarker) {
		t.Errorf("truncated = %q", got)
	}
}

func TestFitCellWidthKeepsRoomForSeparator(t *testing.T) {
	s := New()
	s.SetColumnWidth(0, 5)
	s.SetText(0, 0, "abcde")
	if got := s.FitCellWidth(0, 0, 4); got != "abc"+string(TruncationMarker) {
		t.Errorf("FitCellWidth = %q, want %q", got, "abc"+string(TruncationMarker))
	}
	s.SetText(0, 0, "ab")
	if got := s.FitCellWidth(0, 0, 4); got != "ab  " {
		t.Errorf("FitCellWidth = %q, want %q", got, "ab  ")
	}
	if got := s.FitCell(0, 0); got != "ab   " {
		t.Errorf("FitCell = %q, want %q", got, "ab   ")
	}
}

func TestFindAndReplace(t *testing.T) {
	s := New()
	s.SetText(0, 0, "alpha")
	s.SetText(1, 2, "Alpha beta")
	s.SetText(2, 3, "42")

	point, ok := s.Find(SearchOptions{Pattern: "alpha", CaseSensitive: true}, Point{})
	if !ok || point != (Point{Col: 0, Row: 0}) {
		t.Errorf("case sensitive search found %+v, %v", point, ok)
	}
	point, ok = s.Find(SearchOptions{Pattern: "ALPHA"}, Point{Col: 1, Row: 1})
	if !ok || point != (Point{Col: 1, Row: 2}) {
		t.Errorf("case insensitive search found %+v, %v", point, ok)
	}
	if _, ok := s.Find(SearchOptions{Pattern: "alph", WholeWords: true}, Point{}); ok {
		t.Error("whole word search must not match a prefix")
	}
	point, ok = s.Find(SearchOptions{Pattern: "42", ByValue: true}, Point{})
	if !ok || point != (Point{Col: 2, Row: 3}) {
		t.Errorf("value search found %+v, %v", point, ok)
	}
	if !s.Replace(Point{Col: 1, Row: 2}, SearchOptions{Pattern: "beta"}, "gamma") {
		t.Fatal("replace reported no change")
	}
	if got := s.Cell(1, 2).Text; got != "Alpha gamma" {
		t.Errorf("after replace = %q", got)
	}
}

func TestExportTextAndCSV(t *testing.T) {
	s := New()
	s.SetColumnWidth(0, 6)
	s.SetColumnWidth(1, 6)
	s.SetText(0, 0, "name")
	s.SetText(1, 0, "2")
	s.SetText(1, 1, "=B1*3")

	var text bytes.Buffer
	if err := s.ExportText(&text); err != nil {
		t.Fatalf("ExportText: %v", err)
	}
	lines := strings.Split(strings.TrimRight(text.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "name") {
		t.Errorf("text export = %q", text.String())
	}

	s.Separators = true
	text.Reset()
	if err := s.ExportText(&text); err != nil {
		t.Fatalf("ExportText: %v", err)
	}
	if !strings.ContainsRune(text.String(), ColumnSeparator) {
		t.Errorf("separator mode must draw column separators, got %q", text.String())
	}

	var csvData bytes.Buffer
	if err := s.ExportCSV(&csvData); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}
	if !strings.Contains(csvData.String(), "name,2") {
		t.Errorf("csv export = %q", csvData.String())
	}

	imported := New()
	if err := imported.ImportCSV(strings.NewReader("1,2\n=A1+B1,text\n")); err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if got := imported.Cell(0, 1).Value; got != 3 {
		t.Errorf("imported formula = %v, want 3", got)
	}
}

func TestSQLiteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.f4s")
	s := New()
	s.Title = "Budget"
	s.Separators = true
	s.SetColumnWidth(1, 17)
	s.SetText(0, 0, "item")
	s.SetText(1, 0, "12.5")
	s.SetText(1, 1, "=B1*2")
	s.Format(Rect{Left: 1, Top: 0, Right: 1, Bottom: 1}, DisplayDecimal, JustifyRight, 2, false)

	ctx := context.Background()
	if err := s.Save(ctx, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if s.Modified {
		t.Error("saving must clear the modified flag")
	}
	if !IsSheetFile(ctx, path) {
		t.Error("IsSheetFile must recognise a saved sheet")
	}

	loaded, err := Load(ctx, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "Budget" || !loaded.Separators {
		t.Errorf("metadata lost: title %q separators %v", loaded.Title, loaded.Separators)
	}
	if got := loaded.ColumnWidth(1); got != 17 {
		t.Errorf("column width = %d, want 17", got)
	}
	if got := loaded.Cell(1, 1).Value; got != 25 {
		t.Errorf("formula was not recalculated after loading: %v", got)
	}
	if cell := loaded.Cell(1, 0); cell.Display != DisplayDecimal || cell.Decimals != 2 {
		t.Errorf("cell formatting lost: %+v", cell)
	}
	if loaded.Modified {
		t.Error("a freshly loaded sheet is not modified")
	}
}

func TestXLSXRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.xlsx")
	s := New()
	s.Title = "Report"
	s.SetColumnWidth(0, 20)
	s.SetText(0, 0, "item")
	s.SetText(1, 0, "2")
	s.SetText(2, 0, "3")
	s.SetText(3, 0, "=sum(B1:C1)")
	s.SetText(4, 0, "=12 shl 2") // no Excel counterpart: only the value travels

	if err := s.SaveXLSX(path); err != nil {
		t.Fatalf("SaveXLSX: %v", err)
	}
	loaded, err := LoadXLSX(path)
	if err != nil {
		t.Fatalf("LoadXLSX: %v", err)
	}
	if loaded.Title != "Report" {
		t.Errorf("sheet name = %q, want %q", loaded.Title, "Report")
	}
	if got := loaded.ColumnWidth(0); got != 20 {
		t.Errorf("column width = %d, want 20", got)
	}
	if got := loaded.Cell(0, 0).Text; got != "item" {
		t.Errorf("text cell = %q", got)
	}
	if got := loaded.Cell(1, 0).Value; got != 2 {
		t.Errorf("number cell = %v", got)
	}
	formula := loaded.Cell(3, 0)
	if formula.Kind != KindFormula || formula.Value != 5 {
		t.Errorf("formula cell = %+v, want a formula worth 5", formula)
	}
	if got := loaded.Cell(4, 0).Value; got != 48 {
		t.Errorf("value-only export = %v, want 48", got)
	}
}

func TestFormulaTranslation(t *testing.T) {
	cases := map[string]string{
		"sum(A1:B2,3)": "SUM(A1:B2,3)",
		"mul(A1,2)":    "PRODUCT(A1,2)",
		"@A@1+1":       "($A$1+1)",
	}
	for source, want := range cases {
		got, ok := formulaToExcel(source)
		if !ok || got != want {
			t.Errorf("formulaToExcel(%q) = %q, %v; want %q", source, got, ok, want)
		}
	}
	if _, ok := formulaToExcel("12 shl 2"); ok {
		t.Error("bitwise operators have no Excel form and must be rejected")
	}
	if got, ok := formulaFromExcel("=SUM($A$1:B2)"); !ok || got != "sum(@A@1:B2)" {
		t.Errorf("formulaFromExcel = %q, %v", got, ok)
	}
	if _, ok := formulaFromExcel("=VLOOKUP(A1,B:C,2,FALSE)"); ok {
		t.Error("unsupported functions must be rejected so the value is used instead")
	}
}
