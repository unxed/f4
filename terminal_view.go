package main

import (
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/unxed/vtui"
	"github.com/unxed/vtui/piecetable"
	"github.com/unxed/vtui/textlayout"
)

// StyleChange фиксирует момент смены атрибутов в байтовом потоке лога.
type StyleChange struct {
	Offset int
	Attr   uint64
}

// TerminalView объединяет классическую сетку CharInfo и бесконечный лог.
type TerminalView struct {
	vtui.ScreenObject
	mu sync.Mutex

	// --- Состояние для ANSI Парсера (Grid) ---
	Lines        [][]vtui.CharInfo
	AltLines     [][]vtui.CharInfo
	UseAltScreen bool

	ScrollTop    int
	ScrollBottom int

	Width   int
	Height  int
	CursorX int
	CursorY int

	// Состояние терминала (сохранение координат)
	savedX, savedY       int
	decSavedX, decSavedY int
	Palette              [16]uint32

	// --- Бесконечный лог (History & Reflow) ---
	pt              *piecetable.PieceTable
	li              *piecetable.LineIndex
	engine          *textlayout.WrapEngine
	styles          []StyleChange
	lastAttr        uint64
	lastLineOffset  int // Смещение начала текущей строки в PieceTable

	// Скроллинг истории (визуальный ряд)
	ScrollTopRow int
}

func NewTerminalView(w, h int) *TerminalView {
	tv := &TerminalView{
		Width:  w,
		Height: h,
	}
	tv.ResetBuffer(w, h)
	return tv
}

