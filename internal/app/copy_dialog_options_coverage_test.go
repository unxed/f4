package app

import (
	"testing"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/vtui"
)

func TestCopyDialogOptionHelpers(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{"", 1},
		{"  3 ", 3},
		{"zero", 1},
		{"0", 1},
		{"-2", 1},
		{"999", fileops.MaxReadAttempts},
	} {
		if got := parseReadAttempts(tc.text); got != tc.want {
			t.Errorf("parseReadAttempts(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}

	if got, want := captionCells("A &B && C"), vtui.StringWidth("A B  C"); got != want {
		t.Fatalf("captionCells = %d, want %d", got, want)
	}

	choices := []string{"one", "two"}
	for _, selected := range []int{-1, 0, 1, 2} {
		combo := newChoiceCombo(choices, selected)
		if combo == nil || !combo.DropdownOnly {
			t.Fatalf("newChoiceCombo(%d) did not create a dropdown", selected)
		}
	}

	label := vtui.NewLabel(0, 0, "Name", nil)
	field := newChoiceCombo(choices, 0)
	row := optionRow(40, label, field, "&Name", 8)
	if row == nil {
		t.Fatal("optionRow returned nil")
	}
}
