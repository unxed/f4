package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unxed/vtui"
)

type ParserState int
var DefaultTermAttr = vtui.SetRGBBoth(0, 0xC0C0C0, 0x000000) // Light Gray on Black

const (
	StateGround ParserState = iota
	StateEsc
	StateCSI
	StateOSC
	StateAPC
)

// AnsiParser converts a stream of bytes into ScreenBuf operations.
type AnsiParser struct {
	State     ParserState
	Params    []string
	CurParam  strings.Builder
	Attr      uint64
	term      *TerminalView
	pty       PtyBackend
	runeBuf   []byte
	lastRune  rune
}

func NewAnsiParser(t *TerminalView, p PtyBackend) *AnsiParser {
	return &AnsiParser{
		term: t,
		pty:  p,
		Attr: DefaultTermAttr,
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
				r := rune(b)
				p.term.PutChar(r, p.Attr)
				p.lastRune = r
				p.runeBuf = p.runeBuf[:0]
			} else {
				p.runeBuf = append(p.runeBuf, b)
				if utf8.FullRune(p.runeBuf) {
					r, _ := utf8.DecodeRune(p.runeBuf)
					p.term.PutChar(r, p.Attr)
					p.lastRune = r
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
				p.Params = nil
				p.CurParam.Reset()
			} else if b == '_' {
				p.State = StateAPC
			} else if b == '7' {
				p.term.SaveCursor()
				p.State = StateGround
			} else if b == '8' {
				p.term.RestoreCursor()
				p.State = StateGround
			} else if b == '\\' {
				// String Terminator (ST)
				p.State = StateGround
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
				p.handleOSC()
				p.State = StateGround
			} else if b == 0x1b { // ESC
				p.handleOSC()
				p.State = StateEsc
			} else {
				p.CurParam.WriteByte(b)
			}
		case StateAPC:
			if b == 0x07 { // BEL
				p.State = StateGround
			} else if b == 0x1b { // ESC
				p.State = StateEsc
			}
		}
	}
}

func (p *AnsiParser) handleCSI(cmd byte) {
	args := make([]int, len(p.Params))
	// If there are no arguments, args will be an empty slice.
	// This is important for correct handling of default commands.
	for i, s := range p.Params {
		s = strings.TrimLeft(s, "?<=>")
		val, _ := strconv.Atoi(s)
		args[i] = val
	}

	switch cmd {
	case 'm':
		if len(args) == 0 {
			p.handleSGR(args, 0) // Default reset
		} else {
			for i := 0; i < len(args); {
				consumed := p.handleSGR(args, i)
				i += consumed
			}
		}
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
	case 'r': // DECSTBM - Set Top and Bottom Margins
		top, bottom := 1, p.term.Height
		if len(args) > 0 && args[0] != 0 { top = args[0] }
		if len(args) > 1 && args[1] != 0 { bottom = args[1] }
		p.term.ScrollTop = top - 1
		p.term.ScrollBottom = bottom - 1
		p.term.SetCursor(0, 0)
	case 'h', 'l': // DECSET / DECRST
		isSet := cmd == 'h'
		for _, s := range p.Params {
			if s == "?1049" {
				p.term.SetAltScreen(isSet)
				if isSet {
					p.term.EraseDisplay(2, p.Attr)
				}
			}
		}
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
		if len(args) > 0 && args[0] != 0 {
			row = args[0]
		}
		p.term.SetCursor(p.term.CursorX, row-1)
	case 'n': // DSR - Device Status Report
		if len(args) > 0 {
			if args[0] == 5 {
				if p.pty != nil {
					p.pty.Write([]byte("\x1b[0n"))
				}
			} else if args[0] == 6 {
				if p.pty != nil {
					resp := fmt.Sprintf("\x1b[%d;%dR", p.term.CursorY+1, p.term.CursorX+1)
					p.pty.Write([]byte(resp))
				}
			}
		}
	case 's':
		if len(p.Params) == 0 || (len(p.Params) == 1 && p.Params[0] == "") {
			p.term.SaveCursor()
		}
	case 'u':
		if len(p.Params) == 0 || (len(p.Params) == 1 && p.Params[0] == "") {
			p.term.RestoreCursor()
		}
	case 'b': // REP - Repeat last character
		n := 1
		if len(args) > 0 && args[0] != 0 {
			n = args[0]
		}
		p.term.RepeatLastChar(n, p.lastRune, p.Attr)
	case 'X': // ECH - Erase Character
		n := 1
		if len(args) > 0 && args[0] != 0 {
			n = args[0]
		}
		p.term.EraseCharacter(n, p.Attr)
	}
}

