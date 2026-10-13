package app

import (
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtui"
)

func TestCommandPaletteSheetEntries(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 30)
	vtui.FrameManager.Init(scr)

	sheet := NewSheetFrame()
	vtui.FrameManager.Push(sheet)
	entries := commandPaletteSheetEntries(sheet)
	wantIDs := []string{
		"Sheet.Save", "Sheet.SaveAs", "Sheet.Open", "Sheet.Export", "Sheet.Recalc",
		"Sheet.Goto", "Sheet.Find", "Sheet.Replace", "Sheet.SearchAgain",
		"Sheet.CellFormat", "Sheet.ColumnWidth", "Sheet.ToggleSeparators",
		"Sheet.CSVSeparator",
		"Sheet.InsertRow", "Sheet.InsertColumn", "Sheet.DeleteRow", "Sheet.DeleteColumn",
		"Sheet.Undo", "Sheet.Menu",
	}
	if len(entries) != len(wantIDs) {
		t.Fatalf("sheet command count = %d, want %d", len(entries), len(wantIDs))
	}
	for i, id := range wantIDs {
		if entries[i].ID != id {
			t.Fatalf("sheet command %d = %q, want %q", i, entries[i].ID, id)
		}
		if entries[i].Category != i18n.Msg("CommandPalette.CategorySpreadsheet") {
			t.Fatalf("sheet command %q category = %q", id, entries[i].Category)
		}
	}

	toggle := entries[11]
	if toggle.Checked {
		t.Fatal("separator toggle is checked before the first execution")
	}
	if !executeCommandPaletteEntry(toggle) || !sheet.doc.Separators {
		t.Fatal("separator toggle did not enable separators")
	}

	vtui.FrameManager.Push(&directPaletteOtherFrame{})
	if executeCommandPaletteEntry(toggle) {
		t.Fatal("stale sheet command ran after another frame became top")
	}
}
