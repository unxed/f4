package app

import "testing"

// Maximizing the window does not close the filter window; a panel action does.
func TestActionKeepsFastFind(t *testing.T) {
	if !actionKeepsFastFind("App.ToggleWindowSize") {
		t.Error("Alt+F9 must keep the filter window open")
	}
	for _, name := range []string{"File.Copy", "Panel.Sort", "File.View", ""} {
		if actionKeepsFastFind(name) {
			t.Errorf("%q must still close the search window", name)
		}
	}
}
