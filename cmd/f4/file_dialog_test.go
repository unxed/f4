package main

import "testing"

func TestFileDialogWidth(t *testing.T) {
	cases := []struct{ screen, want int }{
		{0, fileDialogMinWidth},
		{40, 40},
		{50, 50},
		{60, 56},
		{80, 76},
		{200, 196},
	}
	for _, c := range cases {
		if got := fileDialogWidth(c.screen); got != c.want {
			t.Errorf("fileDialogWidth(%d) = %d, want %d", c.screen, got, c.want)
		}
	}
}
