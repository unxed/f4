package panel

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// stickyGroupHeading reuses the cached heading without classifying files while drawing.
func (fp *FileSystemPanel) stickyGroupHeading(top int) int {
	if fp.GroupBy == GroupNone || fp.Table.ViewHeight <= 1 || top < 0 || top >= len(fp.displayRows) {
		return -1
	}
	row := fp.displayRows[top]
	if row.entry < 0 {
		return -1
	}
	return row.heading
}

func (fp *FileSystemPanel) stickyGroupRows(top int) int {
	if fp.stickyGroupHeading(top) >= 0 {
		return 1
	}
	return 0
}

// viewportDisplayRow maps a table cell back to the immutable display sequence.
// The pinned heading consumes one cell; no file is obscured or skipped.
func (fp *FileSystemPanel) viewportDisplayRow(row, column int) int {
	if fp.gridColumnCount() == 1 {
		column = 0
	}
	row += column * max(1, fp.Table.ViewHeight)
	if heading := fp.stickyGroupHeading(fp.Table.TopPos); heading >= 0 {
		if row == fp.Table.TopPos {
			return heading
		}
		row--
	}
	return row
}

func (fp *FileSystemPanel) syncGroupedCursor(visual int) {
	height := max(1, fp.Table.ViewHeight)
	capacity := height * fp.gridColumnCount()
	top := max(0, fp.Table.TopPos)
	if visual < top {
		top = visual
	}
	if visual >= top+capacity-fp.stickyGroupRows(top) {
		top = max(0, visual-capacity+1)
		if visual >= top+capacity-fp.stickyGroupRows(top) {
			top++
		}
	}
	rel := visual - top + fp.stickyGroupRows(top)
	if covered := fp.fastFindBoxHeight() - 1; fp.FastFindMode && height > covered && rel%height >= height-covered {
		top += rel%height - (height - covered - 1)
		top = min(top, visual)
		rel = visual - top + fp.stickyGroupRows(top)
	}
	fp.Table.TopPos = top
	fp.Table.SelectCol = rel / height
	fp.Table.SelectPos = top + rel%height
	fp.Table.SetRowCount(fp.RowCount())
}

func (fp *FileSystemPanel) groupNavigationTarget(key uint16) int {
	idx := fp.GetCursorIndex()
	height := max(1, fp.Table.ViewHeight)
	row, direction := fp.displayOfEntry(idx), 1
	switch key {
	case vtinput.VK_UP:
		return max(0, idx-1)
	case vtinput.VK_DOWN:
		return min(len(fp.Entries)-1, idx+1)
	case vtinput.VK_HOME:
		return 0
	case vtinput.VK_END:
		return len(fp.Entries) - 1
	case vtinput.VK_LEFT:
		row -= height
		direction = -1
	case vtinput.VK_RIGHT:
		row += height
	case vtinput.VK_PRIOR:
		row -= height*fp.gridColumnCount() - fp.stickyGroupRows(fp.Table.TopPos)
		direction = -1
	case vtinput.VK_NEXT:
		row += height*fp.gridColumnCount() - fp.stickyGroupRows(fp.Table.TopPos)
	}
	return fp.nearestDisplayEntry(row, direction)
}

// groupHeadingAt is also used by the frame before synthesizing an Enter event.
func (fp *FileSystemPanel) groupHeadingAt(x, y int) bool {
	if fp.GroupBy == GroupNone || x < fp.Table.X1 || x > fp.Table.X2 {
		return false
	}
	row := y - fp.Table.Y1 - fp.Table.MarginTop
	if row < 0 || row >= fp.Table.ViewHeight {
		return false
	}
	column := 0
	if fp.gridColumnCount() > 1 {
		left := fp.Table.X1
		column = -1
		for i, c := range fp.Table.Columns {
			if x >= left && x < left+c.Width {
				column = i
				break
			}
			left += c.Width + 1
		}
		if column < 0 {
			return false
		}
	}
	row = fp.viewportDisplayRow(fp.Table.TopPos+row, fp.stripeOfColumn(column))
	return row >= 0 && row < len(fp.displayRows) && fp.displayRows[row].entry < 0
}

func (fp *FileSystemPanel) drawGroupHeadings(scr *vtui.ScreenBuf) {
	if fp.GroupBy == GroupNone {
		return
	}
	height := fp.Table.ViewHeight
	left := fp.Table.X1
	stripes := fp.gridColumnCount()
	for column := 0; column < stripes; column++ {
		width := fp.Table.X2 - fp.Table.X1 + 1
		if stripes > 1 {
			width = fp.stripeWidth(column)
		}
		for offset := 0; offset < height; offset++ {
			row := fp.viewportDisplayRow(fp.Table.TopPos+offset, column)
			if row < 0 || row >= len(fp.displayRows) || fp.displayRows[row].entry >= 0 {
				continue
			}
			title := runewidth.Truncate(fp.displayRows[row].title, max(0, width-2), "")
			label := runewidth.Truncate(" "+title+" ", max(0, width), "")
			y := fp.Table.Y1 + fp.Table.MarginTop + offset
			line := strings.Repeat("─", max(0, width))
			scr.Write(left, y, vtui.StringToCharInfo(line, vtui.Palette[fp.Table.ColorBoxIdx]))
			x := left + max(0, width-runewidth.StringWidth(label))/2
			scr.Write(x, y, vtui.StringToCharInfo(label, vtui.Palette[theme.ColPanelColumnTitle]))
		}
		left += width + 1
	}
}
