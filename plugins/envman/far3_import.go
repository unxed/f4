package envman

import (
	"errors"
	"fmt"
)

var errFar3ImportUnsupported = errors.New("Far Manager 3 import is available only on Windows")

const (
	far3SourceRegistry64 = "registry64"
	far3SourceRegistry32 = "registry32"
)

type far3ImportCandidate struct {
	Source              string
	Entries             []Entry
	IgnoredVariables    []string
	HasIgnoredVariables bool
	AlwaysUseEditor     bool
	HasAlwaysUseEditor  bool
}

var far3ImportLoader = loadFar3ImportCandidates

func validateFar3ImportCandidate(candidate far3ImportCandidate, options EngineOptions) error {
	config := DefaultConfig()
	config.Entries = append([]Entry(nil), candidate.Entries...)
	config.IgnoredVariables = append([]string(nil), candidate.IgnoredVariables...)
	config.AlwaysUseEditor = candidate.AlwaysUseEditor
	if err := config.Validate(options); err != nil {
		return fmt.Errorf("Far Manager 3 settings in the %s are not importable: %w", far3SourceDescription(candidate.Source), err)
	}
	return nil
}

func far3SourceDescription(source string) string {
	switch source {
	case far3SourceRegistry64:
		return "64-bit registry view"
	case far3SourceRegistry32:
		return "32-bit registry view"
	default:
		return "Windows registry"
	}
}

func mergeFar3Import(current Config, candidate far3ImportCandidate, replace bool, options EngineOptions) Config {
	next := cloneConfig(current)
	if replace {
		next.Entries = cloneEntries(candidate.Entries)
	} else {
		next.Entries = append(next.Entries, cloneEntries(candidate.Entries)...)
	}
	if candidate.HasIgnoredVariables {
		if replace {
			next.IgnoredVariables = append([]string(nil), candidate.IgnoredVariables...)
		} else {
			next.IgnoredVariables = mergeVariableNames(next.IgnoredVariables, candidate.IgnoredVariables, options)
		}
	}
	if candidate.HasAlwaysUseEditor {
		next.AlwaysUseEditor = candidate.AlwaysUseEditor
	}
	return next
}

func cloneEntries(entries []Entry) []Entry {
	if entries == nil {
		return nil
	}
	cloned := make([]Entry, len(entries))
	for index, entry := range entries {
		cloned[index] = cloneEntry(entry)
	}
	return cloned
}

func mergeVariableNames(current, imported []string, options EngineOptions) []string {
	result := append([]string(nil), current...)
	seen := make(map[string]struct{}, len(current)+len(imported))
	for _, name := range current {
		seen[normalizeName(name, options)] = struct{}{}
	}
	for _, name := range imported {
		key := normalizeName(name, options)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	return result
}
