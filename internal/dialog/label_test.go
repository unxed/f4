package dialog

import "testing"

func TestPadLabelTo(t *testing.T) {
	for _, tc := range []struct {
		text  string
		width int
		want  string
	}{
		{"abc", 5, "abc  "}, {"界", 4, "界  "}, {"long", 2, "long"}, {"abc", -1, "abc"},
	} {
		if got := PadLabelTo(tc.text, tc.width); got != tc.want {
			t.Errorf("PadLabelTo(%q, %d)=%q, want %q", tc.text, tc.width, got, tc.want)
		}
	}
}
