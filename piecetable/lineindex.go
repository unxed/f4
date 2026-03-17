package piecetable

import "sort"

// LineIndex хранит смещения начала каждой строки.
type LineIndex struct {
	offsets []int
}

// NewLineIndex создает новый пустой индекс.
func NewLineIndex() *LineIndex {
	return &LineIndex{
		offsets: []int{0},
	}
}

// Rebuild полностью перестраивает индекс строк на основе PieceTable.
func (li *LineIndex) Rebuild(pt *PieceTable) {
	// Сбрасываем индекс, первая строка всегда начинается с 0
	li.offsets = []int{0}

	if pt.Size() == 0 {
		return
	}

	absPos := 0
	pt.ForEachRange(func(data []byte) {
		for i, b := range data {
			if b == '\n' {
				// Следующая строка начинается сразу за символом переноса
				li.offsets = append(li.offsets, absPos+i+1)
			}
		}
		absPos += len(data)
	})
}

// LineCount возвращает общее количество строк.
func (li *LineIndex) LineCount() int {
	return len(li.offsets)
}

// GetLineOffset возвращает байтовое смещение начала указанной строки (0-based).
func (li *LineIndex) GetLineOffset(line int) int {
	if line < 0 || line >= len(li.offsets) {
		return -1
	}
	return li.offsets[line]
}

// GetLineAtOffset возвращает номер строки (0-based), которой принадлежит указанное смещение.
// Использует бинарный поиск для скорости O(log N).
func (li *LineIndex) GetLineAtOffset(offset int) int {
	if offset <= 0 {
		return 0
	}

	// Поиск первого индекса i, для которого li.offsets[i] > offset
	idx := sort.Search(len(li.offsets), func(i int) bool {
		return li.offsets[i] > offset
	})

	// Номер строки — это idx - 1
	return idx - 1
}