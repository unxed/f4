package main

import (
	"os"
	"unicode/utf8"
	"github.com/unxed/f4/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type visualCell struct {
	info       vtui.CharInfo
	byteOffset int // Офсет в байтах от начала логической строки
}

type lineFragment struct {
	cells            []visualCell
	startOffset      int // Абсолютный офсет начала фрагмента
	startByteInLine  int // Байт в логической строке, с которого начался фрагмент
	endByteInLine    int // Байт, на котором фрагмент закончился
}

// EditorView — компонент текстового редактора.
type EditorView struct {
	vtui.ScreenObject
	pt         *piecetable.PieceTable
	li         *piecetable.LineIndex

	ScrollTop     int // Индекс первой видимой логической строки
	ScrollSubLine int // Индекс визуального фрагмента строки ScrollTop
	ScrollLeft    int // Горизонтальный скролл (для WordWrap=false)

	WordWrap         bool
	CursorLine       int // Текущая строка курсора (логическая)
	CursorPos        int // Текущая позиция в строке (в байтах)
	DesiredCursorPos int // "Желаемая" позиция (визуальная колонка)

	selActive        bool
	selAnchorOffset  int // Абсолютный офсет начала выделения

	pasting          bool
	pasteBuffer      []rune

	filePath   string
	done       bool

	lineLengthCache   map[int]int
	lineFragmentCache map[int]map[int][]lineFragment
}

func NewEditorView(pt *piecetable.PieceTable, path string) *EditorView {
	ev := &EditorView{
		pt:       pt,
		li:       piecetable.NewLineIndex(),
		filePath: path,
		WordWrap: true,
	}
	ev.clearCaches()
	ev.li.Rebuild(pt)
	ev.SetCanFocus(true)
	ev.SetFocus(true)
	return ev
}

func (ev *EditorView) clearCaches() {
	ev.lineLengthCache = make(map[int]int)
	ev.lineFragmentCache = make(map[int]map[int][]lineFragment)
}

func (ev *EditorView) Show(scr *vtui.ScreenBuf) {
	ev.ScreenObject.Show(scr)
	ev.DisplayObject(scr)
}

func (ev *EditorView) DisplayObject(scr *vtui.ScreenBuf) {
	if !ev.IsVisible() { return }
	// Оптимизация: во время активной вставки (Bracketed Paste) не обновляем буфер экрана.
	// Это предотвращает тысячи тяжелых операций StringToCharInfo и аллокаций GetRange.
	if ev.pasting { return }

	width := ev.X2 - ev.X1 + 1
	height := ev.Y2 - ev.Y1 + 1

	bgAttr := vtui.Palette[ColCommandLineUserScreen]

	selAttr := vtui.Palette[vtui.ColDialogEditSelected]

	rowsRendered := 0
	for logLineIdx := ev.ScrollTop; logLineIdx < ev.li.LineCount() && rowsRendered < height; logLineIdx++ {
		fragments := ev.getLineFragments(logLineIdx, width)
		lineLen := ev.getLineLength(logLineIdx)

		startFrag := 0
		if logLineIdx == ev.ScrollTop {
			startFrag = ev.ScrollSubLine
		}

		for fIdx := startFrag; fIdx < len(fragments) && rowsRendered < height; fIdx++ {
			currY := ev.Y1 + rowsRendered
			scr.FillRect(ev.X1, currY, ev.X2, currY, ' ', bgAttr)
			
			frag := fragments[fIdx]
			
			// Отрисовка фрагмента с учетом выделения
			for cellIdx, cell := range frag.cells {
				absOffset := frag.startOffset + cell.byteOffset
				if ev.selActive {
					min, max := ev.getSelectionRange()
					if absOffset >= min && absOffset < max {
						cell.info.Attributes = selAttr
					}
				}
				scr.Write(ev.X1+cellIdx, currY, []vtui.CharInfo{cell.info})
			}

			// Если на этом фрагменте стоит курсор — запоминаем визуальные координаты
			if logLineIdx == ev.CursorLine && ev.CursorPos >= frag.startByteInLine && ev.CursorPos < frag.endByteInLine {
				// Рассчитываем X внутри фрагмента
				vx := 0
				for _, c := range frag.cells {
					if c.byteOffset < (ev.CursorPos - frag.startByteInLine) {
						vx++
					}
				}
				scr.SetCursorPos(ev.X1+vx, currY)
				scr.SetCursorVisible(true)
			} else if logLineIdx == ev.CursorLine && ev.CursorPos == lineLen && fIdx == len(fragments)-1 {
				// Курсор в самом конце строки (после последнего символа)
				vx := len(frag.cells)
				if vx < width {
					scr.SetCursorPos(ev.X1+vx, currY)
					scr.SetCursorVisible(true)
				}
			}

			rowsRendered++
		}
	}
}

