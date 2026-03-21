package main

import (
	"os"
	"unicode/utf8"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"github.com/unxed/vtui/piecetable"
	"github.com/unxed/vtui/textlayout"
)

type visualCell struct {
	info       vtui.CharInfo
	byteOffset int // Offset in bytes from the start of the logical line
}

type lineFragment struct {
	cells            []visualCell
	startOffset      int // Absolute offset of the fragment start
	startByteInLine  int // Byte in the logical line where the fragment starts
	endByteInLine    int // Byte where the fragment ends
}

// EditorView is a text editor component.
type EditorView struct {
	vtui.ScreenObject
	pt     *piecetable.PieceTable
	li     *piecetable.LineIndex
	engine *textlayout.WrapEngine

	ScrollTopRow int // Индекс первой видимой ВИЗУАЛЬНОЙ строки
	ScrollLeft   int // Горизонтальный скролл (когда WordWrap=false)

	WordWrap         bool
	CursorLine       int // Текущая логическая строка (для плагинов)
	CursorPos        int // Позиция в байтах (для плагинов)
	DesiredVisualCol int // Колонка, в которую мы хотим попасть при навигации Up/Down

	selActive       bool
	selAnchorOffset int // Абсолютное смещение начала выделения

	pasting     bool
	pasteBuffer []rune
	renderBytes []byte          // Reusable buffer for text data
	renderCells []vtui.CharInfo // Reusable buffer for row rendering

	filePath string
	done     bool
}

func NewEditorView(pt *piecetable.PieceTable, path string) *EditorView {
	li := piecetable.NewLineIndex()
	li.Rebuild(pt)
	ev := &EditorView{
		pt:       pt,
		li:       li,
		engine:   textlayout.NewWrapEngine(pt, li),
		filePath: path,
		WordWrap: true,
	}
	ev.SetCanFocus(true)
	ev.SetFocus(true)
	return ev
}

func (ev *EditorView) clearCaches() {
	ev.engine.InvalidateCache()
}
func (ev *EditorView) ensureEngineWidth() {
	width := ev.X2 - ev.X1 + 1
	if width < 1 {
		width = 1
	}
	ev.engine.SetWidth(width)
	ev.engine.ToggleWrap(ev.WordWrap)
}

func (ev *EditorView) updateDesiredVisualCol() {
	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	_, vCol := ev.engine.LogicalToVisual(curOffset)
	ev.DesiredVisualCol = vCol
}

func (ev *EditorView) Show(scr *vtui.ScreenBuf) {
	ev.ScreenObject.Show(scr)
	ev.DisplayObject(scr)
}

func (ev *EditorView) DisplayObject(scr *vtui.ScreenBuf) {
	if !ev.IsVisible() || ev.pasting {
		return
	}

	ev.ensureEngineWidth()
	height := ev.Y2 - ev.Y1 + 1

	bgAttr := vtui.Palette[ColCommandLineUserScreen]
	selAttr := vtui.Palette[vtui.ColDialogEditSelected]

	// 1. Позиция курсора
	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	curVRow, curVCol := ev.engine.LogicalToVisual(curOffset)

	// 2. Отрисовка
	startLogLine, startFragIdx := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow)
	rowsRendered := 0

	for logIdx := startLogLine; logIdx < ev.li.LineCount(); logIdx++ {
		frags := ev.engine.GetFragments(logIdx)
		baseVRow := ev.engine.GetRowOffset(logIdx)

		for fIdx, frag := range frags {
			if logIdx == startLogLine && fIdx < startFragIdx {
				continue
			}

			absVRow := baseVRow + fIdx
			currY := ev.Y1 + rowsRendered
			scr.FillRect(ev.X1, currY, ev.X2, currY, ' ', bgAttr)

			ev.renderBytes = ev.renderBytes[:0]
			ev.renderBytes = ev.pt.AppendRange(ev.renderBytes, frag.ByteOffsetStart, frag.ByteOffsetEnd-frag.ByteOffsetStart)

			if ev.selActive {
				selMin, selMax := ev.getSelectionRange()
				ev.renderCells = vtui.FillCharInfoWithSelection(ev.renderCells, ev.renderBytes, bgAttr, selAttr, frag.ByteOffsetStart, selMin, selMax)
			} else {
				ev.renderCells = vtui.FillCharInfo(ev.renderCells, ev.renderBytes, bgAttr)
			}

			scr.Write(ev.X1-ev.ScrollLeft, currY, ev.renderCells)

			if absVRow == curVRow {
				scr.SetCursorPos(ev.X1+curVCol-ev.ScrollLeft, currY)
				scr.SetCursorVisible(true)
			}

			rowsRendered++
			if rowsRendered >= height {
				return
			}
		}
	}
}

