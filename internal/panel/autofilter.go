package panel

import "strings"

// The autofilter is the panel's second way to look for a name, next to the
// quick search: it hides every row that does not match instead of moving the
// cursor to the first match, so what is left on screen is the result (f4
// #1131). The two live side by side -- Alt+letter is always the quick search,
// and the filter has a key of its own: a lone Alt press (when the
// PanelAutoFilter option is on, see LoneAltTap) or the Panel.AutoFilter
// action. Matching itself is shared -- the same fuzzy matcher the
// cursor-moving search uses, so a query behaves identically in both and only
// the presentation differs.
//
// The filter is a window the user opened on purpose, so it is closed on
// purpose too: Alt again, Esc, Enter or leaving the directory. Erasing the
// query does not close it -- an empty filter just shows every row and waits
// for the next attempt. autoFilterMode is that window being open;
// autoFilterOn below is the narrower "rows are being hidden right now".
//
// The rows are deliberately not re-ranked by match quality the way the hotkey
// table ranks its own: a file panel would lose folders-first and its sort order,
// which is the frame of reference the user navigates by.
//
// fp.Entries stays the visible list, which is what nearly everything asks it
// for -- the row count, the cell provider, the cursor, the marked names, the
// totals in the status row. The rows the filter holds back live in
// unfilteredEntries, so a chunked directory load or the two-second mtime
// refresh keeps filling the complete list while the user is still typing.
// unfilteredEntries is meaningful only while autoFilterOn is set; otherwise
// fp.Entries is the complete list.

// AllEntries returns every row the panel holds, the ones the autofilter is
// hiding included. Use it wherever the question is about the directory rather
// than about what the user can see.
func (fp *FileSystemPanel) AllEntries() []*FileEntry {
	if fp == nil {
		return nil
	}
	if fp.autoFilterOn {
		return fp.unfilteredEntries
	}
	return fp.Entries
}

// setEntries replaces the panel's complete row list and re-derives the visible
// one. Directory loads go through here instead of assigning fp.Entries, so a
// load cannot overwrite the rows an active filter is holding back.
func (fp *FileSystemPanel) setEntries(entries []*FileEntry) {
	fp.entriesRevision++
	if fp.autoFilterOn {
		fp.unfilteredEntries = entries
	} else {
		fp.Entries = entries
	}
	fp.refilterEntries()
}

// addEntries appends rows to the complete list. A chunked load calls it once
// per chunk, so the visible list is re-derived per chunk rather than per row.
func (fp *FileSystemPanel) addEntries(entries ...*FileEntry) {
	fp.entriesRevision++
	if fp.autoFilterOn {
		fp.unfilteredEntries = append(fp.unfilteredEntries, entries...)
	} else {
		fp.Entries = append(fp.Entries, entries...)
	}
	fp.refilterEntries()
}

// autoFilterQuery is the search string without the leading '*' that marks an
// unanchored search. A query of only '*' matches everything and is therefore
// no filter at all.
func autoFilterQuery(search string) string {
	return strings.TrimPrefix(search, "*")
}

// autoFilterWanted reports whether the panel should be narrowed right now.
func (fp *FileSystemPanel) autoFilterWanted() bool {
	return fp != nil && fp.autoFilterMode && fp.FastFindMode &&
		autoFilterQuery(fp.FastFindStr) != ""
}

// AutoFilterActive reports whether the filter window is open on this panel,
// whether or not its query is hiding anything yet.
func (fp *FileSystemPanel) AutoFilterActive() bool {
	return fp != nil && fp.FastFindMode && fp.autoFilterMode
}

// ToggleAutoFilter opens the filter window, or closes it when it is open.
// A quick search in progress is turned into a filter over the same query
// rather than thrown away: the user asked for the other presentation of what
// they already typed.
func (fp *FileSystemPanel) ToggleAutoFilter() {
	if fp == nil {
		return
	}
	if fp.AutoFilterActive() {
		fp.ExitFastFind()
		fp.Refresh()
		return
	}
	// A filter answers "which files have this in the name", so it starts
	// unanchored. Seeding the '*' rather than special-casing the matcher keeps
	// one meaning for the prefix: F2 still takes it off and narrows the filter
	// to names starting with the query.
	if !strings.HasPrefix(fp.FastFindStr, "*") {
		fp.FastFindStr = "*" + fp.FastFindStr
	}
	fp.FastFindMode = true
	fp.autoFilterMode = true
	fp.updateAutoFilter()
}

