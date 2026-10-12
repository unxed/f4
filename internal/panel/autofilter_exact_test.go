package panel

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func drawFilterWindow(t *testing.T, fp *FileSystemPanel) (*vtui.ScreenBuf, []string) {
	t.Helper()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	fp.Show(scr)
	return scr, strings.Split(screenText(scr), "\n")
}

func screenText(scr *vtui.ScreenBuf) string {
	var rows []string
	for y := 0; y < scr.Height(); y++ {
		var sb strings.Builder
		for x := 0; x < scr.Width(); x++ {
			ch := scr.GetCell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(rune(ch))
		}
		rows = append(rows, sb.String())
	}
	return strings.Join(rows, "\n")
}

// The exact-match option is a line in the filter window itself, and clicking it
// or pressing Ctrl+E re-filters at once (f4#1131).
func TestAutoFilterWindowHasExactMatchCheckbox(t *testing.T) {
	oldCfg := config.App
	t.Cleanup(func() { config.App = oldCfg })
	config.App.PanelStrictAutoFilter = false

	fp := newAutoFilterPanel(t)
	openAutoFilter(t, fp)
	// One substitution away from "alpha": the fuzzy matcher accepts it, the
	// exact one must not.
	typeIntoFilter(t, fp, "alqha")
	if len(fp.Entries) < 2 {
		t.Skipf("the fuzzy matcher did not tolerate the typo here: %v", panelNames(fp))
	}

	_, rows := drawFilterWindow(t, fp)
	var line string
	for _, r := range rows {
		if strings.Contains(r, "Exact match") {
			line = r
		}
	}
	if line == "" {
		t.Fatalf("the filter window has no exact-match line:\n%s", strings.Join(rows, "\n"))
	}

	// A click on the line turns it on and narrows the list to nothing.
	// #nosec G115 -- screen coordinates of an 80x25 test panel.
	click := &vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: int16(fp.exactBoxX1 + 1), MouseY: int16(fp.exactBoxY),
		ButtonState: vtinput.FromLeft1stButtonPressed,
	}
	if !fp.ProcessMouse(click) {
		t.Fatal("the click on the exact-match line was not taken")
	}
	if !config.App.PanelStrictAutoFilter {
		t.Fatal("the click did not turn the exact match on")
	}
	if len(fp.Entries) != 1 || fp.Entries[0].Name != ".." {
		t.Errorf("exact match still shows %v for the typo", panelNames(fp))
	}
	if !fp.AutoFilterActive() {
		t.Error("the click closed the filter window")
	}
	// The checkbox glyph changes with the option.
	_, rowsOn := drawFilterWindow(t, fp)
	var lineOn string
	for _, r := range rowsOn {
		if strings.Contains(r, "Exact match") {
			lineOn = r
		}
	}
	if lineOn == "" || lineOn == line {
		t.Errorf("the exact-match line did not change with the option: %q", strings.TrimSpace(lineOn))
	}

	// Ctrl+E turns it off again and the typo-tolerant rows come back.
	ctrlE := &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_E, ControlKeyState: vtinput.LeftCtrlPressed,
	}
	if !fp.ProcessKey(ctrlE) {
		t.Fatal("Ctrl+E was not taken by the open filter")
	}
	if config.App.PanelStrictAutoFilter {
		t.Fatal("Ctrl+E did not turn the exact match off")
	}
	if len(fp.Entries) < 2 {
		t.Errorf("the rows did not come back: %v", panelNames(fp))
	}
}

// A plain quick search has no such line: the option belongs to the filter.
func TestQuickSearchWindowHasNoExactMatchLine(t *testing.T) {
	fp := newAutoFilterPanel(t)
	typeIntoPanel(t, fp, "al")
	_, rows := drawFilterWindow(t, fp)
	if strings.Contains(strings.Join(rows, "\n"), "Exact match") {
		t.Error("the quick search window shows the filter's exact-match line")
	}
}

// The query line of the search and filter windows has the colour of an ordinary
// dialog edit field, and the typed text keeps its own foreground (f4#1131).
func TestFastFindQueryLineHasTheEditFieldBackground(t *testing.T) {
	fp := newAutoFilterPanel(t)
	openAutoFilter(t, fp)
	typeIntoFilter(t, fp, "alpha")
	scr, _ := drawFilterWindow(t, fp)

	// The window sits at fp.X1+9, one row below its top border.
	x := fp.X1 + 9 + 4
	y := fp.exactBoxY - 1
	cell := scr.GetCell(x, y)
	_, wantBg := theme.GetColorRGBBoth(vtui.Palette[vtui.ColDialogEdit])
	if _, gotBg := theme.GetColorRGBBoth(cell.Attributes); gotBg != wantBg {
		t.Errorf("query cell background = %06X, want the edit field's %06X", gotBg, wantBg)
	}
	blank := scr.GetCell(fp.X1+9+20, y)
	if _, gotBg := theme.GetColorRGBBoth(blank.Attributes); gotBg != wantBg {
		t.Errorf("the empty end of the query line has background %06X, want %06X", gotBg, wantBg)
	}
}
