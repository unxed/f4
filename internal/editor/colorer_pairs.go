//go:build !lite

package editor

import (
	"unicode/utf8"

	colorer "github.com/unxed/colorer4go"
)

// colorerPairToken is one token of a pair, on its line.
type colorerPairToken struct {
	line int
	pair colorer.Pair
}

// colorerPairMatch is Colorer's PairMatch: the pair token under the cursor
// and, when found, the one that balances it.
type colorerPairMatch struct {
	start colorerPairToken
	end   colorerPairToken
	found bool
	// top is set when the cursor is on a pair start, so the match lies after
	// it; otherwise it lies before.
	top bool
}

// colorerPairWalk is BaseEditor::searchPair's loop, made resumable: the
// search can stop at a line that has not been parsed yet and continue from
// the same place once it has.
type colorerPairWalk struct {
	match   colorerPairMatch
	lno     int            // the line the walk stands on
	i       int            // the index of the last pair visited in pairs
	pairs   []colorer.Pair // the pairs of lno
	balance int
}

// startColorerPairWalk is BaseEditor::getPairMatch. pos is a rune offset on
// line, whose pairs are given; a token matches when it lies within [Start,
// End] — End included, so the cursor just after a bracket still finds it —
// and the last such token wins. It reports false when there is none.
func startColorerPairWalk(line, pos int, pairs []colorer.Pair) (colorerPairWalk, bool) {
	idx := -1
	for i, p := range pairs {
		if pos >= p.Start && pos <= p.End {
			idx = i
		}
	}
	if idx < 0 {
		return colorerPairWalk{}, false
	}
	w := colorerPairWalk{
		match: colorerPairMatch{start: colorerPairToken{line, pairs[idx]}, top: pairs[idx].Opens},
		lno:   line,
		i:     idx,
		pairs: pairs,
	}
	w.balance = -1
	if w.match.top {
		w.balance = 1
	}
	return w, true
}

// colorerWalkStop says why advance returned.
type colorerWalkStop int

const (
	colorerWalkFound     colorerWalkStop = iota // w.match.end holds the match
	colorerWalkExhausted                        // the window ended without one
	colorerWalkNeedsLine                        // the returned line is not parsed yet
)

// advance continues BaseEditor::searchPair within lines [first, last]: walk
// the pairs after the start (before it, for a pair end), +1 per start and -1
// per end, until the balance is zero. Only pair regions move the balance, so
// walking pairs gives Colorer's result. pairsAt reports false for a line not
// parsed yet; advance then returns that line without moving, and calling it
// again once the line is parsed carries on.
func (w *colorerPairWalk) advance(first, last int, pairsAt func(int) ([]colorer.Pair, bool)) (colorerWalkStop, int) {
	for {
		if w.balance > 0 {
			for w.i+1 >= len(w.pairs) {
				next := w.lno + 1
				if next > last {
					return colorerWalkExhausted, 0
				}
				pairs, ok := pairsAt(next)
				if !ok {
					return colorerWalkNeedsLine, next
				}
				w.lno, w.pairs, w.i = next, pairs, -1
			}
			w.i++
		} else {
			for w.i-1 < 0 {
				next := w.lno - 1
				if next < first {
					return colorerWalkExhausted, 0
				}
				pairs, ok := pairsAt(next)
				if !ok {
					return colorerWalkNeedsLine, next
				}
				w.lno, w.pairs, w.i = next, pairs, len(pairs)
			}
			w.i--
		}
		if w.pairs[w.i].Opens {
			w.balance++
		} else {
			w.balance--
		}
		if w.balance == 0 {
			w.match.end = colorerPairToken{w.lno, w.pairs[w.i]}
			w.match.found = true
			return colorerWalkFound, 0
		}
	}
}

// matchColorerPair searches [first, last] for the match of the pair token at
// pos on line, as BaseEditor::searchLocalPair does, and stops without a match
// at a line pairsAt reports as not parsed: Colorer's regions are always
// complete, the cache may not have reached the match yet. It reports false
// when there is no pair token under the cursor.
func matchColorerPair(line, pos, first, last int, pairsAt func(int) ([]colorer.Pair, bool)) (colorerPairMatch, bool) {
	pairs, ok := pairsAt(line)
	if !ok {
		return colorerPairMatch{}, false
	}
	w, ok := startColorerPairWalk(line, pos, pairs)
	if !ok {
		return colorerPairMatch{}, false
	}
	w.advance(first, last, pairsAt)
	return w.match, true
}

// colorerPairOverlay is the pair under the cursor as drawn: the tokens to
// paint, by line.
type colorerPairOverlay map[int][]colorer.Pair

// cachedPairs are the pairs of a line whose colours are cached. A line with
// no cached colours has not been parsed, which is not the same as having no
// pairs.
func (ch *ColorerHighlighter) cachedPairs(idx int) ([]colorer.Pair, bool) {
	if _, ok := ch.attrCache[idx]; !ok {
		return nil, false
	}
	return ch.pairCache[idx], true
}

// pairOverlay finds the pair under the cursor within the visible lines [first,
// last], as FarColorer's searchLocalPair does when it redraws: the token under
// the cursor is painted even when its match is not on screen. cursorByte is a
// byte offset on text, the cursor line.
func (ch *ColorerHighlighter) pairOverlay(cursorLine, cursorByte int, text string, first, last int) colorerPairOverlay {
	if ch == nil || ch.session == nil || ch.closed || ch.disabled {
		return nil
	}
	if cursorByte > len(text) {
		cursorByte = len(text)
	}
	if cursorByte < 0 {
		cursorByte = 0
	}
	pos := utf8.RuneCountInString(text[:cursorByte])
	m, ok := matchColorerPair(cursorLine, pos, first, last, ch.cachedPairs)
	if !ok {
		return nil
	}
	overlay := colorerPairOverlay{m.start.line: {m.start.pair}}
	if m.found {
		overlay[m.end.line] = append(overlay[m.end.line], m.end.pair)
	}
	return overlay
}

// apply paints the overlay's tokens on line idx over its syntax colours, each
// with the pair's own colour style. attrs is the cached slice and is not
// modified; a line without tokens gets attrs back.
func (o colorerPairOverlay) apply(idx int, attrs []uint64) []uint64 {
	pairs := o[idx]
	if len(pairs) == 0 || attrs == nil {
		return attrs
	}
	out := append([]uint64(nil), attrs...)
	for _, p := range pairs {
		start, end, _ := colorerRegionRunes(p.Start, p.End, len(out))
		style := colorer.RegionDefine{Fore: p.Fore, Back: p.Back, Style: p.Style, IsForeSet: p.IsForeSet, IsBackSet: p.IsBackSet}
		for i := start; i < end; i++ {
			out[i] = applyColorerStyle(out[i], &style)
		}
	}
	return out
}
