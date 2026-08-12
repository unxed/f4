package envman

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

// CurrentConfigVersion is the only configuration schema version understood by
// this implementation.
const CurrentConfigVersion = 1

const (
	maxEntryNameBytes = 4 << 10
	maxVariableBytes  = 1 << 20
)

// Kind identifies whether an entry changes the environment or only separates
// groups visually in the entry menu.
type Kind string

const (
	KindProfile   Kind = "profile"
	KindSeparator Kind = "separator"
)

// Entry is one ordered Environment Manager item. Variables use NAME=value
// syntax. An empty value unsets NAME.
type Entry struct {
	Kind      Kind     `json:"kind"`
	Name      string   `json:"name,omitempty"`
	Enabled   bool     `json:"enabled,omitempty"`
	Variables []string `json:"variables,omitempty"`
}

// Config is the complete persisted Environment Manager configuration.
type Config struct {
	Version          int      `json:"version"`
	IgnoredVariables []string `json:"ignoredVariables,omitempty"`
	AlwaysUseEditor  bool     `json:"alwaysUseEditor,omitempty"`
	Entries          []Entry  `json:"entries,omitempty"`
}

// EngineOptions makes platform-sensitive environment behavior explicit and
// injectable. ReservedNames adds names to the built-in protected set.
type EngineOptions struct {
	WindowsCaseFold    bool
	ExpandDollarSyntax bool
	ReservedNames      []string
}

// Assignment is one parsed profile line. Unset is true for NAME=.
type Assignment struct {
	Name  string
	Value string
	Unset bool
}

// LineError attaches a one-based source line to a validation failure.
type LineError struct {
	Line int
	Err  error
}

func (e *LineError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("line %d: %v", e.Line, e.Err)
}

func (e *LineError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Change describes one environment variable before and after a diff.
type Change struct {
	Name   string
	Before string
	After  string
}

// Diff groups variables by how the second environment differs from the first.
type Diff struct {
	Added   []Change
	Changed []Change
	Removed []Change
}

func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Changed) == 0 && len(d.Removed) == 0
}

// Result is the environment produced from the captured baseline and the
// ordered enabled profiles. Changes is baseline -> Environment.
type Result struct {
	Environment []string
	Changes     Diff
}

// Reconciliation compares the current process environment with the target
// generated from a Config. Target preserves ignored and reserved variables
// from Current. Drift is Target -> Current; Changes is Current -> Target and
// is therefore the diff an applier should enact.
type Reconciliation struct {
	Target  []string
	Drift   Diff
	Changes Diff
}

// Engine owns an immutable startup baseline and evaluates configurations
// without mutating the process environment.
type Engine struct {
	baseline []string
	base     *environmentState
	opts     EngineOptions
}

var builtInReservedNames = []string{
	"PROMPT",
	"COMSPEC",
	"SHELL",
	"F4_NESTED",
	"TERM_PROGRAM",
	"KITTY_WINDOW_ID",
}

// DefaultConfig returns an empty, current-version configuration.
func DefaultConfig() Config {
	return Config{Version: CurrentConfigVersion}
}

// OptionsForGOOS returns Environment Manager semantics for a Go GOOS value.
func OptionsForGOOS(goos string) EngineOptions {
	windows := strings.EqualFold(strings.TrimSpace(goos), "windows")
	return EngineOptions{
		WindowsCaseFold:    windows,
		ExpandDollarSyntax: !windows,
		ReservedNames:      append([]string(nil), builtInReservedNames...),
	}
}

func runtimeOptions() EngineOptions {
	return OptionsForGOOS(runtime.GOOS)
}

func resolveOptions(optional []EngineOptions) EngineOptions {
	if len(optional) == 0 {
		return runtimeOptions()
	}
	return cloneOptions(optional[0])
}

func cloneOptions(opts EngineOptions) EngineOptions {
	opts.ReservedNames = append([]string(nil), opts.ReservedNames...)
	return opts
}

func cloneConfig(config Config) Config {
	cloned := config
	cloned.IgnoredVariables = append([]string(nil), config.IgnoredVariables...)
	if config.Entries == nil {
		cloned.Entries = nil
		return cloned
	}
	cloned.Entries = make([]Entry, len(config.Entries))
	for index, entry := range config.Entries {
		cloned.Entries[index] = entry
		cloned.Entries[index].Variables = append([]string(nil), entry.Variables...)
	}
	return cloned
}

