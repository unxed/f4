//go:build !lite

package editor

import (
	"strings"
	"unicode"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// outlineFilterLimit is FarColorer's FILTER_SIZE.
const outlineFilterLimit = 40

// colorerEditorPosition is where the editor stood, to put it back.
type colorerEditorPosition struct {
	line, pos, top, left int
	sel, rect            bool
	anchor               int
}

func (ev *EditorView) colorerPosition() colorerEditorPosition {
	return colorerEditorPosition{ev.CursorLine, ev.CursorPos, ev.ScrollTopRow, ev.ScrollLeft, ev.SelActive, ev.RectSelActive, ev.SelAnchorOffset}
}

func (ev *EditorView) restoreColorerPosition(p colorerEditorPosition) {
	ev.CursorLine, ev.CursorPos, ev.ScrollTopRow, ev.ScrollLeft = p.line, p.pos, p.top, p.left
	ev.SelActive, ev.RectSelActive, ev.SelAnchorOffset = p.sel, p.rect, p.anchor
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// colorerOutlineFrame is FarEditor::showOutliner: the outline list with
// FarColorer's own filter and keys.
//
//   - Letters, digits, space and ; - : _ ~ narrow the list to labels holding
//     the filter, ignoring case; Backspace takes a character back; Tab takes
//     the completion shown after "?" in the title.
//   - Enter or a click goes to the item; Ctrl+Enter inserts its label at the
//     cursor.
//   - Ctrl+Up and Ctrl+Down go to the previous or next item while the list
//     stays open; Esc then puts the editor back where it was.
//   - Ctrl+Left and Ctrl+Right show one tree level less or more.
type colorerOutlineFrame struct {
	*vtui.VMenu
	ev      *EditorView
	entries []colorerOutlineEntry
	rows    []int // the entry each menu row shows
	filter  []rune
	auto    []rune // the filter Tab would set
	visible int    // the deepest tree level shown
	deepest int    // the deepest tree level there is
	origin  colorerEditorPosition
}

func newColorerOutlineFrame(ev *EditorView, entries []colorerOutlineEntry) *colorerOutlineFrame {
	f := &colorerOutlineFrame{
		VMenu:   vtui.NewVMenu(""),
		ev:      ev,
		entries: entries,
		visible: 100, // FarEditor's visibleLevel
		deepest: -1,
		origin:  ev.colorerPosition(),
	}
	f.DisableFilter = true
	f.rebuild(false)
	f.OnKeyDown = f.key
	f.OnAction = func(int) { f.jump() }
	return f
}

// row is an entry's menu text; an ampersand is shown, not taken as a hotkey.
func (f *colorerOutlineFrame) row(entry colorerOutlineEntry, level int) string {
	lineText := ""
	if config.App.EditorColorerOldOutline {
		lineText, _ = f.ev.lineTextForHighlight(entry.line)
	}
	return strings.ReplaceAll(colorerOutlineRow(entry, level, config.App.EditorColorerOldOutline, lineText), "&", "&&")
}

// rebuild fills the menu for the filter and the visible level. keyed is set
// after a key, when FarColorer offers a filter completion.
func (f *colorerOutlineFrame) rebuild(keyed bool) {
	for {
		f.Items = f.Items[:0]
		f.rows = f.rows[:0]
		filter := strings.ToLower(string(f.filter))
		var stack []int
		selected := 0
		for i, entry := range f.entries {
			if !strings.Contains(strings.ToLower(entry.label), filter) {
				continue
			}
			var level int
			stack, level = manageColorerOutlineTree(stack, entry.item.Level)
			f.deepest = max(f.deepest, level)
			if level > f.visible {
				continue
			}
			f.AddItem(vtui.MenuItem{Text: f.row(entry, level)})
			// FarColorer selects the nearest item at or above the cursor.
			if entry.line <= f.ev.CursorLine {
				selected = len(f.rows)
			}
			f.rows = append(f.rows, i)
		}
		// A filter nothing matches loses its last character.
		if len(f.rows) == 0 && len(f.filter) > 0 {
			f.filter = f.filter[:len(f.filter)-1]
			continue
		}
		if len(f.rows) > 0 {
			f.SetSelectPos(selected)
		}
		break
	}
	f.auto = f.filter
	if keyed {
		rows := make([]string, len(f.Items))
		for i, item := range f.Items {
			rows[i] = item.Text
		}
		f.auto = colorerOutlineAutoFilter(rows, f.filter)
	}
}

// colorerOutlineAutoFilter is FarColorer's filter completion: extend the
// filter one character at a time with what follows it in the first row, as
// long as every row still holds the longer text, ignoring case.
func colorerOutlineAutoFilter(rows []string, filter []rune) []rune {
	auto := append([]rune(nil), filter...)
	if len(rows) < 2 {
		return auto
	}
	first := []rune(rows[0])
	lowerFirst := []rune(strings.ToLower(rows[0]))
	for len(auto) < outlineFilterLimit {
		at := runeIndexFold(lowerFirst, []rune(strings.ToLower(string(auto))))
		if at < 0 || len(first)-at < len(auto)+1 {
			break
		}
		prefix := strings.ToLower(string(first[at : at+len(auto)+1]))
		for _, row := range rows[1:] {
			if !strings.Contains(strings.ToLower(row), prefix) {
				return auto
			}
		}
		auto = first[at : at+len(auto)+1]
	}
	return auto
}

// runeIndexFold is the rune offset of needle in hay, both already lower case.
func runeIndexFold(hay, needle []rune) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

// title is FarColorer's: the filter, then "?" and the completion Tab takes.
func (f *colorerOutlineFrame) title() string {
	caption := string(f.filter)
	if len(f.auto) > len(f.filter) {
		caption += "?" + string(f.auto[len(f.filter):])
	}
	if caption == "" {
		return i18n.Msg("Colorer.Outliner")
	}
	return i18n.Msg("Colorer.Outliner") + ": " + caption
}

func (f *colorerOutlineFrame) Show(scr *vtui.ScreenBuf) {
	f.VMenu.Show(scr)
	vtui.NewPainter(scr).DrawTitle(f.X1, f.Y1, f.X2, " "+f.title()+" ", vtui.Palette[f.ColorTitleIdx])
}

func (f *colorerOutlineFrame) selected() (colorerOutlineEntry, bool) {
	if f.SelectPos < 0 || f.SelectPos >= len(f.rows) {
		return colorerOutlineEntry{}, false
	}
	return f.entries[f.rows[f.SelectPos]], true
}

// jump goes to the selected item and closes the list.
func (f *colorerOutlineFrame) jump() {
	entry, ok := f.selected()
	f.Close()
	if ok {
		f.ev.gotoColorerOutline(entry)
	}
}

func (f *colorerOutlineFrame) key(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	if ctrl && !alt {
		switch e.VirtualKeyCode {
		case vtinput.VK_UP, vtinput.VK_DOWN:
			if len(f.rows) == 0 {
				return true
			}
			pos := f.SelectPos + 1
			if e.VirtualKeyCode == vtinput.VK_UP {
				pos = f.SelectPos - 1
			}
			pos = (pos + len(f.rows)) % len(f.rows)
			f.SetSelectPos(pos)
			if entry, ok := f.selected(); ok {
				f.ev.gotoColorerOutline(entry)
			}
			return true
		case vtinput.VK_LEFT:
			if f.visible > f.deepest {
				f.visible = f.deepest - 1
			} else if f.visible > 0 {
				f.visible--
			}
			f.visible = max(f.visible, 0)
			f.rebuild(true)
			return true
		case vtinput.VK_RIGHT:
			f.visible++
			f.rebuild(true)
			return true
		case vtinput.VK_RETURN:
			entry, ok := f.selected()
			f.Close()
			if ok && entry.label != "" {
				f.ev.RectSelActive = false
				f.ev.SelActive = false
				f.ev.InsertTextAtCursor([]byte(entry.label))
				f.ev.EnsureCursorVisible()
			}
			return true
		}
		return false
	}
	if alt {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE:
		f.Close()
		f.ev.restoreColorerPosition(f.origin)
		return true
	case vtinput.VK_RETURN:
		f.jump()
		return true
	case vtinput.VK_BACK:
		if len(f.filter) > 0 {
			f.filter = f.filter[:len(f.filter)-1]
		}
		f.rebuild(true)
		return true
	case vtinput.VK_TAB:
		f.filter = append([]rune(nil), f.auto...)
		f.rebuild(true)
		return true
	}
	if r := e.Char; r != 0 && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ;-:_~", r)) {
		if len(f.filter) < outlineFilterLimit {
			f.filter = append(f.filter, unicode.ToLower(r))
		}
		f.rebuild(true)
		return true
	}
	return false
}
