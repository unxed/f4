package panel

import (
	"strings"
	"unicode"

	"github.com/unxed/vtui"
)

// SelectFromClipboard replaces the marks with names found in clipboard text.
// The previous marks remain available through RestoreSelection.
func (fp *FileSystemPanel) SelectFromClipboard(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	names := make(map[string]bool, len(fp.Entries))
	maxWords := 1
	for _, entry := range fp.Entries {
		if entry.Name != ".." {
			names[clipboardSelectionName(entry.Name)] = true
			maxWords = max(maxWords, len(strings.Fields(entry.Name)))
		}
	}
	selected := clipboardSelectionNames(text, names, maxWords)
	fp.SaveSelection()
	count := 0
	for i, entry := range fp.Entries {
		if entry.Name == ".." {
			continue
		}
		marked := selected[clipboardSelectionName(entry.Name)]
		fp.SetItemSelected(i, marked)
		if marked {
			count++
		}
	}
	fp.resortSelectedFirst()
	vtui.DebugLog("PANEL: select from clipboard matched=%d", count)
	vtui.FrameManager.Redraw()
}

func clipboardSelectionName(name string) string {
	return strings.ToLower(name)
}

func clipboardSelectionBase(name string) string {
	name = strings.TrimRight(name, "/\\")
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	return clipboardSelectionName(name)
}

type clipboardSelectionToken struct {
	text   string
	gap    string
	quoted bool
}

func clipboardSelectionNames(text string, names map[string]bool, maxWords int) map[string]bool {
	selected := make(map[string]bool)
	lookup := func(text string) string {
		if name := clipboardSelectionName(text); names[name] {
			return name
		}
		if name := clipboardSelectionBase(text); names[name] {
			return name
		}
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if name := lookup(line); name != "" {
			selected[name] = true
			continue
		}
		tokens := clipboardSelectionTokens(line)
		for i := 0; i < len(tokens); {
			candidate := tokens[i].text
			name, end := lookup(candidate), i+1
			// Unquoted names may contain spaces. Prefer the longest existing
			// panel name, but never join across an explicitly quoted token.
			if !tokens[i].quoted {
				for j := i + 1; j < len(tokens) && j < i+maxWords && !tokens[j].quoted; j++ {
					candidate += tokens[j].gap + tokens[j].text
					if match := lookup(candidate); match != "" {
						name, end = match, j+1
					}
				}
			}
			if name != "" {
				selected[name] = true
			}
			i = end
		}
	}
	return selected
}

func clipboardSelectionTokens(text string) []clipboardSelectionToken {
	runes := []rune(text)
	var tokens []clipboardSelectionToken
	lastEnd := 0
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		gap := string(runes[lastEnd:i])
		quote := rune(0)
		if runes[i] == '\'' || runes[i] == '"' {
			quote = runes[i]
			i++
		}
		start := i
		for i < len(runes) && (quote != 0 && runes[i] != quote || quote == 0 && !unicode.IsSpace(runes[i])) {
			i++
		}
		if quote != 0 && i == len(runes) {
			break // An unfinished quoted name must not select a partial name.
		}
		tokens = append(tokens, clipboardSelectionToken{text: string(runes[start:i]), gap: gap, quoted: quote != 0})
		if quote != 0 {
			i++
		}
		lastEnd = i
	}
	return tokens
}
