package vtvibe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The user's own commands (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage
// H9, item 3). Claude Code, OpenCode and Cursor CLI let the user keep prompt
// templates as files and call them by name. Here a command is a file
// NAME.md in a folder; "ai:/NAME arguments" sends its text, with
// $ARGUMENTS replaced by the arguments (or the arguments appended when the
// text does not mention them).

var commandName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ErrNoCommand is returned for a name that has no file.
var ErrNoCommand = errors.New("vtvibe: no such command")

// LoadCommand reads the template of command name from dir.
func LoadCommand(dir, name string) (string, error) {
	if !commandName.MatchString(name) {
		return "", fmt.Errorf("vtvibe: %q is not a command name (letters, digits, - and _)", name)
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".md")) // #nosec G304 -- the name is checked above, the folder is f4's own
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoCommand
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListCommands names the commands in dir, sorted; none when it is missing.
func ListCommands(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if ok && !e.IsDir() && commandName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// ExpandCommand puts args into template: in place of every $ARGUMENTS, or
// after the text when it has none and args are given.
func ExpandCommand(template, args string) string {
	text := strings.TrimSpace(template)
	args = strings.TrimSpace(args)
	if strings.Contains(text, "$ARGUMENTS") {
		return strings.ReplaceAll(text, "$ARGUMENTS", args)
	}
	if args == "" {
		return text
	}
	return text + "\n\n" + args
}
