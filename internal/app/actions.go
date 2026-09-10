package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/gui"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const openingProgressDelay = 250 * time.Millisecond

// editorHeaderIsBinary: NUL in the header means binary ? text in any codepage
// (cp1251 included, which the viewer's utf8 check would call binary) has none.
func editorHeaderIsBinary(header []byte, cpID int) bool {
	// DecodeBytes is a no-op for 65001 and leaves the data alone on error.
	if decoded, err := vfs.DecodeBytes(header, cpID); err == nil {
		header = decoded
	}
	return bytes.IndexByte(header, 0) >= 0
}

var (
	LastFindFileMask          = "*"
	LastFindFileText          = ""
	LastFindFileCaseSensitive = false
	LastFindFileWholeWords    = false
	LastFindFileRegexp        = false
	LastFindFileNotContaining = false
	LastFindFileFolders       = false
	LastFindFileSymlinks      = false
)

func choiceText(choices []string, selected int) string {
	for i, choice := range choices {
		if i == selected {
			return choice
		}
	}
	return ""
}

func actionFoldersHistory(pf *panel.PanelsFrame) {
	if vtui.GlobalHistoryProvider == nil {
		return
	}
	richFolders, folderHP := history.LoadFolderHistoryRecords(vtui.GlobalHistoryProvider)
	// Folder bookmarks and folder history are one list now (#407). Fold the
	// bookmark table in before the emptiness check, so a profile whose only
	// saved folders are bookmarks still opens the dialog, and hand the marks
	// their digits straight away ? a folder locked before this existed picks
	// up a hotkey the first time the dialog is opened.
	var pins *panel.FolderPins
	if folderHP != nil {
		if pins = panel.LoadFolderPins(); pins != nil {
			richFolders = panel.MergeFolderPins(richFolders, pins)
			if pins.Reconcile(richFolders) {
				pins.Save()
			}
		}
	}
	h := history.ExtractNames(richFolders)
	if len(richFolders) == 0 {
		vtui.ShowMessage(i18n.Msg("History.Title"), i18n.Msg("History.EmptyFolders"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	menu := vtui.NewVMenu(i18n.Msg("History.FoldersTitle"))
	menu.SetHelp("HistoryFolders")

	search := newHistorySearch(menu, richFolders, i18n.Msg("History.FoldersHint"))
	search.supportsLocks = folderHP != nil
	search.showTimes = true
	search.timeMode = config.App.HistoryShowTimes[config.HistoryTypeFolders]
	if pins != nil {
		search.pinSlotOf = func(rec history.HistoryRecord) int { return pins.SlotOf(rec.Name) }
	}
	search.onTimesChanged = func(mode int) {
		config.App.HistoryShowTimes[config.HistoryTypeFolders] = mode
		config.SaveConfig()
	}

	// persist writes the folder list back and keeps the bookmark table in
	// step with it: pinning an entry claims a digit, unpinning gives it back.
	persist := func() {
		richFolders = append([]history.HistoryRecord(nil), search.all...)
		h = history.ExtractNames(richFolders)
		if folderHP != nil {
			history.SaveFolderHistoryRecords(folderHP, richFolders)
		} else if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory("folders", h)
		}
		if pins != nil && pins.Reconcile(richFolders) {
			pins.Save()
		}
	}
	search.onLockToggled = func() {
		persist()
		// The row has just moved into or out of the pinned area.
		search.refreshKeepingSelection()
	}
	search.applyFilter()

	if activePanel := pf.GetActivePanel(); activePanel != nil {
		currentPath := activePanel.PersistentPath()
		for historyPos, path := range h {
			if panel.SameFolderHistoryPath(path, currentPath) {
				search.selectOriginalIndex(historyPos)
				break
			}
		}
	}

	// Shared "cd on active panel" path used by bare Enter and by mouse click.
	gotoActive := func(pos int) {
		search.cleanup()
		menu.Close()
		if targetPanel := pf.GetActivePanel(); targetPanel != nil {
			// The menu is oldest ? newest. If the selected path disappeared,
			// navigateAvailableFolderHistory walks toward newer entries.
			pf.NavigateAvailableFolderHistory(targetPanel, h, pos, -1)
		}
	}
	menu.OnAction = func(int) {
		if historyPos, _, ok := search.selected(); ok {
			gotoActive(historyPos)
		}
	}

	// Setup shortcuts
	menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
		ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		if search.processKey(e) {
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10 {
			search.cleanup()
			return false
		}

		// Alt+digit reaches the pinned folders by their slot digit, the same
		// table RightCtrl+digit uses from the panel ? that hotkey cannot get
		// through while this dialog is on top of the frame stack (#407).
		if pins != nil && alt && !shift &&
			e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 {
			if path := pins.SlotAt(int(e.VirtualKeyCode - vtinput.VK_0)); path != "" {
				search.cleanup()
				menu.Close()
				if targetPanel := pf.GetActivePanel(); targetPanel != nil {
					pf.NavigateToPath(targetPanel, path)
				}
			}
			return true
		}

		// Ctrl+R: drop entries whose path no longer exists on disk (far2l).
		if e.VirtualKeyCode == vtinput.VK_R && ctrl && !alt && !shift {
			confirmAndPruneMissingFolderHistory(&h, &richFolders, folderHP, search, menu)
			return true
		}

		historyPos, rec, ok := search.selected()
		if !ok {
			return false
		}
		path := rec.Name

		if e.VirtualKeyCode == vtinput.VK_RETURN {
			if ctrl {
				// Insert into command line
				search.cleanup()
				pf.CmdLine.InsertString(path)
				menu.Close()
				return true
			}
			if shift {
				search.cleanup()
				menu.Close()
				if targetPanel := pf.GetInactivePanel(); targetPanel != nil {
					pf.NavigateAvailableFolderHistory(targetPanel, h, historyPos, -1)
				}
				return true
			}
			gotoActive(historyPos)
			return true
		}

		if (e.VirtualKeyCode == vtinput.VK_DELETE || e.VirtualKeyCode == vtinput.VK_BACK) && shift {
			// Delete item
			if search.deleteSelected() {
				persist()
			}
			if len(search.all) == 0 {
				search.cleanup()
				menu.Close()
			}
			return true
		}

		// Del (no modifiers): clear the whole history with a confirmation.
		if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift {
			if folderHP != nil {
				confirmAndClearRichHistory(i18n.Msg("History.FoldersTitle"), "folders", &richFolders, func() {
					h = history.ExtractNames(richFolders)
					folderHP.SaveHistory("folders", h)
					pf.CmdLine.Edit.HistoryPos = -1
				}, search, menu)
			} else {
				confirmAndClearHistory(i18n.Msg("History.FoldersTitle"), "folders", &h, func() {
					pf.CmdLine.Edit.HistoryPos = -1
				}, search, menu)
			}
			return true
		}

		// Ctrl+C / Ctrl+Ins: copy the selected entry to the clipboard.
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			terminal.SetClipboardAsync(path)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
}

func actionCommandHistory(pf *panel.PanelsFrame) {
	h := pf.CmdLine.Edit.History
	if len(h) == 0 {
		vtui.ShowMessage(i18n.Msg("History.Title"), i18n.Msg("History.EmptyCommands"), []string{i18n.Msg("vtui.Ok")})
		return
	}

	var richCmds []history.HistoryRecord
	hp, isF4 := vtui.GlobalHistoryProvider.(*history.F4HistoryProvider)
	if isF4 {
		richCmds = hp.LoadRichHistory("cmdline")
	} else {
		for _, c := range h {
			richCmds = append(richCmds, history.HistoryRecord{Name: c})
		}
	}
	if len(richCmds) == 0 && len(h) > 0 {
		richCmds = history.RecordsFromNames(h)
	}
	// The tab/workspace branch originally stored command directories in a
	// parallel history. Fold those records into upstream's richer history
	// model so existing sessions retain their paths after the merge.
	legacyPaths := history.LoadCommandHistoryPaths(h)
	for i := range richCmds {
		if richCmds[i].Directory() == "" && i < len(legacyPaths) {
			richCmds[i].Dir = legacyPaths[i]
		}
	}

	menu := vtui.NewVMenu(i18n.Msg("History.CommandsTitle"))
	menu.SetHelp("History")
	search := newHistorySearch(menu, richCmds, i18n.Msg("History.CommandsHint"))
	search.supportsLocks = isF4
	search.showDetails = true
	search.showTimes = true
	search.timeMode = config.App.HistoryShowTimes[config.HistoryTypeCommands]
	search.showDirPrefix = true
	search.dirPrefixLen = config.App.HistoryDirsPrefixLen
	search.onTimesChanged = func(mode int) {
		config.App.HistoryShowTimes[config.HistoryTypeCommands] = mode
		config.SaveConfig()
	}
	search.onPrefixChanged = func(width int) {
		config.App.HistoryDirsPrefixLen = width
		config.SaveConfig()
	}
	search.showSecond = search.hasSecondary()
	search.secondWidth = 24
	search.applyFilter()

	search.onLockToggled = func() {
		if isF4 {
			hp.SaveRichHistory("cmdline", search.all)
		}
	}
	search.onCtrlF10 = func(rec history.HistoryRecord) {
		if dir := rec.Directory(); dir != "" {
			search.cleanup()
			menu.Close()
			if targetPanel := pf.GetActivePanel(); targetPanel != nil {
				pf.NavigateToPath(targetPanel, dir)
			}
		}
	}
	search.onDetails = func(rec history.HistoryRecord) {
		showCommandHistoryDetails(pf, rec, search, menu)
	}

	// Shared "paste selected command" path used by Enter and mouse click.
	// The record is passed in because VMenu.Close restores its initial
	// selection, which would otherwise make Enter paste a different row.
	pasteRecord := func(rec history.HistoryRecord) {
		search.cleanup()
		pf.CmdLine.Edit.SetText(rec.Name)
		pf.CmdLine.Edit.HistoryPos = -1
	}
	// VMenu.ProcessMouse calls SetExitCode after OnAction, so click closes
	// the menu automatically ? pasteRecord only does the side effect.
	menu.OnAction = func(int) {
		_, rec, ok := search.selected()
		if ok {
			pasteRecord(rec)
		}
	}

	// Setup shortcuts
	menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
		ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		if search.processKey(e) {
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10 {
			search.cleanup()
			return false
		}

		_, rec, ok := search.selected()
		if !ok {
			return false
		}
		path := search.selectedSecondary()

		if e.VirtualKeyCode == vtinput.VK_RETURN {
			if ctrl && shift && !alt {
				if path != "" {
					search.cleanup()
					menu.Close()
					pf.InsertPathToCmdLine(path)
				}
				return true
			}
			menu.Close()
			pasteRecord(rec)
			return true
		}

		if e.VirtualKeyCode == vtinput.VK_NEXT && ctrl && !shift && !alt {
			if path != "" {
				if targetPanel := pf.GetActivePanel(); targetPanel != nil && pf.NavigateToPath(targetPanel, path) {
					search.cleanup()
					menu.Close()
				}
			}
			return true
		}

		if (e.VirtualKeyCode == vtinput.VK_DELETE || e.VirtualKeyCode == vtinput.VK_BACK) && shift {
			// Delete item
			if search.deleteSelected() {
				if isF4 {
					hp.SaveRichHistory("cmdline", search.all)
				}
				h = history.ExtractNames(search.all)
				pf.CmdLine.Edit.History = h
				if !isF4 && vtui.GlobalHistoryProvider != nil {
					vtui.GlobalHistoryProvider.SaveHistory("cmdline", h)
				}
			}
			if len(search.all) == 0 {
				search.cleanup()
				menu.Close()
			}
			return true
		}

		// Del (no modifiers): clear the whole history with a confirmation.
		if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift {
			confirmAndClearRichHistory(i18n.Msg("History.CommandsTitle"), "cmdline", &richCmds, func() {
				h = history.ExtractNames(richCmds)
				pf.CmdLine.Edit.History = h
				pf.CmdLine.Edit.HistoryPos = -1
				if !isF4 && vtui.GlobalHistoryProvider != nil {
					vtui.GlobalHistoryProvider.SaveHistory("cmdline", h)
				}
			}, search, menu)
			return true
		}

		// Ctrl+C / Ctrl+Ins: copy the selected entry to the clipboard.
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			terminal.SetClipboardAsync(rec.Name)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
}

func showCommandHistoryDetails(pf *panel.PanelsFrame, rec history.HistoryRecord, search *historySearch, menu *vtui.VMenu) {
	dateText := "None"
	timeText := "None"
	if !rec.Timestamp.IsZero() {
		dateText = rec.Timestamp.Format("2006-01-02")
		timeText = rec.Timestamp.Format("15:04:05")
	}
	dir := rec.Directory()
	message := fmt.Sprintf("Command: %s\nDirectory: %s\nDate: %s\nTime: %s", rec.Name, dir, dateText, timeText)
	buttons := []string{"&Close"}
	if dir != "" {
		buttons = append(buttons, "&ChDir", "&Run-up")
	}
	dlg := vtui.ShowMessage(i18n.Msg("History.CommandsTitle"), message, buttons)
	dlg.OnResult = func(code int) {
		if code == 0 || dir == "" {
			return
		}
		search.cleanup()
		menu.Close()
		if code == 1 {
			if pnl := pf.GetActivePanel(); pnl != nil {
				pf.NavigateToPath(pnl, dir)
			}
			return
		}
		if code == 2 {
			pf.CmdLine.Edit.SetText(rec.Name)
			pf.CmdLine.Edit.HistoryPos = -1
		}
	}
}

// confirmAndClearHistory shows a Yes/No dialog, and on Yes wipes the
// history file identified by providerName, invokes localReset (used for
// state kept outside the provider, e.g. cmdLine.Edit.History), and
// closes the history menu. Mirrors far2l's Del handler in history.cpp
// with the "confirm history clear" option always on.
func confirmAndClearHistory(title, providerName string, h *[]string, localReset func(), search *historySearch, menu *vtui.VMenu) {
	buttons := []string{i18n.Msg("vtui.Ok"), i18n.Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(title, i18n.Msg("History.ConfirmClearAll"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		*h = nil
		if localReset != nil {
			localReset()
		}
		if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory(providerName, nil)
		}
		search.cleanup()
		menu.Close()
	}
}

// confirmAndClearRichHistory clears every unpinned record while preserving
// entries explicitly pinned with Insert.
func confirmAndClearRichHistory(title, providerName string, h *[]history.HistoryRecord, localReset func(), search *historySearch, menu *vtui.VMenu) {
	buttons := []string{i18n.Msg("vtui.Ok"), i18n.Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(title, i18n.Msg("History.ConfirmClearAll"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		kept := make([]history.HistoryRecord, 0)
		for _, r := range *h {
			if r.Lock {
				kept = append(kept, r)
			}
		}
		*h = kept
		if localReset != nil {
			localReset()
		}
		if hp, ok := vtui.GlobalHistoryProvider.(*history.F4HistoryProvider); ok {
			hp.SaveRichHistory(providerName, kept)
		}
		search.setItems(kept)
		if len(kept) == 0 {
			search.cleanup()
			menu.Close()
		}
	}
}
func confirmAndPruneMissingFolderHistory(h *[]string, rich *[]history.HistoryRecord, hp *history.F4HistoryProvider, search *historySearch, menu *vtui.VMenu) {
	buttons := []string{i18n.Msg("vtui.Ok"), i18n.Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(i18n.Msg("History.FoldersTitle"), i18n.Msg("History.ConfirmPruneMissing"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		kept := make([]history.HistoryRecord, 0, len(*rich))
		for _, record := range *rich {
			p := record.Name
			if record.Lock {
				kept = append(kept, record)
				continue
			}
			if fileops.IsPersistentURIPath(p) || vfs.FindStandaloneProvider(context.Background(), nil, p) != nil {
				kept = append(kept, record)
				continue
			}
			if _, err := os.Stat(p); err == nil {
				kept = append(kept, record)
			}
		}
		if len(kept) == len(*rich) {
			return
		}
		*rich = kept
		*h = history.ExtractNames(kept)
		if hp != nil {
			history.SaveFolderHistoryRecords(hp, kept)
		} else if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory("folders", *h)
		}
		search.setItems(kept)
		if len(kept) == 0 {
			search.cleanup()
			menu.Close()
		}
	}
}

func actionSortMenu(pf *panel.PanelsFrame) {
	actionSortMenuForPanel(pf, pf.GetActivePanel())
}

func actionSortMenuForPanel(pf *panel.PanelsFrame, fsp *panel.FileSystemPanel) {
	if fsp == nil {
		return
	}

	entries := []struct {
		mode     panel.SortMode
		labelKey string
		shortcut string
	}{
		{mode: panel.SortName, labelKey: "Menu.SortName", shortcut: "Ctrl+F3"},
		{mode: panel.SortExt, labelKey: "Menu.SortExt", shortcut: "Ctrl+F4"},
		{mode: panel.SortTime, labelKey: "Menu.SortTime", shortcut: "Ctrl+F5"},
		{mode: panel.SortSize, labelKey: "Menu.SortSize", shortcut: "Ctrl+F6"},
		{mode: panel.SortUnsorted, labelKey: "Menu.SortUnsorted", shortcut: "Ctrl+F7"},
	}

	menu := vtui.NewVMenu(i18n.Msg("Sort.Title"))
	selected := 0
	for idx, entry := range entries {
		prefix := "  "
		if entry.mode == fsp.SortMode {
			prefix = "? "
			selected = idx
		}
		menu.AddItem(vtui.MenuItem{
			Text:     prefix + i18n.Msg(entry.labelKey),
			Shortcut: entry.shortcut,
		})
	}

	// The last row is a toggle rather than a mode, the way far puts "use sort
	// groups" below the mode list. Its index is len(entries).
	groupsPrefix := "  "
	if fsp.UseSortGroups {
		groupsPrefix = "? "
	}
	menu.AddItem(vtui.MenuItem{
		Text:     groupsPrefix + i18n.Msg("Menu.SortUseGroups"),
		Shortcut: keymap.MenuShortcutsForAction("Shell", "Panel.SortUseGroups"),
	})

	menu.SetSelectPos(selected)
	menu.OnAction = func(idx int) {
		switch {
		case idx >= 0 && idx < len(entries):
			fsp.SetSortMode(entries[idx].mode)
		case idx == len(entries):
			fsp.ToggleSortGroups()
		default:
			return
		}
		pf.UpdateMenuCheckmarks()
		vtui.FrameManager.Redraw()
	}

	w, h := 36, len(entries)+3
	panelX1, panelY1, panelX2, panelY2 := fsp.GetPosition()
	panelW := panelX2 - panelX1 + 1
	panelH := panelY2 - panelY1 + 1
	maxW := panelW - 2
	if maxW < 1 {
		maxW = panelW
	}
	if w > maxW {
		w = maxW
	}
	if h > panelH {
		h = panelH
	}
	x := panelX1 + (panelW-w)/2
	y := panelY1 + (panelH-h)/2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	vtui.FrameManager.Push(menu)
}

func actionEditFileExternal(pf *panel.PanelsFrame, v vfs.VFS, path string, size int64) {
	rememberViewerEditorHistory(v, path, historyModeEdit)
	cmdStr := editor.ConfiguredExternalEditorCommand()
	if cmdStr == "" {
		cmdStr = os.Getenv("EDITOR")
		if cmdStr == "" {
			cmdStr = "nano" // Fallback
		}
	}

	// 1. If it's a local OSVFS file, we can just run the editor directly.
	if osvfs, ok := v.(*vfs.OSVFS); ok {
		absPath, _ := osvfs.Abs(path)
		runExternalEditor(pf, cmdStr, absPath)
		return
	}

	// 2. If it's a remote file, we need to download it to a temp file, edit, and upload back if changed.
	ext := filepath.Ext(path)
	if ext == "" {
		ext = ".txt"
	}
	tmpFile, err := os.CreateTemp("", "f4-extedit-*"+ext)
	if err != nil {
		vtui.ShowMessage(i18n.Msg("Error.Title"), fmt.Sprintf(i18n.Msg("ExtEdit.TempError"), err), []string{i18n.Msg("vtui.Ok")})
		return
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close() // Will be reopened by VFS/editor

	pf.RunProgressTask(" Downloading... ", "Preparing to download...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		src, err := v.Open(ctx, path)
		if err != nil {
			// If file does not exist, it's a new file. Just create an empty temp file.
			if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "file does not exist") {
				return nil
			}
			return err
		}
		defer func() { _ = src.Close() }()

		dst, err := os.Create(tmpPath)
		if err != nil {
			return err
		}
		closeDst := fileops.CloseOnce(dst)
		defer func() { _ = closeDst() }()

		buf := make([]byte, 128*1024)
		var downloaded int64
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := src.Read(ctx, buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					return werr
				}
				downloaded += int64(n)
				pct := 0
				if size > 0 {
					pct = int((downloaded * 100) / size)
				}
				update("Downloading...", pct)
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}
		return closeDst()
	}, func(err error) {
		if err != nil && err != context.Canceled {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to download file:\n%v", err), []string{"&Ok"})
			_ = os.Remove(tmpPath)
			return
		}
		if err == context.Canceled {
			_ = os.Remove(tmpPath)
			return
		}

		stBefore, err := os.Stat(tmpPath)
		if err != nil {
			_ = os.Remove(tmpPath)
			return
		}
		modTimeBefore := stBefore.ModTime()

		runExternalEditor(pf, cmdStr, tmpPath)

		stAfter, err := os.Stat(tmpPath)
		if err == nil && stAfter.ModTime().After(modTimeBefore) {
			pf.RunProgressTask(" Uploading... ", "Preparing to upload...", false, func(ctx context.Context, update func(msg string, percent int)) error {
				src, err := os.Open(tmpPath)
				if err != nil {
					return err
				}
				defer func() { _ = src.Close() }()

				dst, err := v.Create(ctx, path)
				if err != nil {
					return err
				}
				closeDst := fileops.CloseOnce(dst)
				defer func() { _ = closeDst() }()

				buf := make([]byte, 128*1024)
				var uploaded int64
				for {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					n, err := src.Read(buf)
					if n > 0 {
						if _, werr := dst.Write(buf[:n]); werr != nil {
							return werr
						}
						uploaded += int64(n)
						pct := 0
						if stAfter.Size() > 0 {
							pct = int((uploaded * 100) / stAfter.Size())
						}
						update("Uploading...", pct)
					}
					if err != nil {
						if err == io.EOF {
							break
						}
						return err
					}
				}
				return closeDst()
			}, func(err error) {
				_ = os.Remove(tmpPath)
				if err != nil && err != context.Canceled {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to upload file:\n%v", err), []string{"&Ok"})
				}
				pf.RefreshAll()
			})
		} else {
			_ = os.Remove(tmpPath)
			pf.RefreshAll()
		}
	})
}

func runExternalEditor(pf *panel.PanelsFrame, cmdStr, path string) {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return
	}

	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	editor.ConfigureExternalEditorProcess(cmd)
	if fsp := pf.GetActivePanel(); fsp != nil {
		if _, isLocal := fsp.Vfs.(*vfs.OSVFS); isLocal {
			cmd.Dir = fsp.Vfs.GetPath()
		}
	}

	vtui.Suspend()
	err := cmd.Run()
	_ = vtui.Resume()

	if err != nil {
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Editor exited with error:\n%v", err), []string{"&Ok"})
		})
	}
	vtui.FrameManager.PostTask(func() {
		pf.RefreshAll()
	})
}

func showEditor(pf *panel.PanelsFrame, v vfs.VFS, path string, f vfs.ReadAtCloser) {
	var pt *piecetable.PieceTable
	var buf *editor.AsyncBuffer
	var mapped *editor.MappedFile
	cpID := config.App.EditorDefaultCodePage
	binary := false
	dataOffset := int64(0)
	var header []byte

	if f != nil {
		size := f.Size()
		detectLen := 16 * 1024
		if int64(detectLen) > size {
			detectLen = int(size)
		}
		header = make([]byte, detectLen)
		n, _ := f.ReadAt(context.Background(), header, 0)
		// Unread bytes stay zero and would look like NULs, so only inspect
		// what was actually read.
		header = header[:n]

		cpID = vfs.DetectEncoding(header, config.App.EditorAutodetectCodePage, config.App.EditorDefaultCodePage)
		if remembered, ok := fileops.RememberedCodepage(v, path); ok {
			cpID = remembered
		}

		// Binary files open straight into hex; 65001 keeps them on the lazy
		// chunked path instead of a full read, like the viewer.
		binary = editorHeaderIsBinary(header, cpID)
		if binary {
			cpID = 65001
		}
		if cpID == 65001 && !binary && vfs.HasUTF8BOM(header) {
			dataOffset = vfs.UTF8BOMSize
		}

		if cpID == 65001 {
			// A local file is mapped rather than read: the mapping is one
			// contiguous buffer, so the piece table can hand windows onto it
			// and a search scans the file itself instead of a copy of it.
			// Everything else ? remote, empty, or a mapping the kernel
			// refused ? keeps the lazily fetched chunk buffer.
			if config.App.EditorMemoryMap {
				var mapErr error
				mapped, mapErr = editor.MapEditorFileWithOffset(v, f, dataOffset)
				if mapErr != nil && mapErr != editor.ErrNotMappable {
					vtui.DebugLog("EDITOR: memory mapping %s failed, reading lazily instead: %v", path, mapErr)
				}
			}
			if mapped != nil {
				pt = piecetable.New(mapped.Bytes())
			} else {
				buf = editor.NewAsyncBufferWithOffset(context.Background(), f, dataOffset)
				buf.Prewarm()
				pt = piecetable.NewWithBuffer(buf)
			}
		} else {
			fullData := make([]byte, size)
			_, _ = f.ReadAt(context.Background(), fullData, 0)
			decoded, err := vfs.DecodeBytes(fullData, cpID)
			if err != nil {
				decoded = fullData
				cpID = 65001
			}
			pt = piecetable.New(decoded)
		}
	} else {
		pt = piecetable.New(nil)
	}

	// A mapped or lazily loaded file is indexed by StartIndexing below; anything
	// else ? an empty buffer, or a file decoded into memory ? has its index
	// built with it, as it always has.
	var ev *editor.EditorView
	if mapped != nil || buf != nil {
		ev = editor.NewEditorViewIndexedLater(pt, v, path)
	} else {
		ev = editor.NewEditorView(pt, v, path)
	}
	ev.Codepage = cpID
	ev.BinaryFile = binary
	ev.Utf8BOM = cpID == 65001 && dataOffset != 0
	// The decode view's processor mode comes off the header read above,
	// like the codepage: the buffer behind a lazily loaded file may not
	// have its first bytes when the mode is first wanted.
	ev.DisasmMode = viewer.DetectX86Mode(header)
	// StartIndexing skips hex, so binary files open without a line scan.
	if _, isDisks := v.(*vfs.DisksVFS); isDisks || binary {
		ev.HexMode = true
	}
	// A saved position is a line number, meaningless for a hex view.
	if fileops.GlobalFileState != nil && path != "" && !binary {
		if state := fileops.GlobalFileState.GetState(fileops.FileStateKey(v, path)); state != nil {
			ev.ApplyRememberedWordWrap(state.EditorWrap)
			ev.TargetLine = state.EditorLine
			ev.TargetPos = state.EditorPos
			ev.TargetTopRow = state.EditorTopRow
			ev.TargetLeft = state.EditorLeft
		}
	}
	ev.File = f
	ev.AsyncBuf = buf
	ev.Mapped = mapped
	ev.ResizeConsole(pf.LastW, pf.LastH)
	ev.StartIndexing()

	vtui.FrameManager.AddScreen(ev)
}

func findOpenedEditor(v vfs.VFS, path string) (*editor.EditorView, int) {
	var absPath string
	isLocal := false
	if osvfs, ok := v.(*vfs.OSVFS); ok {
		isLocal = true
		absPath, _ = osvfs.Abs(path)
	}

	if vtui.FrameManager == nil {
		return nil, -1
	}

	for i, s := range vtui.FrameManager.Screens {
		for _, f := range s.Frames {
			if ev, ok := f.(*editor.EditorView); ok && !ev.IsDone() {
				if isLocal && ev.Vfs != nil {
					if evOSVFS, evOk := ev.Vfs.(*vfs.OSVFS); evOk {
						evAbsPath, _ := evOSVFS.Abs(ev.FilePath)
						if evAbsPath == absPath {
							return ev, i
						}
					}
				} else {
					if ev.FilePath == path {
						return ev, i
					}
				}
			}
		}
	}
	return nil, -1
}

func actionOpenEditor(pf *panel.PanelsFrame, v vfs.VFS, path string) {
	rememberViewerEditorHistory(v, path, historyModeEdit)
	existingEditor, screenIdx := findOpenedEditor(v, path)
	if existingEditor != nil {
		var buttons []string
		if existingEditor.Modified {
			buttons = []string{i18n.Msg("FileOp.BtnCurrent"), i18n.Msg("FileOp.BtnNewInstance"), i18n.Msg("vtui.Cancel")}
		} else {
			buttons = []string{i18n.Msg("FileOp.BtnCurrent"), i18n.Msg("FileOp.BtnReload"), i18n.Msg("FileOp.BtnNewInstance"), i18n.Msg("vtui.Cancel")}
		}

		vtui.FrameManager.PostTask(func() {
			// This is a choice dialog ("switch / reload / new instance / cancel"),
			// not a warning ? render on the neutral dialog palette. See #379.
			dlg := vtui.ShowMessageEx(i18n.Msg("FileOp.AlreadyOpenedTitle"), fmt.Sprintf(i18n.Msg("FileOp.AlreadyOpened"), vtui.TruncateMiddle(v.Base(path), 40)), buttons, vtui.MessageInfo)
			dlg.OnResult = func(res int) {
				if res == 0 {
					vtui.FrameManager.SwitchScreen(screenIdx)
				} else if res == 1 && len(buttons) == 4 { // Reload
					existingEditor.Close()
					openEditorInternal(pf, v, path)
				} else if (res == 1 && len(buttons) == 3) || (res == 2 && len(buttons) == 4) { // New instance
					openEditorInternal(pf, v, path)
				}
			}
		})
		return
	}
	openEditorInternal(pf, v, path)
}

func openEditorInternal(pf *panel.PanelsFrame, v vfs.VFS, path string) {
	if config.App.EditorHighlighter == "Colorer" && !editor.SchemasExist() {
		// Read on the goroutine that starts this work, not inside it: the
		// work outlives the call, and reading the global from it races
		// anything that reassigns vtui.FrameManager meanwhile.
		uiFrames := vtui.FrameManager
		go func() {
			msg := "Colorer syntax highlighting schemas are missing.\nWould you like to download them from elfmz/far2l GitHub?"
			if pf.Message(" Download Colorer Schemas ", msg, []string{"&Yes", "&No"}) == 0 {
				editor.DownloadColorerSchemas(pf, func(success bool) {
					uiFrames.PostTask(func() {
						if !success {
							config.App.EditorHighlighter = "Chroma"
							config.SaveConfig()
						}
						openEditorInternal(pf, v, path)
					})
				})
			} else {
				uiFrames.PostTask(func() {
					config.App.EditorHighlighter = "Chroma"
					openEditorInternal(pf, v, path)
				})
			}
		}()
		return
	}
	if fileops.IsLocalOSVFS(v) {
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			var f vfs.ReadAtCloser
			if v != nil {
				if stat, errStat := v.Stat(ctx.Context, path); errStat == nil && stat.IsDir {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", "Cannot edit a directory.", []string{"&Ok"})
					})
					return
				}
				var err error
				f, err = v.Open(ctx.Context, path)
				if err != nil {
					if os.IsNotExist(err) {
						f = nil
					} else {
						ctx.RunOnUI(func() {
							if err == os.ErrInvalid {
								vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets, Devices).", []string{"&Ok"})
							} else {
								vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file:\n%v", err), []string{"&Ok"})
							}
						})
						return
					}
				}
			}
			ctx.RunOnUI(func() {
				showEditor(pf, v, path, f)
			})
		})
		return
	}

	var f vfs.ReadAtCloser
	pf.RunProgressTaskAfter(openingProgressDelay, " Opening... ", "Preparing to edit file...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		update("Opening file...", -1)
		var err error
		if v != nil {
			if stat, errStat := v.Stat(ctx, path); errStat == nil && stat.IsDir {
				return fmt.Errorf("cannot edit a directory")
			}
			ctx = context.WithValue(ctx, vfs.ProgressKey, vfs.ProgressCallback(update))
			f, err = v.Open(ctx, path)
			if err != nil {
				if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not found") {
					f = nil
					return nil
				}
				return err
			}
		}
		return nil
	}, func(err error) {
		if err != nil {
			if err != context.Canceled {
				if err == os.ErrInvalid {
					vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
				} else {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file:\n%v", err), []string{"&Ok"})
				}
			}
			return
		}
		showEditor(pf, v, path, f)
	})
}

