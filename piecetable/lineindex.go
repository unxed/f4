package piecetable

import "sort"

// LineIndex stores start offsets of each line.
type LineIndex struct {
	offsets []int
}

// NewLineIndex creates a new empty index.
func NewLineIndex() *LineIndex {
	return &LineIndex{
		offsets: []int{0},
	}
}

// Rebuild completely reconstructs the line index based on PieceTable.
func (li *LineIndex) Rebuild(pt *PieceTable) {
	// Reset index, first line always starts at 0
	li.offsets = []int{0}

	if pt.Size() == 0 {
		return
	}

	absPos := 0
	pt.ForEachRange(func(data []byte) {
		for i, b := range data {
			if b == '\n' {
				// Next line starts immediately after the newline character
				li.offsets = append(li.offsets, absPos+i+1)
			}
		}
		absPos += len(data)
	})
}

// LineCount returns total number of lines.
func (li *LineIndex) LineCount() int {
	return len(li.offsets)
}

// GetLineOffset returns byte offset of the specified line start (0-based).
func (li *LineIndex) GetLineOffset(line int) int {
	if line < 0 || line >= len(li.offsets) {
		return -1
	}
	return li.offsets[line]
}

// GetLineAtOffset returns the line number (0-based) to which specified offset belongs.
// Uses binary search for O(log N) speed.
func (li *LineIndex) GetLineAtOffset(offset int) int {
	if offset <= 0 {
		return 0
	}

	// Search for the first index i where li.offsets[i] > offset
	idx := sort.Search(len(li.offsets), func(i int) bool {
		return li.offsets[i] > offset
	})

	// Line number is idx - 1
	return idx - 1
}

// UpdateAfterInsert incrementally updates the index after data insertion.
func (li *LineIndex) UpdateAfterInsert(offset int, data []byte) {
	lenData := len(data)
	if lenData == 0 {
		return
	}

	// 1. Find the line where insertion occurred
	lineIdx := li.GetLineAtOffset(offset)

	// 2. Search for new line breaks in the inserted fragment
	var newOffsets []int
	currentOffset := offset
	for _, b := range data {
		currentOffset++
		if b == '\n' {
			newOffsets = append(newOffsets, currentOffset)
		}
	}

	// 3. Shift all subsequent offsets
	for i := lineIdx + 1; i < len(li.offsets); i++ {
		li.offsets[i] += lenData
	}

	// 4. Insert new line offsets if any
	if len(newOffsets) > 0 {
		// Create a new slice for insertion
		tail := append(newOffsets, li.offsets[lineIdx+1:]...)
		li.offsets = append(li.offsets[:lineIdx+1], tail...)
	}
}

// UpdateAfterDelete incrementally updates the index after data deletion.
func (li *LineIndex) UpdateAfterDelete(offset, length int) {
	if length == 0 {
		return
	}

	startLine := li.GetLineAtOffset(offset)
	endLine := li.GetLineAtOffset(offset + length)

	// 1. Determine how many lines were removed
	linesRemoved := endLine - startLine

	// 2. Shift all subsequent offsets
	for i := endLine + 1; i < len(li.offsets); i++ {
		li.offsets[i] -= length
	}

	// 3. Remove offsets of "collapsed" lines
	if linesRemoved > 0 {
		li.offsets = append(li.offsets[:startLine+1], li.offsets[endLine+1:]...)
	}
}
