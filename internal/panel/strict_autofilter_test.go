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

// f4 #1709 and #2186: strict mode searches exactly what was typed whenever
// that finds a name ("сфы" typed on a Russian layout must not also find
// "Cas" then). A text that finds no name is tried in the other layout, as
// far2l does, unless "Quick search in the other layout" is off.
func TestFastFindMatch_StrictTriesOtherLayoutOnlyWhenTypedFindsNothing(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.PanelStrictAutoFilter = true
	config.App.PanelQuickSearchXLat = true

	searching := func(names ...string) *FileSystemPanel {
		fp := &FileSystemPanel{}
		for _, name := range names {
			fp.Entries = append(fp.Entries, &FileEntry{VFSItem: vfs.VFSItem{Name: name}})
		}
		fp.FastFindMode = true
		fp.FastFindStr = "*сфы"
		return fp
	}

	fp := searching("Cas", "сфыв")
	if _, _, ok := fp.fastFindMatch("сфыв"); !ok {
		t.Error("strict matcher rejected the exact substring \"сфы\"")
	}
	if _, _, ok := fp.fastFindMatch("Cas"); ok {
		t.Error("strict matcher found \"Cas\" for \"сфы\" although a name holds \"сфы\" as typed")
	}

	fp = searching("Cas")
	if _, _, ok := fp.fastFindMatch("Cas"); !ok {
		t.Error("strict matcher did not try \"сфы\" in the other layout when no name holds it")
	}
	config.App.PanelQuickSearchXLat = false
	if _, _, ok := fp.fastFindMatch("Cas"); ok {
		t.Error("the other layout was tried with PanelQuickSearchXLat off")
	}
}

func TestFastFindRejectsCharacterWithoutMatch(t *testing.T) {
	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// The fuzzy matcher of the default mode tolerates a typo, so one more
	// character always stays within its distance of "test" and nothing is
	// rejected there; the strict matcher is the one that can reject.
	for _, strict := range []bool{true} {
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
		for _, candidate := range "abcdefghijklmnopqrstuvwxyz0123456789#%" {
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