func findOpenedViewer(v vfs.VFS, path string) (*viewer.ViewerView, int) {
	var absPath string
	isLocal := false
	if osvfs, ok := v.(*vfs.OSVFS); ok {
		isLocal = true
		absPath, _ = osvfs.Abs(path)
	}

	if vtui.FrameManager == nil {
		return nil, -1
	}

	for i, s := range vtui.FrameManager.Screens {
		for _, f := range s.Frames {
			if vv, ok := f.(*viewer.ViewerView); ok && !vv.IsDone() {
				if isLocal && vv.VFS != nil {
					if vvOSVFS, evOk := vv.VFS.(*vfs.OSVFS); evOk {
						vvAbsPath, _ := vvOSVFS.Abs(vv.Path)
						if vvAbsPath == absPath {
							return vv, i
						}
					}
				} else {
					if vv.Path == path {
						return vv, i
					}
				}
			}
		}
	}
	return nil, -1
}

func showViewer(pf *panel.PanelsFrame, vv *viewer.ViewerView, path string) {
	if fileops.GlobalFileState != nil && path != "" {
		if state := fileops.GlobalFileState.GetState(fileops.FileStateKey(vv.VFS, path)); state != nil {
			vv.TopOffset = state.ViewerOffset
			if vv.TopOffset > vv.Backend.Size() {
				vv.TopOffset = vv.Backend.Size() - 1
			}
			if vv.TopOffset < 0 {
				vv.TopOffset = 0
			}
			vv.WrapMode = state.ViewerWrap
			// The saved flag is what the user left the file in last time,
			// so it outranks the binary check the same way an F4 does.
			vv.HexMode = state.ViewerHex
			vv.HexAuto = false
		}
	}
	vv.ResizeConsole(pf.LastW, pf.LastH)
	vtui.FrameManager.AddScreen(vv)
}

func actionOpenViewer(pf *panel.PanelsFrame, v vfs.VFS, path string) {
	rememberViewerEditorHistory(v, path, historyModeView)
	existingViewer, screenIdx := findOpenedViewer(v, path)
	if existingViewer != nil {
		vtui.FrameManager.PostTask(func() {
			// Same as actionOpenEditor above ? this is a choice
			// dialog, render on the neutral dialog palette. See #379.
			dlg := vtui.ShowMessageEx(i18n.Msg("FileOp.AlreadyViewedTitle"), fmt.Sprintf(i18n.Msg("FileOp.AlreadyViewed"), vtui.TruncateMiddle(v.Base(path), 40)), []string{i18n.Msg("FileOp.BtnCurrent"), i18n.Msg("FileOp.BtnReload"), i18n.Msg("FileOp.BtnNewInstance"), i18n.Msg("vtui.Cancel")}, vtui.MessageInfo)
			dlg.OnResult = func(res int) {
				switch res {
				case 0:
					vtui.FrameManager.SwitchScreen(screenIdx)
				case 1: // Reload
					existingViewer.Close()
					openViewerInternal(pf, v, path)
				case 2: // New instance
					openViewerInternal(pf, v, path)
				}
			}
		})
		return
	}
	openViewerInternal(pf, v, path)
}
func actionSwitchEditorToViewer(ev *editor.EditorView) {
	if ev == nil || ev.FilePath == "" || ev.Vfs == nil {
		return
	}

	doSwitch := func() {
		targetOffset := int64(0)
		if ev.HexMode || ev.DecodeMode {
			targetOffset = int64(ev.HexTopOffset)
		} else if ev.Li != nil && ev.CursorLine >= 0 {
			// The index owns the answer to "where is line N", and on a file
			// that is still being scanned it may not have reached the cursor
			// yet ? which used to open the viewer at the top of the file
			// instead of where the editor was.
			ev.EnsureIndexedToLine(ev.CursorLine)
			if ev.CursorLine < ev.Li.LineCount() {
				targetOffset = int64(ev.Li.GetLineOffset(ev.CursorLine))
			}
		}

		ctx := context.Background()
		vv, err := viewer.NewViewerView(ctx, ev.Vfs, ev.FilePath)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file in vv:\n%v", err), []string{"&Ok"})
			return
		}

		// viewer.NewViewerView normally follows the saved per-file override, but the
		// editor can have a just-selected or conversion codepage that is not in
		// file_states yet. Rebuild the vv backend so the displayed bytes and
		// the Codepage label cannot diverge.
		vv.ReloadWithCodepage(ev.Codepage)
		vv.HexMode = ev.HexMode
		vv.DecodeMode = ev.DecodeMode
		// A decided mode travels with the switch, whether the header or
		// the user decided it; an undecided one must not undo the
		// vv's own detection.
		if ev.DisasmMode != 0 {
			vv.DisasmMode = ev.DisasmMode
		}
		vv.WrapMode = ev.WordWrap
		if vv.HexMode {
			vv.TopOffset = targetOffset &^ 0xF
		} else {
			vv.TopOffset = targetOffset
		}

		w := vtui.FrameManager.GetScreenSize()
		h := vtui.FrameManager.GetScreenHeight()
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 25
		}
		vv.ResizeConsole(w, h)

		screenIdx := -1
		if vtui.FrameManager != nil {
			for sIdx, s := range vtui.FrameManager.Screens {
				for _, f := range s.Frames {
					if f == ev {
						screenIdx = sIdx
						break
					}
				}
				if screenIdx != -1 {
					break
				}
			}
		}

		ev.Close()
		if screenIdx != -1 && screenIdx < len(vtui.FrameManager.Screens) {
			// SwitchScreen() is a no-op when idx == ActiveIdx (the common case
			// here, since switching to the vv normally happens from the
			// editor screen currently on-screen). Writing straight into
			// Screens[screenIdx].Frames wouldn't be picked up by GetTopFrame(),
			// which reads the live fm.frames slice. Go through
			// RemoveFrame()/Push() instead for the active-screen case, since
			// those mutate fm.frames directly; keep the direct Screens[] +
			// SwitchScreen() combo for background screens, where
			// SwitchScreen() does perform the swap.
			if screenIdx == vtui.FrameManager.ActiveIdx {
				vtui.FrameManager.RemoveFrame(ev)
				vtui.FrameManager.Push(vv)
			} else {
				vtui.FrameManager.Screens[screenIdx].Frames = []vtui.Frame{vv}
				vtui.FrameManager.SwitchScreen(screenIdx)
			}
		} else if vtui.FrameManager != nil {
			vtui.FrameManager.AddScreen(vv)
		}
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
		rememberViewerEditorHistory(ev.Vfs, ev.FilePath, historyModeView)
	}

	if ev.Modified {
		msg := "The file has been modified.\nDo you want to save it?"
		dlg := vtui.ShowMessage(" Confirm ", msg, []string{"&Save", "&Don't Save", "Cancel"})
		dlg.OnResult = func(code int) {
			switch code {
			case 0: // Save
				ev.SaveToFile(func() {
					doSwitch()
				})
			case 1: // Don't save
				doSwitch()
			}
		}
		return
	}

	doSwitch()
}

func actionSwitchViewerToEditor(vv *viewer.ViewerView) {
	if vv == nil || vv.Path == "" || vv.VFS == nil {
		return
	}

	if stat, err := vv.VFS.Stat(context.Background(), vv.Path); err == nil && stat.IsDir {
		vtui.ShowMessage(" Error ", "Cannot edit a directory.", []string{"&Ok"})
		return
	}

	ctx := context.Background()
	f, err := vv.VFS.Open(ctx, vv.Path)
	if err != nil {
		if err == os.ErrInvalid {
			vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
		} else {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file for editing:\n%v", err), []string{"&Ok"})
		}
		return
	}

	var pt *piecetable.PieceTable
	var buf *editor.AsyncBuffer
	var mapped *editor.MappedFile
	cpID := vv.Codepage
	dataOffset := int64(0)
	if vv.Backend != nil {
		dataOffset = vv.Backend.DataOffset
	}

	if cpID == 65001 {
		if config.App.EditorMemoryMap {
			var mapErr error
			mapped, mapErr = editor.MapEditorFileWithOffset(vv.VFS, f, dataOffset)
			if mapErr != nil && mapErr != editor.ErrNotMappable {
				vtui.DebugLog("EDITOR: memory mapping %s failed, reading lazily instead: %v", vv.Path, mapErr)
			}
		}
		if mapped != nil {
			pt = piecetable.New(mapped.Bytes())
		} else {
			buf = editor.NewAsyncBufferWithOffset(ctx, f, dataOffset)
			buf.Prewarm()
			pt = piecetable.NewWithBuffer(buf)
		}
	} else {
		size := f.Size()
		fullData := make([]byte, size)
		_, _ = f.ReadAt(ctx, fullData, 0)
		decoded, errDec := vfs.DecodeBytes(fullData, cpID)
		if errDec != nil {
			decoded = fullData
			cpID = 65001
		}
		pt = piecetable.New(decoded)
	}

	// Same rule as opening from the panel: a file the indexer owns must not be
	// indexed on the way in, or switching to the ev pays the whole file's
	// scan on the UI thread before it appears ? twenty seconds of it on the
	// 8 GB test file.
	var ev *editor.EditorView
	if mapped != nil || buf != nil {
		ev = editor.NewEditorViewIndexedLater(pt, vv.VFS, vv.Path)
	} else {
		ev = editor.NewEditorView(pt, vv.VFS, vv.Path)
	}
	ev.File = f
	ev.AsyncBuf = buf
	ev.Mapped = mapped
	ev.Codepage = cpID
	ev.BinaryFile = vv.HexMode
	ev.Utf8BOM = cpID == 65001 && vv.Backend != nil && vv.Backend.DataOffset != 0
	ev.ApplyRememberedWordWrap(vv.WrapMode)
	ev.HexMode = vv.HexMode
	ev.DecodeMode = vv.DecodeMode
	ev.DisasmMode = vv.DisasmMode

	targetOff := int(vv.TopOffset)
	if ev.HexMode || ev.DecodeMode {
		ev.HexTopOffset = targetOff &^ 0xF
		ev.CursorLine = ev.Li.GetLineAtOffset(targetOff)
		ev.CursorPos = targetOff - ev.Li.GetLineOffset(ev.CursorLine)
	} else {
		line, pos := 0, 0
		if !ev.AwaitOffset(targetOff) {
			line = ev.CursorLine
			pos = ev.CursorPos
		} else {
			// The file has not been read that far ? a chunk of a lazily
			// loaded one is still on its way ? so the offset has no line yet.
			// The ev opens at the top and the scan puts the cursor where
			// the viewer was when it reads past it, rather than guessing now.
			vtui.DebugLog("EDITOR: viewer offset %d is past the index; the scan will place it",
				targetOff)
		}
		ev.CursorLine = line
		ev.CursorPos = pos
		ev.TargetLine = line
		ev.TargetPos = pos
		ev.TargetTopRow = ev.Engine.GetRowOffset(line)
		ev.TargetLeft = 0
		ev.ScrollTopRow = ev.TargetTopRow
	}
	ev.StartIndexing()

	w := vtui.FrameManager.GetScreenSize()
	h := vtui.FrameManager.GetScreenHeight()
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 25
	}
	ev.ResizeConsole(w, h)

	screenIdx := -1
	if vtui.FrameManager != nil {
		for sIdx, s := range vtui.FrameManager.Screens {
			for _, f := range s.Frames {
				if f == vv {
					screenIdx = sIdx
					break
				}
			}
			if screenIdx != -1 {
				break
			}
		}
	}

	vv.Close()
	if screenIdx != -1 && screenIdx < len(vtui.FrameManager.Screens) {
		// See the matching comment in actionSwitchEditorToViewer: SwitchScreen()
		// no-ops when idx == ActiveIdx, so update the live frame stack directly
		// for that (common) case instead of writing into Screens[] and relying
		// on SwitchScreen() to pick it up.
		if screenIdx == vtui.FrameManager.ActiveIdx {
			vtui.FrameManager.RemoveFrame(vv)
			vtui.FrameManager.Push(ev)
		} else {
			vtui.FrameManager.Screens[screenIdx].Frames = []vtui.Frame{ev}
			vtui.FrameManager.SwitchScreen(screenIdx)
		}
	} else if vtui.FrameManager != nil {
		vtui.FrameManager.AddScreen(ev)
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
	rememberViewerEditorHistory(vv.VFS, vv.Path, historyModeEdit)
}

