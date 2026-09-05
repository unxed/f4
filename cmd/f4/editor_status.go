package main

import (
	"fmt"
	"strconv"

	"github.com/unxed/f4/vfs"
)

// editorStatusPosition returns the user-facing logical position. CursorPos is
// kept as a byte offset for editor internals, while the status bar reports a
// one-based visual column so tabs, wide characters, and bidi text match the
// caret the user sees.
func (ev *EditorView) editorStatusPosition() (line, totalLines, column, lineWidth int) {
	line = ev.CursorLine + 1
	if line < 1 {
		line = 1
	}

	totalLines = 1
	if ev.li != nil {
		totalLines = ev.li.LineCount()
		if totalLines < 1 {
			totalLines = 1
		}
	}
	// During lazy indexing the cursor can already point beyond the prefix that
	// the line index has seen. Keep the status internally consistent until the
	// index catches up instead of rendering a denominator smaller than Ln.
	if totalLines < line {
		totalLines = line
	}
	lineWidth = len(strconv.Itoa(totalLines))

	column = ev.CursorPos + 1
	if column < 1 {
		column = 1
	}
	if ev.HexMode || ev.DecodeMode || ev.engine == nil || ev.li == nil ||
		ev.CursorLine < 0 || ev.CursorLine >= ev.li.LineCount() {
		return line, totalLines, column, lineWidth
	}

	lineStart := ev.li.GetLineOffset(ev.CursorLine)
	if lineStart < 0 {
		return line, totalLines, column, lineWidth
	}
	pos := ev.CursorPos
	lineLen := ev.getLineLength(ev.CursorLine)
	if pos < 0 {
		pos = 0
	}
	if pos > lineLen {
		pos = lineLen
	}
	_, visualColumn := ev.engine.LogicalToVisual(lineStart + pos)
	column = visualColumn + ev.CursorVirtualSpaces + 1
	if column < 1 {
		column = 1
	}
	return line, totalLines, column, lineWidth
}

func (ev *EditorView) editorStatusPositionText() string {
	line, totalLines, column, lineWidth := ev.editorStatusPosition()
	// The current line, total line count, and column share a width derived
	// from the largest line number. This keeps the status fields stationary as
	// the cursor moves through a file; wider columns expand only when needed.
	columnWidth := lineWidth
	if digits := len(strconv.Itoa(column)); digits > columnWidth {
		columnWidth = digits
	}
	return fmt.Sprintf("Ln %*d/%*d Col %*d", lineWidth, line, lineWidth, totalLines, columnWidth, column)
}

func (ev *EditorView) editorStatusPrefix() string {
	marker := " "
	if ev.modified {
		marker = "*"
	}
	return fmt.Sprintf("%s %s │ ", marker, vfs.DisplayCodepageName(ev.Codepage))
}

func (ev *EditorView) editorStatusText() string {
	prefix := ev.editorStatusPrefix()
	if ev.colorerIndexing {
		percent := 0
		if ev.colorerTotal > 0 {
			percent = ev.colorerProgress * 100 / ev.colorerTotal
			if percent > 100 {
				percent = 100
			}
		}
		return fmt.Sprintf("%sColorer %d%% (Esc) │ %s     ", prefix, percent, ev.editorStatusPositionText())
	}
	if st := ev.IndexState(); st.Phase == IndexScanning {
		return fmt.Sprintf("%s%s %d%% (Esc) │ %s     ", prefix, Msg("Editor.Indexing"), st.Percent(), ev.editorStatusPositionText())
	}

	if ev.DecodeMode {
		absPos := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		return fmt.Sprintf("%s%s │ 0x%08X     ", prefix, disasmModeLabel(ev.disasmMode()), absPos)
	}
	if ev.HexMode {
		absPos := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		return fmt.Sprintf("%sHex │ 0x%08X     ", prefix, absPos)
	}
	return fmt.Sprintf("%s%s     ", prefix, ev.editorStatusPositionText())
}
