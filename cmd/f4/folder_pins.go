package main

import "github.com/unxed/vtui"

// Pinned folders — issue #407.
//
// Folder bookmarks and folder history used to be two unrelated lists of the
// same thing: a directory the user wants to get back to. They are one list
// now. Marking an entry in the folder history (Ins) pins it, pinned entries
// are drawn in their own area at the top of the dialog ahead of the
// chronological ones, and the first ten of them own far2l's ten bookmark
// slots — which is where their digit hotkeys come from.
//
// Nothing new is stored. The mark is the history record's Lock flag, which
// already survived the 100-entry cap and both "clear" commands, and the slot
// is a section of far2l's bookmarks.ini, so a bookmarks file shared with
// far2l keeps its meaning in both programs. An eleventh pinned folder simply
// stays pinned without a digit.

// folderPins is the bookmark table as the folder-history dialog sees it.
type folderPins struct {
	file string
	set  BookmarkSet
}

// loadFolderPins reads the bookmark table. It returns nil when the file
// cannot be read: pinning then degrades to the plain mark it used to be,
// rather than risking a rewrite of a file we failed to parse.
func loadFolderPins() *folderPins {
	file := BookmarksFilePath()
	set, err := LoadBookmarks(file)
	if err != nil {
		vtui.DebugLog("PINS: load %q failed: %v", file, err)
		return nil
	}
	return &folderPins{file: file, set: set}
}

// slotAt returns the directory a slot points at, expanded the same way
// navigateToBookmark expands it, or "" when the slot holds no path.
func (p *folderPins) slotAt(slot int) string {
	if p == nil || slot < 0 || slot >= len(p.set) || p.set[slot].Path == "" {
		return ""
	}
	return expandPathEnv(p.set[slot].Path)
}

// slotOf reports which slot points at path, or -1 for a folder that has none.
func (p *folderPins) slotOf(path string) int {
	if p == nil || path == "" {
		return -1
	}
	for i := range p.set {
		if stored := p.slotAt(i); stored != "" && sameFolderHistoryPath(stored, path) {
			return i
		}
	}
	return -1
}

// freeSlot is the lowest slot with nothing in it, or -1 when all ten are
// taken. A slot far2l filled with a plugin location counts as taken even
// though f4 cannot navigate to it yet.
func (p *folderPins) freeSlot() int {
	if p == nil {
		return -1
	}
	for i := range p.set {
		if p.set[i].IsEmpty() {
			return i
		}
	}
	return -1
}

// save writes the table back. A failure is logged and swallowed: the digit
// is a convenience on top of the mark, and losing it must not interrupt the
// dialog the user is standing in.
func (p *folderPins) save() {
	if p == nil {
		return
	}
	if err := SaveBookmarks(p.file, p.set); err != nil {
		vtui.DebugLog("PINS: save %q failed: %v", p.file, err)
	}
}

// reconcile makes the bookmark table agree with the marks in records: a
// pinned folder without a slot claims the lowest free one, and a slot whose
// folder is no longer pinned gives it back. Occupied slots are never taken
// over, so bookmarks stored from the panel with Ctrl+Shift+N, or inherited
// from far2l, survive untouched. Reports whether anything changed.
func (p *folderPins) reconcile(records []HistoryRecord) bool {
	if p == nil {
		return false
	}
	changed := false

	// Release first, so a folder unpinned in this same pass hands its digit
	// to a folder pinned in it instead of leaving both without one.
	for slot := range p.set {
		path := p.slotAt(slot)
		if path == "" {
			continue
		}
		if !folderIsPinned(records, path) {
			p.set.deleteAtSlot(slot)
			changed = true
		}
	}

	// records are newest first, so the folder pinned most recently gets the
	// lowest digit that is still available.
	for i := range records {
		if !records[i].Lock || records[i].Name == "" {
			continue
		}
		if p.slotOf(records[i].Name) >= 0 {
			continue
		}
		free := p.freeSlot()
		if free < 0 {
			break
		}
		p.set.setCurrentDir(free, records[i].Name)
		changed = true
	}
	return changed
}

// folderIsPinned reports whether path is marked in the history list.
func folderIsPinned(records []HistoryRecord, path string) bool {
	for i := range records {
		if records[i].Lock && sameFolderHistoryPath(records[i].Name, path) {
			return true
		}
	}
	return false
}

// mergeFolderPins folds the bookmark table into the folder history list: a
// folder that owns a slot counts as pinned, and a bookmark that was never
// visited — stored from the panel, or copied in from far2l — is appended as
// a pinned entry of its own. The dialog is then the union of the two lists
// rather than a view of one of them.
//
// New entries land at the end, the oldest position: a folder nobody has
// visited must not take the "most recent" row the dialog opens on.
func mergeFolderPins(records []HistoryRecord, p *folderPins) []HistoryRecord {
	if p == nil {
		return records
	}
	merged := append([]HistoryRecord(nil), records...)
	claimed := make(map[int]bool, len(p.set))
	for i := range merged {
		if slot := p.slotOf(merged[i].Name); slot >= 0 {
			merged[i].Lock = true
			claimed[slot] = true
		}
	}
	for slot := range p.set {
		path := p.slotAt(slot)
		if path == "" || claimed[slot] {
			continue
		}
		merged = append(merged, HistoryRecord{Name: path, Lock: true})
	}
	return merged
}
