package main

import (
	"strconv"
	"strings"

	"github.com/unxed/vtui"
)

type ParserState int

const (
	StateGround ParserState = iota
	StateEsc
	StateCSI
	StateOSC
)

// AnsiParser converts a stream of bytes into ScreenBuf operations.
type AnsiParser struct {
	State     ParserState
	Params    []string
	CurParam  strings.Builder
	Attr      uint64
	term      *TerminalView
}

func NewAnsiParser(t *TerminalView) *AnsiParser {
	return &AnsiParser{
		term: t,
		Attr: vtui.Palette[vtui.ColCommandLineUserScreen],
	}
}

func (p *AnsiParser) Process(data []byte) {
	for _, b := range data {
		switch p.State {
		case StateGround:
			if b == 0x1b {
				p.State = StateEsc
			} else {
				p.term.PutChar(rune(b), p.Attr)
			}
		case StateEsc:
			if b == '[' {
				p.State = StateCSI
				p.Params = nil
				p.CurParam.Reset()
			} else {
				p.State = StateGround
			}
		case StateCSI:
			if b >= '0' && b <= '9' {
				p.CurParam.WriteByte(b)
			} else if b == ';' {
				p.Params = append(p.Params, p.CurParam.String())
				p.CurParam.Reset()
			} else {
				p.Params = append(p.Params, p.CurParam.String())
				p.handleCSI(b)
				p.State = StateGround
			}
		}
	}
}

func (p *AnsiParser) handleCSI(cmd byte) {
	switch cmd {
	case 'm': // SGR
		for _, s := range p.Params {
			val, _ := strconv.Atoi(s)
			p.handleSGR(val)
		}
	case 'H', 'f': // Cursor position
		row, col := 1, 1
		if len(p.Params) > 0 && p.Params[0] != "" {
			row, _ = strconv.Atoi(p.Params[0])
		}
		if len(p.Params) > 1 && p.Params[1] != "" {
			col, _ = strconv.Atoi(p.Params[1])
		}
		p.term.SetCursor(col-1, row-1)
	case 'J': // Erase in Display
		mode := 0
		if len(p.Params) > 0 && p.Params[0] != "" {
			mode, _ = strconv.Atoi(p.Params[0])
		}
		p.term.EraseDisplay(mode, p.Attr)
	case 'K': // Erase in Line
		mode := 0
		if len(p.Params) > 0 && p.Params[0] != "" {
			mode, _ = strconv.Atoi(p.Params[0])
		}
		p.term.EraseLine(mode, p.Attr)
	}
}

func (p *AnsiParser) handleSGR(n int) {
	if n == 0 {
		p.Attr = vtui.Palette[vtui.ColCommandLineUserScreen]
		return
	}
	// Basic 16 colors mapping (simplified)
	if n >= 30 && n <= 37 { // FG
		p.Attr = vtui.SetRGBFore(p.Attr, far2lPalette[n-30])
	} else if n >= 40 && n <= 47 { // BG
		p.Attr = vtui.SetRGBBack(p.Attr, far2lPalette[n-40])
	} else if n >= 90 && n <= 97 { // FG high intensity
		p.Attr = vtui.SetRGBFore(p.Attr, far2lPalette[n-82])
	} else if n >= 100 && n <= 107 { // BG high intensity
		p.Attr = vtui.SetRGBBack(p.Attr, far2lPalette[n-92])
	}
}