func (p *AnsiParser) handleOSC() {
	s := p.CurParam.String()
	p.CurParam.Reset()
	if s == "" { return }

	parts := strings.Split(s, ";")
	if len(parts) < 3 { return }

	cmd, _ := strconv.Atoi(parts[0])
	if cmd != 4 { return } // We only support OSC 4 (Set Palette)

	idx, _ := strconv.Atoi(parts[1])
	if idx < 0 || idx >= 16 { return }

	colorStr := parts[2]
	var rgbVal uint32
	parsed := false

	if strings.HasPrefix(colorStr, "#") && len(colorStr) >= 7 {
		v, err := strconv.ParseUint(colorStr[1:7], 16, 32)
		if err == nil {
			rgbVal = uint32(v)
			parsed = true
		}
	} else if strings.HasPrefix(colorStr, "rgb:") {
		// format rgb:RR/GG/BB
		rgbParts := strings.Split(colorStr[4:], "/")
		if len(rgbParts) == 3 {
			r, _ := strconv.ParseUint(rgbParts[0], 16, 8)
			g, _ := strconv.ParseUint(rgbParts[1], 16, 8)
			b, _ := strconv.ParseUint(rgbParts[2], 16, 8)
			rgbVal = uint32((r << 16) | (g << 8) | b)
			parsed = true
		}
	}

	if parsed {
		p.term.Palette[idx] = rgbVal
	}
}

func (p *AnsiParser) handleSGR(args []int, i int) int {
	if len(args) == 0 {
		p.Attr = DefaultTermAttr
		return 1
	}

	n := args[i]
	switch {
	case n == 0:
		p.Attr = DefaultTermAttr
	case n == 1:
		p.Attr |= vtui.ForegroundIntensity
	case n == 2:
		p.Attr |= vtui.ForegroundDim
	case n == 4:
		p.Attr |= vtui.CommonLvbUnderscore
	case n == 5:
		// Blink - ignored in many TUIs or mapped to intensity
	case n == 7:
		p.Attr |= vtui.CommonLvbReverse
	case n == 9:
		p.Attr |= vtui.CommonLvbStrikeout
	case n == 22:
		p.Attr &= ^(vtui.ForegroundIntensity | vtui.ForegroundDim)
	case n == 24:
		p.Attr &= ^vtui.CommonLvbUnderscore
	case n == 27:
		p.Attr &= ^vtui.CommonLvbReverse
	case n == 29:
		p.Attr &= ^vtui.CommonLvbStrikeout

	case n >= 30 && n <= 37:
		p.Attr = vtui.SetRGBFore(p.Attr, p.term.Palette[n-30])
	case n == 38:
		if i+2 < len(args) {
			if args[i+1] == 5 { // 256 colors
				idx := args[i+2]
				if idx >= 0 && idx < 256 {
					p.Attr = vtui.SetRGBFore(p.Attr, vtui.XTerm256Palette[idx])
				}
				return 3
			} else if args[i+1] == 2 && i+4 < len(args) { // TrueColor
				r, g, b := uint32(args[i+2]), uint32(args[i+3]), uint32(args[i+4])
				p.Attr = vtui.SetRGBFore(p.Attr, (r<<16)|(g<<8)|b)
				return 5
			}
		}
	case n == 39:
		p.Attr = vtui.SetRGBFore(p.Attr, vtui.GetRGBFore(vtui.Palette[ColCommandLineUserScreen]))

	case n >= 40 && n <= 47:
		p.Attr = vtui.SetRGBBack(p.Attr, p.term.Palette[n-40])
	case n == 48:
		if i+2 < len(args) {
			if args[i+1] == 5 { // 256 colors
				idx := args[i+2]
				if idx >= 0 && idx < 256 {
					p.Attr = vtui.SetRGBBack(p.Attr, vtui.XTerm256Palette[idx])
				}
				return 3
			} else if args[i+1] == 2 && i+4 < len(args) { // TrueColor
				r, g, b := uint32(args[i+2]), uint32(args[i+3]), uint32(args[i+4])
				p.Attr = vtui.SetRGBBack(p.Attr, (r<<16)|(g<<8)|b)
				return 5
			}
		}
	case n == 49:
		p.Attr = vtui.SetRGBBack(p.Attr, vtui.GetRGBBack(vtui.Palette[ColCommandLineUserScreen]))

	case n >= 90 && n <= 97:
		p.Attr = vtui.SetRGBFore(p.Attr, p.term.Palette[n-90+8])
	case n >= 100 && n <= 107:
		p.Attr = vtui.SetRGBBack(p.Attr, p.term.Palette[n-100+8])
	}
	return 1
}