// refilterEntries brings the visible list in line with the current search
// string: it takes the complete list over when the filter starts, gives it
// back when the filter ends, and otherwise rebuilds the matching subset.
func (fp *FileSystemPanel) refilterEntries() {
	defer fp.rebuildDisplayRows()
	// Called directly (updateAutoFilter/ExitFastFind) as well as through
	// setEntries/addEntries above, and any of its branches below can replace
	// fp.Entries wholesale, so it owns its own entriesRevision bump rather
	// than relying on a caller to have already done one.
	fp.entriesRevision++
	switch want := fp.autoFilterWanted(); {
	case want && !fp.autoFilterOn:
		fp.autoFilterOn = true
		fp.unfilteredEntries = fp.Entries
	case !want && fp.autoFilterOn:
		fp.autoFilterOn = false
		fp.Entries = fp.unfilteredEntries
		fp.unfilteredEntries = nil
		return
	case !want:
		return
	}

	visible := make([]*FileEntry, 0, len(fp.unfilteredEntries))
	for _, entry := range fp.unfilteredEntries {
		// ".." survives every query: a filter that matched nothing would
		// otherwise leave a panel with no way out of the directory.
		if entry.Name == ".." {
			visible = append(visible, entry)
			continue
		}
		if _, _, ok := fp.fastFindMatch(entry.Name); ok {
			visible = append(visible, entry)
		}
	}
	fp.Entries = visible
}

// focusEntryByName puts the cursor on name, or on the first row the user can
// act on when that name is not in the visible list any more.
func (fp *FileSystemPanel) focusEntryByName(name string) {
	if name != "" {
		for i, entry := range fp.Entries {
			if entry.Name == name {
				fp.SetCursorIndex(i)
				return
			}
		}
	}
	idx := 0
	if len(fp.Entries) > 1 && fp.Entries[0].Name == ".." {
		idx = 1
	}
	if idx >= len(fp.Entries) {
		idx = len(fp.Entries) - 1
	}
	if idx < 0 {
		idx = 0
	}
	fp.SetCursorIndex(idx)
}

// updateAutoFilter re-derives the visible rows after the search string changed.
// The cursor stays on the file it was on while that file still matches, so
// refining a query does not drag the user away from what they were aiming at.
func (fp *FileSystemPanel) updateAutoFilter() {
	focused := fp.GetRawSelectedName()
	if focused == ".." {
		// ".." survives every query, so keeping the cursor on it would pin the
		// cursor to the top row for the whole search instead of letting it
		// follow the result. Fall through to the first matching row.
		focused = ""
	}
	fp.refilterEntries()
	fp.focusEntryByName(focused)
	fp.Refresh()
}

// applyFastFind reacts to a changed search string, in whichever mode the panel
// is searching: the autofilter re-derives the visible rows, the cursor-moving
// search jumps to the first match. An open filter with an empty query still
// goes the filter way, so erasing the query gives the hidden rows back.
func (fp *FileSystemPanel) applyFastFind() {
	if fp.autoFilterMode || fp.autoFilterOn {
		fp.updateAutoFilter()
		return
	}
	fp.doFastFind(0)
}

// ExitFastFind leaves the panel quick search. When the autofilter was narrowing
// the rows it puts the hidden ones back, keeping the cursor on the file it was
// on, so closing a filter never moves the user somewhere else.
func (fp *FileSystemPanel) ExitFastFind() {
	if fp == nil {
		return
	}
	fp.FastFindMode = false
	fp.FastFindStr = ""
	fp.fastFindRejected = ""
	fp.autoFilterMode = false
	if !fp.autoFilterOn {
		return
	}
	focused := fp.GetRawSelectedName()
	fp.refilterEntries()
	fp.focusEntryByName(focused)
	fp.Refresh()
}
