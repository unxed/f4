package main

import (
	"sync"

	"github.com/unxed/vtui"
)

// TerminalView acts as a buffer for the background shell output.
type TerminalView struct {
	vtui.ScreenObject
	mu      sync.Mutex
	Lines   [][]vtui.CharInfo
	Width   int
	Height  int
	CursorX int
	CursorY int
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
	tv.Lines = make([][]vtui.CharInfo, h)
	for i := range tv.Lines {
		tv.Lines[i] = make([]vtui.CharInfo, w)
		for j := range tv.Lines[i] {
			tv.Lines[i][j] = vtui.CharInfo{Char: ' ', Attributes: vtui.Palette[vtui.ColCommandLineUserScreen]}
		}
	}
	tv.Width, tv.Height = w, h
}

func (tv *TerminalView) PutChar(r rune, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	if r == '\r' {
		tv.CursorX = 0
		return
	}
	if r == '\n' {
		tv.newline()
		return
	}
	if r == '\b' {
		if tv.CursorX > 0 { tv.CursorX-- }
		return
	}
	if r == '\t' {
		tv.CursorX = (tv.CursorX + 8) & ^7
		return
	}

	if tv.CursorX >= tv.Width {
		tv.newline()
	}

	if tv.CursorY >= 0 && tv.CursorY < len(tv.Lines) && tv.CursorX >= 0 && tv.CursorX < tv.Width {
		tv.Lines[tv.CursorY][tv.CursorX] = vtui.CharInfo{Char: uint64(r), Attributes: attr}
		tv.CursorX++
	}
}

func (tv *TerminalView) newline() {
	tv.CursorX = 0
	tv.CursorY++
	if tv.CursorY >= tv.Height {
		// Scroll up
		copy(tv.Lines, tv.Lines[1:])
		tv.Lines[tv.Height-1] = make([]vtui.CharInfo, tv.Width)
		for j := range tv.Lines[tv.Height-1] {
			tv.Lines[tv.Height-1][j] = vtui.CharInfo{Char: ' ', Attributes: vtui.Palette[vtui.ColCommandLineUserScreen]}
		}
		tv.CursorY = tv.Height - 1
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

func (tv *TerminalView) EraseDisplay(mode int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	// Simplified: only mode 2 (clear all) is used often
	for i := range tv.Lines {
		for j := range tv.Lines[i] {
			tv.Lines[i][j] = vtui.CharInfo{Char: ' ', Attributes: attr}
		}
	}
}

func (tv *TerminalView) EraseLine(mode int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.CursorY < 0 || tv.CursorY >= tv.Height { return }
	line := tv.Lines[tv.CursorY]
	for j := range line {
		line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) Show(scr *vtui.ScreenBuf) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	for y, line := range tv.Lines {
		scr.Write(0, y, line)
	}
	if tv.IsVisible() {
		scr.SetCursorPos(tv.CursorX, tv.CursorY)
		scr.SetCursorVisible(true)
	}
}