// tryOpenImageViewer opens the picture viewer when the file looks like an
// image and the backend can actually show one. It returns false to let the
// ordinary viewer handle the file.
// tryOpenVideoPlayer offers to play a video. It comes before the picture
// viewer because a file is one or the other, and before the text viewer
// because a hex dump of an mp4 is not what anybody asked for.
func tryOpenVideoPlayer(pf *panel.PanelsFrame, v vfs.VFS, path string) bool {
	if pf == nil || !media.IsVideoFile(path) {
		return false
	}
	// Video is a local business: the frames of it never fit down a
	// terminal, and the player draws into a window of f4's own on the
	// screen the terminal is on.
	if terminal.SharedTTYXSession() == nil {
		return false
	}
	if !media.ToolMPV.Available() {
		vtui.ShowMessage(" Video ", media.ToolMPV.MissingMessage(), []string{"&Ok"})
		return true
	}

	vv, err := media.NewVideoView(v, path)
	if err != nil {
		vtui.DebugLog("VIDEO: %v", err)
		return false
	}
	vv.ResizeConsole(pf.LastW, pf.LastH)
	vtui.FrameManager.AddScreen(vv)
	return true
}

func tryOpenImageViewer(pf *panel.PanelsFrame, v vfs.VFS, path string) bool {
	if pf == nil || !media.IsImageFile(path) {
		return false
	}
	scr := vtui.FrameManager.Screen()
	// One question, and the X overlay is inside the answer: it is installed
	// as the screen's graphics renderer at startup, so a terminal with no
	// image protocol of its own still supports graphics when there is a
	// local X session behind it. Asking anything else here is how F3 on a
	// PNG in gnome-terminal used to open the hex viewer.
	if scr == nil || !scr.SupportsGraphics() {
		return false
	}

	// The list is taken here, on the UI thread, while the panel is still
	// the one the reader was looking at.
	siblings, index := imageSiblingPaths(pf, v, path)

	// The gallery and the panel share one selection, so that the file
	// operations afterwards act on what the reader picked among the
	// thumbnails. What the panel had picked already is taken here, on the UI
	// thread, for the same reason the sibling list is.
	fsp := pf.GetActivePanel()
	picked := make(map[string]bool)
	if fsp != nil && v != nil {
		for _, sibling := range siblings {
			if fsp.IsNameSelected(v.Base(sibling)) {
				picked[sibling] = true
			}
		}
	}

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		iv, err := media.NewImageView(ctx.Context, v, path)
		ctx.RunOnUI(func() {
			if err != nil {
				vtui.DebugLog("IMAGE: failed to open %s: %v", path, err)
				vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open image:\n%v", err), []string{"&Ok"})
				return
			}
			iv.SetSiblings(siblings, index)
			iv.SetSelection(picked)
			if fsp != nil && v != nil {
				iv.OnSelect = func(sibling string, on bool) {
					if fsp.SetSelectedByName(v.Base(sibling), on) {
						fsp.Refresh()
					}
				}
			}
			iv.ResizeConsole(pf.LastW, pf.LastH)
			vtui.FrameManager.AddScreen(iv)
		})
	})
	return true
}

// imageSiblingPaths lists the pictures next to this one, in the order the
// active panel shows them. A panel looking somewhere else has nothing to say
// about this file, and then the viewer simply shows one picture.
func imageSiblingPaths(pf *panel.PanelsFrame, v vfs.VFS, path string) ([]string, int) {
	if pf == nil || v == nil {
		return nil, -1
	}
	fsp := pf.GetActivePanel()
	if fsp == nil || fsp.Vfs == nil {
		return nil, -1
	}
	dir := v.Dir(path)
	if fsp.Vfs.GetPath() != dir {
		return nil, -1
	}

	names, index := fsp.ImageSiblings()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, v.Join(dir, name))
	}
	return paths, index
}

func openViewerInternal(pf *panel.PanelsFrame, v vfs.VFS, path string) {
	if tryOpenVideoPlayer(pf, v, path) {
		return
	}
	if tryOpenImageViewer(pf, v, path) {
		return
	}
	if fileops.IsLocalOSVFS(v) {
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			if v != nil {
				if stat, err := v.Stat(ctx.Context, path); err == nil && stat.IsDir {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", "Cannot view a directory.", []string{"&Ok"})
					})
					return
				}
			}

			vv, err := viewer.NewViewerView(ctx.Context, v, path)
			ctx.RunOnUI(func() {
				if err == nil {
					showViewer(pf, vv, path)
				} else {
					vtui.DebugLog("PANELS: Failed to open vv for %s: %v", path, err)
					if err == os.ErrInvalid {
						vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
					} else {
						vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file:\n%v", err), []string{"&Ok"})
					}
				}
			})
		})
		return
	}

	var vv *viewer.ViewerView
	pf.RunProgressTaskAfter(openingProgressDelay, " Opening... ", "Preparing to open file...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		update("Opening file...", -1)
		ctx = context.WithValue(ctx, vfs.ProgressKey, vfs.ProgressCallback(update))
		var err error
		vv, err = viewer.NewViewerView(ctx, v, path)
		return err
	}, func(err error) {
		if err != nil {
			if err != context.Canceled {
				if err == os.ErrInvalid {
					vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
				} else {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file:\n%v", err), []string{"&Ok"})
				}
			}
			return
		}
		showViewer(pf, vv, path)
	})
}

func actionViewerSearch(vv *viewer.ViewerView) {
	actionViewerSearchDirection(vv, false)
}

func actionViewerSearchDirection(vv *viewer.ViewerView, reverse bool) {
	dlgW, dlgH := 66, 15
	dlg := vtui.NewCenteredDialog(dlgW, dlgH, i18n.Msg("Viewer.SearchTitle"))
	dlg.ShowClose = true

	lblPrompt := vtui.NewLabel(0, 0, i18n.Msg("Search.Prompt"), nil)
	editPattern := vtui.NewEdit(0, 0, 40, vv.LastSearch)
	history.AttachHistoryUseLast(editPattern, history.SearchTextHistoryID)
	editPattern.SelectAll()
	lblPrompt.FocusLink = editPattern
	dlg.SetFocusedItem(editPattern)

	chkCase := vtui.NewCheckbox(0, 0, i18n.Msg("Search.CaseSensitive"), false)
	if vv.LastSearchCase {
		chkCase.State = 1
	}
	chkWholeWord := vtui.NewCheckbox(0, 0, i18n.Msg("Search.WholeWords"), false)
	if vv.LastSearchWholeWord {
		chkWholeWord.State = 1
	}
	chkReverse := vtui.NewCheckbox(0, 0, i18n.Msg("Search.Reverse"), false)
	if vv.LastSearchReverse || reverse {
		chkReverse.State = 1
	}
	chkRegexp := vtui.NewCheckbox(0, 0, i18n.Msg("Search.Regex"), false)
	if vv.LastSearchRegexp {
		chkRegexp.State = 1
	}

	btnFind := vtui.NewButton(0, 0, i18n.Msg("Search.BtnFind"))
	btnFind.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lblPrompt)
	dlg.AddItem(editPattern)
	dlg.AddItem(chkCase)
	dlg.AddItem(chkWholeWord)
	dlg.AddItem(chkReverse)
	dlg.AddItem(chkRegexp)
	dlg.AddItem(btnFind)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dlgW-4, dlgH-4)
	vbox.Add(lblPrompt, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editPattern, vtui.Margins{Top: 1}, vtui.AlignFill)

	col1 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col1.Add(chkCase, vtui.Margins{}, vtui.AlignLeft)
	col1.Add(chkWholeWord, vtui.Margins{Top: 1}, vtui.AlignLeft)
	col1.Add(chkReverse, vtui.Margins{Top: 1}, vtui.AlignLeft)

	col2 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col2.Add(chkRegexp, vtui.Margins{}, vtui.AlignLeft)

	rowChecks := vtui.NewHBoxLayout(0, 0, dlgW-4, 5)
	rowChecks.Add(col1, vtui.Margins{}, vtui.AlignFill)
	rowChecks.Add(col2, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnFind, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnFind.OnClick = func() {
		pattern := editPattern.GetText()
		if pattern == "" {
			return
		}
		caseSensitive := chkCase.State == 1
		wholeWord := chkWholeWord.State == 1
		searchReverse := chkReverse.State == 1
		useRegexp := chkRegexp.State == 1
		optionsChanged := pattern != vv.LastSearch ||
			caseSensitive != vv.LastSearchCase ||
			searchReverse != vv.LastSearchReverse ||
			useRegexp != vv.LastSearchRegexp ||
			wholeWord != vv.LastSearchWholeWord
		if optionsChanged {
			vv.LastSearchFound = false
		}
		history.CommitHistory(editPattern, pattern)
		vv.LastSearch = pattern
		vv.LastSearchCase = caseSensitive
		vv.LastSearchReverse = searchReverse
		vv.LastSearchRegexp = useRegexp
		vv.LastSearchWholeWord = wholeWord
		dlg.Close()
		runViewerSearch(vv, pattern, searchReverse)
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}

func actionViewerSearchAgain(vv *viewer.ViewerView, reverse bool) {
	if vv.LastSearch == "" {
		actionViewerSearchDirection(vv, reverse)
		return
	}
	runViewerSearch(vv, vv.LastSearch, reverse)
}

func runViewerSearch(vv *viewer.ViewerView, pattern string, reverse bool) {
	vtui.FrameManager.PostTask(func() {
		editor.RunSearchWithProgress(pattern, func(ctx *vtui.TaskContext, dlg *vtui.Window) {
			start := vv.TopOffset + 1
			if reverse {
				start = vv.TopOffset
			}
			if vv.LastSearchFound && vv.TopOffset == vv.LastSearchTopOffset {
				start = vv.LastSearchOffset + 1
				if reverse {
					start = vv.LastSearchOffset
				}
			}
			foundOffset, matchLen, searchErr := viewer.SearchMatch(ctx.Context, vv.Backend, pattern, start, viewer.SearchOptions{
				CaseSensitive: vv.LastSearchCase,
				Reverse:       reverse,
				Regexp:        vv.LastSearchRegexp,
				WholeWord:     vv.LastSearchWholeWord,
			}, func(percent int) {
				ctx.RunOnUI(func() { dlg.SetProgress(percent) })
			})

			ctx.RunOnUI(func() {
				canceled := ctx.Err() != nil
				dlg.Close()
				if canceled || searchErr == context.Canceled {
					return
				}
				if searchErr != nil {
					if vv.LastSearchRegexp {
						vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid regular expression:\n%v", searchErr), []string{"&Ok"})
					} else {
						vtui.ShowMessage(" Error ", "Failed to read file buffer.", []string{"&Ok"})
					}
					return
				}
				if foundOffset != -1 {
					vv.TopOffset = vv.Backend.FindLineStart(foundOffset)
					vv.LastSearchOffset = foundOffset
					vv.LastSearchTopOffset = vv.TopOffset
					vv.LastSearchMatchLen = int64(matchLen)
					vv.LastSearchFound = true
					vtui.FrameManager.Redraw()
				} else {
					vtui.ShowMessage(" Search ", "Pattern not found.", []string{"&Ok"})
				}
			})
		})
	})
}

// openPlayerPanel is the player when it is open on the passive side, which
// is the only side it can be on while a file panel is active.
func openPlayerPanel(pf *panel.PanelsFrame) *panel.PlayerPanel {
	if pf == nil || !pf.ShowPanels || pf.ActiveIdx < 0 || pf.ActiveIdx > 1 {
		return nil
	}
	player, _ := pf.AltPanels[1-pf.ActiveIdx].(*panel.PlayerPanel)
	return player
}

// tryPlayInPlayerPanel is Enter on a recording while the player panel is
// open: the file plays there at once, the file panel keeps the cursor, and
// the rest of the panel's audio files become the queue. Without the player
// open, Enter keeps its usual meaning ? associations, then the system
// opener ? so the rule costs nobody anything they did not ask for.
func tryPlayInPlayerPanel(pf *panel.PanelsFrame, v vfs.VFS, path string) bool {
	player := openPlayerPanel(pf)
	if player == nil || !media.IsAudioFile(path) {
		return false
	}
	osv, isLocal := v.(*vfs.OSVFS)
	if !isLocal {
		vtui.ShowMessage(i18n.Msg("Player.Title"), i18n.Msg("Player.LocalOnly"), []string{i18n.Msg("vtui.Ok")})
		return true
	}
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return false
	}
	dir := v.Dir(path)
	names, index := fsp.AudioSiblings()
	if index < 0 || fsp.Vfs.GetPath() != dir {
		names, index = []string{v.Base(path)}, 0
	}
	files := make([]string, 0, len(names))
	for _, n := range names {
		abs, err := osv.Abs(filepath.Join(dir, n))
		if err != nil {
			abs = filepath.Join(dir, n)
		}
		files = append(files, abs)
	}
	player.PlayFile(files, index)
	vtui.FrameManager.Redraw()
	return true
}

func actionExecute(pf *panel.PanelsFrame, v vfs.VFS, dir, name, path string) {
	// User-defined file associations for Enter (mirrors far2l F9 ?
	// Commands ? File associations). A matching association intercepts
	// before the runnable / xdg-open fallback; no match ? default flow.
	if panel.TryFileAssociation(pf, panel.AssocExecute) {
		return
	}
	if tryPlayInPlayerPanel(pf, v, path) {
		return
	}
	if _, isDisks := v.(*vfs.DisksVFS); isDisks {
		actionOpenEditor(pf, v, path)
		return
	}
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		if _, isLocal := v.(*vfs.OSVFS); isLocal {
			if fi, err := os.Stat(path); err == nil {
				if fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0 {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
					})
					return
				}
			}
		}
		runnable := vfs.IsTerminalRunnable(ctx.Context, v, path)
		if runnable {
			ctx.RunOnUI(func() {
				// Add to command history since it's a shell-executable file.
				// This centralized logic ensures consistent history across manual and Enter launches.
				historyCmd := name
				if strings.Contains(historyCmd, " ") && !strings.HasPrefix(historyCmd, "\"") && !strings.HasPrefix(historyCmd, "'") {
					historyCmd = "\"" + historyCmd + "\""
				}
				_, isOS := v.(*vfs.OSVFS)
				_, isPty := v.(vfs.PtyProvider)
				isWindowsShell := runtime.GOOS == "windows" && isOS

				if !isWindowsShell {
					historyCmd = "./" + historyCmd
				}
				pf.AddCommandHistory(historyCmd)
				pf.CmdLine.Edit.HistoryPos = -1

				useDir := isOS || isPty
				actualDir := ""
				if useDir {
					actualDir = dir
				}

				if pf.ShellMode == terminal.ShellModeSimpleInline {
					pf.RunSimpleInlineCommand(actualDir, historyCmd)
					return
				}
				if pf.ShellMode == terminal.ShellModeSimpleCaptured {
					pf.RunSimpleCapturedCommand(actualDir, historyCmd)
					return
				}

				activePty := pf.GetActivePTY()
				if activePty != nil {
					cmd := name
					var cmdToWire string

					if isWindowsShell {
						// Combine directory sync with the command to allow excision
						if actualDir != "" {
							cmdToWire = fmt.Sprintf("cd /d \"%s\" & %s\r", actualDir, historyCmd)
						} else {
							cmdToWire = fmt.Sprintf("%s\r", historyCmd)
						}
					} else {
						// On Unix, use single quotes for paths to prevent Bash history expansion
						sqCmd := strings.ReplaceAll(cmd, "'", "'\\''")
						// ?????????? OSC 133 ??? ??????????? ????????? ? ?????? ? ????? ??????????.
						if actualDir != "" {
							sqDir := strings.ReplaceAll(actualDir, "'", "'\\''")
							cmdToWire = fmt.Sprintf(" set +H; cd '%s' && { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; ./'%s' ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqDir, sqCmd)
						} else {
							cmdToWire = fmt.Sprintf(" set +H; { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; ./'%s' ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqCmd)
						}
					}
					vtui.DebugLog("ACTIONS: Sending to term.PTY: %q", cmdToWire)

					cleanCmd := "./" + cmd
					if isWindowsShell {
						cleanCmd = cmd
					}
					if !isWindowsShell {
						pf.TermView.PrintCleanCommand(cleanCmd)
					}

					// Only the Unix template above wraps the command in an
					// OSC 133 C/D pair; cmd.exe reports completion through
					// the prompt marker instead.
					if isWindowsShell {
						pf.BeginPromptDrivenExecution()
					} else {
						pf.BeginManagedExecution()
					}
					pf.ReturnToPanels = true

					if !isWindowsShell {
						pf.TermView.SetMuted(true)
					}
					_, _ = pf.WritePTY(activePty, []byte(cmdToWire))
					if isWindowsShell {
						if cmdline.IsBatchCommand(historyCmd) {
							pf.CmdSession.NoteBatchExecution()
						}
						pf.NoteLocalShellLineSent(activePty)
					}
					pf.ShowPanels = false
				}
			})
		} else {
			if _, isLocal := v.(*vfs.OSVFS); !isLocal {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(" Error ", "Cannot execute non-runnable files on a remote file system.", []string{"&Ok"})
				})
				return
			}
			command, args, ok := panel.AssociatedFileCommand(path)
			if ok {
				workingDir := ""
				if _, isLocal := v.(*vfs.OSVFS); isLocal {
					workingDir = dir
				}
				vtui.DebugLog("ACTIONS: Executing external command: %s %q", command, args)
				err := pf.RunExternalUICommand(command, args, workingDir)
				if err != nil {
					vtui.DebugLog("ACTIONS: External command failed: %v", err)
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file:\n%v", err), []string{"&Ok"})
					})
				}
			}
		}
	})
}

func actionNewFile(pf *panel.PanelsFrame) {
	if fsp := pf.GetActivePanel(); fsp != nil {
		dir := fsp.Vfs.GetPath()
		if panel.DispatchPanelAction(pf, vfs.PanelActionCreate, []string{dir}) {
			return
		}
		activeVfs := fsp.Vfs
		var nameEdit *vtui.Edit
		dlg := vtui.InputBox(i18n.Msg("Edit.NewFileTitle"), i18n.Msg("Edit.NewFilePrompt"), "", func(name string) {
			// Record what was actually typed, before the fallback below
			// turns an empty prompt into a placeholder name.
			history.CommitHistory(nameEdit, name)
			if name == "" {
				name = "newfile.txt"
			}
			// Shift+F4 also accepts a complete path. Joining an absolute
			// name to the active directory duplicates the path prefix and
			// makes an existing file look like a new, empty file.
			path := name
			if !activeVfs.IsAbs(name) {
				path = activeVfs.Join(dir, name)
			}
			if config.App.UseExternalEditor {
				actionEditFileExternal(pf, activeVfs, path, 0)
				return
			}
			actionOpenEditor(pf, activeVfs, path)
		})
		history.InputBoxEdit(dlg).PathHintsEnabled = true
		// Plain DIF_HISTORY, as in far2l's dlgOpenEditor: the prompt opens
		// empty rather than on the last file that was created this way.
		nameEdit = history.AttachHistory(history.InputBoxEdit(dlg), history.NewEditHistoryID)
	}
}

func actionViewTerminalLog(pf *panel.PanelsFrame) {
	v := terminal.NewTerminalLogVFS(pf.TermView, pf.HostConsoleLogFallback())
	actionOpenViewer(pf, v, "Terminal Log")
}

func actionEditTerminalLog(pf *panel.PanelsFrame) {
	v := terminal.NewTerminalLogVFS(pf.TermView, pf.HostConsoleLogFallback())
	actionOpenEditor(pf, v, "Terminal Log")
}

func actionViewFile(pf *panel.PanelsFrame) {
	if fsp := pf.GetActivePanel(); fsp != nil {
		idx := fsp.GetCursorIndex()
		if idx < 0 || idx >= len(fsp.Entries) {
			return
		}
		if fsp.Entries[idx].IsDir {
			actionCalcDirSize(pf, fsp, idx)
			return
		}
		// A matching View association intercepts before the built-in
		// viewer, so users can wire F3 to feh, less, or anything else.
		if panel.TryFileAssociation(pf, panel.AssocView) {
			return
		}
		name := fsp.GetSelectedName()
		path := fsp.Vfs.Join(fsp.Vfs.GetPath(), name)
		actionOpenViewer(pf, fsp.Vfs, path)
	}
}

