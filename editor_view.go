package main

import (
	"os"
	"github.com/unxed/f4/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// EditorView — компонент текстового редактора.
type EditorView struct {
	vtui.ScreenObject
	pt         *piecetable.PieceTable
	li         *piecetable.LineIndex

	ScrollTop  int // Первая видимая строка
	ScrollLeft int // Горизонтальный скролл

	CursorLine       int // Текущая строка курсора (логическая)
	CursorPos        int // Текущая позиция в строке (в байтах)
	DesiredCursorPos int // "Желаемая" позиция для навигации вверх/вниз

	selActive        bool
	selAnchorOffset  int // Абсолютный офсет начала выделения

	filePath   string
	done       bool
}

func NewEditorView(pt *piecetable.PieceTable, path string) *EditorView {
	ev := &EditorView{
		pt:       pt,
		li:       piecetable.NewLineIndex(),
		filePath: path,
	}
	ev.li.Rebuild(pt)
	ev.SetCanFocus(true)
	ev.SetFocus(true)
	return ev
}

func (ev *EditorView) Show(scr *vtui.ScreenBuf) {
	ev.ScreenObject.Show(scr)
	ev.DisplayObject(scr)
}

func (ev *EditorView) DisplayObject(scr *vtui.ScreenBuf) {
	if !ev.IsVisible() { return }

	width := ev.X2 - ev.X1 + 1
	height := ev.Y2 - ev.Y1 + 1

	bgAttr := vtui.Palette[ColCommandLineUserScreen]

	selAttr := vtui.Palette[vtui.ColDialogEditSelected]
	cursorOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos

	for i := 0; i < height; i++ {
		lineIdx := ev.ScrollTop + i
		currY := ev.Y1 + i
		scr.FillRect(ev.X1, currY, ev.X2, currY, ' ', bgAttr)

		if lineIdx < ev.li.LineCount() {
			startOffset := ev.li.GetLineOffset(lineIdx)
			endOffset := ev.pt.Size()
			if lineIdx+1 < ev.li.LineCount() {
				endOffset = ev.li.GetLineOffset(lineIdx + 1)
			}

			lineData := ev.pt.GetRange(startOffset, endOffset-startOffset)
			// Визуально обрезаем переносы строк
			drawLen := len(lineData)
			if drawLen > 0 && lineData[drawLen-1] == '\n' { drawLen-- }
			if drawLen > 0 && lineData[drawLen-1] == '\r' { drawLen-- }

			// Рисуем посимвольно для учета выделения
			currX := 0
			byteInLine := 0
			lineStr := string(lineData[:drawLen])
			
			// StringToCharInfo для всей строки, но потом пройдемся по CharInfo
			cells := vtui.StringToCharInfo(lineStr, bgAttr)
			
			for cellIdx, cell := range cells {
				if cellIdx < ev.ScrollLeft {
					if cell.Char != vtui.WideCharFiller {
						byteInLine += len(string(rune(cell.Char)))
					}
					continue
				}
				if currX >= width { break }

				// Проверка выделения для текущего байта
				absOffset := startOffset + byteInLine
				if ev.selActive {
					min, max := ev.selAnchorOffset, cursorOffset
					if min > max { min, max = max, min }
					if absOffset >= min && absOffset < max {
						cell.Attributes = selAttr
					}
				}

				scr.Write(ev.X1+currX, currY, []vtui.CharInfo{cell})
				if cell.Char != vtui.WideCharFiller {
					byteInLine += len(string(rune(cell.Char)))
				}
				currX++
			}
		}
	}

	// Установка курсора
	if ev.IsFocused() {
		scr.SetCursorVisible(true)
		// Упрощенный расчет позиции (без учета wide chars и табов пока)
		vx := ev.CursorPos - ev.ScrollLeft
		vy := ev.CursorLine - ev.ScrollTop

		if vx >= 0 && vx < width && vy >= 0 && vy < height {
			scr.SetCursorPos(ev.X1+vx, ev.Y1+vy)
		} else {
			scr.SetCursorVisible(false)
		}
	}
}

func (ev *EditorView) ProcessKey(e *vtinput.InputEvent) bool {
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

	case vtinput.VK_C:
		if ctrl && ev.selActive {
			ev.CopySelection()
			return true
		}

	case vtinput.VK_UP:
		handleNav()
		if ev.CursorLine > 0 {
			ev.CursorLine--
			ev.updateCursorToDesiredPos()
			ev.ensureCursorVisible()
		}
		return true
	case vtinput.VK_DOWN:
		handleNav()
		if ev.CursorLine < ev.li.LineCount()-1 {
			ev.CursorLine++
			ev.updateCursorToDesiredPos()
			ev.ensureCursorVisible()
		}
		return true
	case vtinput.VK_LEFT:
		handleNav()
		if ev.CursorPos > 0 {
			ev.CursorPos--
		} else if ev.CursorLine > 0 {
			ev.CursorLine--
			ev.CursorPos = ev.getLineLength(ev.CursorLine)
		}
		ev.DesiredCursorPos = ev.CursorPos
		ev.ensureCursorVisible()
		return true
	case vtinput.VK_RIGHT:
		handleNav()
		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos < lineLen {
			ev.CursorPos++
		} else if ev.CursorLine < ev.li.LineCount()-1 {
			ev.CursorLine++
			ev.CursorPos = 0
		}
		ev.DesiredCursorPos = ev.CursorPos
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_BACK:
		if ev.selActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset > 0 {
				if ev.CursorPos == 0 {
					prevLen := ev.getLineLength(ev.CursorLine - 1)
					ev.pt.Delete(offset-1, 1)
					ev.li.UpdateAfterDelete(offset-1, 1)
					ev.CursorLine--
					ev.CursorPos = prevLen
				} else {
					ev.pt.Delete(offset-1, 1)
					ev.li.UpdateAfterDelete(offset-1, 1)
					ev.CursorPos--
				}
			}
		}
		ev.DesiredCursorPos = ev.CursorPos
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DELETE:
		if ev.selActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset < ev.pt.Size() {
				ev.pt.Delete(offset, 1)
				ev.li.UpdateAfterDelete(offset, 1)
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RETURN:
		if ev.selActive { ev.DeleteSelection() }
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		ev.pt.Insert(offset, []byte("\n"))
		ev.li.UpdateAfterInsert(offset, []byte("\n"))
		ev.CursorLine++
		ev.CursorPos = 0
		ev.DesiredCursorPos = 0
		ev.ensureCursorVisible()
		return true
	}

	if e.Char != 0 && ctrl == false {
		if ev.selActive { ev.DeleteSelection() }
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		data := []byte(string(e.Char))
		ev.pt.Insert(offset, data)
		ev.li.UpdateAfterInsert(offset, data)
		ev.CursorPos += len(data)
		ev.DesiredCursorPos = ev.CursorPos
		ev.ensureCursorVisible()
		return true
	}

	return false
}

