package main

import (
	"sync"

	"github.com/unxed/vtui"
)

// TerminalView acts as a buffer for the background shell output.
type TerminalView struct {
	vtui.ScreenObject
	mu           sync.Mutex
	Lines        [][]vtui.CharInfo
	AltLines     [][]vtui.CharInfo
	UseAltScreen bool

	ScrollTop    int
	ScrollBottom int

	Width   int
	Height  int
	CursorX int
	CursorY int

	// Saved state for main screen
	savedX, savedY int
}

func NewTerminalView(w, h int) *TerminalView {
	tv := &TerminalView{
		Width:  w,
		Height: h,
	}
	tv.ResetBuffer(w, h)
	return tv
}

func (tv *TerminalView) Resize(w, h int) {
	if tv.Width == w && tv.Height == h {
		return
	}
	tv.ResetBuffer(w, h)
}

func (tv *TerminalView) ResetBuffer(w, h int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	makeBuf := func() [][]vtui.CharInfo {
		b := make([][]vtui.CharInfo, h)
		for i := range b {
			b[i] = make([]vtui.CharInfo, w)
			for j := range b[i] {
				b[i][j] = vtui.CharInfo{Char: ' ', Attributes: vtui.Palette[ColCommandLineUserScreen]}
			}
		}
		return b
	}

	tv.Lines = makeBuf()
	tv.AltLines = makeBuf()
	tv.Width, tv.Height = w, h
	tv.ScrollTop = 0
	tv.ScrollBottom = h - 1
	tv.CursorX = 0
	tv.CursorY = h - 1
}

func (tv *TerminalView) SetAltScreen(enable bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.UseAltScreen == enable {
		return
	}
	if enable {
		tv.savedX, tv.savedY = tv.CursorX, tv.CursorY
		tv.CursorX, tv.CursorY = 0, 0
	} else {
		tv.CursorX, tv.CursorY = tv.savedX, tv.savedY
	}
	tv.UseAltScreen = enable
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
		// newline might have switched buffer context if it scrolled
		buf = tv.getBuffer()
	}

	if tv.CursorY >= 0 && tv.CursorY < len(buf) && tv.CursorX >= 0 && tv.CursorX < tv.Width {
		buf[tv.CursorY][tv.CursorX] = vtui.CharInfo{Char: uint64(r), Attributes: attr}
		tv.CursorX++
	}
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
	if tv.CursorY < 0 || tv.CursorY >= len(buf) {
		return
	}
	line := buf[tv.CursorY]
	for i := 0; i < n && (tv.CursorX+i) < tv.Width; i++ {
		line[tv.CursorX+i] = vtui.CharInfo{Char: ' ', Attributes: attr}
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
		// Shift lines within the region
		copy(buf[top:bottom], buf[top+1:bottom+1])
		// Clear the last line of the region
		buf[bottom] = make([]vtui.CharInfo, tv.Width)
		for j := range buf[bottom] {
			buf[bottom][j] = vtui.CharInfo{Char: ' ', Attributes: vtui.Palette[ColCommandLineUserScreen]}
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
			start := tv.CursorX
			if start < 0 {
				start = 0
			}
			for j := start; j < tv.Width; j++ {
				line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
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
	if tv.CursorY < 0 || tv.CursorY >= tv.Height {
		return
	}
	buf := tv.getBuffer()
	line := buf[tv.CursorY]
	start, end := 0, tv.Width
	if mode == 0 {
		start = tv.CursorX
		if start < 0 { start = 0 }
		if start > tv.Width { start = tv.Width }
	} else if mode == 1 {
		end = tv.CursorX + 1
		if end > tv.Width { end = tv.Width }
	}
	for j := start; j < end; j++ {
		line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) Show(scr *vtui.ScreenBuf) {
	tv.ScreenObject.Show(scr)
	tv.mu.Lock()
	defer tv.mu.Unlock()

	buf := tv.getBuffer()
	for y, line := range buf {
		scr.Write(tv.X1, tv.Y1+y, line)
	}
	if !tv.IsVisible() {
		return
	}

	if tv.UseAltScreen {
		scr.SetCursorPos(tv.X1+tv.CursorX, tv.Y1+tv.CursorY)
		scr.SetCursorVisible(true)
	}
}