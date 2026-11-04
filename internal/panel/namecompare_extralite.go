//go:build extralite

package panel

import (
	"strings"
	"unicode"
)

// The extra-lite profile (unxed/f4#1671) does not link golang.org/x/text/
// collate, whose tables are about 1.2 MB. This comparer keeps what the panels
// depend on: the order of the root collation for the characters that occur in
// file names -- white space, then punctuation and symbols in the order of the
// Unicode collation table, then digits, then letters -- case-insensitively,
// and the exact string as the last tie-break. Letters are ordered by their
// lower-case code point, which is the collation order within one script and
// puts scripts in code-point order (Latin, Greek, Cyrillic...), as the root
// collation does for these; accented Latin letters are not folded onto their
// base letters, which the full collation does.

// punctuationOrder is the root collation order of the ASCII punctuation and
// symbols; a character not listed sorts after them, by code point.
const punctuationOrder = "_-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func nameRank(r rune) (class int, key rune) {
	switch {
	case unicode.IsSpace(r):
		return 0, r
	case unicode.IsDigit(r):
		return 3, r
	case unicode.IsLetter(r):
		return 4, unicode.ToLower(r)
	}
	if i := strings.IndexRune(punctuationOrder, r); i >= 0 {
		return 1, rune(i)
	}
	return 2, r
}

func newNameComparer() func(a, b string) int {
	return func(a, b string) int {
		ra, rb := []rune(a), []rune(b)
		for i := 0; i < len(ra) && i < len(rb); i++ {
			ca, ka := nameRank(ra[i])
			cb, kb := nameRank(rb[i])
			if ca != cb {
				return cmpInt(ca, cb)
			}
			if ka != kb {
				return cmpInt(int(ka), int(kb))
			}
		}
		if len(ra) != len(rb) {
			return cmpInt(len(ra), len(rb))
		}
		return strings.Compare(a, b)
	}
}

func newNamePrimaryComparer() func(a, b string) int {
	return func(a, b string) int {
		ra, rb := []rune(a), []rune(b)
		for i := 0; i < len(ra) && i < len(rb); i++ {
			if isASCIIPunctuation(ra[i]) && isASCIIPunctuation(rb[i]) && ra[i] != rb[i] {
				return cmpInt(int(ra[i]), int(rb[i]))
			}
			ca, ka := nameRank(ra[i])
			cb, kb := nameRank(rb[i])
			if ca != cb {
				return cmpInt(ca, cb)
			}
			if ka != kb {
				return cmpInt(int(ka), int(kb))
			}
		}
		return cmpInt(len(ra), len(rb))
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
