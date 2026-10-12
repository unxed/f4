package cmdline

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestCommandLineCaretMatchesInsertionCell(t *testing.T) {
	previousPalette := vtui.Palette
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	t.Cleanup(func() { vtui.Palette = previousPalette })
	previousMode := vtui.DefaultBidiMode
	vtui.DefaultBidiMode = vtui.BidiFull
	t.Cleanup(func() { vtui.DefaultBidiMode = previousMode })
	for _, prompt := range []string{">", "xs@HC C:\\work>", "界>"} {
		t.Run(prompt, func(t *testing.T) {
			cl := NewCommandLine("")
			cells := vtui.StringToCharInfo(prompt, 7)
			cl.SetRichPrompt(cells)
			cl.SetPosition(0, 0, 79, 0)
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 2)
			cl.Edit.SetText("abc")
			cl.Edit.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT,
			})
			cl.Show(scr)
			x, y, visible, _ := scr.GetCursorStateForTesting()
			if !visible || x != len(cells)+2 || y != 0 {
				t.Fatalf("caret=(%d,%d), visible=%v, want (%d,0)", x, y, visible, len(cells)+2)
			}
			cl.Edit.InsertString("X")
			if got := cl.Edit.GetText(); got != "abXc" {
				t.Fatalf("inserted text=%q, want abXc", got)
			}
		})
	}
}

func TestCommandLineASCIICaretAfterScrolling(t *testing.T) {
	previousMode, previousPalette := vtui.DefaultBidiMode, vtui.Palette
	vtui.DefaultBidiMode = vtui.BidiFull
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	t.Cleanup(func() {
		vtui.DefaultBidiMode, vtui.Palette = previousMode, previousPalette
	})
	for _, length := range []int{3, 20, 80} {
		for _, left := range []int{0, 1, 2} {
			cl := NewCommandLine(">")
			cl.SetPosition(0, 0, 19, 0)
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(20, 2)
			cl.Edit.SetText(strings.Repeat("a", length))
			cl.Show(scr)
			for i := 0; i < left; i++ {
				cl.Edit.ProcessKey(&vtinput.InputEvent{
					Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT,
				})
				cl.Show(scr)
			}
			x, _, _, _ := scr.GetCursorStateForTesting()
			cl.Edit.InsertString("X")
			// Display without scrolling again: insertion must paint the cell
			// that the caret occupied, even in an already scrolled viewport.
			cl.Edit.DisplayObject(scr)
			if got := vtui.CellString(scr.GetCell(x, 0).Char); got != "X" {
				t.Errorf("length=%d left=%d: caret cell %d contains %q after insertion", length, left, x, got)
			}
		}
	}
}
