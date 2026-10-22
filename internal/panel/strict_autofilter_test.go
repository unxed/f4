package panel

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// f4 #1405: PanelStrictAutoFilter opts fastFindMatch out of the fuzzy
// matcher's typo tolerance. "tes" and "des" are one substitution apart, so
// the default (fuzzy) matcher accepts "destroy" for query "tes"; strict mode
// must reject it while still accepting the exact substring in "test".
func TestFastFindMatch_StrictRejectsOneEditTypo(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()

	fp := &FileSystemPanel{}
	fp.FastFindMode = true
	fp.FastFindStr = "*tes"

	config.App.PanelStrictAutoFilter = false
	if _, _, ok := fp.fastFindMatch("test"); !ok {
		t.Error("fuzzy matcher rejected the exact substring \"tes\" in \"test\"")
	}
	if _, _, ok := fp.fastFindMatch("destroy"); !ok {
		t.Error("fuzzy (default) matcher rejected \"tes\" against \"destroy\" (1 edit) — is the tolerance gone?")
	}

	// Changing the setting must invalidate the cached matchers built above.
	config.App.PanelStrictAutoFilter = true
	if _, _, ok := fp.fastFindMatch("test"); !ok {
		t.Error("strict matcher rejected the exact substring \"tes\" in \"test\"")
	}
	if _, _, ok := fp.fastFindMatch("destroy"); ok {
		t.Error("strict matcher accepted \"tes\" against \"destroy\" (1 edit away) — typo tolerance leaked through")
	}
}

// f4 #1709: the keyboard-layout transcoding ("сфы" typed on a Russian layout
// also searches "cas") is a tolerance, so strict mode must not apply it: it
// searches exactly what was typed.
func TestFastFindMatch_StrictDoesNotTranscodeLayout(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()

	fp := &FileSystemPanel{}
	fp.FastFindMode = true
	fp.FastFindStr = "*сфы"

	config.App.PanelStrictAutoFilter = false
	if _, _, ok := fp.fastFindMatch("Cas"); !ok {
		t.Skip("layout transcoding is unavailable in this environment")
	}

	config.App.PanelStrictAutoFilter = true
	if _, _, ok := fp.fastFindMatch("Cas"); ok {
		t.Error("strict matcher found \"Cas\" for \"сфы\": layout transcoding leaked through")
	}
	if _, _, ok := fp.fastFindMatch("сфыв"); !ok {
		t.Error("strict matcher rejected the exact substring \"сфы\"")
	}
}

func TestFastFindRejectsCharacterWithoutMatch(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	for _, strict := range []bool{false, true} {
		config.App.PanelStrictAutoFilter = strict
		fp := NewFileSystemPanel(0, 0, 80, 24, vfs.NewOSVFS(t.TempDir()))
		waitForLoad(t, fp)
		fp.Entries = []*FileEntry{
			{VFSItem: vfs.VFSItem{Name: "test"}},
			{VFSItem: vfs.VFSItem{Name: "destroy"}},
		}
		fp.Refresh()
		fp.FastFindMode = true
		fp.FastFindStr = "tes"
		selected := "test"
		if !strict {
			selected = "destroy"
		}
		for i, entry := range fp.Entries {
			if entry.Name == selected {
				fp.SetCursorIndex(i)
				break
			}
		}

		var rejected rune
		for candidate := 'a'; candidate <= 'z'; candidate++ {
			fp.FastFindStr = "tes" + string(candidate)
			if !fp.fastFindHasMatches() {
				rejected = candidate
				break
			}
		}
		if rejected == 0 {
			t.Fatalf("strict=%t test setup found no unmatched character", strict)
		}

		fp.FastFindStr = "tes"
		fp.ProcessKey(&vtinput.InputEvent{
			Type:    vtinput.KeyEventType,
			KeyDown: true,
			Char:    rejected,
		})
		if fp.FastFindStr != "tes" {
			t.Errorf("strict=%t search string = %q, want prior string tes", strict, fp.FastFindStr)
		}
		if got := fp.GetSelectedName(); got != selected {
			t.Errorf("strict=%t selection = %q, want prior selection %q", strict, got, selected)
		}
	}
}

func TestFastFindWildcardsMatchFilenamePrefixes(t *testing.T) {
	fp := &FileSystemPanel{FastFindMode: true}
	tests := []struct {
		query string
		name  string
		want  bool
	}{
		{query: "*lo", name: "c.log", want: true},
		{query: "te*t", name: "test.txt", want: true},
		{query: "ab*cd", name: "abcd.txt", want: true},
		{query: "ab*cd", name: "abXYZcd.txt", want: true},
		{query: "te?t", name: "test.txt", want: true},
		{query: "te?t", name: "teast.txt", want: false},
		{query: "?айл", name: "файл", want: true},
		{query: "[a]*", name: "[a]file", want: true},
	}
	for _, tt := range tests {
		fp.FastFindStr = tt.query
		if _, _, ok := fp.fastFindMatch(tt.name); ok != tt.want {
			t.Errorf("query %q against %q matched=%t, want %t", tt.query, tt.name, ok, tt.want)
		}
	}
}