func actionCalcDirSize(pf *panel.PanelsFrame, fsp *panel.FileSystemPanel, idx int) {
	entry := fsp.Entries[idx]
	name := entry.Name
	// ".." is a panel navigation row, not a directory owned by this VFS.
	// Trying to scan it from an archive crosses the virtual root and produces
	// misleading path-escape errors (issue #510).
	if name == ".." {
		return
	}
	basePath := fsp.Vfs.GetPath()

	var targetPath = fsp.Vfs.Join(basePath, name)

	opDlg := fileops.NewFileOpProgressDialog(" Calculating Size... ")
	var taskCtx *vtui.TaskContext
	opDlg.SetOnCancel(func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	})

	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.AddScreenHeadless(opDlg)
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		var totalStats vfs.OpStats
		lastScanUpdate := time.Now()
		totalStats, scanErr := vfs.CalculateStats(ctx.Context, fsp.Vfs, targetPath, []string{""}, func(currentPath string, stats vfs.OpStats) {
			now := time.Now()
			if now.Sub(lastScanUpdate) > 50*time.Millisecond {
				lastScanUpdate = now
				ctx.RunOnUI(func() {
					opDlg.UpdateScan(currentPath, stats.Files, stats.Dirs)
					vtui.FrameManager.Redraw()
				})
			}
		})

		ctx.RunOnUI(func() {
			opDlg.Close()
			if scanErr != nil && scanErr != context.Canceled {
				vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to calculate size:\n%v", scanErr), []string{"&Ok"})
				return
			}
			if ctx.Err() == nil {
				entry.Size = totalStats.Bytes
				entry.SizeCalculated = true
				if fsp.SortMode == panel.SortSize {
					fsp.SortEntries()
					// Keep cursor on the same item after re-sorting
					for i, e := range fsp.Entries {
						if e == entry {
							fsp.SetCursorIndex(i)
							break
						}
					}
				}
				fsp.Refresh()
			}
		})
	})
}

func actionEditFile(pf *panel.PanelsFrame) {
	if fsp := pf.GetActivePanel(); fsp != nil {
		if panel.DispatchPanelAction(pf, vfs.PanelActionEdit, panel.SelectedPanelActionPaths(fsp)) {
			return
		}
		idx := fsp.GetCursorIndex()
		if idx < 0 || idx >= len(fsp.Entries) {
			return
		}
		if fsp.Entries[idx].IsDir {
			actionFileAttributes(pf)
			return
		}
		// A matching Edit association intercepts before the built-in
		// editor (or the external one if UseExternalEditor is on).
		if panel.TryFileAssociation(pf, panel.AssocEdit) {
			return
		}
		name := fsp.GetSelectedName()
		path := fsp.Vfs.Join(fsp.Vfs.GetPath(), name)

		if config.App.UseExternalEditor {
			actionEditFileExternal(pf, fsp.Vfs, path, fsp.Entries[idx].Size)
			return
		}

		actionOpenEditor(pf, fsp.Vfs, path)
	}
}

