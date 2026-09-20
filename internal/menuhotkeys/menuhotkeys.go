// Package menuhotkeys gives the items of every menu their own hotkey.
//
// Menu texts are built from translated labels, most of them marked with the
// hotkey letter by the code that builds the menu (the first letter of the
// label) and a few by the translator. Nothing looked at the menu as a whole, so
// "Brief" and "Background" both answered to B and the second could not be
// reached from the keyboard (#1258). Unique looks at each menu once it is
// built and moves the hotkey of every item that repeats an earlier one.
package menuhotkeys

import (
	"strings"
	"unicode"

	"github.com/unxed/vtui"
)

// UniqueBar makes the hotkeys distinct in the menu bar itself (the letters
// that open each drop-down with Alt) and, separately, in every menu below it.
func UniqueBar(bar []vtui.MenuBarItem) {
	labels := make([]*string, 0, len(bar))
	for i := range bar {
		labels = append(labels, &bar[i].Label)
		Unique(bar[i].SubItems)
	}
	assign(labels)
}

// Unique makes the hotkeys of items distinct, and does the same for every
// submenu. An item keeps its hotkey unless an item above it has the same one;
// then the hotkey moves to another letter of its own text that no item of the
// menu uses, and when there is none the item has no hotkey rather than a
// repeated one. Items without a hotkey are left without: the hotkey of a menu
// is not something to invent for rows that were built without one.
func Unique(items []vtui.MenuItem) {
	texts := make([]*string, 0, len(items))
	for i := range items {
		if len(items[i].SubItems) > 0 {
			Unique(items[i].SubItems)
		}
		if items[i].Separator || items[i].Text == "" {
			continue
		}
		texts = append(texts, &items[i].Text)
	}
	assign(texts)
}

// autoMarker precedes the "&" of a hotkey that was only the first letter of a
// label, not a letter somebody chose. It never leaves this package: assign
// takes it out.
const autoMarker = "\uE000"

// Auto marks text with the hotkey of its first letter, the way a menu builder
// does for a label nobody marked. Such a hotkey gives way to one that a
// translator put in a label of the same menu, wherever the two are: Unique
// settles the marked labels first.
func Auto(text string) string {
	if vtui.ExtractHotkey(text) != 0 {
		return text // the label already says which letter it wants
	}
	return autoMarker + "&" + text
}

// candidate is a letter of a text a hotkey could sit on, and where in the text
// that is.
type candidate struct {
	at   int
	char rune // lower case
}

// candidates lists the letters of s a hotkey could take, best first: the first
// letter of a word reads best, then the letters that carry the sound of the
// word (not the vowels), then any letter or digit. A letter is listed once.
func candidates(s string) []candidate {
	const (
		initial = iota
		consonant
		other
	)
	best := map[rune]candidate{}
	rank := map[rune]int{}
	var order []rune
	prev := rune(0)
	for at, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			lower := unicode.ToLower(r)
			kind := other
			switch {
			case prev == 0 || (!unicode.IsLetter(prev) && !unicode.IsDigit(prev)):
				kind = initial
			case !strings.ContainsRune(vowels, lower):
				kind = consonant
			}
			// A letter counts as the best thing it is anywhere in the text: the
			// s of "Use sort" is the first letter of a word.
			if known, seen := rank[lower]; !seen {
				order = append(order, lower)
				rank[lower] = kind
				best[lower] = candidate{at, lower}
			} else if kind < known {
				rank[lower] = kind
				best[lower] = candidate{at, lower}
			}
		}
		prev = r
	}
	var out []candidate
	for _, kind := range []int{initial, consonant, other} {
		for _, lower := range order {
			if rank[lower] == kind {
				out = append(out, best[lower])
			}
		}
	}
	return out
}

const vowels = "aeiouyаеёиоуыэюяіїєѹ"

