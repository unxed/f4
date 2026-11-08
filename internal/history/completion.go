package history

import (
	"unicode"

	"github.com/unxed/vtui"
)

// CommandCompletions returns the command line's history rows for text, the
// way far2l's History::GetAllSimilar picks them: an entry must begin with
// text (case does not matter), the entry equal to text is left out, and the
// commands run in cwd come first, newest first, then a separator, then the
// rest. f4 also forgives typos, as the menu always has here, but only at the
// start of an entry: "./bulid" still finds "./build.sh", while "./" no longer
// offers "cd ../x" or "git add ./y". Records are expected newest first.
func CommandCompletions(records []HistoryRecord, text, cwd string) []vtui.AutoCompleteItem {
	needle := foldRunes(text)
	if len(needle) == 0 {
		return nil
	}
	maxDist := typoBudget(len(needle))

	type match struct {
		name string
		dist int
		end  int
	}
	var here, elsewhere []match
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		name := record.Name
		if name == "" || name == text || seen[name] || record.IsPluginEntry() {
			continue
		}
		dist, end, ok := prefixDistance(needle, foldRunes(name), maxDist)
		if !ok {
			continue
		}
		seen[name] = true
		m := match{name: name, dist: dist, end: end}
		if cwd != "" && record.Directory() == cwd {
			here = append(here, m)
		} else {
			elsewhere = append(elsewhere, m)
		}
	}

	// An exact prefix beats a typo; records are already newest first, so a
	// stable pass by distance keeps recency inside each distance.
	rank := func(group []match) []vtui.AutoCompleteItem {
		var items []vtui.AutoCompleteItem
		for dist := 0; dist <= maxDist; dist++ {
			for _, m := range group {
				if m.dist == dist {
					items = append(items, vtui.AutoCompleteItem{Text: m.name, MatchStart: 0, MatchEnd: m.end})
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

// prefixDistance is the edit distance between needle and the closest prefix
// of hay, and the rune index where that prefix ends (inclusive). Both are
// already case folded. Two swapped neighbours count as one typo (optimal
// string alignment), the commonest slip when typing fast. Only prefixes up
// to len(needle)+maxDist runes can be within maxDist, so the table never
// grows past that.
func prefixDistance(needle, hay []rune, maxDist int) (dist, end int, ok bool) {
	limit := len(needle) + maxDist
	if len(hay) < limit {
		limit = len(hay)
	}
	hay = hay[:limit]

	// back[j], prev[j], cur[j]: distance between needle[:i-2], needle[:i-1],
	// needle[:i] and hay[:j].
	back := make([]int, len(hay)+1)
	prev := make([]int, len(hay)+1)
	cur := make([]int, len(hay)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(needle); i++ {
		cur[0] = i
		for j := 1; j <= len(hay); j++ {
			cost := 1
			if needle[i-1] == hay[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j-1]+cost, prev[j]+1, cur[j-1]+1)
			if i > 1 && j > 1 && needle[i-1] == hay[j-2] && needle[i-2] == hay[j-1] {
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
	return dist, best - 1, dist <= maxDist
}

func foldRunes(s string) []rune {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	return runes
}
