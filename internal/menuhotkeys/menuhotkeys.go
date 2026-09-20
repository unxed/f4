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

func assign(texts []*string) {
	claimed := make(map[rune]bool, len(texts))
	var repeats []*string
	for _, text := range texts {
		hotkey := vtui.ExtractHotkey(*text)
		if hotkey == 0 {
			continue
		}
		if claimed[hotkey] {
			repeats = append(repeats, text)
			continue
		}
		claimed[hotkey] = true
	}
	for _, text := range repeats {
		plain := withoutHotkey(*text)
		if marked, hotkey, ok := withFreeHotkey(plain, claimed); ok {
			*text = marked
			claimed[hotkey] = true
			continue
		}
		*text = plain
	}
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

// withFreeHotkey marks a letter of s that is not in claimed. The first letter
// of a word reads best, then the letters that carry the sound of the word (not
// the vowels), then any letter or digit.
func withFreeHotkey(s string, claimed map[rune]bool) (string, rune, bool) {
	type candidate struct {
		at   int
		char rune
	}
	var initials, consonants, rest []candidate
	prev := rune(0)
	for at, r := range s {
		isText := unicode.IsLetter(r) || unicode.IsDigit(r)
		if isText && !claimed[unicode.ToLower(r)] {
			c := candidate{at, r}
			switch {
			case prev == 0 || !(unicode.IsLetter(prev) || unicode.IsDigit(prev)):
				initials = append(initials, c)
			case !strings.ContainsRune(vowels, unicode.ToLower(r)):
				consonants = append(consonants, c)
			default:
				rest = append(rest, c)
			}
		}
		prev = r
	}
	for _, group := range [][]candidate{initials, consonants, rest} {
		if len(group) == 0 {
			continue
		}
		c := group[0]
		return s[:c.at] + "&" + s[c.at:], unicode.ToLower(c.char), true
	}
	return s, 0, false
}

const vowels = "aeiouyаеёиоуыэюяіїєѹ"
