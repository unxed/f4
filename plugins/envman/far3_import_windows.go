//go:build windows

package envman

import (
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/windows/registry"
)

const far3EnvManRegistryPath = `Software\Far2\Plugins\EnvMan`

const maxFar3ImportEntries = 10000

type far3RegistryView struct {
	label  string
	access uint32
}

func far3ImportSupported() bool {
	return true
}

func loadFar3ImportCandidates() ([]far3ImportCandidate, error) {
	views := []far3RegistryView{
		{label: far3SourceRegistry64, access: registry.WOW64_64KEY},
		{label: far3SourceRegistry32, access: registry.WOW64_32KEY},
	}
	candidates := make([]far3ImportCandidate, 0, len(views))
	for _, view := range views {
		candidate, found, err := readFar3RegistryCandidate(far3EnvManRegistryPath, view)
		if err != nil {
			return nil, err
		}
		if found {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func readFar3RegistryCandidate(path string, view far3RegistryView) (far3ImportCandidate, bool, error) {
	root, err := registry.OpenKey(
		registry.CURRENT_USER,
		path,
		registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS|view.access,
	)
	if errors.Is(err, registry.ErrNotExist) {
		return far3ImportCandidate{}, false, nil
	}
	if err != nil {
		return far3ImportCandidate{}, false, fmt.Errorf("open Far Manager 3 Environment Manager settings in the %s: %w", far3SourceDescription(view.label), err)
	}
	defer root.Close()

	// These are the effective defaults used when the legacy registry values
	// were absent, so importing an untouched Far configuration preserves its
	// behavior as well as explicitly saved settings.
	candidate := far3ImportCandidate{
		Source:              view.label,
		IgnoredVariables:    []string{"FARENV_EXPORT_HWND"},
		HasIgnoredVariables: true,
		HasAlwaysUseEditor:  true,
	}
	if value, _, valueErr := root.GetStringValue("IgnoredVariables"); valueErr == nil {
		candidate.IgnoredVariables = splitIgnoredVariables(value, OptionsForGOOS("windows"))
	} else if !errors.Is(valueErr, registry.ErrNotExist) {
		return far3ImportCandidate{}, false, fmt.Errorf("read Far Manager 3 ignored variables from the %s: %w", far3SourceDescription(view.label), valueErr)
	}
	if value, _, valueErr := root.GetIntegerValue("AlwaysUseEditor"); valueErr == nil {
		candidate.AlwaysUseEditor = value != 0
	} else if !errors.Is(valueErr, registry.ErrNotExist) {
		return far3ImportCandidate{}, false, fmt.Errorf("read Far Manager 3 editor setting from the %s: %w", far3SourceDescription(view.label), valueErr)
	}

	for index := 0; index < maxFar3ImportEntries; index++ {
		entryKey, openErr := registry.OpenKey(root, strconv.Itoa(index), registry.QUERY_VALUE|view.access)
		if errors.Is(openErr, registry.ErrNotExist) {
			break
		}
		if openErr != nil {
			return far3ImportCandidate{}, false, fmt.Errorf("open Far Manager 3 entry %d from the %s: %w", index, far3SourceDescription(view.label), openErr)
		}
		entry, readErr := readFar3RegistryEntry(entryKey, index, view.label)
		closeErr := entryKey.Close()
		if readErr != nil {
			return far3ImportCandidate{}, false, readErr
		}
		if closeErr != nil {
			return far3ImportCandidate{}, false, fmt.Errorf("close Far Manager 3 entry %d from the %s: %w", index, far3SourceDescription(view.label), closeErr)
		}
		candidate.Entries = append(candidate.Entries, entry)
	}
	if len(candidate.Entries) == maxFar3ImportEntries {
		if extra, extraErr := registry.OpenKey(root, strconv.Itoa(maxFar3ImportEntries), registry.QUERY_VALUE|view.access); extraErr == nil {
			extra.Close()
			return far3ImportCandidate{}, false, fmt.Errorf("Far Manager 3 settings in the %s exceed %d entries", far3SourceDescription(view.label), maxFar3ImportEntries)
		} else if !errors.Is(extraErr, registry.ErrNotExist) {
			return far3ImportCandidate{}, false, fmt.Errorf("check Far Manager 3 entry limit in the %s: %w", far3SourceDescription(view.label), extraErr)
		}
	}
	return candidate, true, nil
}

func readFar3RegistryEntry(key registry.Key, index int, source string) (Entry, error) {
	name, _, err := key.GetStringValue("")
	if err != nil {
		return Entry{}, fmt.Errorf("read Far Manager 3 entry %d name from the %s: %w", index, far3SourceDescription(source), err)
	}
	if name == "-" {
		return Entry{Kind: KindSeparator}, nil
	}
	entry := Entry{Kind: KindProfile, Name: name}
	variables, _, variablesErr := key.GetStringsValue("Vars")
	if variablesErr == nil {
		entry.Variables = append([]string(nil), variables...)
	} else if !errors.Is(variablesErr, registry.ErrNotExist) {
		return Entry{}, fmt.Errorf("read Far Manager 3 entry %d variables from the %s: %w", index, far3SourceDescription(source), variablesErr)
	}
	enabled, _, enabledErr := key.GetIntegerValue("Enabled")
	if enabledErr == nil {
		entry.Enabled = enabled != 0
	} else if !errors.Is(enabledErr, registry.ErrNotExist) {
		return Entry{}, fmt.Errorf("read Far Manager 3 entry %d enabled state from the %s: %w", index, far3SourceDescription(source), enabledErr)
	}
	return entry, nil
}
