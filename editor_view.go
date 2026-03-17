package main

import (
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

	CursorLine int // Текущая строка курсора (логическая)
	CursorPos  int // Текущая позиция в строке (в байтах)

	done       bool
}

func NewEditorView(pt *piecetable.PieceTable) *EditorView {
	ev := &EditorView{
		pt: pt,
		li: piecetable.NewLineIndex(),
	}
	ev.li.Rebuild(pt)
	ev.SetCanFocus(true)
	ev.SetFocus(true) // Чтобы курсор был виден сразу при открытии
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

	for i := 0; i < height; i++ {
		lineIdx := ev.ScrollTop + i
		currY := ev.Y1 + i

		// Заполняем строку фоном
		scr.FillRect(ev.X1, currY, ev.X2, currY, ' ', bgAttr)

		if lineIdx < ev.li.LineCount() {
			start := ev.li.GetLineOffset(lineIdx)
			end := ev.pt.Size()
			if lineIdx+1 < ev.li.LineCount() {
				end = ev.li.GetLineOffset(lineIdx + 1)
			}

			lineLen := end - start
			if lineLen > 0 {
				data := ev.pt.GetRange(start, lineLen)
				// Убираем \n или \r\n в конце для отрисовки
				if len(data) > 0 && data[len(data)-1] == '\n' {
					data = data[:len(data)-1]
				}
				if len(data) > 0 && data[len(data)-1] == '\r' {
					data = data[:len(data)-1]
				}

				// Превращаем байты в CharInfo (с учетом ScrollLeft)
				lineStr := string(data)
				cells := vtui.StringToCharInfo(lineStr, bgAttr)

				if ev.ScrollLeft < len(cells) {
					visibleCells := cells[ev.ScrollLeft:]
					if len(visibleCells) > width {
						visibleCells = visibleCells[:width]
					}
					scr.Write(ev.X1, currY, visibleCells)
				}
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

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE, vtinput.VK_F10:
		ev.done = true
		return true

	case vtinput.VK_UP:
		if ev.CursorLine > 0 {
			ev.CursorLine--
			ev.ensureCursorVisible()
			return true
		}
	case vtinput.VK_DOWN:
		if ev.CursorLine < ev.li.LineCount()-1 {
			ev.CursorLine++
			ev.ensureCursorVisible()
			return true
		}
	case vtinput.VK_LEFT:
		if ev.CursorPos > 0 {
			ev.CursorPos--
			ev.ensureCursorVisible()
			return true
		}
	case vtinput.VK_RIGHT:
		// Получаем длину текущей строки для ограничения
		start := ev.li.GetLineOffset(ev.CursorLine)
		end := ev.pt.Size()
		if ev.CursorLine+1 < ev.li.LineCount() {
			end = ev.li.GetLineOffset(ev.CursorLine + 1)
		}
		lineLen := end - start
		// Учитываем возможный перевод строки в конце
		if lineLen > 0 {
			data := ev.pt.GetRange(start, lineLen)
			if data[len(data)-1] == '\n' { lineLen-- }
			if lineLen > 0 && data[len(data)-1] == '\r' { lineLen-- }
		}

		if ev.CursorPos < lineLen {
			ev.CursorPos++
			ev.ensureCursorVisible()
		}
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