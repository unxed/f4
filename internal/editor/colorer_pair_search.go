//go:build !lite

package editor

import (
	"unicode/utf8"

	"github.com/unxed/vtui"
)

// ColorerPairAction is one of FarColorer's pair actions.
type ColorerPairAction int

const (
	// ColorerMatchPair moves the cursor to the match of the pair token under
	// it (FarEditor::matchPair).
	ColorerMatchPair ColorerPairAction = iota
	// ColorerSelectPair selects what lies between the two tokens
	// (FarEditor::selectPair).
	ColorerSelectPair
	// ColorerSelectBlock selects the two tokens and what lies between them
	// (FarEditor::selectBlock).
	ColorerSelectBlock
)

// colorerPairSearch is a whole-file search in progress, FarColorer's
// searchGlobalPair. It lives on the UI thread and walks the line cache; where
// the cache has no line yet it queues that line to the worker and resumes when
// the result lands, so a match thousands of lines away is reached without
// blocking the editor or copying the file.
type colorerPairSearch struct {
	action ColorerPairAction
	walk   colorerPairWalk
	// The cursor the search started from. Moving it abandons the search: the
	// result would land somewhere the user no longer is.
	cursorLine, cursorPos int
	// The line last queued, and how many times in a row. A line the worker
	// cannot deliver would otherwise be queued forever.
	queued, repeats int
}

// maxColorerPairRequeues bounds how often one line is queued without arriving.
const maxColorerPairRequeues = 3

// ColorerPair runs a FarColorer pair action for the pair token under the
// cursor. The match may be anywhere in the file; the action is applied once
// it is found, and nothing happens when the cursor is on no pair token or the
// token has no match — as in FarColorer.
func (ev *EditorView) ColorerPair(action ColorerPairAction) {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok || ch.session == nil || ch.closed || ch.disabled {
		return
	}
	pairs, parsed := ch.cachedPairs(ev.CursorLine)
	if !parsed {
		// The cursor line is on screen and queued already; its pairs are
		// simply not there yet.
		return
	}
	text, ok := ev.lineTextForHighlight(ev.CursorLine)
	if !ok {
		return
	}
	pos := runeIndexAtByte(text, ev.CursorPos)
	walk, ok := startColorerPairWalk(ev.CursorLine, pos, pairs)
	if !ok {
		return
	}
	ch.pairSearch = &colorerPairSearch{action: action, walk: walk, cursorLine: ev.CursorLine, cursorPos: ev.CursorPos, queued: -1}
	ch.continuePairSearch()
}

// continuePairSearch advances the search in progress, if any. It runs when the
// search starts and whenever a worker result has landed.
func (ch *ColorerHighlighter) continuePairSearch() {
	s := ch.pairSearch
	ev := ch.owner
	if s == nil || ev == nil {
		return
	}
	if ch.closed || ch.disabled || ch.session == nil || ev.CursorLine != s.cursorLine || ev.CursorPos != s.cursorPos {
		ch.pairSearch = nil
		return
	}
	stop, need := s.walk.advance(0, ev.Li.LineCount()-1, ch.cachedPairs)
	switch stop {
	case colorerWalkFound:
		ch.pairSearch = nil
		ev.applyColorerPair(s.action, s.walk.match)
		return
	case colorerWalkExhausted:
		ch.pairSearch = nil
		return
	}
	if ch.pending {
		return // the job in flight resumes the search when it lands
	}
	if need == s.queued {
		s.repeats++
		if s.repeats >= maxColorerPairRequeues {
			vtui.DebugLog("COLORER: pair search abandoned: line %d was queued %d times and never parsed", need, s.repeats)
			ch.pairSearch = nil
			return
		}
	} else {
		s.queued, s.repeats = need, 0
	}
	// A job parses a batch forward from its target. Walking down, the needed
	// line is where the parse position already is; walking up, start the
	// batch below it far enough that one job covers a batch of lines above.
	target := need
	if need < s.walk.lno {
		target = need - hlColorerBatchLines + 1
		if target < 0 {
			target = 0
		}
	}
	text, ok := ev.lineTextForHighlight(target)
	if !ok {
		ch.pairSearch = nil
		return
	}
	ch.queueLine(target, text, ev.colorerBaseAttr())
	if ch.pending {
		// Esc stops the search, not Colorer: the job it queued is ordinary
		// highlighting and may finish.
		ev.colorerCancel = func() { ch.pairSearch = nil }
	}
}

// applyColorerPair does what action asks with a found match, with FarColorer's
// positions: rune offsets of Colorer's tokens become byte offsets here.
func (ev *EditorView) applyColorerPair(action ColorerPairAction, m colorerPairMatch) {
	if !m.found {
		return
	}
	offset := func(line, runeIdx int) (int, bool) {
		text, ok := ev.lineTextForHighlight(line)
		if !ok {
			return 0, false
		}
		return byteIndexAtRune(text, runeIdx), true
	}
	moveTo := func(line, runeIdx int) bool {
		pos, ok := offset(line, runeIdx)
		if !ok {
			return false
		}
		ev.CursorLine, ev.CursorPos = line, pos
		return true
	}

	start, end := m.start, m.end
	switch action {
	case ColorerMatchPair:
		// matchPair: on the first character of a match above, on the last
		// character of a match below.
		col := end.pair.Start
		if m.top && end.pair.End-1 > col {
			col = end.pair.End - 1
		}
		ev.RectSelActive = false
		ev.SelActive = false
		if !moveTo(end.line, col) {
			return
		}
	case ColorerSelectPair, ColorerSelectBlock:
		// selectPair takes what lies between the tokens, selectBlock the
		// tokens too. The selection runs from the upper position to the
		// lower one, and the cursor ends at the lower one.
		upLine, upCol, downLine, downCol := start.line, start.pair.End, end.line, end.pair.Start
		if !m.top {
			upLine, upCol, downLine, downCol = end.line, end.pair.End, start.line, start.pair.Start
		}
		if action == ColorerSelectBlock {
			upCol, downCol = start.pair.Start, end.pair.End
			if !m.top {
				upCol, downCol = end.pair.Start, start.pair.End
			}
		}
		upPos, ok := offset(upLine, upCol)
		if !ok || !moveTo(downLine, downCol) {
			return
		}
		ev.RectSelActive = false
		ev.SelAnchorOffset = ev.Li.GetLineOffset(upLine) + upPos
		ev.SelActive = ev.SelAnchorOffset != ev.Li.GetLineOffset(ev.CursorLine)+ev.CursorPos
	}
	// FarColorer centres a match that is off screen.
	ev.centerCursor(true)
}

// runeIndexAtByte is the rune offset of byte offset b in text, clamped.
func runeIndexAtByte(text string, b int) int {
	if b > len(text) {
		b = len(text)
	}
	if b < 0 {
		b = 0
	}
	return utf8.RuneCountInString(text[:b])
}

// byteIndexAtRune is the byte offset of rune offset r in text, clamped to the
// end of text.
func byteIndexAtRune(text string, r int) int {
	if r <= 0 {
		return 0
	}
	n := 0
	for i := range text {
		if n == r {
			return i
		}
		n++
	}
	return len(text)
}
