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
	return c.CompareString
}
