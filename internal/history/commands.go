package history

import (
	"encoding/json"

	"github.com/unxed/vtui"
)

// CommandRecords is the command line's history as records, newest first. It
// is the one answer to where they come from, for Alt+F8, for the completion
// menu and for adding a command: the rich "cmdline" bucket when provider
// keeps one, the plain names otherwise (a provider that keeps no records, or
// a bucket not written yet). A record with no directory of its own takes the
// one the old parallel "cmdline.paths" history kept for that command, so
// commands from sessions before the rich history still carry where they ran.
func CommandRecords(provider vtui.HistoryProvider, names []string) []HistoryRecord {
	var records []HistoryRecord
	if hp, ok := provider.(*F4HistoryProvider); ok {
		records = hp.LoadRichHistory("cmdline")
	}
	if len(records) == 0 {
		records = RecordsFromNames(names)
	}
	if len(records) == 0 {
		return nil
	}
	legacy := legacyCommandPaths(provider)
	if len(legacy) == 0 {
		return records
	}
	for i := range records {
		if records[i].Directory() != "" {
			continue
		}
		if path, ok := legacy[records[i].Name]; ok {
			records[i].Dir = path
		}
	}
	return records
}

// legacyCommandPaths reads the old parallel history of command directories,
// command to directory.
func legacyCommandPaths(provider vtui.HistoryProvider) map[string]string {
	if provider == nil {
		return nil
	}
	var byCommand map[string]string
	for _, encoded := range provider.LoadHistory(CommandHistoryPathsID) {
		var record commandHistoryPathRecord
		if json.Unmarshal([]byte(encoded), &record) == nil && record.Command != "" {
			if byCommand == nil {
				byCommand = make(map[string]string)
			}
			byCommand[record.Command] = record.Path
		}
	}
	return byCommand
}
