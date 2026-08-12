package envman

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxCodecBytes = 16 << 20

// EncodeEntry returns the Unicode clipboard representation of one entry.
// Profiles use the legacy-compatible Name/Enabled header followed by canonical
// NAME=value lines. A separator is represented by the legacy Name=- marker.
func EncodeEntry(entry Entry) (string, error) {
	config := Config{Version: CurrentConfigVersion, Entries: []Entry{entry}}
	if err := config.Validate(runtimeOptions()); err != nil {
		return "", err
	}

	var output strings.Builder
	if entry.Kind == KindProfile {
		output.WriteString("=Name=")
		output.WriteString(entry.Name)
		output.WriteByte('\n')
		output.WriteString("=Enabled=")
		if entry.Enabled {
			output.WriteByte('1')
		} else {
			output.WriteByte('0')
		}
		output.WriteString("\n\n")
		assignments, err := ParseAssignments(entry.Variables)
		if err != nil {
			return "", err
		}
		for _, assignment := range assignments {
			output.WriteString(assignment.Name)
			output.WriteByte('=')
			output.WriteString(assignment.Value)
			output.WriteByte('\n')
		}
	} else {
		output.WriteString("=Name=-\n")
	}
	if output.Len() > maxCodecBytes {
		return "", fmt.Errorf("encoded entry exceeds %d bytes", maxCodecBytes)
	}
	return output.String(), nil
}

// DecodeEntry parses an entry produced by EncodeEntry. Passing no options
// selects runtime platform validation behavior.
func DecodeEntry(text string, optional ...EngineOptions) (Entry, error) {
	opts := resolveOptions(optional)
	lines, err := splitCodecLines(text)
	if err != nil {
		return Entry{}, err
	}
	if len(lines) == 0 {
		return Entry{}, errors.New("entry text is empty")
	}

	metadata := make(map[string]string, 3)
	metadataLine := make(map[string]int, 3)
	index := 0
	for index < len(lines) && strings.HasPrefix(lines[index], "=") {
		line := lines[index]
		body := strings.TrimPrefix(line, "=")
		separator := strings.IndexByte(body, '=')
		if separator <= 0 {
			return Entry{}, &LineError{Line: index + 1, Err: errors.New("entry metadata must use =Key=value syntax")}
		}
		key, value := body[:separator], body[separator+1:]
		switch key {
		case "Kind", "Name", "Enabled":
		default:
			return Entry{}, &LineError{Line: index + 1, Err: fmt.Errorf("unknown entry metadata %q", key)}
		}
		if previous, duplicated := metadataLine[key]; duplicated {
			return Entry{}, &LineError{Line: index + 1, Err: fmt.Errorf("entry metadata %q duplicates line %d", key, previous)}
		}
		metadata[key] = value
		metadataLine[key] = index + 1
		index++
	}
	for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
		index++
	}

	kindValue, hasKind := metadata["Kind"]
	if !hasKind {
		name, hasName := metadata["Name"]
		_, hasEnabled := metadata["Enabled"]
		switch {
		case hasName && name == "-" && !hasEnabled:
			kindValue = string(KindSeparator)
		case hasName && hasEnabled:
			kindValue = string(KindProfile)
		default:
			return Entry{}, errors.New("entry metadata cannot infer Kind")
		}
	}
	entry := Entry{Kind: Kind(kindValue)}
	switch entry.Kind {
	case KindProfile:
		name, hasName := metadata["Name"]
		if !hasName {
			return Entry{}, errors.New("profile metadata is missing Name")
		}
		enabledText, hasEnabled := metadata["Enabled"]
		if !hasEnabled {
			return Entry{}, errors.New("profile metadata is missing Enabled")
		}
		enabled, parseErr := parseEnabled(enabledText)
		if parseErr != nil {
			return Entry{}, &LineError{Line: metadataLine["Enabled"], Err: parseErr}
		}
		entry.Name = name
		entry.Enabled = enabled
		for index < len(lines) {
			line := lines[index]
			if strings.TrimSpace(line) != "" {
				assignment, parseErr := ParseAssignment(line, opts)
				if parseErr != nil {
					return Entry{}, &LineError{Line: index + 1, Err: parseErr}
				}
				entry.Variables = append(entry.Variables, assignment.Name+"="+assignment.Value)
			}
			index++
		}
	case KindSeparator:
		if name, exists := metadata["Name"]; exists && name != "-" {
			return Entry{}, &LineError{Line: metadataLine["Name"], Err: errors.New("separator Name metadata must be -")}
		}
		if _, exists := metadata["Enabled"]; exists {
			return Entry{}, &LineError{Line: metadataLine["Enabled"], Err: errors.New("separator must not have Enabled metadata")}
		}
		if index < len(lines) {
			return Entry{}, &LineError{Line: index + 1, Err: errors.New("separator must not contain assignments")}
		}
	default:
		return Entry{}, &LineError{Line: metadataLine["Kind"], Err: fmt.Errorf("unknown entry kind %q", kindValue)}
	}

	if err := (Config{Version: CurrentConfigVersion, Entries: []Entry{entry}}).Validate(opts); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// EncodeEnvironment serializes ordinary editable variables one per line.