func actionCopyMove(pf *panel.PanelsFrame, isMove bool) {
	fspSrc := pf.GetActivePanel()
	fspDst := pf.GetInactivePanel()
	if fspSrc == nil || fspDst == nil {
		return
	}

	names := fspSrc.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	title := i18n.Msg("Copy.Title")
	prompt := i18n.Msg("Copy.Prompt")
	if isMove {
		title = i18n.Msg("Move.Title")
		prompt = i18n.Msg("Move.Prompt")
	}

	srcVfs, dstVfs := fspSrc.Vfs, fspDst.Vfs
	srcBasePath := srcVfs.GetPath()
	if player, ok := pf.AltPanels[1-pf.ActiveIdx].(*panel.PlayerPanel); ok {
		// The player panel is a playlist, not a place: F5 adds
		// references, F6 is refused rather than moving music around.
		if isMove {
			vtui.ShowMessage(i18n.Msg("Player.Title"), i18n.Msg("Player.MoveRefused"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		osv, isLocal := srcVfs.(*vfs.OSVFS)
		if !isLocal {
			vtui.ShowMessage(i18n.Msg("Player.Title"), i18n.Msg("Player.LocalOnly"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		paths := make([]string, 0, len(names))
		for _, n := range names {
			if abs, err := osv.Abs(filepath.Join(srcBasePath, n)); err == nil {
				paths = append(paths, abs)
			}
		}
		if player.AddPaths(paths) == 0 {
			vtui.ShowMessage(i18n.Msg("Player.Title"), i18n.Msg("Player.NothingAdded"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		fspSrc.SelectedItems = make(map[string]bool)
		for _, entry := range fspSrc.Entries {
			entry.Selected = false
		}
		vtui.FrameManager.Redraw()
		return
	}
	if temp, ok := dstVfs.(*panel.TempPanelVFS); ok {
		// A temporary panel contains references, not copies. Keep F5/F6
		// useful for it, but never remove the real source on F6: the
		// reference list is intentionally non-destructive.
		if err := temp.AddReferences(context.Background(), srcVfs, names); err != nil {
			vtui.ShowMessage(i18n.Msg("Error.Title"), fmt.Sprintf(i18n.Msg("TempPanel.AddError"), err), []string{i18n.Msg("vtui.Ok")})
		}
		fspSrc.SelectedItems = make(map[string]bool)
		for _, entry := range fspSrc.Entries {
			entry.Selected = false
		}
		pf.RefreshAll()
		return
	}

	initialDest := dstVfs.GetPath()
	if initialDest != "" && !strings.HasSuffix(initialDest, "/") && !strings.HasSuffix(initialDest, "\\") {
		sep := "/"
		if _, isOS := dstVfs.(*vfs.OSVFS); isOS && runtime.GOOS == "windows" {
			sep = "\\"
		}
		initialDest += sep
	}

	onCompleteWithClear := func() {
		if pf != nil {
			if fsp := pf.GetActivePanel(); fsp != nil {
				fsp.SelectedItems = make(map[string]bool)
				for _, e := range fsp.Entries {
					e.Selected = false
				}
			}
			pf.RefreshAll()
		}
	}

	// A move takes the cursor's entry away with it, so the panel is told where
	// to land before the operation starts ? afterwards the name it would look
	// for is gone.
	if isMove {
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.PendingSelection = fsp.GetSuccessorName()
		}
	}

	if isMove && !config.App.ConfirmMove {
		go fileops.ExecuteFileOpAt(srcVfs, dstVfs, srcBasePath, names, initialDest, isMove, config.App.DefaultFileOpMode, onCompleteWithClear)
		return
	}

	if !isMove && !config.App.ConfirmCopy {
		go fileops.ExecuteFileOpAt(srcVfs, dstVfs, srcBasePath, names, initialDest, isMove, config.App.DefaultFileOpMode, onCompleteWithClear)
		return
	}

	dlg := dialog.NewFileDialog(title, dialog.CopyBoxHeight)
	width, height := dlg.Size()

	promptLbl := vtui.NewLabel(0, 0, fmt.Sprintf(prompt, len(names)), nil)
	dlg.AddItem(promptLbl)

	editDest := vtui.NewEdit(0, 0, 10, initialDest)
	editDest.PathHintsEnabled = true
	history.AttachHistoryUseLast(editDest, history.CopyDestHistoryID)
	dlg.AddItem(editDest)

	modes := []string{i18n.Msg("Op.Queue"), i18n.Msg("Op.Background"), i18n.Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	defMode := config.App.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnOk := vtui.NewButton(0, 0, i18n.Msg("Copy.Btn"))
	if isMove {
		btnOk = vtui.NewButton(0, 0, i18n.Msg("Move.Btn"))
	}
	btnOk.IsDefault = true

	btnOk.OnClick = func() {
		dest := editDest.GetText()
		mode := comboMode.Menu.SelectPos
		dlg.Close()
		if dest != "" {
			history.CommitHistory(editDest, dest)
			go fileops.ExecuteFileOpAt(srcVfs, dstVfs, srcBasePath, names, dest, isMove, mode, onCompleteWithClear)
		}
	}
	dlg.AddItem(btnOk)

	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.AddItem(btnCancel)
	dlg.AddItem(comboMode)

	// Layout Engine
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(promptLbl, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editDest, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	// Keep the action row above the mode selector. ComboBox.Open() places its
	// popup below the field, so the popup cannot cover these buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)

	// The same VBox re-applied to the new dialog rectangle is what stretches
	// the destination field when the f4 window is resized; the button row
	// re-centers itself from HBoxLayout.SetPosition.
	dlg.SetLayout(func() {
		vbox.SetPosition(dlg.X1+2, dlg.Y1+2, dlg.X2-2, dlg.Y2-2)
		vbox.Apply()
	})
	dlg.SetFocusedItem(editDest)

	vtui.FrameManager.Push(dlg)
}
func actionRename(pf *panel.PanelsFrame) {
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return
	}

	name := fsp.GetRawSelectedName()
	if name == "" || name == ".." {
		return
	}

	dialog.FileInputBox(i18n.Msg("Dialog.RenameTitle"), fmt.Sprintf(i18n.Msg("Dialog.RenamePrompt"), name), name, func(newName string) {
		if newName == "" || newName == name {
			return
		}
		oldPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), name)
		newPath := fsp.Vfs.Join(fsp.Vfs.GetPath(), newName)

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			// The rename dialog never asks for overwrite confirmation. Carry an
			// atomic no-replace decision so remote providers cannot silently
			// destroy an entry that already has the requested name.
			err := fsp.Vfs.Rename(vfs.WithDestinationOverwrite(ctx.Context, false), oldPath, newPath)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to rename:\n%v", err), []string{"&Ok"})
					fsp.PendingSelection = name
				} else {
					// Clear cache to ensure the new name is visible immediately
					delete(fsp.DirCache, fsp.CacheKey(fsp.Vfs.GetPath()))
					fsp.PendingSelection = newName
				}
				pf.RefreshAll()
			})
		})
	})
}
func actionCreateLink(pf *panel.PanelsFrame) {
	fspSrc := pf.GetActivePanel()
	fspDst := pf.GetInactivePanel()
	if fspSrc == nil || fspDst == nil {
		return
	}

	names := fspSrc.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	prompt := fmt.Sprintf(i18n.Msg("Link.Prompt"), names[0])
	if len(names) > 1 {
		prompt = fmt.Sprintf(i18n.Msg("Link.PromptMultiple"), len(names))
	}

	initialDest := fspDst.Vfs.GetPath()
	if initialDest != "" && !strings.HasSuffix(initialDest, "/") && !strings.HasSuffix(initialDest, "\\") {
		sep := "/"
		if _, isOS := fspDst.Vfs.(*vfs.OSVFS); isOS && runtime.GOOS == "windows" {
			sep = "\\"
		}
		initialDest += sep
	}

	dlg := vtui.NewCenteredDialog(52, 11, i18n.Msg("Link.Title"))
	dlg.ShowClose = true

	promptLbl := vtui.NewLabel(0, 0, prompt, nil)
	dlg.AddItem(promptLbl)

	editDest := vtui.NewEdit(0, 0, 10, initialDest)
	editDest.PathHintsEnabled = true
	dlg.AddItem(editDest)

	linkTypes := []string{
		i18n.Msg("Link.TypeSymlink"),
		i18n.Msg("Link.TypeJunction"),
		i18n.Msg("Link.TypeHardlink"),
	}
	comboType := vtui.NewComboBox(0, 0, 32, linkTypes)
	comboType.DropdownOnly = true
	comboType.Menu.SetSelectPos(0)
	comboType.Edit.SetText(linkTypes[0])
	lblType := vtui.NewLabel(0, 0, i18n.Msg("Link.Type"), comboType)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("Link.Btn"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	btnOk.OnClick = func() {
		dest := editDest.GetText()
		linkType := comboType.Menu.SelectPos
		dlg.Close()
		if dest == "" {
			return
		}

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			srcVfs := fspSrc.Vfs
			dstVfs := fspDst.Vfs
			srcBasePath := srcVfs.GetPath()

			var errs []string
			for _, name := range names {
				targetPath := srcVfs.Join(srcBasePath, name)
				linkPath := dest
				if len(names) > 1 || strings.HasSuffix(dest, "/") || strings.HasSuffix(dest, "\\") {
					linkPath = dstVfs.Join(dest, name)
				} else if stat, err := dstVfs.Stat(ctx.Context, dest); err == nil && stat.IsDir {
					linkPath = dstVfs.Join(dest, name)
				}

				var err error
				switch linkType {
				case 0: // Symlink
					if symVFS, ok := dstVfs.(vfs.SymlinkVFS); ok {
						err = symVFS.Symlink(ctx.Context, targetPath, linkPath)
					} else {
						err = fmt.Errorf("symlinks are not supported on destination filesystem")
					}
				case 1: // Junction
					if juncVFS, ok := dstVfs.(vfs.JunctionVFS); ok {
						err = juncVFS.Junction(ctx.Context, targetPath, linkPath)
					} else if symVFS, ok := dstVfs.(vfs.SymlinkVFS); ok {
						err = symVFS.Symlink(ctx.Context, targetPath, linkPath)
					} else {
						err = fmt.Errorf("junctions are not supported on destination filesystem")
					}
				case 2: // Hardlink
					if hlVFS, ok := dstVfs.(vfs.HardlinkVFS); ok {
						err = hlVFS.Hardlink(ctx.Context, targetPath, linkPath)
					} else {
						err = fmt.Errorf("hardlinks are not supported on destination filesystem")
					}
				}

				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", name, err))
				}
			}

			ctx.RunOnUI(func() {
				if len(errs) > 0 {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to create link(s):\n%s", strings.Join(errs, "\n")), []string{"&Ok"})
				}
				pf.RefreshAll()
			})
		})
	}
	btnCancel.OnClick = func() { dlg.Close() }

	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	dlg.AddItem(lblType)
	dlg.AddItem(comboType)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 52-4, 11-4)
	vbox.Add(promptLbl, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editDest, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowType := vtui.NewHBoxLayout(0, 0, 52-4, 1)
	rowType.Add(lblType, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowType.Add(comboType, vtui.Margins{}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, 52-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	// Keep the action row above the link-type selector. ComboBox.Open() places
	// its popup below the field, so the popup cannot cover these buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(rowType, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()
	dlg.SetFocusedItem(editDest)

	vtui.FrameManager.Push(dlg)
}
func actionCopyInPlace(pf *panel.PanelsFrame) {
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return
	}

	name := fsp.GetRawSelectedName()
	if name == "" || name == ".." {
		return
	}

	sourceVFS := fsp.Vfs
	sourceBasePath := sourceVFS.GetPath()
	dialog.FileInputBox(" Copy ", "Copy '"+name+"' to:", name, func(newName string) {
		if newName == "" || newName == name {
			return
		}
		newPath := sourceVFS.Join(sourceBasePath, newName)

		onCompleteWithClear := func() {
			if pf != nil {
				if fsp := pf.GetActivePanel(); fsp != nil {
					fsp.SelectedItems = make(map[string]bool)
					for _, e := range fsp.Entries {
						e.Selected = false
					}
				}
				pf.RefreshAll()
			}
		}

		go fileops.ExecuteFileOpAt(sourceVFS, sourceVFS, sourceBasePath, []string{name}, newPath, false, config.App.DefaultFileOpMode, onCompleteWithClear)
	})
}
func actionEditorSettings(pf *panel.PanelsFrame) {
	// Height sized so the 3?2 checkbox grid stacks tight (no blank
	// rows between rows of the grid). See #298.
	width, height := 78, 27
	checkCaptions := []string{
		i18n.Msg("EditorSettings.AutoIndent"),
		i18n.Msg("EditorSettings.CursorBeyondEOL"),
		i18n.Msg("EditorSettings.UseEditorConfig"),
		i18n.Msg("EditorSettings.AutoComplete"),
		i18n.Msg("EditorSettings.Crosshair"),
		i18n.Msg("EditorSettings.HighlightOccurrences"),
		i18n.Msg("EditorSettings.ColorerBg"),
		i18n.Msg("EditorSettings.SyntaxAnimation"),
	}
	maxCheckWidth := 0
	for _, caption := range checkCaptions {
		clean, _, _ := vtui.ParseAmpersandString(caption)
		if checkWidth := 4 + vtui.StringWidth(clean); checkWidth > maxCheckWidth {
			maxCheckWidth = checkWidth
		}
	}
	checkRows := (len(checkCaptions) + 1) / 2
	singleCheckColumn := maxCheckWidth > (width-4)/2
	height += checkRows - 3
	if singleCheckColumn {
		height += len(checkCaptions) - checkRows
		checkRows = len(checkCaptions)
	}

	// The external editor has separate commands for the console and GUI. How
	// much of each row the captions take depends on the language, so the
	// fields are sized from what the captions leave rather than from a
	// constant that happens to fit in English.
	captionWidth := func(key string) int {
		clean, _, _ := vtui.ParseAmpersandString(i18n.Msg(key))
		return vtui.StringWidth(clean)
	}
	const minExternalCommandWidth = 20
	extCheckWidth := 4 + captionWidth("EditorSettings.UseExternalEditor")
	extConsoleLabelWidth := captionWidth("EditorSettings.ExternalCommandConsole")
	extGUILabelWidth := captionWidth("EditorSettings.ExternalCommandGUI")
	// The first row spends, left to right: the checkbox, its right margin,
	// the layout's spacing, the label's left margin, the label, its right
	// margin, the spacing again; whatever is left over is the field. The
	// second row has no checkbox, so it gets the full dialog width.
	const extRowSpacing = 1
	extConsoleCmdWidth := (width - 4) - extCheckWidth - 1 - extRowSpacing - 2 - extConsoleLabelWidth - 1 - extRowSpacing
	extGUICmdWidth := (width - 4) - extGUILabelWidth - 1 - extRowSpacing
	stackExternalRows := extConsoleCmdWidth < minExternalCommandWidth || extGUICmdWidth < minExternalCommandWidth
	extCmdWidth := extConsoleCmdWidth
	if stackExternalRows {
		extCmdWidth = width - 4
		height += 4
	} else {
		height++
	}
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("EditorSettings.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	comboExpand := vtui.NewComboBox(0, 0, 40, []string{
		i18n.Msg("EditorSettings.TabExpandNone"),
		i18n.Msg("EditorSettings.TabExpandNew"),
		i18n.Msg("EditorSettings.TabExpandAll"),
	})
	comboExpand.DropdownOnly = true
	if config.App.EditorExpandTabs >= 0 && config.App.EditorExpandTabs <= 2 {
		comboExpand.Menu.SetSelectPos(config.App.EditorExpandTabs)
		comboExpand.Edit.SetText(comboExpand.Menu.Items[config.App.EditorExpandTabs].Text)
	}
	lblExpand := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.ExpandTabs"), comboExpand)
	engines := []string{"Chroma", "Colorer", "None"}
	selectedEngine := 0
	for i, eng := range engines {
		if strings.EqualFold(eng, config.App.EditorHighlighter) {
			selectedEngine = i
			break
		}
	}
	comboHighlighter := vtui.NewComboBox(0, 0, 40, engines)
	comboHighlighter.DropdownOnly = true
	comboHighlighter.Menu.SetSelectPos(selectedEngine)
	comboHighlighter.Edit.SetText(engines[selectedEngine])
	lblHighlighter := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.Highlighter"), comboHighlighter)
	schemeNames := []string{""}
	schemeItems := []string{i18n.Msg("ColorerSettings.BuiltIn")}
	for _, scheme := range editor.ListColorerSchemes() {
		schemeNames = append(schemeNames, scheme.Name)
		schemeItems = append(schemeItems, editor.ColorerSchemeLabel(scheme))
	}
	selectedScheme := 0
	for i := 1; i < len(schemeNames); i++ {
		if strings.EqualFold(schemeNames[i], config.App.EditorColorerScheme) {
			selectedScheme = i
			break
		}
	}
	comboScheme := vtui.NewComboBox(0, 0, 40, schemeItems)
	comboScheme.DropdownOnly = true
	comboScheme.Menu.SetSelectPos(selectedScheme)
	comboScheme.Edit.SetText(schemeItems[selectedScheme])
	lblScheme := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.ColorerStyle"), comboScheme)

	editTabSize := vtui.NewEdit(0, 0, 4, fmt.Sprintf("%d", config.App.EditorTabSize))
	editTabSize.ClearSelection()
	lblTabSize := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.TabSize"), editTabSize)

	editorCodepageIDs, editorCodepageLabels := dialog.CodepageSettingChoices()
	comboEditorCodepage := vtui.NewComboBox(0, 0, 40, editorCodepageLabels)
	comboEditorCodepage.DropdownOnly = true
	editorCodepagePos := dialog.CodepageChoiceIndex(editorCodepageIDs, config.App.EditorDefaultCodePage)
	comboEditorCodepage.Menu.SetSelectPos(editorCodepagePos)
	comboEditorCodepage.Edit.SetText(editorCodepageLabels[editorCodepagePos])
	lblEditorCodepage := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.DefaultCodePage"), comboEditorCodepage)

	chkEditorAutodetect := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.AutodetectCodePage"), false)
	if config.App.EditorAutodetectCodePage {
		chkEditorAutodetect.State = 1
	}

	chkAutoIndent := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.AutoIndent"), false)
	if config.App.EditorAutoIndent {
		chkAutoIndent.State = 1
	}

	chkCursorEOL := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.CursorBeyondEOL"), false)
	if config.App.EditorCursorBeyondEOL {
		chkCursorEOL.State = 1
	}

	chkEditorConfig := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.UseEditorConfig"), false)
	if config.App.EditorUseEditorConfig {
		chkEditorConfig.State = 1
	}

	chkAuto := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.AutoComplete"), false)
	if config.App.EditorAutoComplete {
		chkAuto.State = 1
	}

	chkCrosshair := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.Crosshair"), false)
	if config.App.EditorCrosshair {
		chkCrosshair.State = 1
	}
	chkColorerBg := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.ColorerBg"), false)
	if config.App.EditorColorerBackground {
		chkColorerBg.State = 1
	}

	chkHighlightOccurrences := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.HighlightOccurrences"), false)
	if config.App.EditorMarkOccurrences {
		chkHighlightOccurrences.State = 1
	}

	chkSyntaxAnimation := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.SyntaxAnimation"), false)
	if config.App.EditorSyntaxAnimation {
		chkSyntaxAnimation.State = 1
	}

	editMask := vtui.NewEdit(0, 0, 56, config.App.EditorAutoCompleteMask)
	lblMask := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.Mask"), editMask)

	chkExtEdit := vtui.NewCheckbox(0, 0, i18n.Msg("EditorSettings.UseExternalEditor"), false)
	if config.App.UseExternalEditor {
		chkExtEdit.State = 1
	}

	editExtCmdConsole := vtui.NewEdit(0, 0, extCmdWidth, config.App.ExternalEditorConsole)
	editExtCmdConsole.PathHintsEnabled = true
	history.AttachHistory(editExtCmdConsole, history.ExternalEditorHistoryID)
	lblExtCmdConsole := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.ExternalCommandConsole"), editExtCmdConsole)
	editExtCmdGUI := vtui.NewEdit(0, 0, func() int {
		if stackExternalRows {
			return extCmdWidth
		}
		return extGUICmdWidth
	}(), config.App.ExternalEditorGUI)
	editExtCmdGUI.PathHintsEnabled = true
	history.AttachHistory(editExtCmdGUI, history.ExternalEditorHistoryID)
	lblExtCmdGUI := vtui.NewLabel(0, 0, i18n.Msg("EditorSettings.ExternalCommandGUI"), editExtCmdGUI)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	// 2. Add to Dialog in desired focus order
	dlg.AddItem(lblExpand)
	dlg.AddItem(comboExpand)
	dlg.AddItem(lblHighlighter)
	dlg.AddItem(comboHighlighter)
	dlg.AddItem(lblScheme)
	dlg.AddItem(comboScheme)
	dlg.AddItem(lblTabSize)
	dlg.AddItem(editTabSize)
	dlg.AddItem(chkEditorAutodetect)
	dlg.AddItem(lblEditorCodepage)
	dlg.AddItem(comboEditorCodepage)
	dlg.AddItem(chkAutoIndent)
	dlg.AddItem(chkCursorEOL)
	dlg.AddItem(chkEditorConfig)
	dlg.AddItem(chkAuto)
	dlg.AddItem(chkCrosshair)
	dlg.AddItem(chkHighlightOccurrences)
	dlg.AddItem(chkColorerBg)
	dlg.AddItem(chkSyntaxAnimation)
	dlg.AddItem(lblMask)
	dlg.AddItem(editMask)
	dlg.AddItem(chkExtEdit)
	dlg.AddItem(lblExtCmdConsole)
	dlg.AddItem(editExtCmdConsole)
	dlg.AddItem(lblExtCmdGUI)
	dlg.AddItem(editExtCmdGUI)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	dlg.AddLink(chkExtEdit, editExtCmdConsole, vtui.LinkEnableIfChecked)
	dlg.AddLink(chkExtEdit, editExtCmdGUI, vtui.LinkEnableIfChecked)

	// 3. Layout Configuration
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)

	rowTabs := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTabs.Add(lblExpand, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowTabs.Add(comboExpand, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowTabs, vtui.Margins{}, vtui.AlignFill)
	rowHighlighter := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowHighlighter.Add(lblHighlighter, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowHighlighter.Add(comboHighlighter, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowHighlighter, vtui.Margins{Top: 1}, vtui.AlignFill)
	rowScheme := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowScheme.Add(lblScheme, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowScheme.Add(comboScheme, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowScheme, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowTabSize := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTabSize.Add(lblTabSize, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowTabSize.Add(editTabSize, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowTabSize, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkEditorAutodetect, vtui.Margins{Top: 1}, vtui.AlignLeft)
	rowEditorCodepage := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowEditorCodepage.Add(lblEditorCodepage, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowEditorCodepage.Add(comboEditorCodepage, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowEditorCodepage, vtui.Margins{}, vtui.AlignFill)

	if singleCheckColumn {
		checkColumn := vtui.NewVBoxLayout(0, 0, width-4, checkRows)
		for _, check := range []*vtui.Checkbox{
			chkAutoIndent, chkCursorEOL, chkEditorConfig,
			chkAuto, chkCrosshair, chkHighlightOccurrences,
			chkColorerBg, chkSyntaxAnimation,
		} {
			checkColumn.Add(check, vtui.Margins{}, vtui.AlignLeft)
		}
		vbox.Add(checkColumn, vtui.Margins{Top: 1}, vtui.AlignFill)
	} else {
		col1 := vtui.NewVBoxLayout(0, 0, (width-4)/2, checkRows)
		col1.Add(chkAutoIndent, vtui.Margins{}, vtui.AlignLeft)
		col1.Add(chkEditorConfig, vtui.Margins{}, vtui.AlignLeft)
		col1.Add(chkColorerBg, vtui.Margins{}, vtui.AlignLeft)
		col1.Add(chkHighlightOccurrences, vtui.Margins{}, vtui.AlignLeft)

		col2 := vtui.NewVBoxLayout(0, 0, (width-4)/2, checkRows)
		col2.Add(chkCursorEOL, vtui.Margins{}, vtui.AlignLeft)
		col2.Add(chkAuto, vtui.Margins{}, vtui.AlignLeft)
		col2.Add(chkCrosshair, vtui.Margins{}, vtui.AlignLeft)
		col2.Add(chkSyntaxAnimation, vtui.Margins{}, vtui.AlignLeft)

		rowChecks := vtui.NewHBoxLayout(0, 0, width-4, checkRows)
		rowChecks.Add(col1, vtui.Margins{}, vtui.AlignFill)
		rowChecks.Add(col2, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)
	}

	vbox.Add(lblMask, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editMask, vtui.Margins{}, vtui.AlignFill)

	if stackExternalRows {
		vbox.Add(chkExtEdit, vtui.Margins{Top: 1}, vtui.AlignLeft)
		vbox.Add(lblExtCmdConsole, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(editExtCmdConsole, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(lblExtCmdGUI, vtui.Margins{Top: 1}, vtui.AlignLeft)
		vbox.Add(editExtCmdGUI, vtui.Margins{}, vtui.AlignFill)
	} else {
		rowExtConsole := vtui.NewHBoxLayout(0, 0, width-4, 1)
		rowExtConsole.Add(chkExtEdit, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowExtConsole.Add(lblExtCmdConsole, vtui.Margins{Right: 1, Left: 2}, vtui.AlignLeft)
		rowExtConsole.Add(editExtCmdConsole, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(rowExtConsole, vtui.Margins{Top: 1}, vtui.AlignFill)
		rowExtGUI := vtui.NewHBoxLayout(0, 0, width-4, 1)
		rowExtGUI.Add(lblExtCmdGUI, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowExtGUI.Add(editExtCmdGUI, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(rowExtGUI, vtui.Margins{}, vtui.AlignFill)
	}

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	// Keep the action row above the operation-mode selector. ComboBox.Open()
	// places its popup below the field, so it cannot cover these buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Apply()

	// 4. Logic
	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.EditorHighlighter = comboHighlighter.Menu.Items[comboHighlighter.Menu.SelectPos].Text
		config.App.EditorColorerScheme = ""
		if pos := comboScheme.Menu.SelectPos; pos > 0 && pos < len(schemeNames) {
			config.App.EditorColorerScheme = schemeNames[pos]
		}
		editor.SetColorerScheme(config.App.EditorColorerScheme)
		config.App.EditorExpandTabs = comboExpand.Menu.SelectPos
		config.App.EditorAutodetectCodePage = chkEditorAutodetect.State == 1
		if pos := comboEditorCodepage.Menu.SelectPos; pos >= 0 && pos < len(editorCodepageIDs) {
			config.App.EditorDefaultCodePage = editorCodepageIDs[pos]
		}
		_, _ = fmt.Sscanf(editTabSize.GetText(), "%d", &config.App.EditorTabSize)
		if config.App.EditorTabSize <= 0 {
			config.App.EditorTabSize = 8
		}

		config.App.EditorAutoIndent = chkAutoIndent.State == 1
		config.App.EditorCursorBeyondEOL = chkCursorEOL.State == 1
		config.App.EditorUseEditorConfig = chkEditorConfig.State == 1
		config.App.EditorAutoComplete = chkAuto.State == 1
		config.App.EditorCrosshair = chkCrosshair.State == 1
		config.App.EditorMarkOccurrences = chkHighlightOccurrences.State == 1
		config.App.EditorColorerBackground = chkColorerBg.State == 1
		config.App.EditorSyntaxAnimation = chkSyntaxAnimation.State == 1
		config.App.EditorAutoCompleteMask = editMask.GetText()
		config.App.UseExternalEditor = chkExtEdit.State == 1
		config.App.ExternalEditorConsole = editExtCmdConsole.GetText()
		config.App.ExternalEditorGUI = editExtCmdGUI.GetText()
		// Keep the legacy key useful for older f4 versions.
		config.App.ExternalEditorCommand = config.App.ExternalEditorConsole
		history.CommitHistory(editExtCmdConsole, config.App.ExternalEditorConsole)
		history.CommitHistory(editExtCmdGUI, config.App.ExternalEditorGUI)
		config.SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}

// stopPlayerForDelete lets go of a file the player is reading when it is
// about to be deleted, so the delete succeeds on Windows and the player does
// not stay on a file that is gone. Nothing happens for other files.
func stopPlayerForDelete(pf *panel.PanelsFrame, v vfs.VFS, basePath string, names []string) {
	player := openPlayerPanel(pf)
	if player == nil {
		return
	}
	osv, isLocal := v.(*vfs.OSVFS)
	if !isLocal {
		return
	}
	paths := make([]string, 0, len(names))
	for _, n := range names {
		p := filepath.Join(basePath, n)
		if abs, err := osv.Abs(p); err == nil {
			p = abs
		}
		paths = append(paths, p)
	}
	player.StopIfPlaying(paths)
}

// actionDelete follows the global trash preference. The disposition is
// resolved here, before a task can be queued, so later settings changes cannot
// alter the meaning of an already confirmed operation.
func actionDelete(pf *panel.PanelsFrame) {
	disposition := vfs.DeletePermanently
	if config.App.UseTrash {
		disposition = vfs.DeleteToTrash
	}
	actionDeleteWithDisposition(pf, disposition, false)
}

// actionDeletePermanent is bound to Shift+Del/Shift+NumDel and intentionally
// ignores the global trash preference.
func actionDeletePermanent(pf *panel.PanelsFrame) {
	actionDeleteWithDisposition(pf, vfs.DeletePermanently, true)
}

func actionDeleteWithDisposition(pf *panel.PanelsFrame, disposition vfs.DeleteDisposition, explicitPermanent bool) {
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return
	}

	activeVfs := fsp.Vfs
	basePath := activeVfs.GetPath()
	names := fsp.GetSelectedNames()
	if len(names) == 0 {
		return
	}
	if panel.DispatchPanelAction(pf, vfs.PanelActionDelete, panel.SelectedPanelActionPaths(fsp)) {
		return
	}

	titleKey := "Delete.Title"
	confirmKey := "Delete.ConfirmPermanent"
	buttonKey := "Delete.BtnPermanent"
	if !explicitPermanent {
		buttonKey = "Delete.Btn"
	}
	if disposition == vfs.DeleteToTrash {
		titleKey = "Trash.Title"
		confirmKey = "Trash.Confirm"
		buttonKey = "Trash.Btn"
	}

	if !config.App.ConfirmDelete {
		fsp.PendingSelection = fsp.GetSuccessorName()
		stopPlayerForDelete(pf, activeVfs, basePath, names)
		go fileops.ExecuteDeleteOpWithDispositionAt(activeVfs, basePath, names, config.App.DefaultFileOpMode, disposition, pf.RefreshAll)
		return
	}

	msgName := names[0]
	if len(names) > 1 {
		msgName = fmt.Sprintf(i18n.Msg("Delete.Items"), len(names))
	}

	title := i18n.Msg(titleKey)
	msg := fmt.Sprintf(i18n.Msg(confirmKey), msgName)
	lines := vtui.WrapText(msg, 46)

	dlg := vtui.NewCenteredDialog(50, 8+len(lines), title)
	// Moving an item to the Recycle Bin is recoverable, so keep that prompt on
	// the neutral dialog palette. Only an irreversible deletion is an alarm.
	dlg.IsWarning = disposition == vfs.DeletePermanently
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, (8+len(lines))-4)

	for _, l := range lines {
		t := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(t)
		vbox.Add(t, vtui.Margins{}, vtui.AlignCenter)
	}

	modes := []string{i18n.Msg("Op.Queue"), i18n.Msg("Op.Background"), i18n.Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	defMode := config.App.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnDel := vtui.NewButton(0, 0, i18n.Msg(buttonKey))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	if config.App.DeleteCancelFocused {
		btnCancel.IsDefault = true
	} else {
		btnDel.IsDefault = true
	}

	dlg.AddItem(btnDel)
	dlg.AddItem(btnCancel)
	dlg.AddItem(comboMode)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnDel, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	// Keep the destructive action row above the operation-mode selector.
	// ComboBox.Open() places its popup below the field, so it cannot cover the
	// confirmation buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnDel.OnClick = func() {
		mode := comboMode.Menu.SelectPos
		fsp.PendingSelection = fsp.GetSuccessorName()
		dlg.Close()
		stopPlayerForDelete(pf, activeVfs, basePath, names)
		go fileops.ExecuteDeleteOpWithDispositionAt(activeVfs, basePath, names, mode, disposition, pf.RefreshAll)
	}

	if config.App.DeleteCancelFocused {
		dlg.SetFocusedItem(btnCancel)
	} else {
		dlg.SetFocusedItem(btnDel)
	}

	vtui.FrameManager.Push(dlg)
}

func actionMkDir(pf *panel.PanelsFrame) {
	pnl := pf.GetActivePanel()
	if pnl == nil {
		return
	}
	if temp, isTempPanel := pnl.Vfs.(*panel.TempPanelVFS); isTempPanel {
		// F7 removes references from TempPanel. Do this at the concrete F7
		// action boundary: PanelActionCreate is also used by Shift+F4 for
		// creating a new file and must keep its ordinary meaning.
		paths := panel.SelectedPanelActionPaths(pnl)
		if len(paths) > 0 {
			// Unlike a real delete, removing a TempPanel reference is immediate.
			// Preserve the row above the removed item before the refresh resets
			// the asynchronously loaded panel contents.
			pnl.PendingSelection = pnl.GetPredecessorName()
		}
		if temp.RemovePanelReferences(paths) {
			pf.RefreshAll()
			return
		}
	}

	activeVfs := pnl.Vfs

	dlg := vtui.NewCenteredDialog(40, 11, i18n.Msg("MakeFolder.Title"))
	dlg.ShowClose = true

	editName := vtui.NewEdit(0, 0, 10, "")
	editName.PathHintsEnabled = true
	history.AttachHistory(editName, history.NewFolderHistoryID)
	lblPrompt := vtui.NewLabel(0, 0, i18n.Msg("MakeFolder.Prompt"), editName)
	dlg.AddItem(lblPrompt)
	dlg.AddItem(editName)

	modes := []string{i18n.Msg("Op.Queue"), i18n.Msg("Op.Background"), i18n.Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 30, modes)
	comboMode.DropdownOnly = true
	defMode := config.App.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	dlg.AddItem(comboMode)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 40-4, 11-4)
	vbox.Add(lblPrompt, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editName, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, 40-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	dlg.SetFocusedItem(editName)

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		name := editName.GetText()
		mode := comboMode.Menu.SelectPos
		dlg.Close()
		if name == "" {
			return
		}
		history.CommitHistory(editName, name)
		fullPath := activeVfs.Join(activeVfs.GetPath(), name)

		desc := fmt.Sprintf("Create folder %s", name)
		runFunc := func(ctx context.Context, reporter fileops.TaskReporter, anchor vtui.Frame) error {
			reporter.UpdateTransfer("Creating", name, 100, "Folder", 100, "")
			err := activeVfs.MkDir(ctx, fullPath)
			return err
		}

		if mode == 0 { // Queue
			rk := fileops.GetResourceKey(activeVfs)
			var keys []string
			if rk != "" {
				keys = append(keys, rk)
			}
			task := &fileops.QueueTask{
				Type:    "MkDir",
				Desc:    desc,
				ResKeys: keys,
				Run:     runFunc,
				OnComplete: func() {
					pnl.PendingSelection = name
					pf.RefreshAll()
				},
			}
			fileops.GlobalQueueManager.Enqueue(task)
		} else { // Background / Foreground
			taskCtx := vtui.RunAsync(func(ctx *vtui.TaskContext) {
				err := runFunc(ctx.Context, &fileops.DummyReporter{}, nil)
				ctx.RunOnUI(func() {
					if err != nil {
						vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Operation.Error"), err.Error()), []string{"&Ok"})
					}
					pnl.PendingSelection = name
					pf.RefreshAll()
				})
			})
			_ = taskCtx
		}
	}

	vtui.FrameManager.Push(dlg)
}

// panelCanFindDuplicates reports whether the active panel's file system can
// answer a duplicate search. It is what keeps the menu entry out of sight
// on the ones that cannot, rather than offering it and refusing.
func panelCanFindDuplicates() bool {
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return false
	}
	_, ok := fsp.Vfs.(vfs.DuplicateFinder)
	return ok
}

// actionFindDuplicates asks the file system for files with identical
// content. Only a file system that can do the work on its own side offers
// it: doing it from here would mean reading every candidate over the
// network, which costs more than the answer is worth.
func actionFindDuplicates(pf *panel.PanelsFrame) {
	fsp := pf.GetActivePanel()
	if fsp == nil {
		return
	}
	finder, ok := fsp.Vfs.(vfs.DuplicateFinder)
	if !ok {
		// Reachable through a key binding or a macro, which the menu's
		// visibility rule does not cover.
		vtui.ShowMessage(" Find Duplicates ",
			"This file system cannot search for duplicates.\nOnly a remote one that can hash its own files offers it.",
			[]string{"&Ok"})
		return
	}

	v := fsp.Vfs
	root := v.GetPath()
	opDlg := fileops.NewFileOpProgressDialog(" Searching for duplicates... ")
	var taskCtx *vtui.TaskContext
	// detached is read and written on the UI thread only, which is where
	// both the buttons and the progress updates run.
	detached := false
	opDlg.SetOnCancel(func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	})
	// The hashing runs on the remote host, so the window is only a way of
	// watching it. Closing it that way leaves the work in the job registry.
	opDlg.EnableBackground(func() {
		detached = true
		opDlg.Close()
	})
	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.AddScreenHeadless(opDlg)
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		// The work runs on the remote host whether or not this dialog is
		// still open, so it is listed while it lasts.
		// Started against the connection it runs on, so that a session
		// rebuilt from another panel takes this job off the list instead of
		// leaving it there waiting for an answer that cannot come.
		job := terminal.GlobalBackgroundJobs.StartOn(panel.SessionKeyOf(v), "Duplicates in "+root, ctx.Cancel)
		finished := false
		defer func() {
			if !finished {
				job.Finish()
			}
		}()

		lastUpdate := time.Now()
		groups, err := finder.FindDuplicates(ctx.Context, root, func(p vfs.DuplicateProgress) {
			now := time.Now()
			if now.Sub(lastUpdate) < 50*time.Millisecond {
				return
			}
			lastUpdate = now
			job.SetStatus(fmt.Sprintf("%d of %d files", p.Done, p.Total))
			ctx.RunOnUI(func() {
				if detached {
					return
				}
				opDlg.UpdateCounting("Hashing", p.Path, int64(p.Done), int64(p.Total))
				vtui.FrameManager.Redraw()
			})
		})

		// The panel wants an item per row, and a group is only recognizable
		// by its neighbours, so the groups are flattened in order and the
		// rows of one group stay adjacent.
		var found []panel.FoundFile
		if err == nil {
			for _, group := range groups {
				for _, p := range group {
					item, statErr := v.Stat(ctx.Context, p)
					if statErr != nil {
						item = vfs.VFSItem{Name: v.Base(p)}
					}
					found = append(found, panel.FoundFile{Path: p, Item: item})
				}
			}
		}

		ctx.RunOnUI(func() {
			if !detached {
				opDlg.Close()
			}
			if err != nil && err != context.Canceled {
				vtui.ShowMessage(" Error ", fmt.Sprintf("Duplicate search failed:\n%v", err), []string{"&Ok"})
				return
			}
			if ctx.Err() != nil {
				return
			}
			if len(found) == 0 {
				if !detached {
					vtui.ShowMessage(" Find Duplicates ", "No files with identical content were found.", []string{"&Ok"})
				}
				return
			}
			if detached {
				// Nobody is watching, so the answer waits in the job list
				// rather than opening a window over whatever came next.
				finished = true
				job.FinishWith(fmt.Sprintf("%d duplicate files in %d groups", len(found), len(groups)),
					func() { ShowSearchResults(pf, v, found) })
				return
			}
			ShowSearchResults(pf, v, found)
		})
	})
}

// elementWidth returns the number of columns an element occupies at its
// current position.
func elementWidth(el vtui.UIElement) int {
	x1, _, x2, _ := el.GetPosition()
	return x2 - x1 + 1
}

// checkboxColumnWidth returns the width of the widest checkbox, i.e. the
// width a shared column has to be for a second column placed after it
// to line up across rows.
func checkboxColumnWidth(items ...*vtui.Checkbox) int {
	w := 0
	for _, cb := range items {
		if cw := elementWidth(cb); cw > w {
			w = cw
		}
	}
	return w
}

func boolToCheckboxState(value bool) int {
	if value {
		return 1
	}
	return 0
}

func actionFindFile(pf *panel.PanelsFrame) {
	activePanel := pf.GetActivePanel()
	if activePanel == nil {
		return
	}

	const width, height = 78, 20
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("FindFile.Title"))
	dlg.ShowClose = true

	lblMask := vtui.NewLabel(0, 0, i18n.Msg("FindFile.MaskPrompt"), nil)
	editMask := vtui.NewEdit(0, 0, 20, LastFindFileMask)
	history.AttachHistory(editMask, history.FileMasksHistoryID)
	lblMask.FocusLink = editMask
	dlg.SetFocusedItem(editMask)

	lblText := vtui.NewLabel(0, 0, i18n.Msg("FindFile.TextPrompt"), nil)
	editText := vtui.NewEdit(0, 0, 20, LastFindFileText)
	history.AttachHistory(editText, history.SearchTextHistoryID)
	lblText.FocusLink = editText

	chkCase := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.CaseSensitive"), false)
	chkCase.State = boolToCheckboxState(LastFindFileCaseSensitive)
	chkWhole := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.WholeWords"), false)
	chkWhole.State = boolToCheckboxState(LastFindFileWholeWords)
	chkRegexp := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Regexp"), false)
	chkRegexp.State = boolToCheckboxState(LastFindFileRegexp)
	chkNotContaining := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.NotContaining"), false)
	chkNotContaining.State = boolToCheckboxState(LastFindFileNotContaining)
	chkFolders := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Folders"), false)
	chkFolders.State = boolToCheckboxState(LastFindFileFolders)
	chkSymlinks := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Symlinks"), false)
	chkSymlinks.State = boolToCheckboxState(LastFindFileSymlinks)

	btnFind := vtui.NewButton(0, 0, i18n.Msg("FindFile.BtnFind"))
	btnFind.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lblMask)
	dlg.AddItem(editMask)
	dlg.AddItem(lblText)
	dlg.AddItem(editText)
	dlg.AddItem(chkCase)
	dlg.AddItem(chkWhole)
	dlg.AddItem(chkRegexp)
	dlg.AddItem(chkNotContaining)
	dlg.AddItem(chkFolders)
	dlg.AddItem(chkSymlinks)
	dlg.AddItem(btnFind)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lblMask, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editMask, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(lblText, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editText, vtui.Margins{Top: 1}, vtui.AlignFill)

	// Each row is its own HBox, so the right checkbox would otherwise
	// land wherever its left neighbour ends and the column would
	// zigzag with the caption lengths (#903). Pad every left-column
	// cell to the widest left caption so the right column starts at
	// one X in every row, whatever language the captions come in.
	leftColumn := checkboxColumnWidth(chkCase, chkRegexp, chkFolders)
	optionsRow := func(left, right *vtui.Checkbox) *vtui.HBoxLayout {
		row := vtui.NewHBoxLayout(0, 0, width-4, 1)
		row.Spacing = 8
		row.Add(left, vtui.Margins{Right: leftColumn - elementWidth(left)}, vtui.AlignTop)
		row.Add(right, vtui.Margins{}, vtui.AlignTop)
		return row
	}
	vbox.Add(optionsRow(chkCase, chkWhole), vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(optionsRow(chkRegexp, chkNotContaining), vtui.Margins{}, vtui.AlignFill)
	vbox.Add(optionsRow(chkFolders, chkSymlinks), vtui.Margins{}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter

	hbox.Spacing = 2
	hbox.Add(btnFind, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnFind.OnClick = func() {
		LastFindFileMask = editMask.GetText()
		LastFindFileText = editText.GetText()
		history.CommitHistory(editMask, LastFindFileMask)
		history.CommitHistory(editText, LastFindFileText)
		LastFindFileCaseSensitive = chkCase.State == 1
		LastFindFileWholeWords = chkWhole.State == 1
		LastFindFileRegexp = chkRegexp.State == 1
		LastFindFileNotContaining = chkNotContaining.State == 1
		LastFindFileFolders = chkFolders.State == 1
		LastFindFileSymlinks = chkSymlinks.State == 1
		SaveSession()
		dlg.Close()
		if LastFindFileMask != "" {
			ExecuteFindFile(pf, activePanel.Vfs, activePanel.Vfs.GetPath(), LastFindFileMask, LastFindFileText, FindFileOptions{
				CaseSensitive: LastFindFileCaseSensitive,
				WholeWords:    LastFindFileWholeWords,
				Regex:         LastFindFileRegexp,
				NotContaining: LastFindFileNotContaining,
				FindFolders:   LastFindFileFolders,
				FindSymlinks:  LastFindFileSymlinks,
			})
		}
	}

	vtui.FrameManager.Push(dlg)
}
func actionSaveSettings(pf *panel.PanelsFrame) {
	const width, height = 54, 11
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("SaveSettings.Title"))
	dlg.ShowClose = true

	question := vtui.NewText(0, 0, i18n.Msg("SaveSettings.Question"), 0)
	chkGeneral := vtui.NewCheckbox(0, 0, i18n.Msg("SaveSettings.General"), false)
	chkPanel := vtui.NewCheckbox(0, 0, i18n.Msg("SaveSettings.Panel"), false)
	chkWindow := vtui.NewCheckbox(0, 0, i18n.Msg("SaveSettings.Window"), false)
	chkGeneral.State = 1
	chkPanel.State = 1
	chkWindow.State = 1

	btnSave := vtui.NewButton(0, 0, i18n.Msg("SaveSettings.Save"))
	btnSave.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(question)
	dlg.AddItem(chkGeneral)
	dlg.AddItem(chkPanel)
	dlg.AddItem(chkWindow)
	dlg.AddItem(btnSave)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(question, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkGeneral, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkPanel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkWindow, vtui.Margins{}, vtui.AlignLeft)
	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnSave, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnSave.OnClick = func() {
		saveSettingsGroups(chkGeneral.State == 1, chkPanel.State == 1, chkWindow.State == 1)
		dlg.Close()
		toast.Show(i18n.Msg("SaveSettings.Done"), 2*time.Second)
	}

	vtui.FrameManager.PushToFrameScreen(pf, dlg)
}

func actionAutoSaveSettings(pf *panel.PanelsFrame) {
	const width, height = 66, 13
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("PanelSettings.AutoSaveDetails"))
	dlg.ShowClose = true

	question := vtui.NewText(0, 0, i18n.Msg("SaveSettings.Question"), 0)
	chkDialog := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AutoSave.Dialog"), false)
	chkPanel := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AutoSave.Panel"), false)
	chkCurrent := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AutoSave.Current"), false)
	chkWindow := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AutoSave.GUI"), false)
	chkDialog.State = boolToCheckboxState(config.App.AutoSaveDialogSettings)
	chkPanel.State = boolToCheckboxState(config.App.AutoSavePanelSettings)
	chkCurrent.State = boolToCheckboxState(config.App.AutoSaveCurrentPanel)
	chkWindow.State = boolToCheckboxState(config.App.AutoSaveGUIWindow)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	for _, item := range []vtui.UIElement{question, chkDialog, chkPanel, chkCurrent, chkWindow, btnOk, btnCancel} {
		dlg.AddItem(item)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(question, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDialog, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkPanel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkCurrent, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkWindow, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.AutoSaveDialogSettings = chkDialog.State == 1
		config.App.AutoSavePanelSettings = chkPanel.State == 1
		config.App.AutoSaveCurrentPanel = chkCurrent.State == 1
		config.App.AutoSaveGUIWindow = chkWindow.State == 1
		config.SyncAutoSaveMaster()
		// Changing the autosave policy is an explicit settings action. Persist
		// the policy itself even when the new policy disables future writes.
		config.SaveWithWindowSize(false)
		dlg.Close()
	}

	vtui.FrameManager.PushToFrameScreen(pf, dlg)
}

func actionPanelSettings(pf *panel.PanelsFrame) {
	// Keep the frequently used panel and navigation options in a compact
	// dialog. The less frequently changed performance, console, and operation
	// display options live in actionPanelAdditionalSettings below. Keeping both
	// pages as ordinary dialogs means they remain usable on a 25-row terminal
	// without introducing a second scrolling container for interactive items.
	dlg := vtui.NewCenteredDialog(60, 24, i18n.Msg("PanelSettings.Title"))
	dlg.ShowClose = true

	chkHidden := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.ShowHidden"), false)
	chkHidden.State = 0
	if config.App.ShowHiddenFiles {
		chkHidden.State = 1
	}

	chkDirPrefix := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.ShowDirPrefix"), false)
	chkDirPrefix.State = 0
	if config.App.ShowDirPrefix {
		chkDirPrefix.State = 1
	}

	chkHighlightMarks := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.ShowHighlightMarks"), false)
	chkHighlightMarks.State = 0
	if config.App.ShowHighlightMarks {
		chkHighlightMarks.State = 1
	}

	chkSeparateExtensions := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.SeparateExtensions"), false)
	if config.App.SeparateFileExtensions {
		chkSeparateExtensions.State = 1
	}
	chkFileInfo := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.ShowFileInfo"), false)
	if config.App.ShowPanelFileInfo {
		chkFileInfo.State = 1
	}

	scrollbarModes := []string{
		i18n.Msg("PanelSettings.ScrollbarsOff"),
		i18n.Msg("PanelSettings.ScrollbarsMinimal"),
		i18n.Msg("PanelSettings.ScrollbarsFull"),
	}
	comboScrollbars := vtui.NewComboBox(0, 0, 24, scrollbarModes)
	comboScrollbars.DropdownOnly = true
	comboScrollbars.Menu.SetSelectPos(int(config.App.PanelScrollbarMode))
	comboScrollbars.Edit.SetText(scrollbarModes[config.App.PanelScrollbarMode])
	lblScrollbars := vtui.NewLabel(0, 0, i18n.Msg("PanelSettings.Scrollbars"), comboScrollbars)

	chkPaths := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.SavePaths"), false)
	chkPaths.State = 0
	if config.App.SavePanelPaths {
		chkPaths.State = 1
	}

	chkAutoSave := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AutoSave"), false)
	if config.App.AutoSaveSettings {
		chkAutoSave.State = 1
	}
	btnAutoSaveDetails := vtui.NewButton(0, 0, i18n.Msg("PanelSettings.AutoSaveDetails"))

	chkUseTrash := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.UseTrash"), false)
	if config.App.UseTrash {
		chkUseTrash.State = 1
	}
	chkCmdAc := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.CommandLineAutoComplete"), false)
	chkCmdAc.State = 0
	if config.App.CommandLineAutoComplete {
		chkCmdAc.State = 1
	}

	lblNavigation := vtui.NewText(0, 0, i18n.Msg("PanelSettings.Navigation"), 0)
	navigation := vtui.NewRadioGroup(0, 0, 1, []string{
		i18n.Msg("PanelSettings.NavigationClassic"),
		i18n.Msg("PanelSettings.NavigationVim"),
		i18n.Msg("PanelSettings.NavigationSearch"),
	})
	navigation.Selected = int(config.App.NavigationMode)
	// RadioGroup does not expose its keyboard focus index, so advance it to
	// the selected row to make Space operate on the visibly selected mode.
	for i := 0; i < navigation.Selected; i++ {
		navigation.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	}
	chkStayFocused := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.SearchStayFocused"), false)
	if config.App.SearchCommandStayFocused {
		chkStayFocused.State = 1
	}
	chkStayFocused.SetDisabled(config.App.NavigationMode != config.NavigationSearchFirst)
	navigation.OnChange = func(selected int) {
		chkStayFocused.SetDisabled(config.PanelNavigationMode(selected) != config.NavigationSearchFirst)
	}

	btnAdditional := vtui.NewButton(0, 0, i18n.Msg("PanelSettings.Additional"))
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(chkHidden)
	dlg.AddItem(chkDirPrefix)
	dlg.AddItem(chkHighlightMarks)
	dlg.AddItem(chkSeparateExtensions)
	dlg.AddItem(chkFileInfo)
	dlg.AddItem(lblScrollbars)
	dlg.AddItem(comboScrollbars)
	dlg.AddItem(chkPaths)
	dlg.AddItem(chkAutoSave)
	dlg.AddItem(btnAutoSaveDetails)
	dlg.AddItem(chkUseTrash)
	dlg.AddItem(chkCmdAc)
	dlg.AddItem(lblNavigation)
	dlg.AddItem(navigation)
	dlg.AddItem(chkStayFocused)
	dlg.AddItem(btnAdditional)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 56, 20)
	// First checkbox cluster ? stack tight, no blank rows between.
	vbox.Add(chkHidden, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDirPrefix, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkHighlightMarks, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkSeparateExtensions, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkFileInfo, vtui.Margins{}, vtui.AlignLeft)
	// Blank row before the scrollbar combo ? transition to a different
	// widget kind, worth the visual separator.
	rowScrollbars := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowScrollbars.Add(lblScrollbars, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowScrollbars.Add(comboScrollbars, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowScrollbars, vtui.Margins{}, vtui.AlignFill)
	// Second checkbox cluster.
	vbox.Add(chkPaths, vtui.Margins{}, vtui.AlignLeft)
	autoSaveRow := vtui.NewHBoxLayout(0, 0, 56, 1)
	autoSaveRow.Add(chkAutoSave, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(autoSaveRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(btnAutoSaveDetails, vtui.Margins{Top: 1, Left: 2}, vtui.AlignLeft)
	vbox.Add(chkUseTrash, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkCmdAc, vtui.Margins{}, vtui.AlignLeft)
	// Navigation radio group ? its own visual island.
	vbox.Add(lblNavigation, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(navigation, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkStayFocused, vtui.Margins{Left: 2}, vtui.AlignLeft)
	hbox := vtui.NewHBoxLayout(0, 0, 56, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 1
	hbox.Add(btnAdditional, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.ShowHiddenFiles = chkHidden.State == 1
		config.App.ShowDirPrefix = chkDirPrefix.State == 1
		config.App.ShowHighlightMarks = chkHighlightMarks.State == 1
		config.App.SeparateFileExtensions = chkSeparateExtensions.State == 1
		config.App.ShowPanelFileInfo = chkFileInfo.State == 1
		config.App.PanelScrollbarMode = config.PanelScrollbarMode(comboScrollbars.Menu.SelectPos)
		config.App.SavePanelPaths = chkPaths.State == 1
		if chkAutoSave.State == 0 {
			config.App.AutoSaveDialogSettings = false
			config.App.AutoSavePanelSettings = false
			config.App.AutoSaveCurrentPanel = false
			config.App.AutoSaveGUIWindow = false
		} else if !config.App.AutoSaveDialogSettings && !config.App.AutoSavePanelSettings && !config.App.AutoSaveCurrentPanel && !config.App.AutoSaveGUIWindow {
			config.App.AutoSaveDialogSettings = true
			config.App.AutoSavePanelSettings = true
			config.App.AutoSaveCurrentPanel = true
			config.App.AutoSaveGUIWindow = true
		}
		config.SyncAutoSaveMaster()
		config.App.UseTrash = chkUseTrash.State == 1
		config.App.CommandLineAutoComplete = chkCmdAc.State == 1
		pf.CmdLine.Edit.PathHintsEnabled = config.App.CommandLineAutoComplete
		config.App.NavigationMode = config.PanelNavigationMode(navigation.Selected)
		config.App.SearchCommandStayFocused = chkStayFocused.State == 1
		pf.ApplyNavigationMode()
		config.SaveConfig()
		dlg.Close()
		pf.ResizeConsole(pf.LastW, pf.LastH)
		pf.RefreshAll()
	}
	btnAdditional.OnClick = func() { actionPanelAdditionalSettings(pf) }
	btnAutoSaveDetails.OnClick = func() { actionAutoSaveSettings(pf) }

	vtui.FrameManager.Push(dlg)
}

func actionPanelAdditionalSettings(pf *panel.PanelsFrame) {
	// This page contains the options that are changed less often than the
	// panel/navigation controls. Keep it below 25 rows even when the host
	// console notice needs a separate line.
	consoleModeUnavailable := terminal.ProbeGUIBackend() != "" || !terminal.ProbeHostTTY()
	dlg := vtui.NewCenteredDialog(60, 24, i18n.Msg("PanelSettings.AdvancedTitle"))
	dlg.ShowClose = true

	chkSync := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.SyncPanelLoad"), false)
	if config.App.SyncPanelLoad {
		chkSync.State = 1
	}
	editApplyWorkers := vtui.NewEdit(0, 0, 12, strconv.Itoa(config.App.ApplyCommandParallelism))
	lblApplyWorkers := vtui.NewLabel(0, 0, i18n.Msg("PanelSettings.ApplyWorkers"), editApplyWorkers)

	chkAlwaysMenu := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.AlwaysShowMenuBar"), false)
	if config.App.AlwaysShowMenuBar {
		chkAlwaysMenu.State = 1
	}
	chkCPUGPU := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.InfoPanelCPUGPU"), false)
	if config.App.InfoPanelCPUGPU {
		chkCPUGPU.State = 1
	}
	chkEscToggle := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.EscTogglePanels"), false)
	if config.App.EscTogglePanels {
		chkEscToggle.State = 1
	}
	chkTerminalCtrlN := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.TerminalCtrlNWorkspace"), false)
	if config.App.TerminalCtrlNWorkspace {
		chkTerminalCtrlN.State = 1
	}
	chkExactSearch := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.SearchExactOnHit"), false)
	if config.App.SearchExactOnHit {
		chkExactSearch.State = 1
	}

	lblConsoleMode := vtui.NewText(0, 0, i18n.Msg("PanelSettings.ConsoleMode"), 0)
	radioConsoleMode := vtui.NewRadioGroup(0, 0, 1, []string{
		i18n.Msg("PanelSettings.ConsoleModeOwn"),
		i18n.Msg("PanelSettings.ConsoleModeHost"),
	})
	if strings.EqualFold(config.App.ConsoleMode, "host") {
		radioConsoleMode.Selected = 1
	}
	for i := 0; i < radioConsoleMode.Selected; i++ {
		radioConsoleMode.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	}
	chkOverlay := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.ConsoleOverlayUI"), false)
	if config.App.ConsoleOverlayUI {
		chkOverlay.State = 1
	}
	chkOverlay.SetDisabled(radioConsoleMode.Selected != 1)
	var lblConsoleUnavailable *vtui.Text
	if consoleModeUnavailable {
		lblConsoleUnavailable = vtui.NewText(0, 0, i18n.Msg("PanelSettings.ConsoleModeUnavailable"), 0)
	}
	lblConsoleNote := vtui.NewText(0, 0, i18n.Msg("PanelSettings.ConsoleModeNote"), 0)
	radioConsoleMode.OnChange = func(selected int) {
		chkOverlay.SetDisabled(selected != 1)
	}

	modes := []string{i18n.Msg("Op.Queue"), i18n.Msg("Op.Background"), i18n.Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 24, modes)
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(config.App.DefaultFileOpMode)
	comboMode.Edit.SetText(modes[config.App.DefaultFileOpMode])
	lblMode := vtui.NewLabel(0, 0, i18n.Msg("PanelSettings.DefaultMode"), comboMode)

	pathModes := []string{i18n.Msg("Op.PathNameOnly"), i18n.Msg("Op.PathFullPath"), i18n.Msg("Op.PathSrcDst")}
	comboPath := vtui.NewComboBox(0, 0, 24, pathModes)
	comboPath.DropdownOnly = true
	comboPath.Menu.SetSelectPos(config.App.FileOpPathDisplay)
	comboPath.Edit.SetText(pathModes[config.App.FileOpPathDisplay])
	lblPath := vtui.NewLabel(0, 0, i18n.Msg("PanelSettings.PathDisplay"), comboPath)

	macroModes := []string{"key_macros.ini (Legacy)", "Macros/scripts/*.lua"}
	comboMacro := vtui.NewComboBox(0, 0, 24, macroModes)
	comboMacro.DropdownOnly = true
	comboMacro.Menu.SetSelectPos(config.App.MacroRecordFormat)
	comboMacro.Edit.SetText(macroModes[config.App.MacroRecordFormat])
	lblMacro := vtui.NewLabel(0, 0, i18n.Msg("PanelSettings.RecordMacrosTo"), comboMacro)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{
		chkSync, lblApplyWorkers, editApplyWorkers, chkAlwaysMenu, chkCPUGPU,
		chkEscToggle, chkTerminalCtrlN, chkExactSearch, lblConsoleMode, radioConsoleMode,
		chkOverlay, lblConsoleNote, lblMode, comboMode, lblPath, comboPath,
		lblMacro, comboMacro, btnOk, btnCancel,
	} {
		dlg.AddItem(item)
	}
	if lblConsoleUnavailable != nil {
		dlg.AddItem(lblConsoleUnavailable)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 56, 20)
	vbox.Add(chkSync, vtui.Margins{}, vtui.AlignLeft)
	rowApplyWorkers := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowApplyWorkers.Add(lblApplyWorkers, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowApplyWorkers.Add(editApplyWorkers, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowApplyWorkers, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkAlwaysMenu, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkCPUGPU, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkEscToggle, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkTerminalCtrlN, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkExactSearch, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(lblConsoleMode, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(radioConsoleMode, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkOverlay, vtui.Margins{Left: 2}, vtui.AlignLeft)
	if lblConsoleUnavailable != nil {
		vbox.Add(lblConsoleUnavailable, vtui.Margins{Left: 2}, vtui.AlignLeft)
	}
	vbox.Add(lblConsoleNote, vtui.Margins{Left: 2}, vtui.AlignLeft)

	rowMode := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowMode.Add(lblMode, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowMode.Add(comboMode, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowMode, vtui.Margins{}, vtui.AlignFill)
	rowPath := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowPath.Add(lblPath, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowPath.Add(comboPath, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowPath, vtui.Margins{}, vtui.AlignFill)
	rowMacro := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowMacro.Add(lblMacro, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowMacro.Add(comboMacro, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowMacro, vtui.Margins{}, vtui.AlignFill)
	hbox := vtui.NewHBoxLayout(0, 0, 56, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		applyWorkers, err := strconv.Atoi(strings.TrimSpace(editApplyWorkers.GetText()))
		if err != nil || applyWorkers < 0 {
			vtui.ShowMessageOn(dlg, i18n.Msg("ApplyCommand.InvalidWorkersTitle"), i18n.Msg("ApplyCommand.InvalidWorkers"), []string{i18n.Msg("vtui.Ok")})
			return
		}
		config.App.SyncPanelLoad = chkSync.State == 1
		config.App.ApplyCommandParallelism = applyWorkers
		config.App.AlwaysShowMenuBar = chkAlwaysMenu.State == 1
		config.App.InfoPanelCPUGPU = chkCPUGPU.State == 1
		config.App.EscTogglePanels = chkEscToggle.State == 1
		config.App.TerminalCtrlNWorkspace = chkTerminalCtrlN.State == 1
		config.App.SearchExactOnHit = chkExactSearch.State == 1
		if radioConsoleMode.Selected == 1 {
			config.App.ConsoleMode = "host"
		} else {
			config.App.ConsoleMode = "own"
		}
		config.App.ConsoleOverlayUI = chkOverlay.State == 1
		config.App.DefaultFileOpMode = comboMode.Menu.SelectPos
		config.App.FileOpPathDisplay = comboPath.Menu.SelectPos
		config.App.MacroRecordFormat = comboMacro.Menu.SelectPos
		config.SaveConfig()
		dlg.Close()
		pf.ResizeConsole(pf.LastW, pf.LastH)
		pf.RefreshAll()
	}

	vtui.FrameManager.Push(dlg)
}

func actionConfirmationsSettings(pf *panel.PanelsFrame) {
	const width, height = 56, 15
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("ConfirmationsSettings.Title"))
	dlg.ShowClose = true

	chkCopy := vtui.NewCheckbox(0, 0, i18n.Msg("ConfirmationsSettings.Copy"), false)
	chkCopy.State = 0
	if config.App.ConfirmCopy {
		chkCopy.State = 1
	}

	chkMove := vtui.NewCheckbox(0, 0, i18n.Msg("ConfirmationsSettings.Move"), false)
	chkMove.State = 0
	if config.App.ConfirmMove {
		chkMove.State = 1
	}

	chkDelete := vtui.NewCheckbox(0, 0, i18n.Msg("ConfirmationsSettings.Delete"), false)
	chkDelete.State = 0
	if config.App.ConfirmDelete {
		chkDelete.State = 1
	}

	chkExit := vtui.NewCheckbox(0, 0, i18n.Msg("ConfirmationsSettings.Exit"), false)
	chkExit.State = 0
	if config.App.ConfirmExit {
		chkExit.State = 1
	}

	chkDelFocus := vtui.NewCheckbox(0, 0, i18n.Msg("ConfirmationsSettings.DeleteCancelFocused"), false)
	chkDelFocus.State = 0
	if config.App.DeleteCancelFocused {
		chkDelFocus.State = 1
	}

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(chkCopy)
	dlg.AddItem(chkMove)
	dlg.AddItem(chkDelete)
	dlg.AddItem(chkExit)
	dlg.AddItem(chkDelFocus)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(chkCopy, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkMove, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDelete, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkExit, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDelFocus, vtui.Margins{}, vtui.AlignLeft)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.ConfirmCopy = chkCopy.State == 1
		config.App.ConfirmMove = chkMove.State == 1
		config.App.ConfirmDelete = chkDelete.State == 1
		config.App.ConfirmExit = chkExit.State == 1
		config.App.DeleteCancelFocused = chkDelFocus.State == 1
		config.SaveConfig()
		dlg.Close()
		pf.RefreshAll()
	}

	vtui.FrameManager.Push(dlg)
}

func actionMouseWheelSettings(pf *panel.PanelsFrame) {
	const width, height = 44, 22
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("MouseWheel.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	lblHint := vtui.NewText(0, 0, i18n.Msg("MouseWheel.Hint"), 0)

	newWheelRow := func(upVal, downVal int) (lblUp, lblDown *vtui.Text, editUp, editDown *vtui.Edit) {
		editUp = vtui.NewEdit(0, 0, 5, strconv.Itoa(upVal))
		editDown = vtui.NewEdit(0, 0, 5, strconv.Itoa(downVal))
		lblUp = vtui.NewLabel(0, 0, i18n.Msg("MouseWheel.Up"), editUp)
		lblDown = vtui.NewLabel(0, 0, i18n.Msg("MouseWheel.Down"), editDown)
		return
	}
	lblPanelUp, lblPanelDown, editPanelUp, editPanelDown := newWheelRow(config.App.WheelPanelUp, config.App.WheelPanelDown)
	lblEditorUp, lblEditorDown, editEditorUp, editEditorDown := newWheelRow(config.App.WheelEditorUp, config.App.WheelEditorDown)
	lblViewerUp, lblViewerDown, editViewerUp, editViewerDown := newWheelRow(config.App.WheelViewerUp, config.App.WheelViewerDown)
	lblMenuUp, lblMenuDown, editMenuUp, editMenuDown := newWheelRow(config.App.WheelMenuUp, config.App.WheelMenuDown)
	lblTableUp, lblTableDown, editTableUp, editTableDown := newWheelRow(config.App.WheelTableUp, config.App.WheelTableDown)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	// 2. Add to Dialog. Each area lives in its own GroupBox: the box border
	// and caption are decorative as far as the layout validator is concerned,
	// so a translated caption of any length can never land one cell away
	// from an interactive widget of a neighbouring row (the failure mode
	// that plain text headers kept hitting in some language or other).
	dlg.AddItem(lblHint)

	type wheelGroup struct {
		gb       *vtui.GroupBox
		lblUp    *vtui.Text
		lblDown  *vtui.Text
		editUp   *vtui.Edit
		editDown *vtui.Edit
	}
	newWheelGroup := func(titleKey string, lblUp, lblDown *vtui.Text, editUp, editDown *vtui.Edit) wheelGroup {
		gb := vtui.NewGroupBox(0, 0, width-5, 2, i18n.Msg(titleKey))
		dlg.AddItem(gb)
		gb.AddItem(lblUp)
		gb.AddItem(editUp)
		gb.AddItem(lblDown)
		gb.AddItem(editDown)
		return wheelGroup{gb: gb, lblUp: lblUp, lblDown: lblDown, editUp: editUp, editDown: editDown}
	}
	groups := []wheelGroup{
		newWheelGroup("MouseWheel.Panels", lblPanelUp, lblPanelDown, editPanelUp, editPanelDown),
		newWheelGroup("MouseWheel.Editor", lblEditorUp, lblEditorDown, editEditorUp, editEditorDown),
		newWheelGroup("MouseWheel.Viewer", lblViewerUp, lblViewerDown, editViewerUp, editViewerDown),
		newWheelGroup("MouseWheel.Menus", lblMenuUp, lblMenuDown, editMenuUp, editMenuDown),
		newWheelGroup("MouseWheel.Tables", lblTableUp, lblTableDown, editTableUp, editTableDown),
	}

	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	// 3. Layout Configuration
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lblHint, vtui.Margins{}, vtui.AlignLeft)
	for _, g := range groups {
		vbox.Add(g.gb, vtui.Margins{}, vtui.AlignFill)
	}

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	// The boxes have their final coordinates only after vbox.Apply(),
	// so the rows inside them are laid out in a second pass.
	for _, g := range groups {
		row := vtui.NewHBoxLayout(0, 0, g.gb.X2-g.gb.X1-3, 1)
		row.HorizontalAlign = vtui.AlignCenter
		row.Add(g.lblUp, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(g.editUp, vtui.Margins{Right: 4}, vtui.AlignLeft)
		row.Add(g.lblDown, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(g.editDown, vtui.Margins{}, vtui.AlignLeft)
		row.SetPosition(g.gb.X1+2, g.gb.Y1+1, g.gb.X2-2, g.gb.Y1+1)
	}

	// 4. Logic
	parseWheel := func(e *vtui.Edit) int {
		n := 0
		fmt.Sscanf(e.GetText(), "%d", &n)
		if n < 0 {
			n = 0
		}
		return n
	}
	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.WheelPanelUp = parseWheel(editPanelUp)
		config.App.WheelPanelDown = parseWheel(editPanelDown)
		config.App.WheelEditorUp = parseWheel(editEditorUp)
		config.App.WheelEditorDown = parseWheel(editEditorDown)
		config.App.WheelViewerUp = parseWheel(editViewerUp)
		config.App.WheelViewerDown = parseWheel(editViewerDown)
		config.App.WheelMenuUp = parseWheel(editMenuUp)
		config.App.WheelMenuDown = parseWheel(editMenuDown)
		config.App.WheelTableUp = parseWheel(editTableUp)
		config.App.WheelTableDown = parseWheel(editTableDown)
		config.ApplyWheelSettings()
		config.SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}

func actionPathHintSettings(pf *panel.PanelsFrame) {
	const width, height = 56, 19
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("PathHints.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	chkFullPath := vtui.NewCheckbox(0, 0, i18n.Msg("PathHints.FullPath"), false)
	if config.App.PathHintFullPath {
		chkFullPath.State = 1
	}

	sources := []string{i18n.Msg("PathHints.SourceActive"), i18n.Msg("PathHints.SourcePassive"), i18n.Msg("PathHints.SourceBoth")}
	comboSource := vtui.NewComboBox(0, 0, 24, sources)
	comboSource.DropdownOnly = true
	if config.App.PathHintSource >= 0 && config.App.PathHintSource < len(sources) {
		comboSource.Menu.SetSelectPos(config.App.PathHintSource)
		comboSource.Edit.SetText(sources[config.App.PathHintSource])
	}
	lblSource := vtui.NewLabel(0, 0, i18n.Msg("PathHints.Source"), comboSource)

	editTimeout := vtui.NewEdit(0, 0, 5, strconv.Itoa(config.App.PathHintTimeout))
	lblTimeout := vtui.NewLabel(0, 0, i18n.Msg("PathHints.Timeout"), editTimeout)

	editMaxVisible := vtui.NewEdit(0, 0, 5, strconv.Itoa(config.App.PathHintMaxVisible))
	lblMaxVisible := vtui.NewLabel(0, 0, i18n.Msg("PathHints.MaxVisible"), editMaxVisible)

	chkPerCategory := vtui.NewCheckbox(0, 0, i18n.Msg("PathHints.PerCategory"), false)
	if config.App.PathHintPerCategory {
		chkPerCategory.State = 1
	}

	chkDialogAutoComplete := vtui.NewCheckbox(0, 0, i18n.Msg("PathHints.DialogAutoComplete"), false)
	if config.App.DialogAutoComplete {
		chkDialogAutoComplete.State = 1
	}

	lblNote := vtui.NewText(0, 0, i18n.Msg("PathHints.MarkersNote"), 0)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	// 2. Add to Dialog
	dlg.AddItem(chkFullPath)
	dlg.AddItem(lblSource)
	dlg.AddItem(comboSource)
	dlg.AddItem(lblTimeout)
	dlg.AddItem(editTimeout)
	dlg.AddItem(lblMaxVisible)
	dlg.AddItem(editMaxVisible)
	dlg.AddItem(chkPerCategory)
	dlg.AddItem(chkDialogAutoComplete)
	dlg.AddItem(lblNote)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	// 3. Layout Configuration
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(chkFullPath, vtui.Margins{}, vtui.AlignLeft)

	rowSource := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowSource.Add(lblSource, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowSource.Add(comboSource, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowSource, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowTimeout := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTimeout.Add(lblTimeout, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowTimeout.Add(editTimeout, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowTimeout, vtui.Margins{}, vtui.AlignFill)

	rowMaxVisible := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowMaxVisible.Add(lblMaxVisible, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowMaxVisible.Add(editMaxVisible, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowMaxVisible, vtui.Margins{}, vtui.AlignFill)

	vbox.Add(chkPerCategory, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDialogAutoComplete, vtui.Margins{}, vtui.AlignLeft)

	vbox.Add(lblNote, vtui.Margins{Top: 1}, vtui.AlignLeft)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	// 4. Logic
	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.PathHintFullPath = chkFullPath.State == 1
		config.App.PathHintSource = comboSource.Menu.SelectPos
		timeout := 2
		fmt.Sscanf(editTimeout.GetText(), "%d", &timeout)
		if timeout < 1 {
			timeout = 1
		}
		config.App.PathHintTimeout = timeout
		maxVisible := 5
		fmt.Sscanf(editMaxVisible.GetText(), "%d", &maxVisible)
		if maxVisible < 1 {
			maxVisible = 1
		}
		config.App.PathHintMaxVisible = maxVisible
		config.App.PathHintPerCategory = chkPerCategory.State == 1
		config.App.DialogAutoComplete = chkDialogAutoComplete.State == 1
		panel.ApplyPathHintSettings()
		config.SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}
func actionUpdateSettings(pf *panel.PanelsFrame) {
	width, height := 54, 11
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("UpdateSettings.Title"))
	dlg.ShowClose = true

	channels := []string{i18n.Msg("UpdateSettings.ChannelStable"), i18n.Msg("UpdateSettings.ChannelNightly")}
	comboChannel := vtui.NewComboBox(0, 0, 24, channels)
	comboChannel.DropdownOnly = true
	if config.App.UpdateChannel >= 0 && config.App.UpdateChannel < len(channels) {
		comboChannel.Menu.SetSelectPos(config.App.UpdateChannel)
		comboChannel.Edit.SetText(channels[config.App.UpdateChannel])
	}
	lblChannel := vtui.NewLabel(0, 0, i18n.Msg("UpdateSettings.Channel"), comboChannel)

	intervals := []string{i18n.Msg("UpdateSettings.IntervalNever"), i18n.Msg("UpdateSettings.IntervalStart"), i18n.Msg("UpdateSettings.IntervalDaily"), i18n.Msg("UpdateSettings.IntervalWeekly")}
	comboInterval := vtui.NewComboBox(0, 0, 24, intervals)
	comboInterval.DropdownOnly = true
	if config.App.UpdateInterval >= 0 && config.App.UpdateInterval < len(intervals) {
		comboInterval.Menu.SetSelectPos(config.App.UpdateInterval)
		comboInterval.Edit.SetText(intervals[config.App.UpdateInterval])
	}
	lblInterval := vtui.NewLabel(0, 0, i18n.Msg("UpdateSettings.Interval"), comboInterval)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCheck := vtui.NewButton(0, 0, i18n.Msg("UpdateSettings.BtnCheck"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lblChannel)
	dlg.AddItem(comboChannel)
	dlg.AddItem(lblInterval)
	dlg.AddItem(comboInterval)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCheck)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)

	rowChannel := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowChannel.Add(lblChannel, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowChannel.Add(comboChannel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowChannel, vtui.Margins{}, vtui.AlignFill)

	rowInterval := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowInterval.Add(lblInterval, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowInterval.Add(comboInterval, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowInterval, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCheck, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 2}, vtui.AlignFill)
	vbox.Apply()
	dlg.SetFocusedItem(btnCheck)

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		config.App.UpdateChannel = comboChannel.Menu.SelectPos
		config.App.UpdateInterval = comboInterval.Menu.SelectPos
		config.SaveConfig()
		dlg.Close()
	}
	btnCheck.OnClick = func() {
		config.App.UpdateChannel = comboChannel.Menu.SelectPos
		config.App.UpdateInterval = comboInterval.Menu.SelectPos
		config.SaveConfig()
		dlg.Close()
		// Do not hold the UI mouse-dispatch loop while the manual update
		// check waits for GitHub. The dialog is already closed, so the
		// network request can safely continue in the background and post
		// its result back through FrameManager when it completes.
		go CheckForUpdates(pf, true)
	}

	vtui.FrameManager.Push(dlg)
}

func actionImportFar2lHistory(pf *panel.PanelsFrame) {
	home, err := os.UserHomeDir()
	if err != nil {
		vtui.ShowMessage(" Error ", "Cannot find user home directory.", []string{"&Ok"})
		return
	}
	far2lConfig := filepath.Join(home, ".config", "far2l", "history", "commands.hst")
	if _, err := os.Stat(far2lConfig); os.IsNotExist(err) {
		vtui.ShowMessage(" Error ", "far2l history not found at:\n"+far2lConfig, []string{"&Ok"})
		return
	}

	dlg := vtui.ShowMessage(" Import History ", "Do you want to import command history from far2l?\nThis will merge it with your current history.", []string{"&Import", "Cancel"})
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			recs, err := history.ImportFar2lHistory(ini.Load(far2lConfig), far2lConfig)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to import history:\n%v", err), []string{"&Ok"})
					return
				}

				hp, isF4 := vtui.GlobalHistoryProvider.(*history.F4HistoryProvider)
				if !isF4 {
					vtui.ShowMessage(" Error ", "Incompatible history provider.", []string{"&Ok"})
					return
				}

				current := hp.LoadRichHistory("cmdline")
				seen := make(map[string]bool)
				var merged []history.HistoryRecord

				for _, r := range current {
					if !seen[r.Name] {
						seen[r.Name] = true
						merged = append(merged, r)
					}
				}

				for _, r := range recs {
					if !seen[r.Name] {
						seen[r.Name] = true
						merged = append(merged, r)
					}
				}

				limit := pf.CmdLine.Edit.HistoryLimit
				if limit <= 0 {
					limit = 100
				}
				if len(merged) > limit {
					merged = merged[:limit]
				}

				hp.SaveRichHistory("cmdline", merged)

				h := history.ExtractNames(merged)
				pf.CmdLine.Edit.History = h

				toast.Show(fmt.Sprintf("Imported %d new commands from far2l.", len(merged)-len(current)), 3*time.Second)
			})
		})
	}
}

func actionAppearanceSettings(pf *panel.PanelsFrame) {
	const width, height = 64, 30
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("AppearanceSettings.Title"))
	dlg.ShowClose = true
	// Snapshot the whole palette (not just the style name) so a
	// Cancel restores every runtime tweak ? farcolors.ini overrides
	// loaded at startup, Colorer editor-background pushes, anything
	// else that touched vtui.Palette. Re-applying originalStyle
	// alone would wipe those.
	originalPalette := append([]uint64(nil), vtui.Palette...)

	styles := theme.AvailableColorStyles()
	names := make([]string, len(styles))
	selected := 0
	for i, style := range styles {
		names[i] = style.Name
		if strings.EqualFold(style.Name, config.App.ColorStyle) {
			selected = i
		}
	}

	comboStyle := vtui.NewComboBox(0, 0, 24, names)
	comboStyle.DropdownOnly = true
	if len(names) > 0 {
		comboStyle.Menu.SetSelectPos(selected)
		comboStyle.Edit.SetText(names[selected])
	}
	lblStyle := vtui.NewText(0, 0, i18n.Msg("AppearanceSettings.Style"), 0)
	lblStyle.FocusLink = comboStyle
	defaultMenuAction := comboStyle.Menu.OnAction
	comboStyle.Menu.OnAction = func(idx int) {
		defaultMenuAction(idx)
		if idx >= 0 && idx < len(names) {
			if err := theme.ApplyColorStyle(names[idx]); err == nil {
				vtui.FrameManager.Redraw()
			}
		}
	}
	fontChoices := gui.GuiFontDisplayChoices(config.App.Language, config.App.GuiFont)
	comboFont := vtui.NewComboBox(0, 0, 30, fontChoices)
	comboFont.Edit.SetText(gui.GuiFontCurrentDisplayName(config.App.Language, config.App.GuiFont))
	comboFont.Edit.SelectAll()
	gui.ConfigureGuiFontCombo(comboFont, fontChoices)
	lblFont := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.Font"), comboFont)
	chkSystemMonospace := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.UseSystemMonospace"), false)
	if config.App.GuiUseSystemMonospace {
		chkSystemMonospace.State = 1
	}
	updateFontEditor := func() {
		usePlatformFont := chkSystemMonospace.State == 1 && (runtime.GOOS == "windows" || runtime.GOOS == "darwin")
		lblFont.SetDisabled(usePlatformFont)
		comboFont.SetDisabled(usePlatformFont)
	}
	chkSystemMonospace.OnChange = func(int) { updateFontEditor() }
	updateFontEditor()

	editSize := vtui.NewEdit(0, 0, 6, fmt.Sprintf("%d", config.App.GuiFontSize))
	editSize.Validator = &vtui.IntRangeValidator{Min: 6, Max: 72}
	lblSize := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.FontSize"), editSize)

	editTitle := vtui.NewEdit(0, 0, 30, config.App.ConsoleTitleTemplate)
	lblTitle := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.TitleTemplate"), editTitle)
	chkFullPathTitle := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.DisplayFullPathInTitle"), false)
	if config.App.DisplayFullPathInTitle {
		chkFullPathTitle.State = 1
	}

	workspaceTabModes := []string{
		i18n.Msg("AppearanceSettings.WorkspaceTabsAlways"),
		i18n.Msg("AppearanceSettings.WorkspaceTabsMultiple"),
		i18n.Msg("AppearanceSettings.WorkspaceTabsCtrl"),
		i18n.Msg("AppearanceSettings.WorkspaceTabsNever"),
	}
	comboWorkspaceTabs := vtui.NewComboBox(0, 0, 30, workspaceTabModes)
	comboWorkspaceTabs.DropdownOnly = true
	workspaceTabSelection := config.App.WorkspaceTabMode
	if workspaceTabSelection < 0 || workspaceTabSelection >= len(workspaceTabModes) {
		workspaceTabSelection = int(vtui.WorkspaceTabsMultiple)
	}
	comboWorkspaceTabs.Menu.SetSelectPos(workspaceTabSelection)
	comboWorkspaceTabs.Edit.SetText(choiceText(workspaceTabModes, workspaceTabSelection))
	lblWorkspaceTabs := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.WorkspaceTabs"), comboWorkspaceTabs)
	chkWorkspaceTabsOverlay := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.WorkspaceTabsOverlay"), config.App.WorkspaceTabsOverlay)
	if config.App.WorkspaceTabsOverlay {
		chkWorkspaceTabsOverlay.State = 1
	}

	ctrlTabModes := []string{
		i18n.Msg("AppearanceSettings.CtrlTabDirect"),
		i18n.Msg("AppearanceSettings.CtrlTabMenu"),
	}
	comboCtrlTab := vtui.NewComboBox(0, 0, 30, ctrlTabModes)
	comboCtrlTab.DropdownOnly = true
	ctrlTabSelection := 0
	if config.App.CtrlTabShowsMenu {
		ctrlTabSelection = 1
	}
	comboCtrlTab.Menu.SetSelectPos(ctrlTabSelection)
	comboCtrlTab.Edit.SetText(ctrlTabModes[ctrlTabSelection])
	lblCtrlTab := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.CtrlTab"), comboCtrlTab)

	chkAltNumberTabs := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.AltNumberTabs"), config.App.AltNumberSwitchesTabs)
	if config.App.AltNumberSwitchesTabs {
		chkAltNumberTabs.State = 1
	}
	chkRestoreWorkspaceTabs := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.RestoreWorkspaceTabs"), config.App.RestoreWorkspaceTabs)
	if config.App.RestoreWorkspaceTabs {
		chkRestoreWorkspaceTabs.State = 1
	}
	workspaceNumberingModes := []string{
		i18n.Msg("AppearanceSettings.WorkspaceNumbersAlways"),
		i18n.Msg("AppearanceSettings.WorkspaceNumbersSession"),
		i18n.Msg("AppearanceSettings.WorkspaceNumbersOrder"),
	}
	comboWorkspaceNumbering := vtui.NewComboBox(0, 0, 30, workspaceNumberingModes)
	comboWorkspaceNumbering.DropdownOnly = true
	workspaceNumberingSelection := int(config.App.WorkspaceTabNumbering)
	if workspaceNumberingSelection < 0 || workspaceNumberingSelection >= len(workspaceNumberingModes) {
		workspaceNumberingSelection = int(config.WorkspaceTabNumbersAlways)
	}
	comboWorkspaceNumbering.Menu.SetSelectPos(workspaceNumberingSelection)
	comboWorkspaceNumbering.Edit.SetText(choiceText(workspaceNumberingModes, workspaceNumberingSelection))
	lblWorkspaceNumbering := vtui.NewLabel(0, 0, i18n.Msg("AppearanceSettings.WorkspaceNumbers"), comboWorkspaceNumbering)

	chkCursor := vtui.NewCheckbox(0, 0, i18n.Msg("PanelSettings.KeepCursor"), false)
	chkCursor.State = 0
	if config.App.KeepTerminalCursor {
		chkCursor.State = 1
	}

	chkContrast := vtui.NewCheckbox(0, 0, i18n.Msg("AppearanceSettings.ColorCorrection"), false)
	chkContrast.State = 0
	if config.App.EnforceColorCorrection {
		chkContrast.State = 1
	}

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnExport := vtui.NewButton(0, 0, i18n.Msg("AppearanceSettings.ExportBtn"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lblStyle)
	dlg.AddItem(comboStyle)
	dlg.AddItem(chkSystemMonospace)
	dlg.AddItem(lblFont)
	dlg.AddItem(comboFont)
	dlg.AddItem(lblSize)
	dlg.AddItem(editSize)
	dlg.AddItem(lblTitle)
	dlg.AddItem(editTitle)
	dlg.AddItem(chkFullPathTitle)
	dlg.AddItem(lblWorkspaceTabs)
	dlg.AddItem(comboWorkspaceTabs)
	dlg.AddItem(chkWorkspaceTabsOverlay)
	dlg.AddItem(lblCtrlTab)
	dlg.AddItem(comboCtrlTab)
	dlg.AddItem(chkAltNumberTabs)
	dlg.AddItem(chkRestoreWorkspaceTabs)
	dlg.AddItem(lblWorkspaceNumbering)
	dlg.AddItem(comboWorkspaceNumbering)
	dlg.AddItem(chkCursor)
	dlg.AddItem(chkContrast)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnExport)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	rowStyle := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowStyle.Add(lblStyle, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowStyle.Add(comboStyle, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowStyle, vtui.Margins{}, vtui.AlignFill)

	vbox.Add(chkSystemMonospace, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(lblFont, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(comboFont, vtui.Margins{}, vtui.AlignFill)

	rowSize := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowSize.Add(lblSize, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowSize.Add(editSize, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowSize, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(lblTitle, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editTitle, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(chkFullPathTitle, vtui.Margins{}, vtui.AlignLeft)

	rowWorkspaceTabs := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowWorkspaceTabs.Add(lblWorkspaceTabs, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowWorkspaceTabs.Add(comboWorkspaceTabs, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowWorkspaceTabs, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkWorkspaceTabsOverlay, vtui.Margins{}, vtui.AlignLeft)

	rowCtrlTab := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowCtrlTab.Add(lblCtrlTab, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowCtrlTab.Add(comboCtrlTab, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowCtrlTab, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkAltNumberTabs, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkRestoreWorkspaceTabs, vtui.Margins{}, vtui.AlignLeft)
	rowWorkspaceNumbering := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowWorkspaceNumbering.Add(lblWorkspaceNumbering, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowWorkspaceNumbering.Add(comboWorkspaceNumbering, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowWorkspaceNumbering, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(chkCursor, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkContrast, vtui.Margins{}, vtui.AlignLeft)

	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnExport, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() {
		dlg.SetExitCode(-1)
	}
	btnExport.OnClick = func() {
		colorsPath := filepath.Join(config.GetF4ConfigDir(), "farcolors.ini")
		err := theme.ExportColors(colorsPath)
		if err != nil {
			vtui.ShowMessageOn(dlg, " Error ", fmt.Sprintf("Failed to export colors:\n%v", err), []string{"&Ok"})
		} else {
			customIdx := -1
			for i, item := range comboStyle.Menu.Items {
				if strings.EqualFold(item.Text, theme.CustomColorStyleName) {
					customIdx = i
					break
				}
			}
			if customIdx < 0 {
				names = append(names, theme.CustomColorStyleName)
				comboStyle.Menu.AddItem(vtui.MenuItem{Text: theme.CustomColorStyleName})
				customIdx = len(names) - 1
			}
			comboStyle.Menu.SetSelectPos(customIdx)
			comboStyle.Menu.OnAction(customIdx)
			vtui.ShowMessageOn(dlg, " Info ", "Current colors successfully exported to:\n"+colorsPath+"\n\nYou can edit this file to customize your palette.", []string{"&Ok"})
		}
	}
	btnOk.OnClick = func() {
		if len(names) > 0 {
			config.App.ColorStyle = names[comboStyle.Menu.SelectPos]
		}
		useSystemMonospace := chkSystemMonospace.State == 1
		fontValue := gui.GuiFontValueForDisplay(config.App.Language, config.App.GuiFont, comboFont.Edit.GetText())
		fontChanged := config.App.GuiUseSystemMonospace != useSystemMonospace || config.App.GuiFont != fontValue || fmt.Sprintf("%d", config.App.GuiFontSize) != editSize.GetText()

		config.App.ConsoleTitleTemplate = editTitle.GetText()
		config.App.DisplayFullPathInTitle = chkFullPathTitle.State == 1
		config.App.GuiUseSystemMonospace = useSystemMonospace
		config.App.GuiFont = fontValue
		_, _ = fmt.Sscanf(editSize.GetText(), "%d", &config.App.GuiFontSize)
		if config.App.GuiFontSize <= 0 {
			config.App.GuiFontSize = config.DefaultGuiFontSize(runtime.GOOS)
		}
		config.App.KeepTerminalCursor = chkCursor.State == 1
		vtui.ManageCursorStyle = !config.App.KeepTerminalCursor
		config.App.EnforceColorCorrection = chkContrast.State == 1
		config.App.WorkspaceTabMode = comboWorkspaceTabs.Menu.SelectPos
		config.App.WorkspaceTabsOverlay = chkWorkspaceTabsOverlay.State == 1
		config.App.CtrlTabShowsMenu = comboCtrlTab.Menu.SelectPos == 1
		config.App.AltNumberSwitchesTabs = chkAltNumberTabs.State == 1
		config.App.RestoreWorkspaceTabs = chkRestoreWorkspaceTabs.State == 1
		config.App.WorkspaceTabNumbering = config.WorkspaceTabNumberingMode(comboWorkspaceNumbering.Menu.SelectPos)
		if config.App.WorkspaceTabNumbering == config.WorkspaceTabNumbersOrder {
			panel.RenumberWorkspaceScreens()
		}
		config.SaveConfig()

		dlg.SetExitCode(1)
		ctrlTabMode := vtui.WorkspaceCtrlTabDirect
		if config.App.CtrlTabShowsMenu {
			ctrlTabMode = vtui.WorkspaceCtrlTabMenu
		}
		vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabMode(config.App.WorkspaceTabMode), ctrlTabMode)
		vtui.FrameManager.ConfigureWorkspaceTabOverlay(config.App.WorkspaceTabsOverlay)
		vtui.FrameManager.ConfigureWorkspaceAltNumberSwitch(config.App.AltNumberSwitchesTabs)

		if fontChanged {
			vtui.FrameManager.PostTask(func() {
				vtui.ShowMessage(" Appearance ", "Font changes in GUI mode will take effect\nafter application restart.", []string{"&Ok"})
			})
		}
	}

	dlg.OnResult = func(code int) {
		if code < 0 && len(originalPalette) == len(vtui.Palette) {
			copy(vtui.Palette, originalPalette)
		}
		vtui.FrameManager.Redraw()
	}

	vtui.FrameManager.Push(dlg)
}

func actionManagePlugins(pf *panel.PanelsFrame) {
	width, height := 60, 16
	btnAdd := vtui.NewButton(0, 0, i18n.Msg("Plugins.BtnAdd"))
	btnDel := vtui.NewButton(0, 0, i18n.Msg("Plugins.BtnRemove"))
	btnPerms := vtui.NewButton(0, 0, i18n.Msg("Plugins.BtnPermissions"))
	btnClose := vtui.NewButton(0, 0, i18n.Msg("Plugins.BtnClose"))
	buttonWidth := 0
	for i, button := range []*vtui.Button{btnAdd, btnDel, btnPerms, btnClose} {
		x1, _, x2, _ := button.GetPosition()
		if i > 0 {
			buttonWidth += 2
		}
		buttonWidth += x2 - x1 + 1
	}
	if minWidth := buttonWidth + 4; minWidth > width {
		width = minWidth
	}

	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Plugins.Title"))
	dlg.ShowClose = true

	lb := vtui.NewListBox(0, 0, width-4, 10, config.App.RegisteredPlugins)

	btnPerms.OnClick = func() { plughost.ActionPluginPermissions(plughost.PluginPermissions()) }

	dlg.AddItem(lb)
	dlg.AddItem(btnAdd)
	dlg.AddItem(btnDel)
	dlg.AddItem(btnPerms)
	dlg.AddItem(btnClose)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lb, vtui.Margins{Bottom: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnAdd, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnDel, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnPerms, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnClose, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	btnAdd.OnClick = func() {
		startPath := "."
		if fsp := pf.GetActivePanel(); fsp != nil {
			if _, ok := fsp.Vfs.(*vfs.OSVFS); ok {
				startPath = fsp.Vfs.GetPath()
			}
		}
		showPluginFileDialog(dlg, startPath, func(path string) {
			if path != "" {
				config.App.RegisteredPlugins = append(config.App.RegisteredPlugins, path)
				config.SaveConfig()
				lb.Items = config.App.RegisteredPlugins
				lb.UpdateRows()
				vtui.FrameManager.Redraw()
				if plughost.GlobalPluginManager != nil {
					plughost.GlobalPluginManager.LoadExternalPlugin(path)
				}
			}
		})
	}

	btnDel.OnClick = func() {
		idx := lb.SelectPos
		if idx >= 0 && idx < len(config.App.RegisteredPlugins) {
			pluginPath := config.App.RegisteredPlugins[idx]
			confirm := vtui.ShowMessageOn(dlg, " Confirm ", "Remove plugin:\n"+vtui.TruncateMiddle(pluginPath, 40)+"?", []string{"&Remove", "Cancel"})
			confirm.OnResult = func(code int) {
				if code == 0 {
					config.App.RegisteredPlugins = append(config.App.RegisteredPlugins[:idx], config.App.RegisteredPlugins[idx+1:]...)
					config.SaveConfig()
					lb.Items = config.App.RegisteredPlugins
					lb.UpdateRows()
					vtui.ShowMessageOn(dlg, " Info ", "Plugin removed from config.\nRestart f4 to fully unload the process.", []string{"&Ok"})
				}
			}
		}
	}

	btnClose.OnClick = func() { dlg.Close() }

	lb.OnKeyDown = func(e *vtinput.InputEvent) bool {
		if !e.KeyDown {
			return false
		}
		switch e.VirtualKeyCode {
		case vtinput.VK_INSERT:
			btnAdd.OnClick()
			return true
		case vtinput.VK_DELETE, vtinput.VK_F8:
			btnDel.OnClick()
			return true
		}
		return false
	}

	vtui.FrameManager.Push(dlg)
}

func showPluginFileDialog(parent *vtui.Window, startPath string, onSelect func(string)) {
	w, h := 70, 22
	dlg := vtui.NewCenteredDialog(w, h, i18n.Msg("Plugins.AddTitle"))
	dlg.ShowClose = true

	lbl := vtui.NewLabel(0, 0, i18n.Msg("Plugins.SelectFilePrompt"), nil)
	edit := vtui.NewEdit(0, 0, w-4, startPath)
	edit.PathHintsEnabled = true
	lb := vtui.NewListBox(0, 0, w-4, h-10, nil)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lbl)
	dlg.AddItem(edit)
	dlg.AddItem(lb)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	loadDir := func(dir string) {
		dir = filepath.Clean(dir)
		entries, err := os.ReadDir(dir)
		var items []string

		parentDir := filepath.Dir(dir)
		if parentDir != dir {
			items = append(items, "..")
		}

		if err == nil {
			var dirs []string
			var files []string
			for _, e := range entries {
				name := e.Name()
				isDir := e.IsDir()
				if !isDir && (e.Type()&os.ModeSymlink != 0) {
					if stat, err := os.Stat(filepath.Join(dir, name)); err == nil {
						isDir = stat.IsDir()
					}
				}
				if isDir {
					dirs = append(dirs, string(filepath.Separator)+name)
				} else {
					files = append(files, name)
				}
			}
			items = append(items, dirs...)
			items = append(items, files...)
		}
		lb.Items = items
		lb.UpdateRows()
		edit.SetText(dir)
		vtui.FrameManager.Redraw()
	}

	if stat, err := os.Stat(startPath); err == nil && !stat.IsDir() {
		loadDir(filepath.Dir(startPath))
		edit.SetText(startPath)
	} else {
		loadDir(startPath)
	}

	lb.OnAction = func(idx int) {
		if idx < 0 || idx >= len(lb.Items) {
			return
		}
		item := lb.Items[idx]
		currDir := filepath.Dir(edit.GetText())

		if stat, err := os.Stat(edit.GetText()); err == nil && stat.IsDir() {
			currDir = edit.GetText()
		}

		if item == ".." {
			parentDir := filepath.Dir(currDir)
			if parentDir == "" {
				parentDir = "/"
			}
			loadDir(parentDir)
		} else if strings.HasPrefix(item, string(filepath.Separator)) {
			loadDir(filepath.Join(currDir, item[1:]))
		} else {
			edit.SetText(filepath.Join(currDir, item))
			btnOk.OnClick()
		}
	}

	btnOk.OnClick = func() {
		path := edit.GetText()
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			loadDir(path)
			return
		}
		dlg.Close()
		onSelect(path)
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, w-4, h-3)
	vbox.Add(lbl, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(edit, vtui.Margins{Top: 1, Bottom: 1}, vtui.AlignLeft)
	vbox.Add(lb, vtui.Margins{}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, w-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Apply()

	dlg.SetFocusedItem(edit)
	vtui.FrameManager.PushToFrameScreen(parent, dlg)
}

func actionFileAttributes(pf *panel.PanelsFrame) {
	fsp := pf.GetActivePanel()
	if fsp == nil || fsp.Vfs == nil {
		return
	}

	names := fsp.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	paths := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" && name != ".." {
			paths = append(paths, fsp.Vfs.Join(fsp.Vfs.GetPath(), name))
		}
	}
	if len(paths) == 0 {
		return
	}

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		targets := make([]dialog.AttributesTarget, 0, len(paths))
		for _, path := range paths {
			item, err := vfs.Lstat(ctx.Context, fsp.Vfs, path)
			if err != nil {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
				})
				return
			}
			targets = append(targets, dialog.AttributesTarget{Path: path, Item: item})
		}
		ctx.RunOnUI(func() {
			dialog.ShowAttributesDialogForTargets(pf.RefreshAll, fsp.Vfs, targets)
		})
	})
}

func actionEditSymlink(pf *panel.PanelsFrame) {
	fsp := pf.GetActivePanel()
	if fsp == nil || fsp.Vfs == nil {
		return
	}

	names := fsp.GetSelectedNames()
	if len(names) != 1 {
		vtui.ShowMessage(i18n.Msg("SymlinkEdit.ErrorTitle"), i18n.Msg("SymlinkEdit.OneFile"), []string{"&Ok"})
		return
	}

	v := fsp.Vfs
	path := v.Join(v.GetPath(), names[0])
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		item, err := vfs.Lstat(ctx.Context, v, path)
		if err == nil && !item.IsSymlink {
			err = fmt.Errorf("%s", i18n.Msg("SymlinkEdit.NotSymlink"))
		}
		if err == nil {
			if _, ok := v.(vfs.SymlinkVFS); !ok {
				err = fmt.Errorf("%s", i18n.Msg("SymlinkEdit.Unsupported"))
			}
		}
		if err != nil {
			ctx.RunOnUI(func() {
				vtui.ShowMessage(i18n.Msg("SymlinkEdit.ErrorTitle"), err.Error(), []string{"&Ok"})
			})
			return
		}
		target, err := vfs.Readlink(ctx.Context, v, path)
		if err != nil {
			ctx.RunOnUI(func() {
				vtui.ShowMessage(i18n.Msg("SymlinkEdit.ErrorTitle"), err.Error(), []string{"&Ok"})
			})
			return
		}
		ctx.RunOnUI(func() {
			dialog.ShowSymlinkTargetDialog(pf.RefreshAll, v, path, target)
		})
	})
}

func actionLanguage(pf *panel.PanelsFrame) {
	uiLangs := i18n.ListAvailable(userLangDir())
	helpLangs := dialog.ListAvailableHelpLanguages()

	width, height := 54, 15
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("LanguageSettings.Title"))
	dlg.ShowClose = true

	uiNames := make([]string, len(uiLangs))
	selectedUI := 0
	for i, l := range uiLangs {
		uiNames[i] = l.Name
		if l.Code == config.App.Language {
			selectedUI = i
		}
	}
	comboUI := vtui.NewComboBox(0, 0, 24, uiNames)
	comboUI.DropdownOnly = true
	comboUI.Menu.SetSelectPos(selectedUI)
	comboUI.Edit.SetText(uiNames[selectedUI])
	lblUI := vtui.NewLabel(0, 0, i18n.Msg("LanguageSettings.Primary"), comboUI)

	helpNames := make([]string, len(helpLangs))
	selectedHelp := 0
	for i, l := range helpLangs {
		helpNames[i] = l.Name
		if l.Code == config.App.HelpLanguage {
			selectedHelp = i
		}
	}
	comboHelp := vtui.NewComboBox(0, 0, 24, helpNames)
	comboHelp.DropdownOnly = true
	comboHelp.Menu.SetSelectPos(selectedHelp)
	comboHelp.Edit.SetText(helpNames[selectedHelp])
	lblHelp := vtui.NewLabel(0, 0, i18n.Msg("HelpLanguage.Title")+":", comboHelp)
	chkLocalFiles := vtui.NewCheckbox(0, 0, i18n.Msg("LanguageSettings.UseLocalFiles"), false)
	chkLocalFiles.State = boolToCheckboxState(config.App.UseLocalLanguageFiles)

	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	dlg.AddItem(lblUI)
	dlg.AddItem(comboUI)
	dlg.AddItem(lblHelp)
	dlg.AddItem(comboHelp)
	dlg.AddItem(chkLocalFiles)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)

	rowUI := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowUI.Add(lblUI, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowUI.Add(comboUI, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowUI, vtui.Margins{}, vtui.AlignFill)

	rowHelp := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowHelp.Add(lblHelp, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowHelp.Add(comboHelp, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowHelp, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(chkLocalFiles, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 2}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		uiChanged := false
		helpChanged := false
		localFilesChanged := config.App.UseLocalLanguageFiles != (chkLocalFiles.State != 0)
		suggestFontChoice := false
		if idx := comboUI.Menu.SelectPos; idx >= 0 && idx < len(uiLangs) {
			if config.App.Language != uiLangs[idx].Code {
				suggestFontChoice = gui.ShouldSuggestFontForLanguage(uiLangs[idx].Code, config.App.GuiFont)
				config.App.Language = uiLangs[idx].Code
				uiChanged = true
			}
		}
		if idx := comboHelp.Menu.SelectPos; idx >= 0 && idx < len(helpLangs) {
			if config.App.HelpLanguage != helpLangs[idx].Code {
				config.App.HelpLanguage = helpLangs[idx].Code
				helpChanged = true
			}
		}
		if localFilesChanged {
			config.App.UseLocalLanguageFiles = chkLocalFiles.State != 0
		}
		if uiChanged || helpChanged || localFilesChanged {
			config.SaveConfig()
			initLang()
			InitHelpSystem()
			vtui.FrameManager.PostTask(func() {
				if uiChanged && suggestFontChoice {
					actionAppearanceSettings(pf)
				} else if uiChanged {
					vtui.ShowMessage(i18n.Msg("Info.Title"), i18n.Msg("Language.Changed"), []string{i18n.Msg("vtui.Ok")})
				}
				vtui.FrameManager.Redraw()
			})
		}
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}
