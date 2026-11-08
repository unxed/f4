package panel

import (
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/vtui"
)

// AttachCommandHistory backs the command line with the "cmdline" history and
// gives it far2l's completion: history by prefix, the commands run in the
// current directory first, file suggestions after them.
func (pf *PanelsFrame) AttachCommandHistory() {
	pf.CmdLine.Edit.HistoryID = "cmdline"
	pf.CmdLine.Edit.ClearHistory = history.ClearKeepingPinned(pf.CmdLine.Edit, "cmdline")
	pf.CmdLine.Edit.HistoryMatches = pf.commandHistoryMatches
	pf.CmdLine.Edit.PathHintsLast = true
	if vtui.GlobalHistoryProvider != nil {
		pf.CmdLine.Edit.History = vtui.GlobalHistoryProvider.LoadHistory("cmdline")
	}
}

// commandHistoryMatches is the command line's vtui.Edit.HistoryMatches: the
// records carry the directory each command ran in, which is what puts the
// commands run in the active panel's directory first.
func (pf *PanelsFrame) commandHistoryMatches(text string) []vtui.AutoCompleteItem {
	curDir := ""
	if fsp := pf.GetActivePanel(); fsp != nil && fsp.Vfs != nil {
		curDir = fsp.Vfs.GetPath()
	}
	// In per-category mode the menu gives each separator-delimited group its
	// own cap; history is one category, however many groups it shows.
	limit := 0
	if config.App.PathHintPerCategory {
		limit = config.App.PathHintMaxVisible
	}
	return history.CommandCompletions(pf.commandRecords(), text, curDir, limit)
}

// commandRecordsCache holds history.CommandRecords for as long as nothing it
// was built from has changed: the provider (by its save count) and the
// command line's own list, which vtui.Edit.AddHistory replaces without
// saving.
type commandRecordsCache struct {
	provider *history.F4HistoryProvider
	gen      uint64
	names    []string
	records  []history.HistoryRecord
}

func (c *commandRecordsCache) valid(hp *history.F4HistoryProvider, gen uint64, names []string) bool {
	if c.records == nil || c.provider != hp || c.gen != gen || len(c.names) != len(names) {
		return false
	}
	return len(names) == 0 || &c.names[0] == &names[0]
}

// commandRecords is history.CommandRecords for the command line, built once
// per change to the history rather than on every keystroke of the menu.
func (pf *PanelsFrame) commandRecords() []history.HistoryRecord {
	names := pf.CmdLine.Edit.History
	hp, ok := vtui.GlobalHistoryProvider.(*history.F4HistoryProvider)
	if !ok {
		return history.CommandRecords(vtui.GlobalHistoryProvider, names)
	}
	gen := hp.Generation()
	if pf.cmdRecords.valid(hp, gen, names) {
		return pf.cmdRecords.records
	}
	records := history.CommandRecords(hp, names)
	if records == nil {
		records = []history.HistoryRecord{}
	}
	pf.cmdRecords = commandRecordsCache{provider: hp, gen: gen, names: names, records: records}
	return records
}
