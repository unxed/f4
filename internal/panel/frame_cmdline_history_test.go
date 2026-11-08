package panel

import (
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/vtui"
)

func newHistoryTestFrame(t *testing.T) (*PanelsFrame, *history.F4HistoryProvider) {
	t.Helper()
	oldConfig := config.App
	t.Cleanup(func() { config.App = oldConfig })
	oldHistory := vtui.GlobalHistoryProvider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = oldHistory })
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	hp := history.NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	vtui.GlobalHistoryProvider = hp
	pf := NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	return pf, hp
}

// The completion menu re-loaded and re-copied the whole history on every
// keystroke. It now keeps the records until the history changes, and must
// see every change: a save through the provider, and vtui.Edit.AddHistory,
// which replaces the line's list without saving.
func TestCommandRecordsAreCachedUntilTheHistoryChanges(t *testing.T) {
	pf, hp := newHistoryTestFrame(t)
	hp.SaveRichHistory("cmdline", []history.HistoryRecord{{Name: "./a.sh"}})
	pf.AttachCommandHistory()

	first := pf.commandRecords()
	if len(first) != 1 || first[0].Name != "./a.sh" {
		t.Fatalf("records = %+v", first)
	}
	if again := pf.commandRecords(); &again[0] != &first[0] {
		t.Fatal("unchanged history was loaded again")
	}

	hp.SaveRichHistory("cmdline", []history.HistoryRecord{{Name: "./b.sh"}, {Name: "./a.sh"}})
	if got := pf.commandRecords(); len(got) != 2 || got[0].Name != "./b.sh" {
		t.Fatalf("after a save, records = %+v", got)
	}

	// No rich bucket: the records come from the line's own list.
	hp.SaveRichHistory("cmdline", nil)
	pf.CmdLine.Edit.History = nil
	pf.CmdLine.Edit.AddHistory("./c.sh")
	if got := pf.commandRecords(); len(got) != 1 || got[0].Name != "./c.sh" {
		t.Fatalf("after AddHistory, records = %+v", got)
	}
	pf.CmdLine.Edit.AddHistory("./d.sh")
	if got := pf.commandRecords(); len(got) != 2 || got[0].Name != "./d.sh" {
		t.Fatalf("after a second AddHistory, records = %+v", got)
	}
}

// In per-category mode history is one category: its rows across both groups
// stay within the configured cap.
func TestCommandHistoryMatchesKeepHistoryWithinItsCap(t *testing.T) {
	pf, hp := newHistoryTestFrame(t)
	cwd := pf.GetActivePanel().Vfs.GetPath()
	hp.SaveRichHistory("cmdline", []history.HistoryRecord{
		{Name: "./h1", Dir: cwd}, {Name: "./h2", Dir: cwd},
		{Name: "./e1"}, {Name: "./e2"},
	})
	pf.AttachCommandHistory()
	config.App.PathHintPerCategory = true
	config.App.PathHintMaxVisible = 3

	rows := 0
	for _, it := range pf.commandHistoryMatches("./") {
		if !it.Separator {
			rows++
		}
	}
	if rows != 3 {
		t.Fatalf("history rows = %d, want the cap of 3", rows)
	}
}
