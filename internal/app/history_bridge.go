package app

import (
	"encoding/json"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"path/filepath"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const viewerEditorHistoryID = "viewer-editor"

type viewerEditorHistoryMode string

const (
	historyModeView viewerEditorHistoryMode = "view"
	historyModeEdit viewerEditorHistoryMode = "edit"
)

type viewerEditorHistoryEntry struct {
	Path     string                  `json:"path"`
	Display  string                  `json:"display"`
	Mode     viewerEditorHistoryMode `json:"mode"`
	Local    bool                    `json:"local,omitempty"`
	VFSType  string                  `json:"vfs_type,omitempty"`
	VFSTitle string                  `json:"vfs_title,omitempty"`
	Lock     bool                    `json:"lock,omitempty"`
	// Timestamp is when the file was last opened. far2l stamps every
	// history record it stores; the dialog shows the stamp so a file can be
	// found by when it was opened rather than by its name (#408). Records
	// written before this existed have a zero timestamp and keep working.
	Timestamp time.Time `json:"timestamp,omitempty"`
}

func loadViewerEditorHistory() []viewerEditorHistoryEntry {
	if vtui.GlobalHistoryProvider == nil {
		return nil
	}
	encoded := vtui.GlobalHistoryProvider.LoadHistory(viewerEditorHistoryID)
	entries := make([]viewerEditorHistoryEntry, 0, len(encoded))
	for _, item := range encoded {
		var entry viewerEditorHistoryEntry
		if json.Unmarshal([]byte(item), &entry) != nil || entry.Path == "" {
			continue
		}
		if entry.Display == "" {
			entry.Display = entry.Path
		}
		if entry.Mode != historyModeEdit {
			entry.Mode = historyModeView
		}
		entries = append(entries, entry)
	}
	return entries
}

func saveViewerEditorHistory(entries []viewerEditorHistoryEntry) {
	if vtui.GlobalHistoryProvider == nil {
		return
	}
	encoded := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err == nil {
			encoded = append(encoded, string(data))
		}
	}
	vtui.GlobalHistoryProvider.SaveHistory(viewerEditorHistoryID, encoded)
}

func rememberViewerEditorHistory(fs vfs.VFS, path string, mode viewerEditorHistoryMode) {
	if fs == nil || path == "" || vtui.GlobalHistoryProvider == nil {
		return
	}
	// The terminal log is generated live and has no stable file to reopen.
	if _, transient := fs.(*terminal.TerminalLogVFS); transient {
		return
	}

	entry := viewerEditorHistoryEntry{
		Path:      path,
		Display:   path,
		Mode:      mode,
		VFSType:   fmt.Sprintf("%T", fs),
		Timestamp: time.Now(),
	}
	if local, ok := fs.(*vfs.OSVFS); ok {
		if absolute, err := local.Abs(path); err == nil {
			entry.Path = absolute
			entry.Display = absolute
		}
		entry.Local = true
	} else if titled, ok := fs.(vfs.TitleProvider); ok {
		entry.VFSTitle = titled.GetTitle()
		if entry.VFSTitle != "" && !strings.HasPrefix(path, entry.VFSTitle+":") {
			entry.Display = entry.VFSTitle + ":" + path
		}
	}

	entries := loadViewerEditorHistory()
	filtered := make([]viewerEditorHistoryEntry, 0, len(entries)+1)
	filtered = append(filtered, entry)
	for _, old := range entries {
		if sameViewerEditorHistoryFile(old, entry) {
			entry.Lock = old.Lock
			filtered[0] = entry
			continue
		}
		filtered = append(filtered, old)
	}
	filtered = limitViewerEditorHistory(filtered, 100)
	saveViewerEditorHistory(filtered)
}

