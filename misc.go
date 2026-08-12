package main

import (
	"github.com/unxed/vtui"
)

// ScreenRow reads a stretch of one row back out of the screen.
func ScreenRow(scr *vtui.ScreenBuf, y, x1, x2 int) string {
	runes := make([]rune, x2-x1+1)
	for i := range runes {
		cell := scr.GetCell(x1+i, y)
		runes[i] = rune(cell.Char)
		if runes[i] == 0 {
			runes[i] = ' '
		}
	}
	return string(runes)
}
