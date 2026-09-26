package panel

import "strings"

// This file implements the "numeric" (a.k.a. natural) name-sort toggle
// requested in f4#1471: with SortNumeric on, digit runs inside a file name
// are compared as numbers rather than as plain text, so "2.Track_2" sorts
// before "10.Track_10" instead of after it.

// isASCIIDigit reports whether r is a plain '0'-'9' digit. Only ASCII digits
// are treated as a numeric run — matches every natural-sort implementation
// this feature is modeled on (Far3, Explorer, Nautilus) and keeps the split
// trivially reversible.
func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// splitDigitRuns splits s into alternating runs of non-digit and digit
// characters. The result always starts with a (possibly empty) non-digit
// run, so index parity tells the run's kind without looking at its content:
// even indices are text, odd indices are digits. That invariant is what lets
// naturalCompare zip two split results together positionally.
func splitDigitRuns(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var runs []string
	i := 0
	for i < len(runes) {
		start := i
		digit := isASCIIDigit(runes[i])
		for i < len(runes) && isASCIIDigit(runes[i]) == digit {
			i++
		}
		runs = append(runs, string(runes[start:i]))
	}
	if isASCIIDigit(runes[0]) {
		runs = append([]string{""}, runs...)
	}
	return runs
}

// compareDigitRun compares two runs of ASCII digits as numbers, without ever
// parsing them into an integer: a file name's digit run can be arbitrarily
// long and need not fit any fixed-width integer type. Leading zeros carry no
// numeric weight ("007" == "7"), so they are stripped first; what remains is
// compared by length (a longer remainder is the larger number) and then, once
// the lengths match, byte by byte — lexicographic order over equal-length
// digit strings is exactly numeric order.
func compareDigitRun(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// naturalCompare orders a and b the way a "numeric" panel sort should: each
// run of ASCII digits is compared as a number, everything else is handed to
// textCompare unchanged. textCompare is a parameter rather than a hardcoded
// strings.Compare so the caller can reuse whatever collation/case-sensitivity
// the plain name comparator already applies — numeric sort is meant to change
// how digits compare, not introduce a second, independent case-sensitivity
// rule. Returns <0, 0 or >0 the way strings.Compare does.
func naturalCompare(a, b string, textCompare func(x, y string) int) int {
	runsA := splitDigitRuns(a)
	runsB := splitDigitRuns(b)
	n := len(runsA)
	if len(runsB) < n {
		n = len(runsB)
	}
	for i := 0; i < n; i++ {
		if i%2 == 1 {
			if cmp := compareDigitRun(runsA[i], runsB[i]); cmp != 0 {
				return cmp
			}
			continue
		}
		if cmp := textCompare(runsA[i], runsB[i]); cmp != 0 {
			return cmp
		}
	}
	switch {
	case len(runsA) < len(runsB):
		return -1
	case len(runsA) > len(runsB):
		return 1
	default:
		return 0
	}
}
