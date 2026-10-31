package panel

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The quick search after a keyboard layout switch (#2186).

// searchingPanel is a panel listing names with the quick search open on text.
func searchingPanel(t *testing.T, text string, names ...string) *FileSystemPanel {
	t.Helper()
	fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
	waitForLoad(t, fp)
	fp.Entries = nil
	for _, name := range names {
		fp.Entries = append(fp.Entries, &FileEntry{VFSItem: vfs.VFSItem{Name: name}})
	}
	fp.Refresh()
	fp.FastFindMode = true
	fp.FastFindStr = text
	return fp
}

// A text typed half in one layout and half in the other finds the name it
// spells: "fщ" is "fo" with the Russian layout switched on after the f. The
// whole text in the other layout ("аo") finds nothing, so the characters are
// tried one at a time, as far2l does -- in both matching modes.
func TestFastFindMatchesTextTypedInTwoLayouts(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	for _, strict := range []bool{false, true} {
		config.App.PanelStrictAutoFilter = strict
		config.App.PanelQuickSearchXLat = true
		fp := searchingPanel(t, "fщ", "foo", "bar")
		if _, _, ok := fp.fastFindMatch("foo"); !ok {
			t.Errorf("strict=%t: \"fщ\" does not find \"foo\"", strict)
		}
		if _, _, ok := fp.fastFindMatch("bar"); ok {
			t.Errorf("strict=%t: \"fщ\" finds \"bar\"", strict)
		}
		config.App.PanelQuickSearchXLat = false
		if _, _, ok := fp.fastFindMatch("foo"); ok && strict {
			t.Errorf("strict=%t: \"fщ\" finds \"foo\" with PanelQuickSearchXLat off", strict)
		}
	}
}

// The character of the other layout goes into the search, the box keeps it
// as typed, and the cursor goes to the name it finds.
func TestFastFindAcceptsCharacterOfTheOtherLayout(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	config.App.PanelQuickSearchXLat = true

	fp := searchingPanel(t, "f", "far", "foo")
	fp.SetCursorIndex(0)
	fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, Char: 'щ'})
	if fp.FastFindStr != "fщ" {
		t.Fatalf("search text = %q, want what was typed, %q", fp.FastFindStr, "fщ")
	}
	if got := fp.GetSelectedName(); got != "foo" {
		t.Fatalf("cursor on %q, want %q", got, "foo")
	}
}

// A character the search refuses stands after its text for a moment, in the
// colour of a search that finds nothing, with the cursor on it; the next key
// of the search or the timer takes it away. It used to vanish without a
// trace, which after a layout switch looked like a keyboard that stopped
// working.
func TestFastFindShowsRefusedCharacter(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	oldShown := fastFindRejectShown
	defer func() { fastFindRejectShown = oldShown }()
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	config.App.PanelStrictAutoFilter = true
	config.App.PanelQuickSearchXLat = false

	fp := searchingPanel(t, "fo", "foo", "bar")
	refuse := func() {
		fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_Z, Char: 'z'})
	}
	fastFindRejectShown = time.Hour
	refuse()
	if fp.FastFindStr != "fo" {
		t.Fatalf("search text = %q, want %q: the character found nothing", fp.FastFindStr, "fo")
	}
	fp.Show(scr)
	inputX, inputY := fp.X1+10, fp.Y2-1
	cell := scr.GetCell(inputX+2, inputY)
	if cell.Char != 'z' {
		t.Fatalf("after the text: %q, want the refused 'z'", testutil.Rune(cell.Char))
	}
	if got, want := vtui.GetRGBFore(cell.Attributes), vtui.GetRGBFore(vtui.Palette[theme.ColPanelFastFindNoMatch]); got != want {
		t.Fatalf("refused character foreground = %#06x, want the no-match colour %#06x", got, want)
	}
	if got, want := vtui.GetRGBFore(scr.GetCell(inputX, inputY).Attributes), vtui.GetRGBFore(vtui.Palette[vtui.ColMenuHighlight]); got != want {
		t.Fatalf("search text foreground = %#06x, want the match colour %#06x", got, want)
	}

	// The next key of the search takes it away.
	fp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_O, Char: 'o'})
	fp.Show(scr)
	if fp.FastFindStr != "foo" || scr.GetCell(inputX+3, inputY).Char == 'z' {
		t.Fatalf("after another key: text %q, refused character still shown", fp.FastFindStr)
	}

	// So does the timer.
	fastFindRejectShown = time.Millisecond
	refuse()
	deadline := time.After(5 * time.Second)
	for fp.fastFindRejected != "" {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the refused character is still shown")
		}
	}
}
