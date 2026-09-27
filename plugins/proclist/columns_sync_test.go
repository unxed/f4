//go:build linux || windows || darwin

package proclist

import "testing"

// TestAllColumnSpecsMatchesValidColumnKeys guards the split settings.go's
// own comment explains: allColumnSpecs (panel.go, this build only) and
// validColumnKeys (settings.go, every build) must name the exact same
// columns, in the same order, or Settings.VisibleColumns' default
// (defaultColumnKeys) and validation (columnKeysValid) would silently drift
// from what the table and its ProcList.Config dialog actually offer.
func TestAllColumnSpecsMatchesValidColumnKeys(t *testing.T) {
	if len(allColumnSpecs) != len(validColumnKeys) {
		t.Fatalf("allColumnSpecs has %d entries, validColumnKeys has %d", len(allColumnSpecs), len(validColumnKeys))
	}
	for i, c := range allColumnSpecs {
		if c.key != validColumnKeys[i] {
			t.Errorf("allColumnSpecs[%d].key = %q, validColumnKeys[%d] = %q", i, c.key, i, validColumnKeys[i])
		}
	}
}