// ParseAssignment parses and validates one portable NAME=value line. Passing
// no options selects runtime platform behavior.
func ParseAssignment(line string, optional ...EngineOptions) (Assignment, error) {
	opts := resolveOptions(optional)
	line = strings.TrimSuffix(line, "\r")
	if !utf8.ValidString(line) {
		return Assignment{}, errors.New("assignment is not valid UTF-8")
	}
	if strings.IndexByte(line, 0) >= 0 {
		return Assignment{}, errors.New("assignment contains NUL")
	}
	if strings.ContainsAny(line, "\r\n") {
		return Assignment{}, errors.New("assignment contains a line break")
	}
	if len(line) > maxVariableBytes {
		return Assignment{}, fmt.Errorf("assignment is larger than %d bytes", maxVariableBytes)
	}
	separator := strings.IndexByte(line, '=')
	if separator <= 0 {
		return Assignment{}, errors.New("assignment must use NAME=value syntax")
	}
	name, value := line[:separator], line[separator+1:]
	if err := validateVariableName(name); err != nil {
		return Assignment{}, err
	}
	if isReservedName(name, opts) {
		return Assignment{}, fmt.Errorf("environment variable %q is reserved by f4", name)
	}
	return Assignment{Name: name, Value: value, Unset: value == ""}, nil
}

// ParseAssignments validates a list and reports failures with one-based line
// numbers. Blank rows are ignored.
func ParseAssignments(lines []string, optional ...EngineOptions) ([]Assignment, error) {
	opts := resolveOptions(optional)
	assignments := make([]Assignment, 0, len(lines))
	for index, line := range lines {
		normalized := strings.TrimSuffix(line, "\r")
		if strings.ContainsAny(normalized, "\r\n") {
			_, err := ParseAssignment(line, opts)
			return nil, &LineError{Line: index + 1, Err: err}
		}
		if strings.TrimSpace(normalized) == "" {
			continue
		}
		assignment, err := ParseAssignment(line, opts)
		if err != nil {
			return nil, &LineError{Line: index + 1, Err: err}
		}
		assignments = append(assignments, assignment)
	}
	return assignments, nil
}

func validateVariableName(name string) error {
	if name == "" {
		return errors.New("environment variable name is empty")
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		valid := char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
		if index > 0 {
			valid = valid || char >= '0' && char <= '9'
		}
		if !valid {
			return fmt.Errorf("environment variable name %q is not portable", name)
		}
	}
	return nil
}

func (config Config) Validate(opts EngineOptions) error {
	if config.Version != CurrentConfigVersion {
		return fmt.Errorf("unsupported Environment Manager config version %d", config.Version)
	}
	seenIgnored := make(map[string]struct{}, len(config.IgnoredVariables))
	for index, name := range config.IgnoredVariables {
		if err := validateVariableName(name); err != nil {
			return fmt.Errorf("ignored variable %d: %w", index+1, err)
		}
		key := normalizeName(name, opts)
		if _, exists := seenIgnored[key]; exists {
			return fmt.Errorf("ignored variable %q is duplicated", name)
		}
		seenIgnored[key] = struct{}{}
	}
	for index, entry := range config.Entries {
		switch entry.Kind {
		case KindSeparator:
			if entry.Name != "" || entry.Enabled || len(entry.Variables) != 0 {
				return fmt.Errorf("entry %d: separator contains profile data", index+1)
			}
		case KindProfile:
			if !utf8.ValidString(entry.Name) {
				return fmt.Errorf("entry %d: name is not valid UTF-8", index+1)
			}
			if strings.TrimSpace(entry.Name) == "" {
				return fmt.Errorf("entry %d: profile name is empty", index+1)
			}
			if len(entry.Name) > maxEntryNameBytes || strings.ContainsAny(entry.Name, "\x00\r\n") {
				return fmt.Errorf("entry %d: profile name is invalid or too long", index+1)
			}
			if _, err := ParseAssignments(entry.Variables, opts); err != nil {
				return fmt.Errorf("entry %d (%s): %w", index+1, entry.Name, err)
			}
		default:
			return fmt.Errorf("entry %d: unknown kind %q", index+1, entry.Kind)
		}
	}
	return nil
}

// NewEngine captures baseline by value. It accepts arbitrary valid OS
// environment names in the baseline while profile assignments stay portable.
func NewEngine(baseline []string, opts EngineOptions) (*Engine, error) {
	state, err := newEnvironmentState(baseline, opts)
	if err != nil {
		return nil, fmt.Errorf("capture baseline: %w", err)
	}
	return &Engine{
		baseline: append([]string(nil), baseline...),
		base:     state,
		opts:     cloneOptions(opts),
	}, nil
}

func (engine *Engine) Baseline() []string {
	if engine == nil {
		return nil
	}
	return append([]string(nil), engine.baseline...)
}

