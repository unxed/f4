package main

import "testing"

func TestFileDialogWidth(t *testing.T) {
	cases := []struct{ screen, want int }{
		{0, 50},
		{40, 40},
		{50, 40},
		{60, 40},
		{80, 40},
		{120, 60},
		{200, 100},
	}
	for _, c := range cases {
		if got := fileDialogWidth(c.screen); got != c.want {
			t.Errorf("fileDialogWidth(%d) = %d, want %d", c.screen, got, c.want)
		}
	}
}
