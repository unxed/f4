package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

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
	runeBuf   []byte
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
				p.runeBuf = p.runeBuf[:0]
			} else if b < 0x80 {
				p.term.PutChar(rune(b), p.Attr)
				p.runeBuf = p.runeBuf[:0]
			} else {
				p.runeBuf = append(p.runeBuf, b)
				if utf8.FullRune(p.runeBuf) {
					r, _ := utf8.DecodeRune(p.runeBuf)
					p.term.PutChar(r, p.Attr)
					p.runeBuf = p.runeBuf[:0]
				} else if len(p.runeBuf) >= 4 {
					// Invalid sequence or too long, flush as is
					p.term.PutChar(rune(p.runeBuf[0]), p.Attr)
					p.runeBuf = p.runeBuf[1:]
				}
			}
		case StateEsc:
			if b == '[' {
				p.State = StateCSI
				p.Params = nil
				p.CurParam.Reset()
			} else if b == ']' {
				p.State = StateOSC
			} else {
				p.State = StateGround
			}
		case StateCSI:
			if b >= 0x30 && b <= 0x39 { // '0'-'9'
				p.CurParam.WriteByte(b)
			} else if b == ';' {
				p.Params = append(p.Params, p.CurParam.String())
				p.CurParam.Reset()
			} else if b >= 0x3C && b <= 0x3F { // < = > ?
				p.CurParam.WriteByte(b)
			} else if b >= 0x20 && b <= 0x2F {
				// Intermediate bytes - ignore
			} else if b >= 0x40 && b <= 0x7E {
				p.Params = append(p.Params, p.CurParam.String())
				p.handleCSI(b)
				p.State = StateGround
			}
		case StateOSC:
			if b == 0x07 { // BEL
				p.State = StateGround
			} else if b == 0x1b { // ESC
				// Could be ST (\x1b\\), we just fall back to Esc state
				p.State = StateEsc
			}
		}
	}
}

func (p *AnsiParser) handleCSI(cmd byte) {
	args := make([]int, len(p.Params))
	for i, s := range p.Params {
		s = strings.TrimLeft(s, "?<=>")
		val, _ := strconv.Atoi(s)
		args[i] = val
	}

	switch cmd {
	case 'm':
		for _, n := range args { p.handleSGR(n) }
	case 'H', 'f':
		row, col := 1, 1
		if len(args) > 0 && args[0] != 0 { row = args[0] }
		if len(args) > 1 && args[1] != 0 { col = args[1] }
		p.term.SetCursor(col-1, row-1)
	case 'J':
		mode := 0
		if len(args) > 0 { mode = args[0] }
		p.term.EraseDisplay(mode, p.Attr)
	case 'K':
		mode := 0
		if len(args) > 0 { mode = args[0] }
		p.term.EraseLine(mode, p.Attr)
	case 'A':
		n := 1
		if len(args) > 0 && args[0] != 0 { n = args[0] }
		p.term.SetCursor(p.term.CursorX, p.term.CursorY-n)
	case 'B':
		n := 1
		if len(args) > 0 && args[0] != 0 { n = args[0] }
		p.term.SetCursor(p.term.CursorX, p.term.CursorY+n)
	case 'C':
		n := 1
		if len(args) > 0 && args[0] != 0 { n = args[0] }
		p.term.SetCursor(p.term.CursorX+n, p.term.CursorY)
	case 'D':
		n := 1
		if len(args) > 0 && args[0] != 0 { n = args[0] }
		p.term.SetCursor(p.term.CursorX-n, p.term.CursorY)
	case 'G', '`':
		col := 1
		if len(args) > 0 && args[0] != 0 { col = args[0] }
		p.term.SetCursor(col-1, p.term.CursorY)
	case 'd':
		row := 1
		if len(args) > 0 && args[0] != 0 { row = args[0] }
		p.term.SetCursor(p.term.CursorX, row-1)
	}
}

func (p *AnsiParser) handleSGR(n int) {
	if n == 0 {
		p.Attr = vtui.Palette[vtui.ColCommandLineUserScreen]
		return
	}
	if n >= 30 && n <= 37 {
		p.Attr = vtui.SetRGBFore(p.Attr, far2lPalette[n-30])
	} else if n >= 40 && n <= 47 {
		p.Attr = vtui.SetRGBBack(p.Attr, far2lPalette[n-40])
	} else if n >= 90 && n <= 97 {
		p.Attr = vtui.SetRGBFore(p.Attr, far2lPalette[n-82])
	} else if n >= 100 && n <= 107 {
		p.Attr = vtui.SetRGBBack(p.Attr, far2lPalette[n-92])
	} else if n == 39 {
		p.Attr = vtui.SetRGBFore(p.Attr, vtui.GetRGBFore(vtui.Palette[vtui.ColCommandLineUserScreen]))
	} else if n == 49 {
		p.Attr = vtui.SetRGBBack(p.Attr, vtui.GetRGBBack(vtui.Palette[vtui.ColCommandLineUserScreen]))
	}
}