func (engine *Engine) Evaluate(config Config) (Result, error) {
	if engine == nil || engine.base == nil {
		return Result{}, errors.New("Environment Manager engine is unavailable")
	}
	if err := config.Validate(engine.opts); err != nil {
		return Result{}, err
	}
	state := engine.base.clone()
	for _, entry := range config.Entries {
		if entry.Kind != KindProfile || !entry.Enabled {
			continue
		}
		assignments, err := ParseAssignments(entry.Variables, engine.opts)
		if err != nil {
			return Result{}, fmt.Errorf("profile %q: %w", entry.Name, err)
		}
		for _, assignment := range assignments {
			if assignment.Unset {
				state.unset(assignment.Name)
				continue
			}
			state.set(assignment.Name, expandAssignmentValue(assignment.Value, state, engine.opts))
		}
	}
	environment := state.environ()
	changes, err := DiffEnvironment(engine.baseline, environment, engine.opts)
	if err != nil {
		return Result{}, err
	}
	return Result{Environment: environment, Changes: changes}, nil
}

func (engine *Engine) Reconcile(config Config, current []string) (Reconciliation, error) {
	result, err := engine.Evaluate(config)
	if err != nil {
		return Reconciliation{}, err
	}
	currentState, err := newEnvironmentState(current, engine.opts)
	if err != nil {
		return Reconciliation{}, fmt.Errorf("read current environment: %w", err)
	}
	targetState, err := newEnvironmentState(result.Environment, engine.opts)
	if err != nil {
		return Reconciliation{}, err
	}
	ignored := ignoredNameSet(config.IgnoredVariables, engine.opts)
	assigned, err := assignedNameSet(config, engine.opts)
	if err != nil {
		return Reconciliation{}, err
	}
	for key := range assigned {
		delete(ignored, key)
	}
	// Ignored variables which are not mentioned by an enabled profile and all
	// f4-reserved variables belong to the process, not to Environment Manager.
	// First remove their baseline-derived target values so absence in the
	// current environment is preserved as faithfully as presence.
	for key, item := range targetState.values {
		if _, keepCurrent := ignored[key]; keepCurrent || isReservedName(item.name, engine.opts) || validateVariableName(item.name) != nil {
			targetState.unset(item.name)
		}
	}
	for key, item := range currentState.values {
		if _, keep := ignored[key]; keep || isReservedName(item.name, engine.opts) || validateVariableName(item.name) != nil {
			targetState.set(item.name, item.value)
		}
	}
	targetState.opaque = append([]string(nil), currentState.opaque...)
	target := targetState.environ()
	drift, err := diffEnvironmentIgnoring(target, current, engine.opts, nil)
	if err != nil {
		return Reconciliation{}, err
	}
	changes, err := diffEnvironmentIgnoring(current, target, engine.opts, nil)
	if err != nil {
		return Reconciliation{}, err
	}
	return Reconciliation{Target: target, Drift: drift, Changes: changes}, nil
}

// DiffEnvironment returns a stable, name-sorted comparison of two environment
// snapshots. Windows-style matching is controlled by opts.WindowsCaseFold.
func DiffEnvironment(before, after []string, opts EngineOptions) (Diff, error) {
	return diffEnvironmentIgnoring(before, after, opts, nil)
}

func diffEnvironmentIgnoring(before, after []string, opts EngineOptions, ignored map[string]struct{}) (Diff, error) {
	left, err := newEnvironmentState(before, opts)
	if err != nil {
		return Diff{}, fmt.Errorf("parse before environment: %w", err)
	}
	right, err := newEnvironmentState(after, opts)
	if err != nil {
		return Diff{}, fmt.Errorf("parse after environment: %w", err)
	}
	var diff Diff
	for key, afterItem := range right.values {
		if _, skip := ignored[key]; skip {
			continue
		}
		beforeItem, existed := left.values[key]
		switch {
		case !existed:
			diff.Added = append(diff.Added, Change{Name: afterItem.name, After: afterItem.value})
		case beforeItem.value != afterItem.value:
			diff.Changed = append(diff.Changed, Change{Name: beforeItem.name, Before: beforeItem.value, After: afterItem.value})
		}
	}
	for key, beforeItem := range left.values {
		if _, skip := ignored[key]; skip {
			continue
		}
		if _, exists := right.values[key]; !exists {
			diff.Removed = append(diff.Removed, Change{Name: beforeItem.name, Before: beforeItem.value})
		}
	}
	sortChanges := func(changes []Change) {
		sort.SliceStable(changes, func(i, j int) bool {
			return normalizeName(changes[i].Name, opts) < normalizeName(changes[j].Name, opts)
		})
	}
	sortChanges(diff.Added)
	sortChanges(diff.Changed)
	sortChanges(diff.Removed)
	return diff, nil
}

type environmentValue struct {
	name  string
	value string
}

type environmentState struct {
	opts   EngineOptions
	order  []string
	values map[string]environmentValue
	opaque []string
}