func limitViewerEditorHistory(entries []viewerEditorHistoryEntry, limit int) []viewerEditorHistoryEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	locked := 0
	for _, entry := range entries {
		if entry.Lock {
			locked++
		}
	}
	unlockedBudget := limit - locked
	if unlockedBudget < 0 {
		unlockedBudget = 0
	}
	kept := make([]viewerEditorHistoryEntry, 0, limit)
	for _, entry := range entries {
		if entry.Lock {
			kept = append(kept, entry)
		} else if unlockedBudget > 0 {
			kept = append(kept, entry)
			unlockedBudget--
		}
	}
	return kept
}

func sameViewerEditorHistoryFile(a, b viewerEditorHistoryEntry) bool {
	if a.Local != b.Local || a.VFSType != b.VFSType || a.VFSTitle != b.VFSTitle {
		return false
	}
	if a.Local {
		return panel.SameFolderHistoryPath(a.Path, b.Path)
	}
	return a.Path == b.Path
}

func viewerEditorHistoryVFS(pf *panel.PanelsFrame, entry viewerEditorHistoryEntry) vfs.VFS {
	if entry.Local {
		return vfs.NewOSVFS(filepath.Dir(entry.Path))
	}
	for _, pnl := range []*panel.FileSystemPanel{pf.GetActivePanel(), pf.GetInactivePanel()} {
		if pnl == nil || fmt.Sprintf("%T", pnl.Vfs) != entry.VFSType {
			continue
		}
		title := ""
		if titled, ok := pnl.Vfs.(vfs.TitleProvider); ok {
			title = titled.GetTitle()
		}
		if title == entry.VFSTitle {
			return pnl.Vfs
		}
	}
	return nil
}

func openViewerEditorHistoryEntry(pf *panel.PanelsFrame, entry viewerEditorHistoryEntry, mode viewerEditorHistoryMode) bool {
	fs := viewerEditorHistoryVFS(pf, entry)
	if fs == nil {
		vtui.ShowMessage(i18n.Msg("History.ViewEditTitle"), i18n.Msg("History.SourceUnavailable"), []string{i18n.Msg("vtui.Ok")})
		return false
	}
	if mode == historyModeEdit {
		actionOpenEditor(pf, fs, entry.Path)
	} else {
		actionOpenViewer(pf, fs, entry.Path)
	}
	return true
}

// revealViewerEditorHistoryEntry opens the folder of a history entry in the
// active panel and puts the cursor on the file, the way Find file's "Go to"
// does: for the case where the name is forgotten but the place is not, and the
// file is wanted in the panel, not in the viewer (#408). A file that no longer
// exists still gets its folder. Only files of the local disk can be shown, as a
// panel has to be pointed at their folder.
func revealViewerEditorHistoryEntry(pf *panel.PanelsFrame, entry viewerEditorHistoryEntry) bool {
	target := pf.GetActivePanel()
	if target == nil || !entry.Local {
		vtui.ShowMessage(i18n.Msg("History.ViewEditTitle"), i18n.Msg("History.SourceUnavailable"), []string{i18n.Msg("vtui.Ok")})
		return false
	}
	dir, name := filepath.Dir(entry.Path), filepath.Base(entry.Path)
	if panel.SameFolderHistoryPath(target.Vfs.GetPath(), dir) {
		// Already there: navigating would reload the folder and put the cursor
		// back where it was, so only the cursor is moved.
		pf.ShowPanels = true
		target.SelectName(name)
		return true
	}
	if !pf.NavigateToPath(target, dir) {
		return false
	}
	pf.ShowPanels = true
	// After the navigation, which sets a selection of its own.
	target.PendingSelection = name
	return true
}

