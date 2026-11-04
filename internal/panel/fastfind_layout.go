package panel

import (
	"slices"
	"time"

	"github.com/unxed/vtui"
)

// The quick search after a keyboard layout switch (#2186).
//
// A search started with Alt+F and continued after switching to the Russian
// layout reads "fщ": no name has letters of both layouts, so the щ found
// nothing and was dropped without a sign. far2l tries a text that finds
// nothing in the other layout, one character at a time: a character is taken
// in the other layout when that still finds a name, as typed when that does,
// and the text finds nothing when neither does (Panel::FindPartNameXLat,
// behind XLat.EnableForFastFileFind). f4 does the same behind
// PanelQuickSearchXLat. The box keeps what was typed -- the user looks at what
// they type -- and only the names are matched against the other spelling.

// fastFindRejectShown is how long a character the search refused stays
// after its text.
var fastFindRejectShown = 800 * time.Millisecond

// newFastFindMatcher is the matcher of one spelling of the search text.
func newFastFindMatcher(query string, strict bool) *vtui.FuzzyMatcher {
	m := vtui.NewFuzzyMatcher(query, false)
	if m != nil && strict {
		m.Strict()
	}
	return m
}

// fastFindQueries are the spellings of query that names are matched
// against: what was typed and, with PanelQuickSearchXLat on, the other
// layout's. The fuzzy mode has always tried the whole text in the other
// layout as one more tolerance ("сфы" also finds "Cas"); it still does. A
// text that finds no name exactly is tried the far2l way too, which is what
// finds a text typed half in one layout and half in the other. Strict mode
// knows only the far2l way, so whenever the text finds a name as typed it
// searches exactly what was typed (f4 #1709).
func (fp *FileSystemPanel) fastFindQueries(query string, anywhere, strict, xlat bool) []string {
	queries := []string{query}
	if !xlat {
		return queries
	}
	add := func(q string) {
		if q != "" && !slices.Contains(queries, q) {
			queries = append(queries, q)
		}
	}
	if !strict {
		add(vtui.GlobalXlator.TranscodeString(query))
	}
	if !fp.fastFindFinds(query, anywhere) {
		add(fp.fastFindOtherLayout(query, anywhere))
	}
	return queries
}

// fastFindOtherLayout spells query the way far2l's FindPartNameXLat does,
// one character at a time, each in the other layout when that keeps a name
// found and as typed otherwise. It returns "" when no spelling finds a name.
func (fp *FileSystemPanel) fastFindOtherLayout(query string, anywhere bool) string {
	spelled := make([]rune, 0, len(query))
	for _, r := range query {
		if other := vtui.GlobalXlator.Translate(r); other != r && fp.fastFindFinds(string(append(spelled, other)), anywhere) {
			spelled = append(spelled, other)
			continue
		}
		spelled = append(spelled, r)
		if !fp.fastFindFinds(string(spelled), anywhere) {
			return ""
		}
	}
	return string(spelled)
}

// fastFindFinds reports whether some name of the panel holds query exactly,
// at its start or, with anywhere, at any place. Which layout a character
// belongs to is decided by exact hits, whichever mode the search is in.
func (fp *FileSystemPanel) fastFindFinds(query string, anywhere bool) bool {
	m := newFastFindMatcher(query, true)
	if m == nil {
		return false
	}
	for _, entry := range fp.AllEntries() {
		if _, start, _, ok := m.Match(entry.Name); ok && (anywhere || start == 0) {
			return true
		}
	}
	return false
}

// rejectFastFindChar shows ch, a character the search refused, after its
// text for fastFindRejectShown, so a refused key is seen to have been
// pressed: it used to vanish without a trace, which after a layout switch
// looked like a keyboard that stopped working.
func (fp *FileSystemPanel) rejectFastFindChar(ch rune) {
	fp.fastFindRejected = string(ch)
	fp.fastFindRejectedGen++
	gen := fp.fastFindRejectedGen
	// Read here, not in the timer: the timer outlives this call, and reading
	// the global from it races whatever reassigns it meanwhile.
	frames := vtui.FrameManager
	if frames == nil {
		return
	}
	time.AfterFunc(fastFindRejectShown, func() {
		frames.PostTask(func() {
			if fp.fastFindRejectedGen == gen && fp.fastFindRejected != "" {
				fp.fastFindRejected = ""
				frames.Redraw()
			}
		})
	})
}
