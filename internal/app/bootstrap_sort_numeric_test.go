package app

import (
	"testing"

	"github.com/unxed/f4/internal/panel"
)

func TestLegacyNumericSortPreferenceIncludesEverySavedPanel(t *testing.T) {
	tests := []struct {
		name       string
		left       bool
		right      bool
		workspaces []panel.WorkspaceSessionState
		want       bool
	}{
		{name: "disabled by default"},
		{name: "left legacy panel", left: true, want: true},
		{name: "right legacy panel", right: true, want: true},
		{
			name:       "saved workspace left panel",
			workspaces: []panel.WorkspaceSessionState{{Left: panel.PanelSessionState{SortNumeric: true}}},
			want:       true,
		},
		{
			name:       "saved workspace right panel",
			workspaces: []panel.WorkspaceSessionState{{Right: panel.PanelSessionState{SortNumeric: true}}},
			want:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := globalNumericSortPreference(false, false, test.left, test.right, test.workspaces); got != test.want {
				t.Fatalf("globalNumericSortPreference() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGlobalNumericSortPreferenceHonorsExplicitValue(t *testing.T) {
	workspaces := []panel.WorkspaceSessionState{{Left: panel.PanelSessionState{SortNumeric: true}}}
	if got := globalNumericSortPreference(true, false, true, false, workspaces); got {
		t.Fatal("legacy numeric-sort values overrode the explicitly saved false setting")
	}
	if got := globalNumericSortPreference(true, true, false, false, nil); !got {
		t.Fatal("explicitly saved true setting was not preserved")
	}
}
