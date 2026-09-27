//go:build linux || windows || darwin

package proclist

import (
	"errors"
	"testing"
)

func TestAppendDetailsSectionRendersLinesErrorAndEmpty(t *testing.T) {
	got := appendDetailsSection(nil, "Header:", procDetailsSection{lines: []string{"a", "b"}})
	want := []string{"Header:", "  a", "  b"}
	if !equalStrings(got, want) {
		t.Fatalf("lines case = %#v, want %#v", got, want)
	}

	got = appendDetailsSection(nil, "Header:", procDetailsSection{err: errors.New("boom")})
	if len(got) != 2 || got[0] != "Header:" {
		t.Fatalf("error case = %#v", got)
	}

	got = appendDetailsSection(nil, "Header:", procDetailsSection{})
	if len(got) != 2 || got[0] != "Header:" {
		t.Fatalf("empty case = %#v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
