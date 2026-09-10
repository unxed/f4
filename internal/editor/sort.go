package editor

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

var errSortIndexIncomplete = errors.New("the line index is not complete")

type sortableLine struct {
	raw        []byte
	key        string
	terminated bool
}

// splitSortableLines keeps each line terminator attached to its line. This
// preserves mixed line endings while sorting by the line content only.
func splitSortableLines(data []byte) []sortableLine {
	if len(data) == 0 {
		return nil
	}

	lines := make([]sortableLine, 0, bytes.Count(data, []byte{'\n'}))
	for start := 0; start < len(data); {
		end := len(data)
		if newline := bytes.IndexByte(data[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}

		contentEnd := end
		terminated := false
		if end > start && data[end-1] == '\n' {
			terminated = true
			contentEnd--
			if contentEnd > start && data[contentEnd-1] == '\r' {
				contentEnd--
			}
		}
		lines = append(lines, sortableLine{
			raw:        append([]byte(nil), data[start:end]...),
			key:        string(data[start:contentEnd]),
			terminated: terminated,
		})
		start = end
	}
	return lines
}

func sortedLinesData(data []byte, ascending, caseSensitive bool) []byte {
	lines := splitSortableLines(data)
	if len(lines) < 2 {
		return append([]byte(nil), data...)
	}
	unterminatedLine := -1
	if !lines[len(lines)-1].terminated {
		unterminatedLine = len(lines) - 1
	}
	if !caseSensitive {
		for i := range lines {
			lines[i].key = strings.ToLower(lines[i].key)
		}
	}

	sort.SliceStable(lines, func(i, j int) bool {
		if ascending {
			return lines[i].key < lines[j].key
		}
		return lines[i].key > lines[j].key
	})

	// A missing final terminator belongs to the file, not to the line that
	// happened to be last before sorting. Transfer the final sorted line's
	// terminator to the unterminated line when that line moved, so the lines
	// stay separate while the file keeps its original no-newline-at-EOF state.
	if unterminatedLine >= 0 {
		for i := range lines {
			if !lines[i].terminated {
				unterminatedLine = i
				break
			}
		}
		if unterminatedLine != len(lines)-1 {
			last := &lines[len(lines)-1]
			term := trailingLineTerminator(last.raw)
			if len(term) > 0 {
				lines[unterminatedLine].raw = append(lines[unterminatedLine].raw, term...)
				last.raw = last.raw[:len(last.raw)-len(term)]
			}
		}
	}

	result := make([]byte, 0, len(data))
	for _, line := range lines {
		result = append(result, line.raw...)
	}
	return result
}

// SortLines sorts the selected logical lines, or the complete file when no
// selection is active. The edit is kept as one undo step and does not change
// the line endings or the selection's byte range. The cursor stays on the same
// logical line and column, like MoveLines does.
func (ev *EditorView) SortLines(ascending, caseSensitive bool) error {
	if ev.Pt == nil || ev.Pt.Size() == 0 {
		return nil
	}

	hasSelection := ev.SelActive || ev.RectSelActive
	first, last := 0, 0
	if hasSelection {
		if ev.SelActive {
			cursorOffset := ev.Li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			maxOffset := cursorOffset
			if ev.SelAnchorOffset > maxOffset {
				maxOffset = ev.SelAnchorOffset
			}
			if !ev.IndexIsComplete() {
				ev.CancelIndexing()
				indexOffset := maxOffset
				if indexOffset >= ev.Pt.Size() {
					indexOffset = ev.Pt.Size() - 1
				}
				if !ev.ensureIndexedTo(indexOffset) {
					return errSortIndexIncomplete
				}
			}
		}
		first, last = ev.selectedLineSpan()
		if first < 0 || last < first {
			return nil
		}
		ev.EnsureIndexedToLine(last + 1)
		if last >= ev.Li.LineCount() {
			return errSortIndexIncomplete
		}
	}

	start := 0
	end := ev.Pt.Size()
	if hasSelection {
		start = ev.Li.GetLineOffset(first)
		if last+1 < ev.Li.LineCount() {
			end = ev.Li.GetLineOffset(last + 1)
		}
	}
	if start < 0 || end <= start || end > ev.Pt.Size() {
		return nil
	}

	block, err := ev.Pt.GetRange(start, end-start)
	if err != nil {
		return fmt.Errorf("read lines: %w", err)
	}
	replacement := sortedLinesData(block, ascending, caseSensitive)
	if bytes.Equal(block, replacement) {
		return nil
	}

	// Sorting does not change the size of the block, so the selection's byte
	// range remains valid. Keep the cursor's logical line and column instead of
	// its absolute byte offset: otherwise different line lengths would make the
	// caret jump to a different column after a sort.
	cursorLine := ev.CursorLine
	cursorPos := ev.CursorPos
	selectionAnchor := ev.SelAnchorOffset
	selectionWasActive := ev.SelActive
	rectSelectionWasActive := ev.RectSelActive
	rectStartLine, rectStartCol := ev.rectSelStartLine, ev.rectSelStartCol

	ev.replaceRange(start, end, replacement)

	if cursorLine >= ev.Li.LineCount() {
		cursorLine = ev.Li.LineCount() - 1
	}
	ev.CursorLine = cursorLine
	ev.CursorPos = min(cursorPos, ev.GetLineLength(cursorLine))
	if selectionWasActive {
		ev.SelActive = true
		ev.SelAnchorOffset = selectionAnchor
	}
	if rectSelectionWasActive {
		ev.RectSelActive = true
		ev.rectSelStartLine = rectStartLine
		ev.rectSelStartCol = rectStartCol
	}
	ev.updateDesiredVisualCol()
	ev.EnsureCursorVisible()
	return nil
}

// ShowSortDialog asks for the two sorting options before applying the edit.
func (ev *EditorView) ShowSortDialog() {
	if vtui.FrameManager == nil {
		return
	}

	const (
		dialogWidth  = 52
		dialogHeight = 12
	)
	dlg := vtui.NewCenteredDialog(dialogWidth, dialogHeight, i18n.Msg("Editor.Sort.Title"))
	dlg.ShowClose = true

	directionLabel := vtui.NewText(0, 0, i18n.Msg("Editor.Sort.Direction"), 0)
	direction := vtui.NewRadioGroup(0, 0, 1, []string{
		i18n.Msg("Editor.Sort.Ascending"),
		i18n.Msg("Editor.Sort.Descending"),
	})
	caseSensitive := vtui.NewCheckbox(0, 0, i18n.Msg("Editor.Sort.CaseSensitive"), false)
	caseSensitive.State = 1
	btnSort := vtui.NewButton(0, 0, i18n.Msg("Editor.Sort.Apply"))
	btnSort.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(directionLabel)
	dlg.AddItem(direction)
	dlg.AddItem(caseSensitive)
	dlg.AddItem(btnSort)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dialogWidth-4, dialogHeight-4)
	vbox.Add(directionLabel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(direction, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(caseSensitive, vtui.Margins{Top: 1}, vtui.AlignLeft)

	hbox := vtui.NewHBoxLayout(0, 0, dialogWidth-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnSort, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	dlg.SetFocusedItem(direction)
	btnCancel.OnClick = func() { dlg.Close() }
	btnSort.OnClick = func() {
		err := ev.SortLines(direction.Selected == 0, caseSensitive.State == 1)
		dlg.Close()
		if err != nil {
			vtui.ShowMessage(i18n.Msg("Editor.Sort.Title"), err.Error(), []string{i18n.Msg("vtui.Ok")})
		}
	}
	vtui.FrameManager.Push(dlg)
}