func actionViewerEditorHistory(pf *panel.PanelsFrame) {
	entries := loadViewerEditorHistory()
	if len(entries) == 0 {
		vtui.ShowMessage(i18n.Msg("History.Title"), i18n.Msg("History.EmptyViewEdit"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	paths := make([]history.HistoryRecord, len(entries))
	modes := make([]string, len(entries))
	for i, entry := range entries {
		paths[i] = history.HistoryRecord{Name: entry.Display, Lock: entry.Lock, Timestamp: entry.Timestamp}
		if entry.Mode == historyModeEdit {
			modes[i] = i18n.Msg("History.Mode.Edit")
		} else {
			modes[i] = i18n.Msg("History.Mode.View")
		}
	}

	menu := vtui.NewVMenu(i18n.Msg("History.ViewEditTitle"))
	menu.SetHelp("HistoryViewEdit")
	search := newHistorySearch(menu, paths, i18n.Msg("History.ViewEditHint"))
	search.supportsLocks = true
	// Same timestamp column the command and folder histories use, with its
	// own Ctrl+T mode remembered separately — far2l keeps one setting per
	// history type too.
	search.showTimes = true
	search.timeMode = config.App.HistoryShowTimes[config.HistoryTypeViewEdit]
	search.onTimesChanged = func(mode int) {
		config.App.HistoryShowTimes[config.HistoryTypeViewEdit] = mode
		config.SaveConfig()
	}
	search.onLockToggled = func() {
		for i := range entries {
			entries[i].Lock = search.all[i].Lock
		}
		saveViewerEditorHistory(entries)
	}
	search.setSecondaryWidth(modes, true, 10)

	// Ctrl+F10, as in the command history: show the file's folder in the panel.
	search.onCtrlF10 = func(history.HistoryRecord) {
		idx, _, ok := search.selected()
		if !ok || idx < 0 || idx >= len(entries) {
			return
		}
		entry := entries[idx]
		search.cleanup()
		menu.Close()
		revealViewerEditorHistoryEntry(pf, entry)
	}

	openCurrent := func(override viewerEditorHistoryMode) {
		idx, _, ok := search.selected()
		if !ok || idx < 0 || idx >= len(entries) {
			return
		}
		mode := override
		if mode == "" {
			mode = entries[idx].Mode
		}
		if openViewerEditorHistoryEntry(pf, entries[idx], mode) {
			search.cleanup()
			menu.Close()
		}
	}
	menu.OnAction = func(int) { openCurrent("") }
	menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		shift := e.ControlKeyState&vtinput.ShiftPressed != 0
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
		if search.processKey(e) {
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10 {
			search.cleanup()
			return false
		}

		idx, _, ok := search.selected()
		if !ok || idx < 0 || idx >= len(entries) {
			return false
		}
		switch e.VirtualKeyCode {
		case vtinput.VK_RETURN:
			openCurrent("")
			return true
		case vtinput.VK_F3:
			openCurrent(historyModeView)
			return true
		case vtinput.VK_F4:
			openCurrent(historyModeEdit)
			return true
		}

		if (e.VirtualKeyCode == vtinput.VK_DELETE || e.VirtualKeyCode == vtinput.VK_BACK) && shift {
			if search.deleteSelected() {
				entries = append(entries[:idx], entries[idx+1:]...)
				saveViewerEditorHistory(entries)
			}
			if len(entries) == 0 {
				search.cleanup()
				menu.Close()
			}
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift {
			confirmAndClearViewerEditorHistory(&entries, search, menu)
			return true
		}
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			terminal.SetClipboardAsync(entries[idx].Path)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
}

func confirmAndClearViewerEditorHistory(entries *[]viewerEditorHistoryEntry, search *historySearch, menu *vtui.VMenu) {
	dlg := vtui.ShowMessage(i18n.Msg("History.ViewEditTitle"), i18n.Msg("History.ConfirmClearAll"), []string{i18n.Msg("vtui.Ok"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		keptEntries := make([]viewerEditorHistoryEntry, 0)
		keptRecords := make([]history.HistoryRecord, 0)
		for i, entry := range *entries {
			if entry.Lock {
				keptEntries = append(keptEntries, entry)
				keptRecords = append(keptRecords, search.all[i])
			}
		}
		*entries = keptEntries
		saveViewerEditorHistory(keptEntries)
		search.setItems(keptRecords)
		if len(keptEntries) == 0 {
			search.cleanup()
			menu.Close()
		}
	}
}
