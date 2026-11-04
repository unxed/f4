//go:build !extralite

package panel

import (
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// newNameComparer returns the comparison of file names in the panels: the
// Unicode collation of the root locale, case-insensitive, with ties broken by
// the exact string. Every call returns its own comparer, because a collator
// reuses iterator state and is not safe for concurrent use.
func newNameComparer() func(a, b string) int {
	c := collate.New(language.Und, collate.IgnoreCase, collate.Force)
	return c.CompareString
}

// newNamePrimaryComparer returns the name collation without its exact-string
// tie-break. Numeric sorting uses it to compare text and number runs first,
// then falls back to newNameComparer for a stable total order.
func newNamePrimaryComparer() func(a, b string) int {
	c := collate.New(language.Und, collate.IgnoreCase)
	return func(a, b string) int {
		ra, rb := []rune(a), []rune(b)
		n := len(ra)
		if len(rb) < n {
			n = len(rb)
		}
		for i := 0; i < n; i++ {
			if ra[i] == rb[i] {
				continue
			}
			if isASCIIPunctuation(ra[i]) && isASCIIPunctuation(rb[i]) {
				if ra[i] < rb[i] {
					return -1
				}
				return 1
			}
			// Preserve root collation for every difference except two ASCII
			// punctuation characters, whose numeric-sort order is by code point.
			if c.CompareString(string(ra[i]), string(rb[i])) != 0 {
				return c.CompareString(string(ra[i:]), string(rb[i:]))
			}
		}
		return c.CompareString(a, b)
	}
}
