package panel

import "testing"

// TestDriveBookmarkDialogWidth mirrors dialog.TestFileDialogWidth: the
// add/edit drive link dialog's fields are laid out once and never reflow, so
// f4#1148 asked for a fixed proportion of the screen (half its width, with a
// floor) instead of letting the user stretch the dialog and leave the fields
// behind.
func TestDriveBookmarkDialogWidth(t *testing.T) {
	cases := []struct{ screen, want int }{
		{0, driveBookmarkDialogMinWidth},
		{60, driveBookmarkDialogMinWidth},
		{128, driveBookmarkDialogMinWidth},
		{140, 70},
		{200, 100},
	}
	for _, c := range cases {
		if got := driveBookmarkDialogWidth(c.screen); got != c.want {
			t.Errorf("driveBookmarkDialogWidth(%d) = %d, want %d", c.screen, got, c.want)
		}
	}
}
