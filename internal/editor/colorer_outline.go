//go:build !lite

package editor

import (
	"fmt"
	"strings"
	"unicode"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// colorerOutlineEntry is one item of Colorer's Outliner, on its line, with the
// label cut from the text that was parsed.
type colorerOutlineEntry struct {
	line  int
	item  colorer.OutlineItem
	label string
}

// colorerOutlineBuild collects the outline of the whole file, which
// FarColorer gets by parsing it all (BaseEditor::validate(-1)). Like the pair
// search it walks the line cache on the UI thread and queues each line the
// cache has not reached to the worker, resuming when the result lands.
type colorerOutlineBuild struct {
	errors  bool // def:Error (the list of errors) rather than def:Outlined
	next    int  // the first line not collected yet
	entries []colorerOutlineEntry
	done    func([]colorerOutlineEntry)
	// As in colorerPairSearch: a line queued repeatedly without arriving
	// abandons the build.
	queued, repeats int
}

// outlineLabelLimit is where FarColorer cuts a menu row.
const outlineLabelLimit = 110

// buildOutline starts collecting the outline and calls done on the UI thread
// with every entry once all lines are parsed. An edit or Esc drops it.
func (ch *ColorerHighlighter) buildOutline(errors bool, done func([]colorerOutlineEntry)) {
	if ch.session == nil || ch.closed || ch.disabled || ch.owner == nil {
		return
	}
	ch.outlineBuild = &colorerOutlineBuild{errors: errors, done: done, queued: -1}
	ch.continueOutline()
}

// continueOutline advances the outline build in progress, if any. It runs
// when the build starts and whenever a worker result has landed.
func (ch *ColorerHighlighter) continueOutline() {
	b := ch.outlineBuild
	ev := ch.owner
	if b == nil || ev == nil {
		return
	}
	if ch.closed || ch.disabled || ch.session == nil {
		ch.outlineBuild = nil
		return
	}
	total := ev.Li.LineCount()
	for b.next < total {
		if _, ok := ch.attrCache[b.next]; !ok {
			break
		}
		for _, entry := range ch.outlineCache[b.next] {
			if entry.item.Error == b.errors {
				b.entries = append(b.entries, entry)
			}
		}
		b.next++
	}
	if b.next >= total {
		ch.outlineBuild = nil
		b.done(b.entries)
		return
	}
	if ch.pending {
		return // the job in flight resumes the build when it lands
	}
	if b.next == b.queued {
		b.repeats++
		if b.repeats >= maxColorerPairRequeues {
			vtui.DebugLog("COLORER: outline abandoned: line %d was queued %d times and never parsed", b.next, b.repeats)
			ch.outlineBuild = nil
			return
		}
	} else {
		b.queued, b.repeats = b.next, 0
	}
	text, ok := ev.lineTextForHighlight(b.next)
	if !ok {
		ch.outlineBuild = nil
		return
	}
	ch.queueLine(b.next, text, ev.colorerBaseAttr())
	if ch.pending {
		ev.colorerCancel = func() { ch.outlineBuild = nil }
	}
}

// colorerOutlineEntries turns a parsed line's outline items into entries.
func colorerOutlineEntries(line int, text string, items []colorer.OutlineItem) []colorerOutlineEntry {
	if len(items) == 0 {
		return nil
	}
	entries := make([]colorerOutlineEntry, len(items))
	for i, item := range items {
		entries[i] = colorerOutlineEntry{line: line, item: item, label: item.Label(text)}
	}
	return entries
}

// manageColorerOutlineTree is Outliner::manageTree: the indentation of an item
// at level, given the levels of the items before it.
func manageColorerOutlineTree(stack []int, level int) ([]int, int) {
	for len(stack) > 0 && level < stack[len(stack)-1] {
		stack = stack[:len(stack)-1]
	}
	if len(stack) == 0 || level > stack[len(stack)-1] {
		stack = append(stack, level)
		return stack, len(stack) - 1
	}
	if level == stack[len(stack)-1] {
		return stack, len(stack) - 1
	}
	return stack, 0
}

// colorerOutlineRow is an entry's menu row, as FarEditor::showOutliner writes
// it: the line number, two spaces per tree level, the first letter of the
// region's own name and the label; or, in the old outline view, the text of
// the line itself.
func colorerOutlineRow(entry colorerOutlineEntry, treeLevel int, old bool, lineText string) string {
	cut := func(s string) string {
		if r := []rune(s); len(r) > outlineLabelLimit {
			return string(r[:outlineLabelLimit])
		}
		return s
	}
	if old {
		return cut(strings.TrimRight(lineText, "\r\n"))
	}
	class := ""
	name := entry.item.Region[strings.IndexByte(entry.item.Region, ':')+1:]
	if r := []rune(name); len(r) > 0 {
		class = string(unicode.ToLower(r[0]))
	}
	return fmt.Sprintf("%4d %s%s %s", entry.line+1, strings.Repeat("  ", treeLevel), class, cut(entry.label))
}

// ColorerListOutline opens FarColorer's outliner: the functions, or with
// errors the syntax errors, of the whole file. Choosing one moves the cursor
// to it; an empty list says so.
func (ev *EditorView) ColorerListOutline(errors bool) {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok {
		return
	}
	ch.buildOutline(errors, func(entries []colorerOutlineEntry) {
		ev.showColorerOutline(entries)
	})
}

func colorerNothingFound() {
	vtui.ShowMessage(i18n.Msg("Colorer.Title"), i18n.Msg("Colorer.NothingFound"), []string{i18n.Msg("vtui.Ok")})
}

func (ev *EditorView) showColorerOutline(entries []colorerOutlineEntry) {
	if vtui.FrameManager == nil {
		return
	}
	if len(entries) == 0 {
		colorerNothingFound()
		return
	}
	f := newColorerOutlineFrame(ev, entries)
	// Room for the filter in the title, as far as FarColorer lets it grow.
	width := vtui.StringWidth(i18n.Msg("Colorer.Outliner")) + outlineFilterLimit + 8
	for _, item := range f.Items {
		width = max(width, vtui.StringWidth(item.Text)+6)
	}
	screenW, screenH := vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight()
	width = min(width, screenW-4)
	height := min(len(entries)+2, screenH-4)
	x, y := max((screenW-width)/2, 0), max((screenH-height)/2, 0)
	f.SetPosition(x, y, x+width-1, y+height-1)
	vtui.FrameManager.Push(f)
}

// gotoColorerOutline puts the cursor on an outline entry and the entry's line
// in the middle of the window, as FarColorer does.
func (ev *EditorView) gotoColorerOutline(entry colorerOutlineEntry) {
	text, ok := ev.lineTextForHighlight(entry.line)
	if !ok {
		return
	}
	ev.RectSelActive = false
	ev.SelActive = false
	ev.CursorLine, ev.CursorPos = entry.line, byteIndexAtRune(text, entry.item.Start)
	ev.centerCursor(false)
}

// centerCursor scrolls the cursor's row to the middle of the window — always,
// or only when it is off screen.
func (ev *EditorView) centerCursor(onlyIfHidden bool) {
	h := ev.Y2 - ev.Y1
	if h > 0 && ev.Engine != nil {
		row, _ := ev.Engine.LogicalToVisual(ev.Li.GetLineOffset(ev.CursorLine) + ev.CursorPos)
		hidden := row < ev.ScrollTopRow || row >= ev.ScrollTopRow+h
		if hidden || !onlyIfHidden {
			maxTop := max(ev.Engine.GetTotalVisualRows()-h, 0)
			ev.ScrollTopRow = min(max(row-h/2, 0), maxTop)
		}
	}
	ev.EnsureCursorVisible()
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// colorerWordAt is the identifier around byte offset pos: letters, digits and
// underscores, as FarEditor::locateFunction takes it. FarColorer's own loop
// drops a word's first character at the start of a line and its last at the
// end; this takes the whole word.
func colorerWordAt(text string, pos int) string {
	runes := []rune(strings.TrimRight(text, "\r\n"))
	cur := runeIndexAtByte(text, pos)
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	if cur >= len(runes) || !isWord(runes[cur]) {
		return ""
	}
	start, end := cur, cur
	for start > 0 && isWord(runes[start-1]) {
		start--
	}
	for end < len(runes) && isWord(runes[end]) {
		end++
	}
	return string(runes[start:end])
}

// pickColorerFunction is FarEditor::locateFunction's choice: the last function
// whose label holds word, ignoring case, preferring one that is not on the
// cursor's line.
func pickColorerFunction(entries []colorerOutlineEntry, word string, cursorLine int) (colorerOutlineEntry, bool) {
	word = strings.ToLower(word)
	var found, onLine *colorerOutlineEntry
	for i := range entries {
		if !strings.Contains(strings.ToLower(entries[i].label), word) {
			continue
		}
		if entries[i].line == cursorLine {
			onLine = &entries[i]
		} else {
			found = &entries[i]
		}
	}
	if found == nil {
		found = onLine
	}
	if found == nil {
		return colorerOutlineEntry{}, false
	}
	return *found, true
}

// ColorerLocateFunction moves the cursor to the function named by the word
// under it, found in the whole file's outline; nothing found says so.
func (ev *EditorView) ColorerLocateFunction() {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok {
		return
	}
	text, _ := ev.lineTextForHighlight(ev.CursorLine)
	word := colorerWordAt(text, ev.CursorPos)
	if word == "" {
		colorerNothingFound()
		return
	}
	cursorLine := ev.CursorLine
	ch.buildOutline(false, func(entries []colorerOutlineEntry) {
		if entry, ok := pickColorerFunction(entries, word, cursorLine); ok {
			ev.gotoColorerOutline(entry)
			return
		}
		colorerNothingFound()
	})
}