// Reserved f4 variables and Windows pseudo-variables are intentionally omitted
// because importing them is prohibited by the model.
func EncodeEnvironment(environment []string, optional ...EngineOptions) (string, error) {
	opts := resolveOptions(optional)
	state, err := newEnvironmentState(environment, opts)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	keys := append([]string(nil), state.order...)
	sort.SliceStable(keys, func(left, right int) bool {
		return normalizeName(state.values[keys[left]].name, opts) < normalizeName(state.values[keys[right]].name, opts)
	})
	for _, key := range keys {
		item, exists := state.values[key]
		if !exists || isReservedName(item.name, opts) {
			continue
		}
		if err := validateVariableName(item.name); err != nil {
			// OS environments can contain names that the portable profile format
			// cannot safely import (for example ProgramFiles(x86)). They remain in
			// the engine baseline but are outside the managed/exportable set.
			continue
		}
		if !utf8.ValidString(item.value) || strings.ContainsAny(item.value, "\x00\r\n") {
			return "", fmt.Errorf("environment variable %q contains a value that cannot be encoded safely", item.name)
		}
		output.WriteString(item.name)
		output.WriteByte('=')
		output.WriteString(item.value)
		output.WriteByte('\n')
		if output.Len() > maxCodecBytes {
			return "", fmt.Errorf("encoded environment exceeds %d bytes", maxCodecBytes)
		}
	}
	return output.String(), nil
}

// DecodeEnvironment validates line-oriented environment text without
// expanding references. Blank lines are ignored and errors retain their
// one-based source line.
func DecodeEnvironment(text string, optional ...EngineOptions) ([]string, error) {
	opts := resolveOptions(optional)
	lines, err := splitCodecLines(text)
	if err != nil {
		return nil, err
	}
	state := &environmentState{
		opts:   cloneOptions(opts),
		values: make(map[string]environmentValue, len(lines)),
	}
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		assignment, parseErr := ParseAssignment(line, opts)
		if parseErr != nil {
			return nil, &LineError{Line: index + 1, Err: parseErr}
		}
		if assignment.Unset {
			state.unset(assignment.Name)
		} else {
			state.set(assignment.Name, assignment.Value)
		}
	}
	return state.environ(), nil
}

func parseEnabled(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid Enabled value %q", value)
	}
}

func splitCodecLines(text string) ([]string, error) {
	if len(text) > maxCodecBytes {
		return nil, fmt.Errorf("text exceeds %d bytes", maxCodecBytes)
	}
	if !utf8.ValidString(text) {
		return nil, errors.New("text is not valid UTF-8")
	}
	if strings.IndexByte(text, 0) >= 0 {
		return nil, errors.New("text contains NUL")
	}
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.ContainsRune(text, '\r') {
		return nil, errors.New("text contains a bare carriage return")
	}
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}
