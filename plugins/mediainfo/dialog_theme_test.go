package mediainfo

import (
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

func TestReportViewUsesDialogThemeAndDimsFieldNames(t *testing.T) {
	oldText := vtui.Palette[vtui.ColDialogText]
	oldSelected := vtui.Palette[vtui.ColDialogSelectedButton]
	oldBox := vtui.Palette[vtui.ColDialogBox]
	t.Cleanup(func() {
		vtui.Palette[vtui.ColDialogText] = oldText
		vtui.Palette[vtui.ColDialogSelectedButton] = oldSelected
		vtui.Palette[vtui.ColDialogBox] = oldBox
	})
	normal := vtui.SetRGBBoth(0, 0x102030, 0x405060)
	selected := vtui.SetRGBBoth(0, 0x708090, 0xa0b0c0)
	box := vtui.SetRGBBoth(0, 0x334455, 0x405060)
	vtui.Palette[vtui.ColDialogText] = normal
	vtui.Palette[vtui.ColDialogSelectedButton] = selected
	vtui.Palette[vtui.ColDialogBox] = box

	line := "Field     : Value"
	view := newReportTextView(2, 2, 24, 4, []string{line, "Section", "Third", "Fourth", "Overflow"}, true)
	if !view.ShowScrollBar || view.ScrollBar == nil || view.ScrollBar.ColorIdx != vtui.ColDialogBox {
		t.Fatal("report view does not use an overflow-aware dialog scrollbar")
	}
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	view.SetFocus(false)
	view.Show(screen)
	if got, want := screen.GetCell(2, 2).Attributes, subduedReportFieldAttr(normal); got != want {
		t.Fatalf("field-name attr = %#x, want subdued dialog text %#x", got, want)
	}
	// Subdued sits between the normal text and the fully dimmed one, measured
	// by how far each is from the background (vtui.DimColor fades into it).
	away := func(attr uint64) int {
		sum := 0
		fg, bg := vtui.GetRGBFore(attr), vtui.GetRGBBack(attr)
		for shift := 0; shift < 24; shift += 8 {
			diff := int((fg>>shift)&0xff) - int((bg>>shift)&0xff)
			if diff < 0 {
				diff = -diff
			}
			sum += diff
		}
		return sum
	}
	if got, dim, full := away(subduedReportFieldAttr(normal)), away(vtui.DimColor(normal)), away(normal); got <= dim || got >= full {
		t.Fatalf("subdued distance from the background %d must be more than the fully dimmed %d and less than the normal %d", got, dim, full)
	}
	valueX := 2 + strings.Index(line, " : ") + len(" : ")
	if got := screen.GetCell(valueX, 2).Attributes; got != normal {
		t.Fatalf("field-value attr = %#x, want dialog text %#x", got, normal)
	}
	if cell := screen.GetCell(view.X2, view.Y1); cell.Char != vtui.ScrollUpArrow || cell.Attributes != box {
		t.Fatalf("overflow scrollbar cell = %#v, want arrow %#x with dialog box attr %#x", cell, vtui.ScrollUpArrow, box)
	}

	view.SetFocus(true)
	view.Show(screen)
	if got, want := screen.GetCell(2, 2).Attributes, subduedReportFieldAttr(selected); got != want {
		t.Fatalf("focused field-name attr = %#x, want subdued selection %#x", got, want)
	}
	if got := screen.GetCell(valueX, 2).Attributes; got != selected {
		t.Fatalf("focused field-value attr = %#x, want dialog selection %#x", got, selected)
	}

	updatedNormal := vtui.SetRGBBoth(0, 0x506070, 0x203040)
	updatedSelected := vtui.SetRGBBoth(0, 0xc0d0e0, 0x304050)
	updatedBox := vtui.SetRGBBoth(0, 0x8090a0, 0x203040)
	vtui.Palette[vtui.ColDialogText] = updatedNormal
	vtui.Palette[vtui.ColDialogSelectedButton] = updatedSelected
	vtui.Palette[vtui.ColDialogBox] = updatedBox
	view.SetFocus(false)
	view.Show(screen)
	if got, want := screen.GetCell(2, 2).Attributes, subduedReportFieldAttr(updatedNormal); got != want {
		t.Fatalf("live-theme field-name attr = %#x, want %#x", got, want)
	}
	if got := screen.GetCell(valueX, 2).Attributes; got != updatedNormal {
		t.Fatalf("live-theme field-value attr = %#x, want %#x", got, updatedNormal)
	}
	if got := screen.GetCell(view.X2, view.Y1).Attributes; got != updatedBox {
		t.Fatalf("live-theme scrollbar attr = %#x, want %#x", got, updatedBox)
	}
}

func TestReportViewHidesScrollbarWhenContentFits(t *testing.T) {
	view := newReportTextView(2, 2, 24, 4, []string{"one", "two", "three", "four"}, true)
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	border := vtui.SetRGBBoth(0, 0x112233, 0x445566)
	for y := view.Y1; y <= view.Y2; y++ {
		screen.FillRect(view.X2, y, view.X2, y, '│', border)
	}
	view.Show(screen)
	if cell := screen.GetCell(view.X2, view.Y1); cell.Char != '│' || cell.Attributes != border {
		t.Fatalf("fitting report overwrote dialog border: %#v", cell)
	}
}

func TestReportViewResizeClampsScrollPosition(t *testing.T) {
	view := newReportTextView(2, 2, 24, 4, []string{"one", "two", "three", "four", "five", "six"}, true)
	view.SetSelectPos(5)
	if view.TopPos != 2 {
		t.Fatalf("initial TopPos = %d, want 2", view.TopPos)
	}

	view.SetPosition(2, 2, 25, 7)
	if view.ViewHeight != 6 || view.TopPos != 0 {
		t.Fatalf("grown view = height %d, top %d; want height 6, top 0", view.ViewHeight, view.TopPos)
	}

	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	view.Show(screen)
	if got := screen.GetCell(view.X2, view.Y1).Char; got == vtui.ScrollUpArrow {
		t.Fatal("grown report view retained a scrollbar although all rows fit")
	}

	view.SetPosition(2, 2, 25, 5)
	if view.ViewHeight != 4 || view.TopPos != 2 {
		t.Fatalf("shrunk view = height %d, top %d; want height 4, top 2", view.ViewHeight, view.TopPos)
	}
	view.Show(screen)
	if got := screen.GetCell(view.X2, view.Y1).Char; got != vtui.ScrollUpArrow {
		t.Fatalf("shrunk report view scrollbar = %#x, want %#x", got, vtui.ScrollUpArrow)
	}
}

func TestReportViewLeavesCustomTemplateTextUnstyled(t *testing.T) {
	oldText := vtui.Palette[vtui.ColDialogText]
	t.Cleanup(func() { vtui.Palette[vtui.ColDialogText] = oldText })
	normal := vtui.SetRGBBoth(0, 0x90a0b0, 0x102030)
	vtui.Palette[vtui.ColDialogText] = normal

	view := newReportTextView(2, 2, 30, 3, []string{"Literal : remains literal"}, false)
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	view.Show(screen)
	for x := view.X1; x < view.X1+len("Literal : remains literal"); x++ {
		if got := screen.GetCell(x, view.Y1).Attributes; got != normal {
			t.Fatalf("custom template cell %d attr = %#x, want %#x", x-view.X1, got, normal)
		}
	}
}

func TestMediaInfoDialogLabelTracksLiveTheme(t *testing.T) {
	oldText := vtui.Palette[vtui.ColDialogText]
	t.Cleanup(func() { vtui.Palette[vtui.ColDialogText] = oldText })

	first := vtui.SetRGBBoth(0, 0x112233, 0x445566)
	second := vtui.SetRGBBoth(0, 0xaabbcc, 0x223344)
	vtui.Palette[vtui.ColDialogText] = first
	label := newMediaInfoDialogLabel(3, 3, "Label")
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	label.Show(screen)
	if got := screen.GetCell(3, 3).Attributes; got != first {
		t.Fatalf("initial label attr = %#x, want %#x", got, first)
	}

	vtui.Palette[vtui.ColDialogText] = second
	label.Show(screen)
	if got := screen.GetCell(3, 3).Attributes; got != second {
		t.Fatalf("label retained stale theme attr = %#x, want %#x", got, second)
	}
}
