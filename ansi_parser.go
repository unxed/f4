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
			} else if b == 0x07 {
				// BEL - often used as OSC terminator
				p.State = StateGround
			} else {
				p.term.PutChar(rune(b), p.Attr)
			}
		case StateEsc:
			switch b {
			case '[':
				p.State = StateCSI
				p.Params = nil
				p.CurParam.Reset()
			case ']':
				p.State = StateOSC
			case '(', ')':
				// Character set selection, swallow next byte
				p.State = StateGround
			default:
				p.State = StateGround
			}
		case StateCSI:
			// Parameter bytes: 0x30–0x3F (0-9 : ; < = > ?)
			if b >= 0x30 && b <= 0x3F {
				if b == ';' {
					p.Params = append(p.Params, p.CurParam.String())
					p.CurParam.Reset()
				} else {
					p.CurParam.WriteByte(b)
				}
			} else {
				// Final byte: 0x40–0x7E
				p.Params = append(p.Params, p.CurParam.String())
				p.handleCSI(b)
				p.State = StateGround
			}
		case StateOSC:
			// Operating System Command: ends with BEL (0x07) or ST (Esc \)
			if b == 0x07 {
				p.State = StateGround
			} else if b == 0x1b {
				p.State = StateEsc
			}
			// Just swallow all OSC content for now
		}
	}
}

func (p *AnsiParser) handleCSI(cmd byte) {
	// Filter out prefixes like '?' from the first parameter if present
	firstParam := ""
	if len(p.Params) > 0 {
		firstParam = p.Params[0]
		if len(firstParam) > 0 && (firstParam[0] == '?' || firstParam[0] == '>') {
			firstParam = firstParam[1:]
		}
	}

	switch cmd {
	case 'm': // SGR
		for _, s := range p.Params {
			val, _ := strconv.Atoi(s)
			p.handleSGR(val)
		}
	case 'H', 'f': // Cursor position
		row, col := 1, 1
		if firstParam != "" {
			row, _ = strconv.Atoi(firstParam)
		}
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