func (ev *EditorView) ProcessKey(e *vtinput.InputEvent) bool {
	// 1. Обработка Bracketed Paste (события приходят вне KeyDown)
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
				// Инкрементальное обновление вместо тяжелого Rebuild
				ev.li.UpdateAfterInsert(offset, data)
				ev.clearCaches()

				newOffset := offset + len(data)
				ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
				ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
				ev.DesiredCursorPos = ev.CursorPos
				ev.ensureCursorVisible()
			}
		}
		return true
	}

	// 2. Накопление символов в режиме вставки
	if ev.pasting {
		if e.Type == vtinput.KeyEventType && e.KeyDown {
			if e.Char != 0 {
				// Обрабатываем системные переносы внутри вставки
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

	// 3. Обычная обработка клавиш
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
		ev.ScrollSubLine = 0
		ev.clearCaches()
		ev.updateDesiredPos()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_C:
		if ctrl && ev.selActive {
			ev.CopySelection()
			return true
		}

	case vtinput.VK_UP:
		handleNav()
		if ev.WordWrap {
			if !ev.moveCursorVisual(0, -1) {
				if ev.CursorLine > 0 {
					ev.CursorLine--
					frags := ev.getLineFragments(ev.CursorLine, ev.X2-ev.X1+1)
					lastFrag := frags[len(frags)-1]
					// Переход в ту же визуальную колонку на последней подстроке
					targetX := ev.DesiredCursorPos
					if targetX >= len(lastFrag.cells) { targetX = len(lastFrag.cells) - 1 }
					if targetX < 0 { targetX = 0 }
					if len(lastFrag.cells) > 0 {
						ev.CursorPos = lastFrag.startByteInLine + lastFrag.cells[targetX].byteOffset
					} else {
						ev.CursorPos = lastFrag.startByteInLine
					}
				}
			}
		} else {
			if ev.CursorLine > 0 {
				ev.CursorLine--
				ev.updateCursorToDesiredPos()
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DOWN:
		handleNav()
		if ev.WordWrap {
			if !ev.moveCursorVisual(0, 1) {
				if ev.CursorLine < ev.li.LineCount()-1 {
					ev.CursorLine++
					ev.CursorPos = 0
					ev.updateCursorToDesiredPos()
				}
			}
		} else {
			if ev.CursorLine < ev.li.LineCount()-1 {
				ev.CursorLine++
				ev.updateCursorToDesiredPos()
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_LEFT:
		handleNav()
		if ev.CursorPos > 0 {
			// Ищем начало предыдущего UTF-8 символа в строке
			lineStart := ev.li.GetLineOffset(ev.CursorLine)
			data := ev.pt.GetRange(lineStart, ev.CursorPos)
			_, size := utf8.DecodeLastRune(data)
			ev.CursorPos -= size
		} else if ev.CursorLine > 0 {
			ev.CursorLine--
			ev.CursorPos = ev.getLineLength(ev.CursorLine)
		}
		ev.updateDesiredPos()
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
		ev.updateDesiredPos()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_HOME:
		handleNav()
		ev.CursorPos = 0
		ev.updateDesiredPos()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_END:
		handleNav()
		ev.CursorPos = ev.getLineLength(ev.CursorLine)
		ev.updateDesiredPos()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_BACK:
		if ev.selActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset > 0 {
				if ev.CursorPos == 0 {
					// Склеиваем с предыдущей строкой (удаляем \n)
					prevLen := ev.getLineLength(ev.CursorLine - 1)
					ev.pt.Delete(offset-1, 1)
					ev.li.UpdateAfterDelete(offset-1, 1)
					ev.clearCaches()
					ev.CursorLine--
					ev.CursorPos = prevLen
				} else {
					// Удаляем UTF-8 символ перед курсором
					lineStart := ev.li.GetLineOffset(ev.CursorLine)
					lineData := ev.pt.GetRange(lineStart, ev.CursorPos)
					_, size := utf8.DecodeLastRune(lineData)

					ev.pt.Delete(offset-size, size)
					ev.li.UpdateAfterDelete(offset-size, size)
					ev.clearCaches()
					ev.CursorPos -= size
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
				// Удаляем UTF-8 символ под курсором
				peekLen := 4
				if ev.pt.Size()-offset < 4 { peekLen = ev.pt.Size() - offset }
				data := ev.pt.GetRange(offset, peekLen)
				_, size := utf8.DecodeRune(data)

				ev.pt.Delete(offset, size)
				ev.li.UpdateAfterDelete(offset, size)
				ev.clearCaches()
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RETURN:
		if ev.selActive { ev.DeleteSelection() }
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		ev.pt.Insert(offset, []byte("\n"))
		ev.li.UpdateAfterInsert(offset, []byte("\n"))
		ev.clearCaches()
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
		ev.clearCaches()
		ev.CursorPos += len(data)
		ev.DesiredCursorPos = ev.CursorPos
		ev.ensureCursorVisible()
		return true
	}

	return false
}

func (ev *EditorView) ensureCursorVisible() {
	width := ev.X2 - ev.X1 + 1
	height := ev.Y2 - ev.Y1 + 1
	if width <= 0 || height <= 0 { return }

	if !ev.WordWrap {
		// Классический скроллинг для режима без переносов
		ev.ScrollSubLine = 0
		if ev.CursorLine < ev.ScrollTop {
			ev.ScrollTop = ev.CursorLine
		} else if ev.CursorLine >= ev.ScrollTop+height {
			ev.ScrollTop = ev.CursorLine - height + 1
		}
		if ev.CursorPos < ev.ScrollLeft {
			ev.ScrollLeft = ev.CursorPos
		} else if ev.CursorPos >= ev.ScrollLeft+width {
			ev.ScrollLeft = ev.CursorPos - width + 1
		}
		return
	}

	// Скроллинг для Word Wrap: считаем визуальные ряды
	ev.ScrollLeft = 0

	// 1. Находим, на каком визуальном ряду сейчас курсор
	cursorRow := -1
	totalRows := 0

	// Считаем ряды от начала документа до курсора
	for l := 0; l <= ev.CursorLine; l++ {
		frags := ev.getLineFragments(l, width)
		lineLen := ev.getLineLength(l)
		for fIdx, f := range frags {
			onThisFrag := (l == ev.CursorLine && ev.CursorPos >= f.startByteInLine && ev.CursorPos < f.endByteInLine)
			// Краевой случай: курсор в самом конце строки
			if !onThisFrag && l == ev.CursorLine && ev.CursorPos == lineLen && fIdx == len(frags)-1 {
				onThisFrag = true
			}

			if onThisFrag {
				cursorRow = totalRows
				break
			}
			totalRows++
		}
		if cursorRow != -1 {
			break
		}
	}

	// 2. Находим, какой визуальный ряд сейчас самый верхний (ScrollTop + ScrollSubLine)
	startRow := 0
	for l := 0; l < ev.ScrollTop; l++ {
		startRow += len(ev.getLineFragments(l, width))
	}
	startRow += ev.ScrollSubLine

	for {
		// 3. Проверяем видимость
		if cursorRow < startRow {
			// Скроллим вверх по одному визуальному ряду
			if ev.ScrollSubLine > 0 {
				ev.ScrollSubLine--
			} else if ev.ScrollTop > 0 {
				ev.ScrollTop--
				ev.ScrollSubLine = len(ev.getLineFragments(ev.ScrollTop, width)) - 1
			} else { break }
			startRow--
		} else if cursorRow >= startRow+height {
			// Скроллим вниз по одному визуальному ряду
			maxSub := len(ev.getLineFragments(ev.ScrollTop, width)) - 1
			if ev.ScrollSubLine < maxSub {
				ev.ScrollSubLine++
			} else {
				ev.ScrollTop++
				ev.ScrollSubLine = 0
			}
			startRow++
		} else {
			break // Курсор виден
		}
	}
}

func (ev *EditorView) ProcessMouse(e *vtinput.InputEvent) bool { return false }
func (ev *EditorView) ResizeConsole(w, h int) {}
func (ev *EditorView) GetType() vtui.FrameType { return vtui.TypeUser + 2 }
func (ev *EditorView) SetExitCode(c int) { ev.done = true }
func (ev *EditorView) IsDone() bool { return ev.done }
func (ev *EditorView) IsBusy() bool { return ev.pasting }
func (ev *EditorView) getLineLength(line int) int {
	if line < 0 || line >= ev.li.LineCount() {
		return 0
	}
	if length, ok := ev.lineLengthCache[line]; ok {
		return length
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
	
	// Безопасно уменьшаем длину, если в конце есть переносы строк.
	// Сначала проверяем \n, затем (если он был) проверяем стоящий перед ним \r.
	if totalLen > 0 && data[totalLen-1] == '\n' {
		totalLen--
		if totalLen > 0 && data[totalLen-1] == '\r' {
			totalLen--
		}
	}
	ev.lineLengthCache[line] = totalLen
	return totalLen
}

func (ev *EditorView) updateCursorToDesiredPos() {
	lineLen := ev.getLineLength(ev.CursorLine)
	if ev.DesiredCursorPos > lineLen {
		ev.CursorPos = lineLen
	} else {
		ev.CursorPos = ev.DesiredCursorPos
	}
}
func (ev *EditorView) updateDesiredPos() {
	if !ev.WordWrap {
		ev.DesiredCursorPos = ev.CursorPos
		return
	}
	width := ev.X2 - ev.X1 + 1
	frags := ev.getLineFragments(ev.CursorLine, width)
	for _, f := range frags {
		if ev.CursorPos >= f.startByteInLine && ev.CursorPos < f.endByteInLine {
			vx := 0
			for _, c := range f.cells {
				if c.byteOffset < (ev.CursorPos - f.startByteInLine) { vx++ }
			}
			ev.DesiredCursorPos = vx
			return
		}
	}
}

func (ev *EditorView) moveCursorVisual(dx, dy int) bool {
	width := ev.X2 - ev.X1 + 1
	frags := ev.getLineFragments(ev.CursorLine, width)

	currentFragIdx := -1
	for i, f := range frags {
		if ev.CursorPos >= f.startByteInLine && ev.CursorPos < f.endByteInLine {
			currentFragIdx = i
			break
		}
	}
	if currentFragIdx == -1 && ev.CursorPos == ev.getLineLength(ev.CursorLine) {
		currentFragIdx = len(frags) - 1
	}

	newFragIdx := currentFragIdx + dy
	if newFragIdx >= 0 && newFragIdx < len(frags) {
		f := frags[newFragIdx]
		// Пытаемся сохранить визуальную колонку (DesiredCursorPos)
		targetX := ev.DesiredCursorPos
		if targetX >= len(f.cells) { targetX = len(f.cells) - 1 }
		if targetX < 0 { targetX = 0 }

		if len(f.cells) > 0 {
			ev.CursorPos = f.startByteInLine + f.cells[targetX].byteOffset
		} else {
			ev.CursorPos = f.startByteInLine
		}
		return true
	}
	return false
}

func (ev *EditorView) getLineFragments(lineIdx, width int) []lineFragment {
	if lineIdx < 0 || lineIdx >= ev.li.LineCount() || width <= 0 {
		return nil
	}

	if wCache, ok := ev.lineFragmentCache[width]; ok {
		if frags, ok := wCache[lineIdx]; ok {
			return frags
		}
	}

	startOffset := ev.li.GetLineOffset(lineIdx)
	endOffset := ev.pt.Size()
	if lineIdx+1 < ev.li.LineCount() {
		endOffset = ev.li.GetLineOffset(lineIdx + 1)
	}

	lineData := ev.pt.GetRange(startOffset, endOffset-startOffset)
	realLen := len(lineData)
	if realLen > 0 && lineData[realLen-1] == '\n' {
		realLen--
		if realLen > 0 && lineData[realLen-1] == '\r' { realLen-- }
	}

	lineStr := string(lineData[:realLen])
	bgAttr := vtui.Palette[ColCommandLineUserScreen]
	cells := vtui.StringToCharInfo(lineStr, bgAttr)

	if !ev.WordWrap {
		vCells := make([]visualCell, len(cells))
		currByte := 0
		for i, c := range cells {
			vCells[i] = visualCell{info: c, byteOffset: currByte}
			if c.Char != vtui.WideCharFiller {
				currByte += len(string(rune(c.Char)))
			}
		}
		return []lineFragment{{
			cells: vCells,
			startOffset: startOffset,
			startByteInLine: 0,
			endByteInLine: realLen + 1,
		}}
	}

	var fragments []lineFragment
	currByte := 0
	for i := 0; i < len(cells); i += width {
		end := i + width
		if end > len(cells) { end = len(cells) }

		fCells := make([]visualCell, 0, width)
		fStartByte := currByte

		// Если следующий символ — WideCharFiller, значит текущий фрагмент
		// должен закончиться раньше, чтобы не разрывать широкий символ.
		actualEnd := end
		if actualEnd < len(cells) && cells[actualEnd].Char == vtui.WideCharFiller {
			actualEnd--
		}

		for j := i; j < actualEnd; j++ {
		fCells = append(fCells, visualCell{info: cells[j], byteOffset: currByte - fStartByte})
			if cells[j].Char != vtui.WideCharFiller {
				currByte += len(string(rune(cells[j].Char)))
			}
		}

		// Защита от бесконечного цикла: если фрагмент пустой (actualEnd <= i),
		// значит ширина окна меньше ширины символа. В этом случае забираем
		// хотя бы один физический символ (даже если он широкий), чтобы сдвинуться.
		if actualEnd <= i && i < len(cells) {
			actualEnd = i + 1
			if actualEnd < len(cells) && cells[actualEnd].Char == vtui.WideCharFiller {
				actualEnd++ // Забираем широкого целиком
			}
			// Повторяем наполнение для этого спец-случая
			fCells = nil
			for j := i; j < actualEnd && j < len(cells); j++ {
				fCells = append(fCells, visualCell{info: cells[j], byteOffset: currByte - fStartByte})
				if cells[j].Char != vtui.WideCharFiller {
					currByte += len(string(rune(cells[j].Char)))
				}
			}
		}

		fragments = append(fragments, lineFragment{
			cells: fCells,
			startOffset: startOffset + fStartByte,
			startByteInLine: fStartByte,
			endByteInLine: currByte,
		})

		// Переходим к следующему фрагменту
		i = i + (actualEnd - i) - width // Корректировка шага цикла (т.к. цикл делает i += width)
	}

	if len(fragments) == 0 {
		// Для пустой строки создаем один пустой фрагмент
		fragments = append(fragments, lineFragment{
			startOffset: startOffset,
			startByteInLine: 0,
			endByteInLine: 0,
		})
	}

	if ev.lineFragmentCache[width] == nil {
		ev.lineFragmentCache[width] = make(map[int][]lineFragment)
	}
	ev.lineFragmentCache[width][lineIdx] = fragments

	return fragments
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
		// Инкрементальное обновление
		ev.li.UpdateAfterDelete(min, max-min)
		ev.clearCaches()
		ev.selActive = false
		// Обновляем позицию курсора на начало бывшего выделения
		ev.CursorLine = ev.li.GetLineAtOffset(min)
		ev.CursorPos = min - ev.li.GetLineOffset(ev.CursorLine)
	}
}
