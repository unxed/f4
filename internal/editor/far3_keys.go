package editor

import (
	"strings"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

// DeleteToLineEnd implements FAR's Ctrl+K/Alt+D command.  The line ending is
// deliberately left in place: the command removes the text on the current
// line, not the line itself.
func (ev *EditorView) DeleteToLineEnd() {
	if ev.SelActive || ev.RectSelActive {
		ev.DeleteSelection()
		return
	}
	start := ev.caretOffset()
	lineEnd := ev.Li.GetLineOffset(ev.CursorLine) + ev.GetLineLength(ev.CursorLine)
	if start >= lineEnd {
		return
	}
	ev.deleteRange(start, lineEnd)
}

// isEditSpace and isWordDiv are far2l's IsSpace and IsWordDiv with the default
// word dividers (viewer.DefaultWordDiv). Spaces are not dividers there.
func isEditSpace(r rune) bool { return r == ' ' || r == '\t' }

func isWordDiv(r rune) bool { return strings.ContainsRune(viewer.DefaultWordDiv, r) }

// wordStartBackward is where far2l's Ctrl+Backspace stops deleting
// (Edit::ProcessKey, KEY_CTRLBS) when the cursor is at rune index pos of line:
// one rune at a time, stopping where spaces meet non-spaces, or before a word
// divider. So the quote in front of a string literal stays (DkmS1953, f4#1729).
func wordStartBackward(line []rune, pos int) int {
	for pos > 0 {
		stop := pos > 1 && isEditSpace(line[pos-1]) != isEditSpace(line[pos-2])
		pos--
		if pos == 0 || stop || isWordDiv(line[pos-1]) {
			break
		}
	}
	return pos
}

// wordEndForward is the rightward counterpart, far2l's KEY_CTRLDEL: the rune
// at pos goes, then the following ones up to a divider or up to the last space
// before a non-space.
func wordEndForward(line []rune, pos int) int {
	for pos < len(line) {
		stop := pos < len(line)-1 && isEditSpace(line[pos]) && !isEditSpace(line[pos+1])
		pos++
		if pos >= len(line) || stop || isWordDiv(line[pos]) {
			break
		}
	}
	return pos
}

// DeleteWordBackward implements FAR's Ctrl+Backspace on the current line
// (wordStartBackward). At the start of a line there is nothing to delete
// here; the caller treats the key as Backspace, which joins the lines, as
// far2l does.
func (ev *EditorView) DeleteWordBackward() {
	if ev.SelActive || ev.RectSelActive {
		ev.DeleteSelection()
		return
	}
	ev.CursorVirtualSpaces = 0
	pos := ev.CursorPos
	if pos <= 0 {
		return
	}
	lineStart := ev.Li.GetLineOffset(ev.CursorLine)
	text, _ := ev.Pt.GetRange(lineStart, pos)
	runes := []rune(string(text))
	keep := wordStartBackward(runes, len(runes))
	ev.deleteRange(lineStart+len(string(runes[:keep])), lineStart+pos)
}

// DeleteWordForward implements FAR's Ctrl+Del/Ctrl+T on the current line
// (wordEndForward). At the end of a line it does nothing; the caller treats
// the key as Del, which joins the lines, as far2l does.
func (ev *EditorView) DeleteWordForward() {
	if ev.SelActive || ev.RectSelActive {
		ev.DeleteSelection()
		return
	}
	lineStart := ev.Li.GetLineOffset(ev.CursorLine)
	lineLen := ev.GetLineLength(ev.CursorLine)
	if ev.CursorVirtualSpaces > 0 || ev.CursorPos >= lineLen {
		return
	}
	text, _ := ev.Pt.GetRange(lineStart+ev.CursorPos, lineLen-ev.CursorPos)
	runes := []rune(string(text))
	end := wordEndForward(runes, 0)
	start := lineStart + ev.CursorPos
	ev.deleteRange(start, start+len(string(runes[:end])))
}

func (ev *EditorView) deleteRange(start, end int) {
	if start < 0 {
		start = 0
	}
	if end > ev.Pt.Size() {
		end = ev.Pt.Size()
	}
	if end <= start {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.Modified = true
	ev.Pt.Delete(start, end-start)
	ev.Li.UpdateAfterDelete(start, end-start)
	line := ev.Li.GetLineAtOffset(start)
	ev.CursorLine = line
	ev.CursorPos = start - ev.Li.GetLineOffset(line)
	ev.CursorVirtualSpaces = 0
	ev.invalidateStates(line)
	ev.Engine.InvalidateFrom(line)
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
}

func (ev *EditorView) ClearSelection() {
	ev.SelActive = false
	ev.RectSelActive = false
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (ev *EditorView) MoveToScreenEdge(bottom bool) {
	height := ev.Y2 - ev.Y1
	if height <= 0 {
		return
	}
	target := ev.ScrollTopRow
	if bottom {
		target += height - 1
	}
	total := ev.Engine.GetTotalVisualRows()
	if total <= 0 {
		return
	}
	if target >= total {
		target = total - 1
	}
	if target < 0 {
		target = 0
	}
	offset := ev.Engine.VisualToLogical(target, ev.DesiredVisualCol)
	ev.CursorLine = ev.Li.GetLineAtOffset(offset)
	ev.CursorPos = offset - ev.Li.GetLineOffset(ev.CursorLine)
	ev.CursorVirtualSpaces = 0
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
}

func (ev *EditorView) AppendSelectionToClipboard() {
	if !ev.SelActive && !ev.RectSelActive {
		return
	}
	old := vtui.GetClipboard()
	ev.CopySelection()
	part := vtui.GetClipboard()
	if part == "" {
		return
	}
	if old != "" && !strings.HasSuffix(old, "\n") {
		old += "\n"
	}
	terminal.SetF4Clipboard(old + part)
}

func (ev *EditorView) CopySelectionToCursor() {
	ev.transferSelectionToCursor(false)
}

func (ev *EditorView) MoveSelectionToCursor() {
	ev.transferSelectionToCursor(true)
}

func (ev *EditorView) transferSelectionToCursor(move bool) {
	if !ev.SelActive || ev.RectSelActive {
		return
	}
	start, end := ev.GetSelectionRange()
	if end <= start {
		return
	}
	dest := ev.caretOffset()
	if dest >= start && dest < end {
		return
	}
	if move && dest == end {
		return
	}
	data, err := ev.Pt.GetRange(start, end-start)
	if err != nil || len(data) == 0 {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.Modified = true
	if move {
		ev.Pt.Delete(start, end-start)
		ev.Li.UpdateAfterDelete(start, end-start)
		if dest > end {
			dest -= end - start
		}
	}
	ev.Pt.Insert(dest, data)
	ev.Li.UpdateAfterInsert(dest, data)
	dest += len(data)
	ev.SelActive = false
	ev.RectSelActive = false
	ev.CursorLine = ev.Li.GetLineAtOffset(dest)
	ev.CursorPos = dest - ev.Li.GetLineOffset(ev.CursorLine)
	ev.CursorVirtualSpaces = 0
	ev.invalidateStates(ev.Li.GetLineAtOffset(min(start, ev.Pt.Size())))
	ev.Engine.InvalidateFrom(ev.Li.GetLineAtOffset(min(start, ev.Pt.Size())))
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
}

// ShiftCurrentOrSelectedLines is the FAR Alt+U/Alt+I operation.  The shared
// indentation implementation already handles a multi-line selection; with no
// block selected FAR applies the same one-indent change to the current line.
func (ev *EditorView) ShiftCurrentOrSelectedLines(left bool) {
	if ev.shiftSelectedLines(left) {
		return
	}
	line := ev.CursorLine
	lineStart := ev.Li.GetLineOffset(line)
	lineLen := ev.GetLineLength(line)
	data, _ := ev.Pt.GetRange(lineStart, min(lineLen, ev.TabSize))
	if left {
		remove := 0
		if len(data) > 0 && data[0] == '\t' {
			remove = 1
		} else {
			for remove < len(data) && remove < ev.TabSize && data[remove] == ' ' {
				remove++
			}
		}
		if remove > 0 {
			ev.deleteRange(lineStart, lineStart+remove)
		}
		return
	}
	indent := []byte("\t")
	if ev.ExpandTabs > 0 {
		indent = []byte(strings.Repeat(" ", ev.TabSize))
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.Modified = true
	ev.Pt.Insert(lineStart, indent)
	ev.Li.UpdateAfterInsert(lineStart, indent)
	ev.CursorPos += len(indent)
	ev.invalidateStates(line)
	ev.Engine.InvalidateFrom(line)
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
}

func (ev *EditorView) ToggleStatusBar() {
	if ev.topBar == nil {
		return
	}
	ev.topBar.SetVisible(!ev.topBar.IsVisible())
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (ev *EditorView) SetEditorBookmark(slot int, save bool) {
	if slot < 0 || slot >= len(ev.editorBookmarks) {
		return
	}
	if save {
		ev.editorBookmarks[slot] = ev.caretOffset()
		ev.editorBookmarkSet[slot] = true
		return
	}
	if !ev.editorBookmarkSet[slot] {
		return
	}
	if ev.AwaitOffset(ev.editorBookmarks[slot]) {
		return
	}
	ev.CursorLine = ev.Li.GetLineAtOffset(ev.editorBookmarks[slot])
	ev.CursorPos = ev.editorBookmarks[slot] - ev.Li.GetLineOffset(ev.CursorLine)
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
}
