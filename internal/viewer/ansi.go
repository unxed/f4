package viewer

import (
	"path"
	"strings"

	"github.com/unxed/vtui"
)

// Terminal escape sequences in the text viewer (f4#1705). Log files written
// by systemd, apt or a build tool carry SGR colour sequences; the "ANSI mode"
// draws them as colours, the way the terminal that printed them would have,
// instead of showing "^[[0;32m" between the words. As in far2l's processed
// viewer mode the sequences take no room: the row layout, wrapping and the
// search highlighting see only the text around them.

// ansiSeq is one CSI sequence found in the text: the index in the stripped
// text it takes effect at, its parameters and its final byte.
type ansiSeq struct {
	pos    int
	params []int
	final  byte
}

// stripANSI removes the CSI sequences (ESC [ parameters intermediates final)
// from data. orig has one entry more than plain: orig[i] is where plain[i]
// stands in data, and orig[len(plain)] is len(data), so a row of k stripped
// bytes ends at orig[k] in data and takes the sequences that sit right before
// the next character along with it. A sequence cut by the end of data is
// dropped.
func stripANSI(data []byte) (plain []byte, orig []int, seqs []ansiSeq) {
	plain = make([]byte, 0, len(data))
	orig = make([]int, 0, len(data)+1)
	for i := 0; i < len(data); {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			j := i + 2
			for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
				j++
			}
			paramEnd := j
			for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
				j++
			}
			if j >= len(data) {
				// The sequence runs into the end of the data.
				break
			}
			if data[j] >= 0x40 && data[j] <= 0x7e {
				params, ok := parseANSIParams(data[i+2 : paramEnd])
				final := data[j]
				if !ok {
					final = 0
				}
				seqs = append(seqs, ansiSeq{pos: len(plain), params: params, final: final})
				i = j + 1
				continue
			}
			// Not a sequence after all: the ESC is text.
		}
		plain = append(plain, data[i])
		orig = append(orig, i)
		i++
	}
	orig = append(orig, len(data))
	return plain, orig, seqs
}

// parseANSIParams reads the numbers of a parameter string. An empty field is
// 0, and ':' separates like ';'. A private marker such as '?' makes the list
// unusable: ok is false.
func parseANSIParams(b []byte) (params []int, ok bool) {
	if len(b) == 0 {
		return nil, true
	}
	n := 0
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			if n < 100000 {
				n = n*10 + int(c-'0')
			}
		case c == ';' || c == ':':
			params = append(params, n)
			n = 0
		default:
			return nil, false
		}
	}
	return append(params, n), true
}

// layoutViewerTextRowANSI is layoutViewerTextRow for text that may hold
// escape sequences: the row is laid out on the text without them and the
// counts are carried back to data.
func layoutViewerTextRowANSI(data []byte, width, tabSize int, wrap, ansi bool) viewerTextRow {
	if !ansi {
		return layoutViewerTextRow(data, width, tabSize, wrap)
	}
	plain, orig, _ := stripANSI(data)
	row := layoutViewerTextRow(plain, width, tabSize, wrap)
	if row.lineLen >= 0 && row.lineLen < len(orig) {
		row.lineLen = orig[row.lineLen]
	}
	if row.textLen >= 0 && row.textLen < len(orig) {
		row.textLen = orig[row.textLen]
	}
	return row
}

// ansiRowCells draws one row of ANSI text: the cells of the text with the
// sequences taken out, coloured by the state the sequences leave, which
// carries from row to row through *state (start it at base). base is the plain text attribute
// that SGR 0, 39 and 49 return to. cellOffsets are byte offsets in text.
func ansiRowCells(text []byte, base uint64, state *uint64, tabSize, maxWidth int) (cells []vtui.CharInfo, cellOffsets []int) {
	plain, orig, seqs := stripANSI(text)
	cells, offs := viewerTextCells(string(plain), base, tabSize, maxWidth)
	next := 0
	for i := range cells {
		for next < len(seqs) && seqs[next].pos <= offs[i] {
			*state = applyANSISeq(*state, base, seqs[next])
			next++
		}
		cells[i].Attributes = *state
	}
	for ; next < len(seqs); next++ {
		*state = applyANSISeq(*state, base, seqs[next])
	}
	cellOffsets = make([]int, len(offs))
	for i, o := range offs {
		cellOffsets[i] = orig[o]
	}
	return cells, cellOffsets
}