func newEnvironmentState(lines []string, opts EngineOptions) (*environmentState, error) {
	state := &environmentState{
		opts:   cloneOptions(opts),
		values: make(map[string]environmentValue, len(lines)),
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "=") {
			state.addOpaque(line)
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return nil, &LineError{Line: index + 1, Err: errors.New("environment entry must use NAME=value syntax")}
		}
		state.set(line[:separator], line[separator+1:])
	}
	return state, nil
}

func (state *environmentState) clone() *environmentState {
	cloned := &environmentState{
		opts:   cloneOptions(state.opts),
		order:  append([]string(nil), state.order...),
		values: make(map[string]environmentValue, len(state.values)),
		opaque: append([]string(nil), state.opaque...),
	}
	for key, value := range state.values {
		cloned.values[key] = value
	}
	return cloned
}

func (state *environmentState) set(name, value string) {
	key := normalizeName(name, state.opts)
	if previous, exists := state.values[key]; exists {
		previous.value = value
		state.values[key] = previous
		return
	}
	state.order = append(state.order, key)
	state.values[key] = environmentValue{name: name, value: value}
}

func (state *environmentState) unset(name string) {
	key := normalizeName(name, state.opts)
	if _, exists := state.values[key]; !exists {
		return
	}
	delete(state.values, key)
	for index, orderedKey := range state.order {
		if orderedKey == key {
			state.order = append(state.order[:index], state.order[index+1:]...)
			break
		}
	}
}

func (state *environmentState) lookup(name string) (string, bool) {
	value, exists := state.values[normalizeName(name, state.opts)]
	return value.value, exists
}

func (state *environmentState) addOpaque(value string) {
	for _, existing := range state.opaque {
		if existing == value {
			return
		}
	}
	state.opaque = append(state.opaque, value)
}

func (state *environmentState) environ() []string {
	result := append([]string(nil), state.opaque...)
	for _, key := range state.order {
		if value, exists := state.values[key]; exists {
			result = append(result, value.name+"="+value.value)
		}
	}
	return result
}

func normalizeName(name string, opts EngineOptions) string {
	if opts.WindowsCaseFold {
		return strings.ToUpper(name)
	}
	return name
}

func isReservedName(name string, opts EngineOptions) bool {
	if strings.HasPrefix(name, "=") {
		return true
	}
	for _, reserved := range builtInReservedNames {
		if normalizeName(name, opts) == normalizeName(reserved, opts) {
			return true
		}
	}
	for _, reserved := range opts.ReservedNames {
		if normalizeName(name, opts) == normalizeName(reserved, opts) {
			return true
		}
	}
	return false
}

func ignoredNameSet(names []string, opts EngineOptions) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[normalizeName(name, opts)] = struct{}{}
	}
	return result
}

func assignedNameSet(config Config, opts EngineOptions) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, entry := range config.Entries {
		if entry.Kind != KindProfile || !entry.Enabled {
			continue
		}
		assignments, err := ParseAssignments(entry.Variables, opts)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", entry.Name, err)
		}
		for _, assignment := range assignments {
			result[normalizeName(assignment.Name, opts)] = struct{}{}
		}
	}
	return result, nil
}

func expandAssignmentValue(value string, state *environmentState, opts EngineOptions) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '%' {
			if end := strings.IndexByte(value[index+1:], '%'); end >= 0 {
				end += index + 1
				name := value[index+1 : end]
				if name == "" {
					result.WriteByte('%')
				} else if replacement, exists := state.lookup(name); exists {
					result.WriteString(replacement)
				}
				index = end + 1
				continue
			}
		}
		if opts.ExpandDollarSyntax && value[index] == '$' {
			if index+1 < len(value) && value[index+1] == '$' {
				result.WriteByte('$')
				index += 2
				continue
			}
			name, end := dollarReference(value, index)
			if end > index {
				if replacement, exists := state.lookup(name); exists {
					result.WriteString(replacement)
				}
				index = end
				continue
			}
		}
		result.WriteByte(value[index])
		index++
	}
	return result.String()
}

func dollarReference(value string, start int) (string, int) {
	if start+1 >= len(value) {
		return "", start
	}
	if value[start+1] == '{' {
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			return "", start
		}
		end += start + 2
		name := value[start+2 : end]
		if validateVariableName(name) != nil {
			return "", start
		}
		return name, end + 1
	}
	end := start + 1
	for end < len(value) {
		char := value[end]
		valid := char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
		if end > start+1 {
			valid = valid || char >= '0' && char <= '9'
		}
		if !valid {
			break
		}
		end++
	}
	if end == start+1 {
		return "", start
	}
	return value[start+1 : end], end
}
