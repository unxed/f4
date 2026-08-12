package envman

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/vfs"
)

func decodeEnvironmentBytes(data []byte) (string, error) {
	codepage := vfs.DetectEncoding(data, true, 22222)
	decoded, err := vfs.DecodeBytes(data, codepage)
	if err != nil {
		return "", fmt.Errorf("decode environment file: %w", err)
	}
	return string(decoded), nil
}

// decodeEnvironmentDocument retains which names appeared in the document.
// DecodeEnvironment intentionally omits NAME= from its target, while direct
// import still needs to distinguish an explicit unset from an absent ignored
// variable that Environment Manager must preserve.
func (plugin *Plugin) decodeEnvironmentDocument(text string) ([]string, map[string]struct{}, error) {
	environment, err := DecodeEnvironment(text, plugin.options)
	if err != nil {
		return nil, nil, err
	}
	lines, err := splitCodecLines(text)
	if err != nil {
		return nil, nil, err
	}
	explicit := make(map[string]struct{}, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		assignment, parseErr := ParseAssignment(line, plugin.options)
		if parseErr != nil {
			return nil, nil, &LineError{Line: index + 1, Err: parseErr}
		}
		explicit[normalizeName(assignment.Name, plugin.options)] = struct{}{}
	}
	return environment, explicit, nil
}

func (plugin *Plugin) editedEnvironmentTarget(current, edited []string, explicit map[string]struct{}) ([]string, error) {
	currentState, err := newEnvironmentState(current, plugin.options)
	if err != nil {
		return nil, err
	}
	targetState, err := newEnvironmentState(edited, plugin.options)
	if err != nil {
		return nil, err
	}
	ignored := ignoredNameSet(plugin.snapshotConfig().IgnoredVariables, plugin.options)
	for key, item := range currentState.values {
		_, explicitlyEdited := explicit[key]
		_, ignoredName := ignored[key]
		if isReservedName(item.name, plugin.options) || validateVariableName(item.name) != nil || ignoredName && !explicitlyEdited {
			targetState.set(item.name, item.value)
		}
	}
	// Windows drive pseudo-variables are opaque to the portable editor and
	// therefore always survive a full-environment import.
	targetState.opaque = append([]string(nil), currentState.opaque...)
	return targetState.environ(), nil
}

func (plugin *Plugin) exportableEnvironment(environment []string) []string {
	state, err := newEnvironmentState(environment, plugin.options)
	if err != nil {
		return nil
	}
	ignored := ignoredNameSet(plugin.snapshotConfig().IgnoredVariables, plugin.options)
	result := make([]string, 0, len(state.order))
	for _, key := range state.order {
		item, exists := state.values[key]
		if !exists {
			continue
		}
		if _, skip := ignored[key]; skip {
			continue
		}
		if isReservedName(item.name, plugin.options) || validateVariableName(item.name) != nil {
			continue
		}
		result = append(result, item.name+"="+item.value)
	}
	return result
}
