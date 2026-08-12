package envman

import (
	"fmt"
	"strings"
)

func setEntryEnabled(config Config, index int, enabled bool) (Config, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, fmt.Errorf("entry index %d is out of range", index)
	}
	if config.Entries[index].Kind != KindProfile {
		return config, nil
	}
	next := cloneConfig(config)
	next.Entries[index].Enabled = enabled
	return next, nil
}

func toggleEntry(config Config, index int) (Config, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, fmt.Errorf("entry index %d is out of range", index)
	}
	if config.Entries[index].Kind != KindProfile {
		return config, nil
	}
	return setEntryEnabled(config, index, !config.Entries[index].Enabled)
}

func nextProfileIndex(config Config, index int) int {
	for candidate := index + 1; candidate < len(config.Entries); candidate++ {
		if config.Entries[candidate].Kind == KindProfile {
			return candidate
		}
	}
	return index
}

func insertEntry(config Config, index int, entry Entry) Config {
	next := cloneConfig(config)
	if index < 0 {
		index = 0
	}
	if index > len(next.Entries) {
		index = len(next.Entries)
	}
	next.Entries = append(next.Entries, Entry{})
	copy(next.Entries[index+1:], next.Entries[index:])
	next.Entries[index] = cloneEntry(entry)
	return next
}

func replaceEntry(config Config, index int, entry Entry) (Config, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, fmt.Errorf("entry index %d is out of range", index)
	}
	next := cloneConfig(config)
	next.Entries[index] = cloneEntry(entry)
	return next, nil
}

func deleteEntry(config Config, index int) (Config, Entry, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, Entry{}, fmt.Errorf("entry index %d is out of range", index)
	}
	next := cloneConfig(config)
	removed := cloneEntry(next.Entries[index])
	copy(next.Entries[index:], next.Entries[index+1:])
	next.Entries = next.Entries[:len(next.Entries)-1]
	return next, removed, nil
}

func duplicateEntry(config Config, index int) (Config, int, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, index, fmt.Errorf("entry index %d is out of range", index)
	}
	entry := cloneEntry(config.Entries[index])
	return insertEntry(config, index+1, entry), index + 1, nil
}

func moveEntry(config Config, index, delta int) (Config, int, error) {
	if index < 0 || index >= len(config.Entries) {
		return config, index, fmt.Errorf("entry index %d is out of range", index)
	}
	destination := index + delta
	if destination < 0 || destination >= len(config.Entries) {
		// Far's original manager used an attempted move past an edge to create
		// a group boundary. Insert a separator on that side and then move the
		// selected row across it, leaving the separator between profile groups.
		next := cloneConfig(config)
		if destination < 0 {
			next.Entries = append([]Entry{{Kind: KindSeparator}}, next.Entries...)
			index++
			destination = index - 1
		} else {
			next.Entries = append(next.Entries, Entry{Kind: KindSeparator})
			destination = index + 1
		}
		next.Entries[index], next.Entries[destination] = next.Entries[destination], next.Entries[index]
		next, destination = trimSeparators(next, destination)
		return next, destination, nil
	}
	next := cloneConfig(config)
	next.Entries[index], next.Entries[destination] = next.Entries[destination], next.Entries[index]
	next, destination = trimSeparators(next, destination)
	return next, destination, nil
}

func trimSeparators(config Config, selected int) (Config, int) {
	next := cloneConfig(config)
	entries := make([]Entry, 0, len(next.Entries))
	newSelected := selected
	for index, entry := range next.Entries {
		if entry.Kind == KindSeparator && (len(entries) == 0 || entries[len(entries)-1].Kind == KindSeparator) {
			if index <= newSelected {
				newSelected--
			}
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) != 0 && entries[len(entries)-1].Kind == KindSeparator {
		if len(entries)-1 <= newSelected {
			newSelected--
		}
		entries = entries[:len(entries)-1]
	}
	if newSelected < 0 {
		newSelected = 0
	}
	if newSelected >= len(entries) && len(entries) != 0 {
		newSelected = len(entries) - 1
	}
	next.Entries = entries
	return next, newSelected
}

func cloneEntry(entry Entry) Entry {
	copy := entry
	copy.Variables = append([]string(nil), entry.Variables...)
	return copy
}

func importDriftEntry(diff Diff, name string, options EngineOptions) Entry {
	variables := make([]string, 0, len(diff.Added)+len(diff.Changed)+len(diff.Removed))
	for _, change := range diff.Added {
		variables = append(variables, change.Name+"="+escapeImportedProfileValue(change.After, options))
	}
	for _, change := range diff.Changed {
		value := escapeImportedProfileValue(change.After, options)
		if change.Before != "" {
			if index := strings.Index(change.After, change.Before); index >= 0 {
				value = escapeImportedProfileValue(change.After[:index], options) +
					"%" + change.Name + "%" +
					escapeImportedProfileValue(change.After[index+len(change.Before):], options)
			}
		}
		variables = append(variables, change.Name+"="+value)
	}
	for _, change := range diff.Removed {
		variables = append(variables, change.Name+"=")
	}
	return Entry{Kind: KindProfile, Name: name, Enabled: true, Variables: variables}
}

func escapeImportedProfileValue(value string, options EngineOptions) string {
	value = strings.ReplaceAll(value, "%", "%%")
	if options.ExpandDollarSyntax {
		value = strings.ReplaceAll(value, "$", "$$")
	}
	return value
}