// entry is one item of a menu that has a hotkey.
type entry struct {
	text    *string
	plain   string // the text without its hotkey marker
	letter  rune   // the hotkey it has now, 0 for none
	fixed   bool   // marked by a translator and first to claim its letter: moved only as a last resort
	cands   []candidate
	changed bool
}

func assign(texts []*string) {
	var entries []*entry
	var auto []bool
	for _, text := range texts {
		isAuto := strings.Contains(*text, autoMarker)
		if isAuto {
			*text = strings.ReplaceAll(*text, autoMarker, "")
		}
		letter := vtui.ExtractHotkey(*text)
		if letter == 0 {
			continue
		}
		plain := withoutHotkey(*text)
		entries = append(entries, &entry{text: text, plain: plain, letter: letter, cands: candidates(plain)})
		auto = append(auto, isAuto)
	}

	// The letters somebody chose go first, then the defaults, each in menu
	// order; whoever finds its letter taken is a repeat.
	owner := make(map[int32]*entry, len(entries))
	var repeats []*entry
	for _, wantAuto := range []bool{false, true} {
		for i, e := range entries {
			if auto[i] != wantAuto {
				continue
			}
			if _, taken := owner[int32(e.letter)]; taken {
				repeats = append(repeats, e)
				e.letter = 0
				continue
			}
			owner[int32(e.letter)] = e
			e.fixed = !wantAuto
		}
	}
	// A repeat takes the best letter still free.
	var stranded []*entry
	for _, e := range repeats {
		e.changed = true
		for _, c := range e.cands {
			if _, taken := owner[int32(c.char)]; !taken {
				e.letter = c.char
				owner[int32(c.char)] = e
				break
			}
		}
		if e.letter == 0 {
			stranded = append(stranded, e)
		}
	}
	// One that found none may still be given a letter by moving items that had
	// only settled for theirs: a big menu of short words runs out of letters
	// for whoever comes last, though a way to give every item one exists. Free
	// letters, and letters of items already moved, are tried before the untouched
	// ones, and a letter somebody chose is moved only as a last resort.
	for _, e := range stranded {
		visited := map[rune]bool{}
		place(e, owner, visited)
	}

	for _, e := range entries {
		if !e.changed {
			continue
		}
		*e.text = e.plain
		for _, c := range e.cands {
			if c.char == e.letter {
				*e.text = e.plain[:c.at] + "&" + e.plain[c.at:]
				break
			}
		}
	}
}

// place gives e a letter, moving other items to other letters if that is what
// it takes. It reports whether it did. visited holds the letters already tried
// in this search, which keeps it from going in circles.
func place(e *entry, owner map[int32]*entry, visited map[rune]bool) bool {
	take := func(c rune) {
		if e.letter != 0 && owner[int32(e.letter)] == e {
			delete(owner, int32(e.letter))
		}
		owner[int32(c)] = e
		e.letter = c
		e.changed = true
	}
	// Free letters first.
	for _, c := range e.cands {
		if visited[c.char] {
			continue
		}
		if _, taken := owner[int32(c.char)]; !taken {
			visited[c.char] = true
			take(c.char)
			return true
		}
	}
	// Then the letters of items that were already moved, then of the untouched
	// defaults, and only last of the letters a translator chose: those are moved
	// only when the alternative is an item with no hotkey at all.
	for _, pass := range []struct{ fixed, moved bool }{{false, true}, {false, false}, {true, false}, {true, true}} {
		for _, c := range e.cands {
			if visited[c.char] {
				continue
			}
			holder := owner[int32(c.char)]
			if holder == nil || holder.fixed != pass.fixed || holder.changed != pass.moved {
				continue
			}
			visited[c.char] = true
			if place(holder, owner, visited) {
				// The holder has left this letter for another one.
				take(c.char)
				return true
			}
		}
	}
	return false
}

// withoutHotkey drops the first hotkey marker of s. A doubled ampersand is a
// literal one and stays.
func withoutHotkey(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '&' {
			i++
			continue
		}
		return s[:i] + s[i+1:]
	}
	return s
}
