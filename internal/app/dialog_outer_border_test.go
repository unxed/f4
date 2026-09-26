package app

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtui"
)

// TestRenderDialogOuterBorderDrawsRingWhenEnabled covers f4#1399: with the
// setting on, an extra frame appears one screen cell outside a modal
// dialog's own border, in the same colour the dialog's border was just
// drawn with.
func TestRenderDialogOuterBorderDrawsRingWhenEnabled(t *testing.T) {
	orig := config.App.DialogOuterBorder
	t.Cleanup(func() { config.App.DialogOuterBorder = orig })
	config.App.DialogOuterBorder = true

	scr := vtui.NewScreenBuf()
	scr.AllocBuf(40, 20)

	dlg := vtui.NewDialog(5, 5, 20, 12, "Test")
	dlg.Show(scr)
	RenderDialogOuterBorder(scr, dlg)

	x1, y1, x2, y2 := dlg.GetPosition()
	corners := [][2]int{{x1 - 1, y1 - 1}, {x2 + 1, y1 - 1}, {x1 - 1, y2 + 1}, {x2 + 1, y2 + 1}}
	for _, c := range corners {
		if cell := scr.GetCell(c[0], c[1]); cell.Char == 0 {
			t.Fatalf("expected a border glyph at (%d,%d) outside the dialog, got %+v", c[0], c[1], cell)
		}
	}
	mid := [][2]int{{(x1 + x2) / 2, y1 - 1}, {(x1 + x2) / 2, y2 + 1}, {x1 - 1, (y1 + y2) / 2}, {x2 + 1, (y1 + y2) / 2}}
	for _, c := range mid {
		if cell := scr.GetCell(c[0], c[1]); cell.Char == 0 {
			t.Fatalf("expected a border glyph at (%d,%d) outside the dialog, got %+v", c[0], c[1], cell)
		}
	}

	// The ring must reuse the colour the dialog's own border was drawn
	// with, not some unrelated palette entry.
	wantAttr := scr.GetCell(x1, y1).Attributes
	if got := scr.GetCell(x1-1, y1-1).Attributes; got != wantAttr {
		t.Fatalf("outer border attr = %x, want %x (dialog's own border colour)", got, wantAttr)
	}

	// It stays a single cell wide: nothing further out gets touched.
	if cell := scr.GetCell(x1-2, y1-1); cell.Char != 0 {
		t.Fatalf("outer border painted past its one-cell ring: %+v", cell)
	}
}

// TestRenderDialogOuterBorderDefaultOff covers the "must not change default
// behaviour" requirement: with the setting left at its zero value (as
// config.Default has it), nothing is painted outside the dialog's own
// border.
func TestRenderDialogOuterBorderDefaultOff(t *testing.T) {
	orig := config.App.DialogOuterBorder
	t.Cleanup(func() { config.App.DialogOuterBorder = orig })
	config.App.DialogOuterBorder = false

	scr := vtui.NewScreenBuf()
	scr.AllocBuf(40, 20)

	dlg := vtui.NewDialog(5, 5, 20, 12, "Test")
	dlg.Show(scr)
	RenderDialogOuterBorder(scr, dlg)

	x1, y1, x2, y2 := dlg.GetPosition()
	for _, c := range [][2]int{{x1 - 1, y1 - 1}, {x2 + 1, y2 + 1}} {
		if cell := scr.GetCell(c[0], c[1]); cell.Char != 0 {
			t.Fatalf("outer border drawn while DialogOuterBorder is off: %+v at (%d,%d)", cell, c[0], c[1])
		}
	}
}

// TestRenderDialogOuterBorderUserMenuOnlyNotPlainMenus covers the other half
// of f4#1399: the ring also wraps the user menu, but not an ordinary
// dropdown/context menu built from the same vtui.VMenu.
func TestRenderDialogOuterBorderUserMenuOnlyNotPlainMenus(t *testing.T) {
	orig := config.App.DialogOuterBorder
	t.Cleanup(func() { config.App.DialogOuterBorder = orig })
	config.App.DialogOuterBorder = true

	scr := vtui.NewScreenBuf()
	scr.AllocBuf(40, 20)
	menu := vtui.NewVMenu("User menu")
	menu.SetPosition(5, 5, 20, 12)
	frame := &panel.UserMenuFrame{VMenu: menu}
	frame.Show(scr)
	RenderDialogOuterBorder(scr, frame)
	if cell := scr.GetCell(4, 4); cell.Char == 0 {
		t.Fatal("expected an outer border ring around the user menu")
	}

	scr2 := vtui.NewScreenBuf()
	scr2.AllocBuf(40, 20)
	plain := vtui.NewVMenu("Commands")
	plain.SetPosition(5, 5, 20, 12)
	plain.Show(scr2)
	RenderDialogOuterBorder(scr2, plain)
	if cell := scr2.GetCell(4, 4); cell.Char != 0 {
		t.Fatalf("outer border drawn around an ordinary menu: %+v", cell)
	}
}
