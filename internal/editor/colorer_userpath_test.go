package editor

import (
	"path/filepath"
	"runtime"
	"testing"
)

// A path a user writes in the Colorer settings is a path the way a shell reads
// it (#277).
func TestExpandColorerUserPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("F4_TEST_STYLES", filepath.Join(home, "styles"))

	cases := []struct{ in, want string }{
		{"~/.config/f4/colorer/my.xml", filepath.Join(home, ".config/f4/colorer/my.xml")},
		{"~", home},
		{"$F4_TEST_STYLES/dark.hrd", filepath.Join(home, "styles") + "/dark.hrd"},
		{"${F4_TEST_STYLES}/dark.hrd", filepath.Join(home, "styles") + "/dark.hrd"},
		{"/abs/path/x.hrc", "/abs/path/x.hrc"},
		{"$F4_TEST_UNSET_VARIABLE/x", "$F4_TEST_UNSET_VARIABLE/x"},
		{"~other/x", "~other/x"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct{ in, want string }{`%F4_TEST_STYLES%\dark.hrd`, filepath.Join(home, "styles") + `\dark.hrd`})
	}
	for _, tc := range cases {
		if got := expandColorerUserPath(tc.in); got != tc.want {
			t.Errorf("expandColorerUserPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
