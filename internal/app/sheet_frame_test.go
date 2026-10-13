package app

import (
	"github.com/unxed/f4/internal/paneltest"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/sheet"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// newSheetFrameForTest builds a frame on a silent screen buffer.
func newSheetFrameForTest(t *testing.T) *SheetFrame {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	sf := NewSheetFrame()
	if sf == nil {
		t.Fatal("the spreadsheet frame was not created")
	}
	return sf
}

// key builds a key-down event, applying the given modifier mask.
func key(code uint16, modifiers vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  code,
		ControlKeyState: modifiers,
	}
}

// typeKey builds a key-down event carrying a printable character.
func typeKey(char rune) *vtinput.InputEvent {
	return &vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    char,
	}
}

// typeText feeds a string followed by Enter, the way a user fills a cell.
func typeText(sf *SheetFrame, text string) {
	for _, char := range text {
		sf.ProcessKey(typeKey(char))
	}
	sf.ProcessKey(key(vtinput.VK_RETURN, 0))
}

func TestSheetFrameTypingFillsCells(t *testing.T) {
	sf := newSheetFrameForTest(t)

	typeText(sf, "12")
	typeText(sf, "30")
	typeText(sf, "=A1+A2")

	if got := sf.Document().Cell(0, 0).Text; got != "12" {
		t.Errorf("A1 = %q, want %q", got, "12")
	}
	cell := sf.Document().Cell(0, 2)
	if cell == nil || cell.Kind != sheet.KindFormula {
		t.Fatalf("A3 must hold a formula, got %+v", cell)
	}
	if cell.Value != 42 {
		t.Errorf("A3 = %v, want 42", cell.Value)
	}
	// Enter moves down one row, exactly like the classic editor.
	if sf.Cursor().Row != 3 || sf.Cursor().Col != 0 {
		t.Errorf("cursor at %v, want A4", sf.Cursor())
	}
}

func TestSheetFrameEscapeCancelsEdit(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "keep")
	sf.gotoCell(0, 0)

	sf.ProcessKey(typeKey('x'))
	sf.ProcessKey(typeKey('y'))
	sf.ProcessKey(key(vtinput.VK_ESCAPE, 0))

	if got := sf.Document().Cell(0, 0).Text; got != "keep" {
		t.Errorf("A1 = %q, the cancelled edit must not be stored", got)
	}
}

func TestSheetFrameShiftArrowsMarkBlock(t *testing.T) {
	sf := newSheetFrameForTest(t)

	sf.ProcessKey(key(vtinput.VK_RIGHT, vtinput.ShiftPressed))
	sf.ProcessKey(key(vtinput.VK_DOWN, vtinput.ShiftPressed))

	block := sf.Block()
	want := sheet.Rect{Left: 0, Top: 0, Right: 1, Bottom: 1}
	if block != want {
		t.Errorf("block = %+v, want %+v", block, want)
	}

	// An unmodified arrow drops the marking again.
	sf.ProcessKey(key(vtinput.VK_LEFT, 0))
	if got := sf.Block(); got.Right != got.Left || got.Top != got.Bottom {
		t.Errorf("moving without shift must collapse the block, got %+v", got)
	}
}

func TestSheetFrameClipboardKeys(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "7")
	sf.gotoCell(0, 0)

	sf.ProcessKey(key(vtinput.VK_INSERT, vtinput.LeftCtrlPressed))
	sf.gotoCell(2, 4)
	sf.ProcessKey(key(vtinput.VK_INSERT, vtinput.ShiftPressed))

	cell := sf.Document().Cell(2, 4)
	if cell == nil || cell.Text != "7" {
		t.Fatalf("C5 = %+v, want a pasted copy of A1", cell)
	}
}

func TestSheetFrameStructuralHotkeys(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "top")
	sf.gotoCell(0, 0)

	sf.ProcessKey(key('I', vtinput.LeftAltPressed))
	if sf.Document().Cell(0, 1) == nil {
		t.Error("Alt+I must push the existing row down")
	}
	sf.ProcessKey(key('L', vtinput.LeftAltPressed))
	if sf.Document().Cell(0, 0) == nil {
		t.Error("Alt+L must pull the row back up")
	}

	before := sf.Document().ColumnWidth(0)
	sf.ProcessKey(key(vtinput.VK_RIGHT, vtinput.LeftAltPressed))
	if after := sf.Document().ColumnWidth(0); after != before+1 {
		t.Errorf("column width = %d, want %d", after, before+1)
	}
	sf.ProcessKey(key(vtinput.VK_LEFT, vtinput.LeftAltPressed))
	if after := sf.Document().ColumnWidth(0); after != before {
		t.Errorf("column width = %d, want the original %d", after, before)
	}

	separators := sf.Document().Separators
	sf.ProcessKey(key('S', vtinput.LeftAltPressed))
	if sf.Document().Separators == separators {
		t.Error("Alt+S must toggle the column separators")
	}
}

func TestSheetFrameUndoHotkey(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "value")

	sf.ProcessKey(key(vtinput.VK_BACK, vtinput.LeftAltPressed))
	if !sf.Document().Cell(0, 0).IsEmpty() {
		t.Error("Alt+Backspace must undo the last edit")
	}
}

func TestSheetFrameRendersGrid(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "42")

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 30)
	sf.SetPosition(0, 0, 99, 28)
	sf.Show(scr)

	var header strings.Builder
	for x := 0; x < 40; x++ {
		header.WriteRune(testutil.Rune(scr.GetCell(x, sf.headerY()).Char))
	}
	if !strings.Contains(header.String(), "A") || !strings.Contains(header.String(), "B") {
		t.Errorf("column header row = %q, want the column letters", header.String())
	}

	var firstRow strings.Builder
	for x := 0; x < 20; x++ {
		firstRow.WriteRune(testutil.Rune(scr.GetCell(x, sf.gridTopY()).Char))
	}
	if !strings.Contains(firstRow.String(), "1") || !strings.Contains(firstRow.String(), "42") {
		t.Errorf("first grid row = %q, want the row number and the value", firstRow.String())
	}
}

func TestSheetFrameSeparatorsDoNotCoverText(t *testing.T) {
	sf := newSheetFrameForTest(t)
	typeText(sf, "aaa")
	sf.gotoCell(0, 0)
	sf.ProcessKey(key('S', vtinput.LeftAltPressed))
	if !sf.Document().Separators {
		t.Fatal("Alt+S must switch the column separators on")
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 30)
	sf.SetPosition(0, 0, 99, 28)
	sf.Show(scr)

	var firstRow strings.Builder
	for x := 0; x < 40; x++ {
		firstRow.WriteRune(testutil.Rune(scr.GetCell(x, sf.gridTopY()).Char))
	}
	rendered := firstRow.String()
	if !strings.Contains(rendered, "aaa") {
		t.Errorf("first grid row = %q, want the full cell text with separators on", rendered)
	}
	if !strings.ContainsRune(rendered, sheet.ColumnSeparator) {
		t.Errorf("first grid row = %q, want the column separator drawn", rendered)
	}
}

func TestSheetFrameKeyLabelsAreLocalized(t *testing.T) {
	sf := newSheetFrameForTest(t)
	labels := sf.GetKeyLabels()
	if labels == nil {
		t.Fatal("the spreadsheet must publish a key bar")
	}
	for index, label := range labels.Normal {
		if strings.HasPrefix(label, "{") {
			t.Errorf("F%d label %q has no translation", index+1, label)
		}
	}
}
