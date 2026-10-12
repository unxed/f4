package terminal

import "strings"

// maxConsoleReadCells caps one ReadConsoleOutputW call. The call's buffer goes
// through the console's shared heap, which is 64 KB on Windows NT and XP:
// 80 columns by 198 rows of CHAR_INFO (4 bytes each) is already too much there
// (ReactOS reproduces the same CSR limit), and on Windows 7 reads fail from
// about 31 KB. f4 read 200 rows at a time, so on XP every read of an
// 80-column console failed and F3/F4 on the console output showed rows of NUL
// characters instead of the text (Zeroes1, f4#897, item 3). 8000 cells is
// 32000 bytes, under both limits.
const maxConsoleReadCells = 8000

// ConsoleReadRows is how many rows of a console width columns wide one
// ReadConsoleOutputW call may read: as many as fit in maxConsoleReadCells,
// and at least one.
func ConsoleReadRows(width int) int {
	if width <= 0 {
		return 1
	}
	if rows := maxConsoleReadCells / width; rows > 1 {
		return rows
	}
	return 1
}

// consoleRowText turns one row of console cells into text: NUL cells (never
// written, or left by a read that failed) count as blanks, and trailing
// blanks are dropped.
func consoleRowText(chars []uint16) string {
	last := len(chars) - 1
	for last >= 0 && (chars[last] == ' ' || chars[last] == 0) {
		last--
	}
	var sb strings.Builder
	for i := 0; i <= last; i++ {
		c := chars[i]
		if c == 0 {
			c = ' '
		}
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// ConsoleStrip is a run of rows read or written in one console call.
type ConsoleStrip struct{ Row, Rows int }

// ConsoleStrips splits rows rows of a console width columns wide into strips
// of at most ConsoleReadRows(width) rows, in order and without gaps.
func ConsoleStrips(width, rows int) []ConsoleStrip {
	step := ConsoleReadRows(width)
	var strips []ConsoleStrip
	for row := 0; row < rows; row += step {
		strips = append(strips, ConsoleStrip{Row: row, Rows: min(step, rows-row)})
	}
	return strips
}