func (ev *EditorView) ensureCursorVisible() {
	height := ev.Y2 - ev.Y1 + 1
	if ev.CursorLine < ev.ScrollTop {
		ev.ScrollTop = ev.CursorLine
	} else if ev.CursorLine >= ev.ScrollTop+height {
		ev.ScrollTop = ev.CursorLine - height + 1
	}

	width := ev.X2 - ev.X1 + 1
	if ev.CursorPos < ev.ScrollLeft {
		ev.ScrollLeft = ev.CursorPos
	} else if ev.CursorPos >= ev.ScrollLeft+width {
		ev.ScrollLeft = ev.CursorPos - width + 1
	}
}

func (ev *EditorView) ProcessMouse(e *vtinput.InputEvent) bool { return false }
func (ev *EditorView) ResizeConsole(w, h int) {}
func (ev *EditorView) GetType() vtui.FrameType { return vtui.TypeUser + 2 }
func (ev *EditorView) SetExitCode(c int) { ev.done = true }
func (ev *EditorView) IsDone() bool { return ev.done }
func (ev *EditorView) getLineLength(line int) int {
	if line < 0 || line >= ev.li.LineCount() {
		return 0
	}
	start := ev.li.GetLineOffset(line)
	end := ev.pt.Size()
	if line+1 < ev.li.LineCount() {
		end = ev.li.GetLineOffset(line + 1)
	}

	realLen := end - start
	if realLen <= 0 {
		return 0
	}

	data := ev.pt.GetRange(start, realLen)
	// Учитываем переносы строк для ограничения курсора
	if realLen > 0 && data[realLen-1] == '\n' {
		realLen--
	}
	if realLen > 0 && data[realLen-1] == '\r' {
		realLen--
	}
	return realLen
}

func (ev *EditorView) updateCursorToDesiredPos() {
	lineLen := ev.getLineLength(ev.CursorLine)
	if ev.DesiredCursorPos > lineLen {
		ev.CursorPos = lineLen
	} else {
		ev.CursorPos = ev.DesiredCursorPos
	}
}
func (ev *EditorView) SaveToFile() {
	if ev.filePath == "" {
		return
	}
	// Сохранение содержимого PieceTable на диск.
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
		// Для сложного удаления (через несколько строк) проще всего перестроить индекс
		ev.li.Rebuild(ev.pt)
		ev.selActive = false
		// Обновляем позицию курсора на начало бывшего выделения
		ev.CursorLine = ev.li.GetLineAtOffset(min)
		ev.CursorPos = min - ev.li.GetLineOffset(ev.CursorLine)
	}
}