func (ev *EditorView) ProcessKey(e *vtinput.InputEvent) bool {
	ev.ensureEngineWidth()
	// 1. Processing Bracketed Paste (events arrive outside KeyDown)
	if e.Type == vtinput.PasteEventType {
		if e.PasteStart {
			ev.pasting = true
			ev.pasteBuffer = nil
		} else {
			ev.pasting = false
			if len(ev.pasteBuffer) > 0 {
				if ev.selActive { ev.DeleteSelection() }
				offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
				data := []byte(string(ev.pasteBuffer))
				ev.pt.Insert(offset, data)
				// Incremental update instead of heavy Rebuild
				ev.li.UpdateAfterInsert(offset, data)
				ev.engine.InvalidateFrom(ev.CursorLine)

				newOffset := offset + len(data)
				ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
				ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
			}
		}
		return true
	}

	// 2. Accumulating characters in paste mode
	if ev.pasting {
		if e.Type == vtinput.KeyEventType && e.KeyDown {
			if e.Char != 0 {
				// Handle system line breaks inside the paste
				if e.Char == '\r' || e.Char == '\n' {
					ev.pasteBuffer = append(ev.pasteBuffer, '\n')
				} else {
					ev.pasteBuffer = append(ev.pasteBuffer, e.Char)
				}
			} else if e.VirtualKeyCode == vtinput.VK_RETURN {
				ev.pasteBuffer = append(ev.pasteBuffer, '\n')
			}
		}
		return true
	}

	// 3. Regular key processing
	if !e.KeyDown { return false }

	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0

	handleNav := func() {
		if shift {
			if !ev.selActive {
				ev.selActive = true
				ev.selAnchorOffset = ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			}
		} else {
			ev.selActive = false
		}
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE, vtinput.VK_F10:
		ev.done = true
		return true

	case vtinput.VK_F2:
		ev.SaveToFile()
		return true

	case vtinput.VK_F6:
		ev.WordWrap = !ev.WordWrap
		ev.ScrollLeft = 0
		ev.clearCaches()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_C:
		if ctrl && ev.selActive {
			ev.CopySelection()
			return true
		}

	case vtinput.VK_UP:
		handleNav()
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		if vRow > 0 {
			newOffset := ev.engine.VisualToLogical(vRow-1, ev.DesiredVisualCol)
			ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
			ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DOWN:
		handleNav()
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		newOffset := ev.engine.VisualToLogical(vRow+1, ev.DesiredVisualCol)
		ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
		ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_LEFT:
		handleNav()
		if ev.CursorPos > 0 {
			lineStart := ev.li.GetLineOffset(ev.CursorLine)
			data := ev.pt.GetRange(lineStart, ev.CursorPos)
			_, size := utf8.DecodeLastRune(data)
			ev.CursorPos -= size
		} else if ev.CursorLine > 0 {
			ev.CursorLine--
			ev.CursorPos = ev.getLineLength(ev.CursorLine)
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RIGHT:
		handleNav()
		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos < lineLen {
			lineStart := ev.li.GetLineOffset(ev.CursorLine)
			peekLen := 4
			if lineLen-ev.CursorPos < 4 { peekLen = lineLen - ev.CursorPos }
			data := ev.pt.GetRange(lineStart+ev.CursorPos, peekLen)
			_, size := utf8.DecodeRune(data)
			ev.CursorPos += size
		} else if ev.CursorLine < ev.li.LineCount()-1 {
			ev.CursorLine++
			ev.CursorPos = 0
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_HOME:
		handleNav()
		ev.CursorPos = 0
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_END:
		handleNav()
		ev.CursorPos = ev.getLineLength(ev.CursorLine)
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_BACK:
		if ev.selActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset > 0 {
				if ev.CursorPos == 0 {
					// Merge with the previous line (remove \n)
					prevLen := ev.getLineLength(ev.CursorLine - 1)
					ev.pt.Delete(offset-1, 1)
					ev.li.UpdateAfterDelete(offset-1, 1)
					ev.engine.InvalidateFrom(ev.CursorLine - 1)
					ev.CursorLine--
					ev.CursorPos = prevLen
				} else {
					// Remove the UTF-8 character before the cursor
					lineStart := ev.li.GetLineOffset(ev.CursorLine)
					lineData := ev.pt.GetRange(lineStart, ev.CursorPos)
					_, size := utf8.DecodeLastRune(lineData)

					ev.pt.Delete(offset-size, size)
					ev.li.UpdateAfterDelete(offset-size, size)
					ev.engine.InvalidateFrom(ev.CursorLine)
					ev.CursorPos -= size
				}
			}
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DELETE:
		if ev.selActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset < ev.pt.Size() {
				// Remove the UTF-8 character under the cursor
				peekLen := 4
				if ev.pt.Size()-offset < 4 { peekLen = ev.pt.Size() - offset }
				data := ev.pt.GetRange(offset, peekLen)
				_, size := utf8.DecodeRune(data)

				ev.pt.Delete(offset, size)
				ev.li.UpdateAfterDelete(offset, size)
				ev.engine.InvalidateFrom(ev.CursorLine)
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RETURN:
		if ev.selActive {
			ev.DeleteSelection()
		}
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		ev.pt.Insert(offset, []byte("\n"))
		ev.li.UpdateAfterInsert(offset, []byte("\n"))
		ev.engine.InvalidateFrom(ev.CursorLine)
		ev.CursorLine++
		ev.CursorPos = 0
		ev.DesiredVisualCol = 0
		ev.ensureCursorVisible()
		return true
	}

	if e.Char != 0 && ctrl == false {
		if ev.selActive {
			ev.DeleteSelection()
		}
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		data := []byte(string(e.Char))
		ev.pt.Insert(offset, data)
		ev.li.UpdateAfterInsert(offset, data)
		ev.engine.InvalidateFrom(ev.CursorLine)
		ev.CursorPos += len(data)
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true
	}

	return false
}

func (ev *EditorView) ensureCursorVisible() {
	width := ev.X2 - ev.X1 + 1
	height := ev.Y2 - ev.Y1 + 1
	if width <= 0 || height <= 0 { return }

	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	vRow, vCol := ev.engine.LogicalToVisual(curOffset)

	// 1. Вертикальный скролл
	if vRow < ev.ScrollTopRow {
		ev.ScrollTopRow = vRow
	} else if vRow >= ev.ScrollTopRow + height {
		ev.ScrollTopRow = vRow - height + 1
	}

	// 2. Горизонтальный скролл (только если WordWrap выключен)
	if !ev.WordWrap {
		if vCol < ev.ScrollLeft {
			ev.ScrollLeft = vCol
		} else if vCol >= ev.ScrollLeft+width {
			ev.ScrollLeft = vCol - width + 1
		}
	} else {
		ev.ScrollLeft = 0
	}
}

func (ev *EditorView) ProcessMouse(e *vtinput.InputEvent) bool { return false }

func (ev *EditorView) SetPosition(x1, y1, x2, y2 int) {
	ev.ScreenObject.SetPosition(x1, y1, x2, y2)
	ev.ensureEngineWidth()
	ev.ensureCursorVisible()
}

func (ev *EditorView) ResizeConsole(w, h int) {
	// Редактор в f4 обычно занимает всё пространство кроме статус-бара (h-3)
	ev.SetPosition(0, 0, w-1, h-3)
}

func (ev *EditorView) GetType() vtui.FrameType { return vtui.TypeUser + 2 }
func (ev *EditorView) SetExitCode(c int) { ev.done = true }
func (ev *EditorView) IsDone() bool { return ev.done }
func (ev *EditorView) IsBusy() bool { return ev.pasting }
func (ev *EditorView) IsModal() bool { return false }
func (ev *EditorView) GetWindowNumber() int { return 0 }
func (ev *EditorView) SetWindowNumber(n int) {}
func (ev *EditorView) RequestFocus() bool { return true }
func (ev *EditorView) Close() { ev.done = true }
func (ev *EditorView) getLineLength(line int) int {
	if line < 0 || line >= ev.li.LineCount() {
		return 0
	}
	start := ev.li.GetLineOffset(line)
	end := ev.pt.Size()
	if line+1 < ev.li.LineCount() {
		end = ev.li.GetLineOffset(line + 1)
	}

	totalLen := end - start
	if totalLen <= 0 {
		return 0
	}

	data := ev.pt.GetRange(start, totalLen)

	// Safely decrease length if there are line breaks at the end.
	// First check for \n, then (if present) check for \r before it.
	if totalLen > 0 && data[totalLen-1] == '\n' {
		totalLen--
		if totalLen > 0 && data[totalLen-1] == '\r' {
			totalLen--
		}
	}
	return totalLen
}

func (ev *EditorView) SaveToFile() {
	if ev.filePath == "" {
		return
	}
	// Saving PieceTable content to disk.
	err := os.WriteFile(ev.filePath, ev.pt.Bytes(), 0644)
	if err != nil {
		vtui.DebugLog("EDITOR: Failed to save file: %v", err)
	} else {
		vtui.DebugLog("EDITOR: Saved file %s", ev.filePath)
	}
}
func (ev *EditorView) getSelectionRange() (int, int) {
	if !ev.selActive { return 0, 0 }
	cursorOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	min, max := ev.selAnchorOffset, cursorOffset
	if min > max { min, max = max, min }
	return min, max
}

func (ev *EditorView) CopySelection() {
	min, max := ev.getSelectionRange()
	if max > min {
		data := ev.pt.GetRange(min, max-min)
		vtui.SetClipboard(string(data))
		vtui.DebugLog("EDITOR: Copied %d bytes to clipboard", max-min)
	}
}

func (ev *EditorView) DeleteSelection() {
	min, max := ev.getSelectionRange()
	if max > min {
		ev.pt.Delete(min, max-min)
		// Incremental update
		ev.li.UpdateAfterDelete(min, max-min)
		ev.clearCaches()
		ev.selActive = false
		// Update cursor position to the start of the former selection
		ev.CursorLine = ev.li.GetLineAtOffset(min)
		ev.CursorPos = min - ev.li.GetLineOffset(ev.CursorLine)
	}
}