func (tv *TerminalView) ResetBuffer(w, h int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// Инициализация PieceTable (только один раз)
	if tv.pt == nil {
		tv.pt = piecetable.New([]byte{})
		tv.li = piecetable.NewLineIndex()
		tv.engine = textlayout.NewWrapEngine(tv.pt, tv.li)
		tv.styles = []StyleChange{{0, DefaultTermAttr}}
		tv.lastAttr = DefaultTermAttr
		tv.lastLineOffset = 0
	}
	tv.engine.SetWidth(w)

	// Создание сеток (Grid)
	makeBuf := func() [][]vtui.CharInfo {
		b := make([][]vtui.CharInfo, h)
		for i := range b {
			b[i] = make([]vtui.CharInfo, w)
			for j := range b[i] {
				b[i][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
			}
		}
		return b
	}

	tv.Lines = makeBuf()
	tv.AltLines = makeBuf()

	// Сброс параметров прокрутки и курсора
	tv.Width, tv.Height = w, h
	tv.ScrollTop = 0
	tv.ScrollBottom = h - 1
	tv.CursorX = 0
	tv.CursorY = h - 1

	// Палитра по умолчанию (ANSI order)
	tv.Palette[0] = far2lPalette[0] // Black
	tv.Palette[1] = far2lPalette[4] // Red
	tv.Palette[2] = far2lPalette[2] // Green
	tv.Palette[3] = far2lPalette[6] // Yellow
	tv.Palette[4] = far2lPalette[1] // Blue
	tv.Palette[5] = far2lPalette[5] // Magenta
	tv.Palette[6] = far2lPalette[3] // Cyan
	tv.Palette[7] = far2lPalette[7] // White
	for i := 0; i < 8; i++ {
		winIdx := []int{0, 4, 2, 6, 1, 5, 3, 7}[i]
		tv.Palette[i+8] = far2lPalette[winIdx+8]
	}
}

func (tv *TerminalView) getBuffer() [][]vtui.CharInfo {
	if tv.UseAltScreen {
		return tv.AltLines
	}
	return tv.Lines
}

func (tv *TerminalView) PutChar(r rune, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// 1. Запись в бесконечный лог (если не AltScreen)
	if !tv.UseAltScreen {
		if r == '\n' {
			offset := tv.pt.Size()
			tv.pt.Insert(offset, []byte("\n"))
			tv.li.UpdateAfterInsert(offset, []byte("\n"))
			tv.lastLineOffset = tv.pt.Size()
			tv.engine.InvalidateFrom(tv.li.LineCount() - 2)
		} else if r >= 0x20 {
			// Если мы в начале строки и в логе уже что-то есть для этой строки —
			// вероятно, это перерисовка промпта оболочкой. Удаляем старое.
			if tv.CursorX == 0 && tv.pt.Size() > tv.lastLineOffset {
				tv.pt.Delete(tv.lastLineOffset, tv.pt.Size()-tv.lastLineOffset)
				tv.li.UpdateAfterDelete(tv.lastLineOffset, tv.pt.Size()-tv.lastLineOffset)
				// Откатываем стили
				for i := len(tv.styles) - 1; i >= 0; i-- {
					if tv.styles[i].Offset > tv.lastLineOffset {
						tv.styles = tv.styles[:i]
					} else {
						tv.lastAttr = tv.styles[i].Attr
						break
					}
				}
			}

			offset := tv.pt.Size()
			if attr != tv.lastAttr {
				tv.styles = append(tv.styles, StyleChange{offset, attr})
				tv.lastAttr = attr
			}
			buf := []byte(string(r))
			tv.pt.Insert(offset, buf)
			tv.li.UpdateAfterInsert(offset, buf)
			tv.engine.InvalidateFrom(tv.li.LineCount() - 1)
		}
	}

	// 2. Обработка в текущей сетке (Grid)
	if r == '\r' {
		tv.CursorX = 0
		return
	}
	if r == '\n' {
		tv.newline()
		return
	}
	if r == '\b' {
		if tv.CursorX > 0 {
			tv.CursorX--
		}
		return
	}
	if r == '\t' {
		tv.CursorX = (tv.CursorX + 8) & ^7
		return
	}
	if r < 0x20 {
		return
	}

	buf := tv.getBuffer()
	if tv.CursorX >= tv.Width {
		tv.newline()
		buf = tv.getBuffer()
	}

	if tv.CursorY >= 0 && tv.CursorY < len(buf) && tv.CursorX >= 0 && tv.CursorX < tv.Width {
		buf[tv.CursorY][tv.CursorX] = vtui.CharInfo{Char: uint64(r), Attributes: attr}
		tv.CursorX++
	}
}

func (tv *TerminalView) newline() {
	tv.CursorX = 0
	tv.CursorY++
	if tv.CursorY > tv.ScrollBottom {
		tv.scrollUp(tv.ScrollTop, tv.ScrollBottom, 1)
		tv.CursorY = tv.ScrollBottom
	}
}

func (tv *TerminalView) scrollUp(top, bottom, n int) {
	buf := tv.getBuffer()
	if top < 0 { top = 0 }
	if bottom >= len(buf) { bottom = len(buf) - 1 }
	if top >= bottom { return }

	for i := 0; i < n; i++ {
		copy(buf[top:bottom], buf[top+1:bottom+1])
		buf[bottom] = make([]vtui.CharInfo, tv.Width)
		for j := range buf[bottom] {
			buf[bottom][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
		}
	}
}

func (tv *TerminalView) SetCursor(x, y int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if x < 0 { x = 0 }
	if x >= tv.Width { x = tv.Width - 1 }
	if y < 0 { y = 0 }
	if y >= tv.Height { y = tv.Height - 1 }
	tv.CursorX, tv.CursorY = x, y
}

func (tv *TerminalView) SaveCursor() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.decSavedX, tv.decSavedY = tv.CursorX, tv.CursorY
}

func (tv *TerminalView) RestoreCursor() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.CursorX, tv.CursorY = tv.decSavedX, tv.decSavedY
}

func (tv *TerminalView) RepeatLastChar(n int, r rune, attr uint64) {
	for i := 0; i < n; i++ {
		tv.PutChar(r, attr)
	}
}

