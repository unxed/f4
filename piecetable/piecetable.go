package piecetable

// BufferType указывает, в каком буфере находится фрагмент текста.
type BufferType int

const (
	Original BufferType = iota
	Add
)

// Piece описывает один фрагмент текста.
type Piece struct {
	Buf    BufferType
	Start  int // Смещение начала фрагмента в соответствующем буфере
	Length int // Длина фрагмента
}

// PieceTable — структура для эффективного редактирования больших текстов.
type PieceTable struct {
	orig   []byte  // Исходный (Read-only) буфер
	add    []byte  // Добавочный (Append-only) буфер
	pieces []Piece // Таблица фрагментов
	size   int     // Текущая логическая длина всего текста
}

// New создает новую таблицу фрагментов из исходного текста.
func New(text []byte) *PieceTable {
	pt := &PieceTable{
		orig: text,
		size: len(text),
	}
	if len(text) > 0 {
		pt.pieces = []Piece{{Buf: Original, Start: 0, Length: len(text)}}
	}
	return pt
}

// Size возвращает текущую логическую длину текста.
func (pt *PieceTable) Size() int {
	return pt.size
}

// offsetToPiece находит индекс фрагмента и смещение внутри него по глобальному смещению.
func (pt *PieceTable) offsetToPiece(offset int) (pieceIdx int, offsetInPiece int) {
	if offset == pt.size {
		return len(pt.pieces), 0
	}
	curr := 0
	for i, p := range pt.pieces {
		if offset < curr+p.Length {
			return i, offset - curr
		}
		curr += p.Length
	}
	return len(pt.pieces), 0
}

// Insert вставляет данные по указанному смещению.
func (pt *PieceTable) Insert(offset int, data []byte) {
	if offset < 0 || offset > pt.size || len(data) == 0 {
		return
	}

	addStart := len(pt.add)
	pt.add = append(pt.add, data...)
	newPiece := Piece{Buf: Add, Start: addStart, Length: len(data)}

	// Если таблица пуста
	if pt.size == 0 {
		pt.pieces = []Piece{newPiece}
		pt.size += len(data)
		return
	}

	// Оптимизация: если вставляем в самый конец и предыдущий кусок тоже Add — сливаем их
	if offset == pt.size && len(pt.pieces) > 0 {
		lastIdx := len(pt.pieces) - 1
		lastP := pt.pieces[lastIdx]
		if lastP.Buf == Add && lastP.Start+lastP.Length == addStart {
			pt.pieces[lastIdx].Length += len(data)
			pt.size += len(data)
			return
		}
		// Иначе просто добавляем новый кусок в конец
		pt.pieces = append(pt.pieces, newPiece)
		pt.size += len(data)
		return
	}

	// Общий случай: вставка в середину
	idx, off := pt.offsetToPiece(offset)
	p := pt.pieces[idx]

	var newPieces []Piece
	newPieces = append(newPieces, pt.pieces[:idx]...)

	if off == 0 {
		// Вставка ровно перед куском
		newPieces = append(newPieces, newPiece, p)
	} else {
		// Разрезаем текущий кусок на два
		left := Piece{Buf: p.Buf, Start: p.Start, Length: off}
		right := Piece{Buf: p.Buf, Start: p.Start + off, Length: p.Length - off}
		newPieces = append(newPieces, left, newPiece, right)
	}

	if idx+1 < len(pt.pieces) {
		newPieces = append(newPieces, pt.pieces[idx+1:]...)
	}

	pt.pieces = newPieces
	pt.size += len(data)
}

// Delete удаляет фрагмент текста заданной длины начиная со смещения.
func (pt *PieceTable) Delete(offset, length int) {
	if offset < 0 || length <= 0 || offset+length > pt.size {
		return
	}

	startIdx, startOff := pt.offsetToPiece(offset)
	endIdx, endOff := pt.offsetToPiece(offset + length)

	var newPieces []Piece
	newPieces = append(newPieces, pt.pieces[:startIdx]...)

	// Остаток левого разрезанного куска
	if startOff > 0 {
		p := pt.pieces[startIdx]
		newPieces = append(newPieces, Piece{Buf: p.Buf, Start: p.Start, Length: startOff})
	}

	// Остаток правого разрезанного куска
	if endIdx < len(pt.pieces) {
		p := pt.pieces[endIdx]
		if endOff < p.Length {
			newPieces = append(newPieces, Piece{Buf: p.Buf, Start: p.Start + endOff, Length: p.Length - endOff})
		}
	}

	// Все куски после endIdx
	if endIdx+1 < len(pt.pieces) {
		newPieces = append(newPieces, pt.pieces[endIdx+1:]...)
	}

	pt.pieces = newPieces
	pt.size -= length
}

// Bytes собирает и возвращает весь текущий текст.
// Примечание: для рендеринга больших файлов в будущем мы напишем методы ReadAt,
// чтобы не выгружать весь буфер в память.
func (pt *PieceTable) Bytes() []byte {
	res := make([]byte, 0, pt.size)
	for _, p := range pt.pieces {
		if p.Buf == Original {
			res = append(res, pt.orig[p.Start:p.Start+p.Length]...)
		} else {
			res = append(res, pt.add[p.Start:p.Start+p.Length]...)
		}
	}
	return res
}

// String возвращает текущий текст в виде строки (удобно для тестов).
func (pt *PieceTable) String() string {
	return string(pt.Bytes())
}// ForEachRange последовательно вызывает функцию для каждого фрагмента данных.
// Это позволяет обрабатывать текст без аллокации единого большого слайса.
func (pt *PieceTable) ForEachRange(fn func(data []byte)) {
	for _, p := range pt.pieces {
		if p.Buf == Original {
			fn(pt.orig[p.Start : p.Start+p.Length])
		} else {
			fn(pt.add[p.Start : p.Start+p.Length])
		}
	}
}
// GetRange возвращает срез байт для указанного диапазона.
func (pt *PieceTable) GetRange(offset, length int) []byte {
	if offset < 0 || length <= 0 || offset+length > pt.size {
		return nil
	}

	res := make([]byte, 0, length)
	remaining := length

	startIdx, offInPiece := pt.offsetToPiece(offset)

	for i := startIdx; i < len(pt.pieces) && remaining > 0; i++ {
		p := pt.pieces[i]

		// Определяем, сколько данных берем из этого куска
		take := p.Length - offInPiece
		if take > remaining {
			take = remaining
		}

		var buf []byte
		if p.Buf == Original {
			buf = pt.orig
		} else {
			buf = pt.add
		}

		res = append(res, buf[p.Start+offInPiece : p.Start+offInPiece+take]...)

		remaining -= take
		offInPiece = 0 // Для последующих кусков читаем с начала
	}

	return res
}