// applyANSISeq applies one sequence to attr. Only SGR ("m") changes colours;
// every other sequence is dropped.
func applyANSISeq(attr, base uint64, s ansiSeq) uint64 {
	if s.final != 'm' {
		return attr
	}
	args := s.params
	if len(args) == 0 {
		return base
	}
	for i := 0; i < len(args); {
		var used int
		attr, used = applySGR(attr, base, args, i)
		i += used
	}
	return attr
}

func resetFore(attr, base uint64) uint64 {
	if base&vtui.IsFgRGB != 0 {
		return vtui.SetRGBFore(attr, vtui.GetRGBFore(base))
	}
	return vtui.SetIndexFore(attr, vtui.GetIndexFore(base))
}

func resetBack(attr, base uint64) uint64 {
	if base&vtui.IsBgRGB != 0 {
		return vtui.SetRGBBack(attr, vtui.GetRGBBack(base))
	}
	return vtui.SetIndexBack(attr, vtui.GetIndexBack(base))
}

// applySGR applies the SGR code at args[i] and reports how many numbers it
// used: 38 and 48 take a colour after them. The codes are the ones the
// terminal panel understands (internal/terminal/ansi.go).
func applySGR(attr, base uint64, args []int, i int) (uint64, int) {
	n := args[i]
	switch {
	case n == 0:
		return base, 1
	case n == 1:
		return attr | vtui.ForegroundIntensity, 1
	case n == 2:
		return attr | vtui.ForegroundDim, 1
	case n == 4:
		return attr | vtui.CommonLvbUnderscore, 1
	case n == 7:
		return attr | vtui.CommonLvbReverse, 1
	case n == 9:
		return attr | vtui.CommonLvbStrikeout, 1
	case n == 22:
		return attr &^ (vtui.ForegroundIntensity | vtui.ForegroundDim), 1
	case n == 24:
		return attr &^ vtui.CommonLvbUnderscore, 1
	case n == 27:
		return attr &^ vtui.CommonLvbReverse, 1
	case n == 29:
		return attr &^ vtui.CommonLvbStrikeout, 1
	case n >= 30 && n <= 37:
		return vtui.SetIndexFore(attr, uint8(n-30)), 1 //nolint:gosec // n is 30..37
	case n >= 90 && n <= 97:
		return vtui.SetIndexFore(attr, uint8(n-90+8)), 1 //nolint:gosec // n is 90..97
	case n >= 40 && n <= 47:
		return vtui.SetIndexBack(attr, uint8(n-40)), 1 //nolint:gosec // n is 40..47
	case n >= 100 && n <= 107:
		return vtui.SetIndexBack(attr, uint8(n-100+8)), 1 //nolint:gosec // n is 100..107
	case n == 39:
		return resetFore(attr, base), 1
	case n == 49:
		return resetBack(attr, base), 1
	case n == 38 || n == 48:
		fore := n == 38
		if i+2 < len(args) && args[i+1] == 5 {
			if idx := args[i+2]; idx >= 0 && idx < 256 {
				if fore {
					attr = vtui.SetIndexFore(attr, uint8(idx)) //nolint:gosec // idx is 0..255
				} else {
					attr = vtui.SetIndexBack(attr, uint8(idx)) //nolint:gosec // idx is 0..255
				}
			}
			return attr, 3
		}
		if i+4 < len(args) && args[i+1] == 2 {
			r, g, b := args[i+2]&0xff, args[i+3]&0xff, args[i+4]&0xff
			rgb := uint32(r)<<16 | uint32(g)<<8 | uint32(b) //nolint:gosec // each is 0..255
			if fore {
				attr = vtui.SetRGBFore(attr, rgb)
			} else {
				attr = vtui.SetRGBBack(attr, rgb)
			}
			return attr, 5
		}
		return attr, len(args) - i
	}
	return attr, 1
}

// isANSIFileName reports whether the name says the file is ANSI art or a
// terminal capture: far2l opens *.ans and *.ansi in its colour mode.
func isANSIFileName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".ans", ".ansi":
		return true
	}
	return false
}