func (tv *TerminalView) EraseCharacter(n int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	buf := tv.getBuffer()
	if tv.CursorY < 0 || tv.CursorY >= len(buf) { return }
	line := buf[tv.CursorY]
	for i := 0; i < n && (tv.CursorX+i) < tv.Width; i++ {
		line[tv.CursorX+i] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) EraseDisplay(mode int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	buf := tv.getBuffer()
	if mode == 2 {
		for i := range buf {
			for j := range buf[i] {
				buf[i][j] = vtui.CharInfo{Char: ' ', Attributes: attr}
			}
		}
	} else if mode == 0 {
		if tv.CursorY >= 0 && tv.CursorY < tv.Height {
			line := buf[tv.CursorY]
			for j := (tv.CursorX); j < tv.Width; j++ {
				if j >= 0 { line[j] = vtui.CharInfo{Char: ' ', Attributes: attr} }
			}
		}
		for i := tv.CursorY + 1; i < tv.Height; i++ {
			if i >= 0 {
				for j := range buf[i] {
					buf[i][j] = vtui.CharInfo{Char: ' ', Attributes: attr}
				}
			}
		}
	}
}

func (tv *TerminalView) EraseLine(mode int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	buf := tv.getBuffer()
	if tv.CursorY < 0 || tv.CursorY >= tv.Height { return }
	line := buf[tv.CursorY]
	start, end := 0, tv.Width
	if mode == 0 {
		start = tv.CursorX
	} else if mode == 1 {
		end = tv.CursorX + 1
	}
	for j := start; j < end; j++ {
		if j >= 0 && j < tv.Width {
			line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
		}
	}
}

func (tv *TerminalView) SetAltScreen(enable bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.UseAltScreen == enable { return }
	if enable {
		tv.savedX, tv.savedY = tv.CursorX, tv.CursorY
		tv.CursorX, tv.CursorY = 0, 0
	} else {
		tv.CursorX, tv.CursorY = tv.savedX, tv.savedY
	}
	tv.UseAltScreen = enable
}

func (tv *TerminalView) getAttrAt(offset int) uint64 {
	idx := sort.Search(len(tv.styles), func(i int) bool {
		return tv.styles[i].Offset > offset
	})
	if idx > 0 {
		return tv.styles[idx-1].Attr
	}
	return DefaultTermAttr
}

func (tv *TerminalView) Show(scr *vtui.ScreenBuf) {
	tv.ScreenObject.Show(scr)
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// Очищаем всю область терминала черным цветом
	scr.FillRect(tv.X1, tv.Y1, tv.X1+tv.Width-1, tv.Y1+tv.Height-1, ' ', DefaultTermAttr)

	if tv.UseAltScreen {
		for y, line := range tv.AltLines {
			scr.Write(tv.X1, tv.Y1+y, line)
		}
		if tv.IsVisible() {
			scr.SetCursorPos(tv.X1+tv.CursorX, tv.Y1+tv.CursorY)
			scr.SetCursorVisible(true)
		}
		return
	}

	tv.engine.SetWidth(tv.Width)
	totalRows := tv.engine.GetTotalVisualRows()

	if totalRows > tv.Height {
		tv.ScrollTopRow = totalRows - tv.Height
	} else {
		tv.ScrollTopRow = 0
	}

	// Рассчитываем вертикальный отступ для короткого лога (прижимаем к низу)
	yPadding := 0
	if totalRows < tv.Height {
		yPadding = tv.Height - totalRows
	}

	rowsRendered := 0
	startLogLine, startFragIdx := tv.engine.GetLogLineAtVisualRow(tv.ScrollTopRow)

	for logIdx := startLogLine; logIdx < tv.li.LineCount() && rowsRendered < tv.Height; logIdx++ {
		frags := tv.engine.GetFragments(logIdx)
		for fIdx, frag := range frags {
			if logIdx == startLogLine && fIdx < startFragIdx {
				continue
			}

			currY := tv.Y1 + yPadding + rowsRendered
			if currY > tv.Y1+tv.Height-1 { break }

			textBytes := tv.pt.GetRange(frag.ByteOffsetStart, frag.ByteOffsetEnd-frag.ByteOffsetStart)
			cells := make([]vtui.CharInfo, 0, frag.VisualWidth)
			currentByte := 0
			for currentByte < len(textBytes) {
				r, size := utf8.DecodeRune(textBytes[currentByte:])
				attr := tv.getAttrAt(frag.ByteOffsetStart + currentByte)
				cells = append(cells, vtui.StringToCharInfo(string(r), attr)...)
				currentByte += size
			}

			scr.Write(tv.X1, currY, cells)
			rowsRendered++
		}
	}

	if tv.IsVisible() {
		lastOffset := tv.pt.Size()
		vRow, vCol := tv.engine.LogicalToVisual(lastOffset)
		visualRowOnScreen := vRow - tv.ScrollTopRow
		if visualRowOnScreen >= 0 && visualRowOnScreen < tv.Height {
			scr.SetCursorPos(tv.X1+vCol, tv.Y1 + yPadding + visualRowOnScreen)
			scr.SetCursorVisible(true)
		}
	}
}

func (tv *TerminalView) Resize(w, h int) {
	if tv.Width == w && tv.Height == h {
		return
	}
	tv.ResetBuffer(w, h)
}
func (tv *TerminalView) IsModal() bool        { return false }
func (tv *TerminalView) RequestFocus() bool   { return true }
func (tv *TerminalView) Close()               {}
func (tv *TerminalView) GetWindowNumber() int { return 0 }
func (tv *TerminalView) SetWindowNumber(n int) {}
