package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// Path hint VFS sources (AppConfig.PathHintSource).
const (
	PathHintSourceActive  = 0 // active panel only
	PathHintSourcePassive = 1 // passive panel only
	PathHintSourceBoth    = 2 // active first, then passive
)

// applyPathHintSettings pushes the path hint row limits into vtui.
func applyPathHintSettings() {
	vtui.SetAutoCompleteMaxVisible(AppConfig.PathHintMaxVisible)
	vtui.SetAutoCompletePerCategory(AppConfig.PathHintPerCategory)
}

// pathHintProvider is installed as vtui.PathHintProvider. It resolves the
// token under the cursor against the panel VFS selected by
// AppConfig.PathHintSource and returns directory listing items for the
// autocomplete menu.
func pathHintProvider(edit *vtui.Edit, word string, from, to int) []vtui.AutoCompleteItem {
	if vtui.FrameManager == nil {
		return nil
	}
	var pf *PanelsFrame
	frames := vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx)
	for i := len(frames) - 1; i >= 0; i-- {
		if candidate, ok := frames[i].(*PanelsFrame); ok {
			pf = candidate
			break
		}
	}
	if pf == nil {
		return nil
	}

	var panels []*FileSystemPanel
	switch AppConfig.PathHintSource {
	case PathHintSourcePassive:
		panels = append(panels, pf.getInactivePanel())
	case PathHintSourceBoth:
		panels = append(panels, pf.getActivePanel(), pf.getInactivePanel())
	default:
		panels = append(panels, pf.getActivePanel())
	}

	var items []vtui.AutoCompleteItem
	for _, panel := range panels {
		if panel == nil || panel.vfs == nil {
			continue
		}
		group := pathHintItems(panel.vfs, word, from, to)
		if len(group) == 0 {
			continue
		}
		if len(items) > 0 {
			items = append(items, vtui.AutoCompleteItem{Separator: true, MatchStart: -1, MatchEnd: -1})
		}
		items = append(items, group...)
	}
	return items
}

// pathHintItems does the actual work, decoupled from the panel for tests:
// split the token at the last path separator, list the directory through
// the VFS and rank the entries (fuzzy when a needle follows the separator).
func pathHintItems(v vfs.VFS, word string, from, to int) []vtui.AutoCompleteItem {
	word = strings.Trim(word, `"`)
	if word == "" {
		return nil
	}
	sepIdx := strings.LastIndexAny(word, `/\`)
	if sepIdx < 0 {
		return nil
	}
	dirPart := word[:sepIdx+1] // includes the trailing separator
	needle := word[sepIdx+1:]

	dir := dirPart
	if !v.IsAbs(dirPart) {
		base := v.GetPath()
		if base == "" {
			return nil
		}
		dir = v.Join(base, dirPart)
	}

	// A slow remote VFS degrades to "no hint" instead of freezing the UI.
	timeout := time.Duration(AppConfig.PathHintTimeout) * time.Second
	if timeout < time.Second {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var entries []vfs.VFSItem
	err := v.ReadDir(ctx, dir, func(chunk []vfs.VFSItem) {
		entries = append(entries, chunk...)
	})
	if err != nil || len(entries) == 0 {
		return nil
	}

	sort.Slice(entries, func(a, b int) bool {
		if entries[a].IsDir != entries[b].IsDir {
			return entries[a].IsDir // directories first
		}
		return strings.ToLower(entries[a].Name) < strings.ToLower(entries[b].Name)
	})

	matcher := vtui.NewFuzzyMatcher(needle, false) // nil for an empty needle
	sep := dirPart[len(dirPart)-1:]

	type cand struct {
		item       vfs.VFSItem
		start, end int
		score      int
	}
	var cands []cand
	for _, e := range entries {
		if e.Name == ".." {
			continue
		}
		if matcher == nil {
			cands = append(cands, cand{item: e, start: -1, end: -1})
			continue
		}
		score, start, end, ok := matcher.Match(e.Name)
		if !ok {
			continue
		}
		cands = append(cands, cand{item: e, start: start, end: end, score: score})
	}
	if matcher != nil {
		sort.SliceStable(cands, func(a, b int) bool {
			if cands[a].score != cands[b].score {
				return cands[a].score < cands[b].score
			}
			return cands[a].start < cands[b].start
		})
	}

	base := vtui.Palette[vtui.ColDialogText]
	items := make([]vtui.AutoCompleteItem, 0, len(cands))
	for _, c := range cands {
		text := dirPart + c.item.Name
		if c.item.IsDir {
			text += sep
		}

		// Display form: full path or just the final element; the highlight
		// marker prefixes the name exactly like in the panels.
		name := c.item.Name
		if c.item.IsDir {
			name += sep
		}
		marker := ""
		if AppConfig.ShowHighlightMarks && GlobalFileHighlighter != nil {
			if m := GlobalFileHighlighter.GetMarker(&c.item); m != "" {
				marker = m + " "
			}
		}
		display := marker + name
		matchOffset := len([]rune(marker))
		if AppConfig.PathHintFullPath {
			display = dirPart + display
			matchOffset += len([]rune(dirPart))
		}

		acItem := vtui.AutoCompleteItem{
			Text:        text,
			Display:     display,
			MatchStart:  -1,
			MatchEnd:    -1,
			ReplaceFrom: from,
			ReplaceTo:   to,
		}
		if c.start >= 0 {
			acItem.MatchStart = matchOffset + c.start
			acItem.MatchEnd = matchOffset + c.end
		}
		if GlobalFileHighlighter != nil {
			item := c.item
			acItem.Attr = GlobalFileHighlighter.GetColor(&item, base, false, false)
		}
		items = append(items, acItem)
	}
	return items
}
