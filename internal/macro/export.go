package macro

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/vtinput"
)

// recordedMacroLineWidth wraps generated Keys() calls so that an exported
// macro stays readable when it is opened for editing, which is most of the
// reason to export one rather than leave it in an ini file.
const recordedMacroLineWidth = 60

// RecordedMacroToLua renders a recorded key sequence as a Far-compatible
// Macro{} declaration.
//
// Its Lua form is a list of Keys() calls. Ordinary keys use Far spellings;
// confirmed text uses f4's versioned text-event token so replay cannot turn it
// into a shortcut. The output is meant to be opened and edited: this is the step
// from "I pressed record" to "I am writing macros", and it should not read
// like something a machine emitted.
func RecordedMacroToLua(area, key, description string, events []*vtinput.InputEvent) string {
	if strings.TrimSpace(area) == "" {
		area = "Common"
	}
	if strings.TrimSpace(description) == "" {
		description = "Recorded macro"
	}

	var sb strings.Builder
	sb.WriteString("-- Recorded in f4. This is an ordinary macro file: edit it freely.\n")
	sb.WriteString("Macro {\n")
	fmt.Fprintf(&sb, "  area = %s;\n", luaQuote(area))
	fmt.Fprintf(&sb, "  key = %s;\n", luaQuote(key))
	fmt.Fprintf(&sb, "  description = %s;\n", luaQuote(description))
	sb.WriteString("  action = function()\n")
	for _, line := range recordedKeyLines(events) {
		fmt.Fprintf(&sb, "    Keys(%s)\n", luaQuote(line))
	}
	sb.WriteString("  end;\n")
	sb.WriteString("}\n")
	return sb.String()
}

// recordedKeyLines turns events into key/text tokens, wrapped into lines.
func recordedKeyLines(events []*vtinput.InputEvent) []string {
	var lines []string
	var current string

	for _, event := range events {
		if event == nil {
			continue
		}
		name := encodeMacroEvent(event)
		if name == "" {
			continue
		}
		switch {
		case current == "":
			current = name
		case len(current)+1+len(name) > recordedMacroLineWidth:
			lines = append(lines, current)
			current = name
		default:
			current += " " + name
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// luaQuote renders a string as a Lua literal.
func luaQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// RecordedMacroFileName names an exported macro the way Far names its own:
// lowercase, area first, then the key. Anything a filesystem might object to
// becomes its hex code, so that every key can be exported, including the
// punctuation ones, and on every platform.
func RecordedMacroFileName(area, key string) string {
	if strings.TrimSpace(area) == "" {
		area = "Common"
	}
	return fmt.Sprintf("%s_%s.lua", sanitizeMacroFilePart(area), sanitizeMacroFilePart(key))
}

// SaveRecordedMacro writes a recorded macro into the scripts directory and
// hands it to the running engine, so it takes effect immediately rather than
// at the next start. That immediacy is the whole appeal of recording one.
func (m *MacroManager) SaveRecordedMacro(dir, area, key, description string, events []*vtinput.InputEvent) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	source := RecordedMacroToLua(area, key, description, events)
	path := filepath.Join(dir, RecordedMacroFileName(area, key))
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}

	if m.Lua == nil {
		// No engine is running because the user had no macros until now. The
		// file is on disk and will be read at the next start.
		return nil
	}
	return m.Lua.LoadString(path, source)
}

func sanitizeMacroFilePart(part string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(part) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		default:
			fmt.Fprintf(&sb, "%%%02x", r)
		}
	}
	if sb.Len() == 0 {
		return "key"
	}
	return sb.String()
}
