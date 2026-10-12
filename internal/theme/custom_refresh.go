package theme

import (
	"os"
	"sort"
	"strings"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

// AddMissingColorKeys writes into the user's farcolors.ini the colour
// elements that f4 gained after the file was written (f4#234). A Custom scheme
// is a complete export; a release that adds an element leaves it silently
// incomplete, and the new colour then comes from the base style where the
// user cannot see it, let alone change it. The lines added are the colours
// the palette holds now, so nothing on screen changes; the user's own lines,
// comments and order are not touched. Each new line goes to the end of the
// group of the file it belongs to ("# Terminal" and so on), where its
// neighbours are, and not into a block of its own at the end. A block
// "# <group>: added by a newer f4" that an earlier release wrote at the end of
// the section is dissolved the same way: its lines move into their groups.
// It returns how many keys it added.
//
// It must run with the palette just built from the file (base style first,
// then the file), before anything adjusts it, and only for the Custom scheme:
// the palette of another style says nothing about what belongs in this file.
func AddMissingColorKeys(path string, file *ini.File) int {
	var missing []ColorSlot
	for _, slot := range ColorSlots {
		// These two are opt-in surfaces: absent means "not chosen".
		if slot.Index == vtui.ColDialogIndicatorBackground || slot.Index == ColDialogSettingsBackground {
			continue
		}
		if !colorIniDefinesSlot(file, slot) {
			missing = append(missing, slot)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	text := string(data)
	if len(missing) == 0 && !strings.Contains(text, addedByNewerMarker) {
		return 0
	}
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	additions := map[string][]string{}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Canonical < missing[j].Canonical })
	for _, slot := range missing {
		additions[slot.Group] = append(additions[slot.Group], slot.Canonical+" = "+exportSlotValue(slot))
	}
	out, ok := placeColorLines(lines, additions)
	if !ok {
		return 0
	}
	// #nosec G703 -- path is the user's farcolors.ini in the profile directory, given by UserColorOverridesPath.
	if err := os.WriteFile(path, []byte(strings.Join(out, newline)), 0600); err != nil {
		return 0
	}
	return len(missing)
}

// addedByNewerMarker ends the heading of the block the first version of this
// file's feature wrote at the end of the section.
const addedByNewerMarker = ": added by a newer f4"

// colorSection returns the lines [start+1, end) of the [farcolors] section, and
// false when the file has none.
func colorSection(lines []string) (start, end int, ok bool) {
	start, end = -1, len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		if strings.EqualFold(trimmed, "[farcolors]") {
			start = i
			continue
		}
		if start >= 0 {
			end = i
			break
		}
	}
	return start, end, start >= 0
}

// isColorGroupHeader reports whether line is "# <group>" for one of the groups.
func isColorGroupHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
	for _, group := range ColorGroups {
		if name == group {
			return group, true
		}
	}
	return "", false
}

// placeColorLines puts each group's new lines at the end of that group in the
// [farcolors] section, after dissolving the "added by a newer f4" blocks into
// the same additions. A group the file has no heading for gets one at the end
// of the section. It reports false when there is no section to put anything in.
func placeColorLines(lines []string, additions map[string][]string) ([]string, bool) {
	start, end, ok := colorSection(lines)
	if !ok {
		return lines, false
	}
	// The blank lines that close the section (and the file's last newline, as
	// an empty last element) stay where they are.
	section := lines[start+1 : end]
	closing := 0
	for closing < len(section) && strings.TrimSpace(section[len(section)-1-closing]) == "" {
		closing++
	}
	tail := append([]string{}, section[len(section)-closing:]...)
	section = section[:len(section)-closing]
	// 1. Take the old blocks out: heading, its lines, and the blank line above.
	var kept []string
	for i := 0; i < len(section); i++ {
		trimmed := strings.TrimSpace(section[i])
		if strings.HasPrefix(trimmed, "#") && strings.HasSuffix(trimmed, addedByNewerMarker) {
			group := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "#"), addedByNewerMarker))
			if n := len(kept); n > 0 && strings.TrimSpace(kept[n-1]) == "" {
				kept = kept[:n-1]
			}
			for i+1 < len(section) {
				next := strings.TrimSpace(section[i+1])
				if next == "" || strings.HasPrefix(next, "#") || strings.HasPrefix(next, "[") {
					break
				}
				additions[group] = append([]string{section[i+1]}, additions[group]...)
				i++
			}
			continue
		}
		kept = append(kept, section[i])
	}
	// 2. Group by group, after the last line of the group's own block.
	for _, group := range ColorGroups {
		add := additions[group]
		if len(add) == 0 {
			continue
		}
		header := -1
		for i, line := range kept {
			if name, isHeader := isColorGroupHeader(line); isHeader && name == group {
				header = i
				break
			}
		}
		if header < 0 {
			for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
				kept = kept[:len(kept)-1]
			}
			kept = append(kept, "", "# "+group)
			kept = append(kept, add...)
			continue
		}
		blockEnd := len(kept)
		for i := header + 1; i < len(kept); i++ {
			if _, isHeader := isColorGroupHeader(kept[i]); isHeader {
				blockEnd = i
				break
			}
		}
		insertAt := blockEnd
		for insertAt > header+1 && strings.TrimSpace(kept[insertAt-1]) == "" {
			insertAt--
		}
		kept = append(kept[:insertAt], append(append([]string{}, add...), kept[insertAt:]...)...)
	}
	// A group the program does not know (an old heading of a group that no longer
	// exists) keeps its lines, under a heading of its own.
	known := map[string]bool{}
	for _, group := range ColorGroups {
		known[group] = true
	}
	var unknown []string
	for group := range additions {
		if !known[group] {
			unknown = append(unknown, group)
		}
	}
	sort.Strings(unknown)
	for _, group := range unknown {
		kept = append(kept, "", "# "+group)
		kept = append(kept, additions[group]...)
	}
	kept = append(kept, tail...)
	out := append(append(append([]string{}, lines[:start+1]...), kept...), lines[end:]...)
	return out, true
}
