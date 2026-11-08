package history

import (
	"unicode"

	"github.com/unxed/vtui"
)

// CommandCompletions returns the command line's history rows for text, the
// way far2l's History::GetAllSimilar picks them: an entry must begin with
// text (case does not matter) and be longer than it, so the menu only ever
// offers to extend what is typed; the commands run in cwd come first, newest
// first, then a separator, then the rest. f4 also forgives typos, as the menu
// always has here, but only at the start of an entry: "./bulid" still finds
// "./build.sh", while "./" no longer offers "cd ../x" or "git add ./y".
// Records are expected newest first.
//
// limit caps the rows across both groups (0: no cap). The menu caps each
// separator-delimited group on its own, so without it the separator between
// the two groups would let history take twice its share of the list.
func CommandCompletions(records []HistoryRecord, text, cwd string, limit int) []vtui.AutoCompleteItem {
	m := newPrefixMatcher(text)
	if m == nil {
		return nil
	}

	type match struct {
		name string
		dist int
		end  int
	}
	var here, elsewhere []match
	var seen map[string]bool
	for _, record := range records {
		name := record.Name
		if name == "" || seen[name] || record.IsPluginEntry() {
			continue
		}
		dist, end, ok := m.match(name)
		if !ok {
			continue
		}
		if seen == nil {
			seen = make(map[string]bool)
		}
		seen[name] = true
		hit := match{name: name, dist: dist, end: end}
		if cwd != "" && record.Directory() == cwd {
			here = append(here, hit)
		} else {
			elsewhere = append(elsewhere, hit)
		}
	}

	// An exact prefix beats a typo; records are already newest first, so a
	// stable pass by distance keeps recency inside each distance. Both
	// groups draw on one budget, the current directory's first.
	room := limit
	rank := func(group []match) []vtui.AutoCompleteItem {
		var items []vtui.AutoCompleteItem
		for dist := 0; dist <= m.maxDist; dist++ {
			for _, hit := range group {
				if limit > 0 && room == 0 {
					return items
				}
				if hit.dist == dist {
					items = append(items, vtui.AutoCompleteItem{Text: hit.name, MatchStart: 0, MatchEnd: hit.end})
					room--
				}
			}
		}
		return items
	}
	items := rank(here)
	rest := rank(elsewhere)
	if len(items) > 0 && len(rest) > 0 {
		items = append(items, vtui.AutoCompleteItem{Separator: true, MatchStart: -1, MatchEnd: -1})
	}
	return append(items, rest...)
}

// typoBudget is how many typos a needle of n runes may carry: none up to
// four runes, where a typo would turn "./b" into "./anything", then one more
// for every four runes.
func typoBudget(n int) int {
	if n <= 0 {
		return 0
	}
	return (n - 1) / 4
}

// prefixMatcher matches one typed text against many history entries. It
// owns every buffer the match needs, so a keystroke allocates once however
// long the history is, and it never reads an entry past the runes that can
// matter.
type prefixMatcher struct {
	needle  []rune
	maxDist int
	hay     []rune
	// back[j], prev[j], cur[j]: distance between needle[:i-2], needle[:i-1],
	// needle[:i] and hay[:j].
	back, prev, cur []int
}

func newPrefixMatcher(text string) *prefixMatcher {
	needle := []rune(text)
	if len(needle) == 0 {
		return nil
	}
	for i, r := range needle {
		needle[i] = unicode.ToLower(r)
	}
	maxDist := typoBudget(len(needle))
	// One rune past the longest prefix that can be within maxDist tells an
	// entry longer than the needle from one that is not.
	window := len(needle) + maxDist + 1
	return &prefixMatcher{
		needle:  needle,
		maxDist: maxDist,
		hay:     make([]rune, 0, window),
		back:    make([]int, window+1),
		prev:    make([]int, window+1),
		cur:     make([]int, window+1),
	}
}

// match is the edit distance between the needle and the closest prefix of
// name, and the rune index where that prefix ends (inclusive). An entry no
// longer than the needle never matches: it has nothing to add, and with
// typos allowed it would otherwise turn up as a truncation of the typed text
// ("./build.sh" for "./build.sh -v") or as the typed text in another case
// ("ls" for "LS"). Two swapped neighbours count as one typo (optimal string
// alignment), the commonest slip when typing fast.
func (m *prefixMatcher) match(name string) (dist, end int, ok bool) {
	m.hay = m.hay[:0]
	for _, r := range name {
		if len(m.hay) == cap(m.hay) {
			break
		}
		m.hay = append(m.hay, unicode.ToLower(r))
	}
	if len(m.hay) <= len(m.needle) {
		return 0, 0, false
	}
	// Only prefixes up to len(needle)+maxDist runes can be within maxDist.
	hay := m.hay[:min(len(m.hay), len(m.needle)+m.maxDist)]

	back, prev, cur := m.back[:len(hay)+1], m.prev[:len(hay)+1], m.cur[:len(hay)+1]
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(m.needle); i++ {
		cur[0] = i
		for j := 1; j <= len(hay); j++ {
			cost := 1
			if m.needle[i-1] == hay[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j-1]+cost, prev[j]+1, cur[j-1]+1)
			if i > 1 && j > 1 && m.needle[i-1] == hay[j-2] && m.needle[i-2] == hay[j-1] {
				cur[j] = min(cur[j], back[j-2]+1)
			}
		}
		back, prev, cur = prev, cur, back
	}

	dist, best := prev[0], 0
	for j := 1; j <= len(hay); j++ {
		if prev[j] < dist {
			dist, best = prev[j], j
		}
	}
	return dist, best - 1, dist <= m.maxDist
}
