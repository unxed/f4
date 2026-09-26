package app

import "testing"

// TestCtrlRowActionsHaveLabelKey guards the fix for f4#1218 (the "Ctrl+F1..
// Ctrl+F7 keybar captions are not translated" follow-up). The default Ctrl
// row is always bound (see actions_table.go), so keymap.KeyBarLabelsForArea
// always resolves these keys through the bound action's DisplayLabel and
// never through the KeyBar.CtrlF* i18n fallbacks in panel/frame.go. Without a
// LabelKey, DisplayLabel falls back to the English Label unconditionally,
// which is exactly how the Russian keybar ended up showing "Toggle Le",
// "Sort by N" and so on regardless of the active language.
func TestCtrlRowActionsHaveLabelKey(t *testing.T) {
	cases := []struct {
		action  string
		wantKey string
	}{
		{"Panel.ToggleLeftPanel", "Action.Panel.ToggleLeftPanel"},
		{"Panel.ToggleRightPanel", "Action.Panel.ToggleRightPanel"},
		{"Panel.SortByName", "Action.Panel.SortByName"},
		{"Panel.SortByExt", "Action.Panel.SortByExt"},
		{"Panel.SortByTime", "Action.Panel.SortByTime"},
		{"Panel.SortBySize", "Action.Panel.SortBySize"},
		{"Panel.SortUnsorted", "Action.Panel.SortUnsorted"},
	}
	for _, tc := range cases {
		a, ok := GetAction(tc.action)
		if !ok {
			t.Errorf("action %s is not registered", tc.action)
			continue
		}
		if a.LabelKey != tc.wantKey {
			t.Errorf("%s.LabelKey = %q, want %q (the Ctrl-row keybar caption would leak English otherwise)",
				tc.action, a.LabelKey, tc.wantKey)
		}
	}
}
