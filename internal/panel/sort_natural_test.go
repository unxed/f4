package panel

import (
	"strings"
	"testing"
)

// caseInsensitiveTextCompare is a stand-in for the real name comparator (a
// golang.org/x/text/collate.Collator in production) so these tests exercise
// naturalCompare's own digit-run logic without depending on that package.
func caseInsensitiveTextCompare(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func TestSplitDigitRuns(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"abc", []string{"abc"}},
		{"123", []string{"", "123"}},
		{"1.Track_1", []string{"", "1", ".Track_", "1"}},
		{"Track_1", []string{"Track_", "1"}},
		{"a1b2", []string{"a", "1", "b", "2"}},
	}
	for _, tc := range cases {
		got := splitDigitRuns(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitDigitRuns(%q) = %#v, want %#v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitDigitRuns(%q) = %#v, want %#v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestCompareDigitRun(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1", "2", -1},
		{"2", "1", 1},
		{"9", "10", -1},
		{"10", "9", 1},
		{"007", "7", 0},
		{"007", "8", -1},
		{"00", "0", 0},
		{"", "", 0},
		{"123", "123", 0},
	}
	for _, tc := range cases {
		if got := compareDigitRun(tc.a, tc.b); sign(got) != sign(tc.want) {
			t.Errorf("compareDigitRun(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

func TestNaturalCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// The motivating example from f4#1471: numbered music tracks.
		{"1.Track_1", "2.Track_2", -1},
		{"2.Track_2", "10.Track_10", -1},
		{"1.Track_1", "10.Track_10", -1},
		{"10.Track_10", "1.Track_1", 1},
		// Mixed-length digit runs elsewhere in the name.
		{"img2.png", "img10.png", -1},
		{"img10.png", "img2.png", 1},
		{"file2", "file2", 0},
		// Leading zeros carry no numeric weight.
		{"file007", "file7", 0},
		{"file007", "file8", -1},
		{"file07", "file7x", -1}, // equal numeric prefix, "" < "x" for the rest
		// Non-digit-only names fall back to plain text comparison.
		{"banana", "apple", 1},
		{"apple", "banana", -1},
		{"apple", "apple", 0},
		// Empty names never panic and compare equal.
		{"", "", 0},
		{"", "a", -1},
		{"a", "", 1},
		// Case-insensitivity is delegated entirely to textCompare.
		{"Apple", "apple", 0},
		{"Track_2", "track_10", -1},
		// A name that is a text-prefix of another, differing only by a
		// trailing digit run, sorts before it (shorter run list wins).
		{"track", "track1", -1},
		{"track1", "track", 1},
	}
	for _, tc := range cases {
		got := naturalCompare(tc.a, tc.b, caseInsensitiveTextCompare)
		if sign(got) != sign(tc.want) {
			t.Errorf("naturalCompare(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
		// Symmetry: swapping the arguments must flip the sign.
		if swapped := naturalCompare(tc.b, tc.a, caseInsensitiveTextCompare); sign(swapped) != -sign(got) {
			t.Errorf("naturalCompare(%q, %q) = %d is not antisymmetric with (%q, %q) = %d",
				tc.a, tc.b, got, tc.b, tc.a, swapped)
		}
	}
}

func TestNaturalCompareOrdersTrackNumbersNumerically(t *testing.T) {
	names := []string{"10.Track_10", "1.Track_1", "2.Track_2"}
	want := []string{"1.Track_1", "2.Track_2", "10.Track_10"}

	// Bubble sort keeps the test independent of sort.Slice's stability
	// guarantees and easy to read as "is this order already correct".
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names)-1-i; j++ {
			// #nosec G602 -- j < len(names)-1-i guarantees j+1 < len(names); a
			// standard bubble-sort bound gosec's taint analysis can't verify.
			if naturalCompare(names[j], names[j+1], caseInsensitiveTextCompare) > 0 {
				names[j], names[j+1] = names[j+1], names[j]
			}
		}
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("sorted = %v, want %v", names, want)
		}
	}
}
