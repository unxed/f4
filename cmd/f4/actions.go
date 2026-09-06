package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

const openingProgressDelay = 250 * time.Millisecond

// editorHeaderIsBinary: NUL in the header means binary — text in any codepage
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
	LastLeftPath              = ""
	LastRightPath             = ""
	LastLeftCursor            = ""
	LastRightCursor           = ""
	LastActivePanel           = 1
	LastWidePanel             = -1

	LastLeftViewMode  = 0
	LastRightViewMode = 0
	LastLeftSortMode  = 0
	LastRightSortMode = 0
	LastLeftSortRev   = false
	LastRightSortRev  = false

	LastShowPanels = true
	LastShowLeft   = true
	LastShowRight  = true
)

func extractNames(recs []HistoryRecord) []string {
	var res []string
	for _, r := range recs {
		res = append(res, r.Name)
	}
	return res
}

func choiceText(choices []string, selected int) string {
	for i, choice := range choices {
		if i == selected {
			return choice
		}
	}
	return ""
}

func actionFoldersHistory(pf *PanelsFrame) {
	if vtui.GlobalHistoryProvider == nil {
		return
	}
	richFolders, folderHP := loadFolderHistoryRecords(vtui.GlobalHistoryProvider)
	h := extractNames(richFolders)
	if len(richFolders) == 0 {
		vtui.ShowMessage(Msg("History.Title"), Msg("History.EmptyFolders"), []string{Msg("vtui.Ok")})
		return
	}

	menu := vtui.NewVMenu(Msg("History.FoldersTitle"))
	menu.SetHelp("HistoryFolders")

	search := newHistorySearch(menu, richFolders, Msg("History.FoldersHint"))
	search.supportsLocks = folderHP != nil
	search.showTimes = true
	search.timeMode = AppConfig.HistoryShowTimes[historyTypeFolders]
	search.onTimesChanged = func(mode int) {
		AppConfig.HistoryShowTimes[historyTypeFolders] = mode
		SaveConfig()
	}
	search.onLockToggled = func() {
		if folderHP != nil {
			richFolders = append([]HistoryRecord(nil), search.all...)
			h = extractNames(richFolders)
			saveFolderHistoryRecords(folderHP, richFolders)
		}
	}
	search.applyFilter()

	if activePanel := pf.getActivePanel(); activePanel != nil {
		currentPath := activePanel.persistentPath()
		for historyPos, path := range h {
			if sameFolderHistoryPath(path, currentPath) {
				search.selectOriginalIndex(historyPos)
				break
			}
		}
	}

	// Shared "cd on active panel" path used by bare Enter and by mouse click.
	gotoActive := func(pos int) {
		search.cleanup()
		menu.Close()
		if targetPanel := pf.getActivePanel(); targetPanel != nil {
			// The menu is oldest → newest. If the selected path disappeared,
			// navigateAvailableFolderHistory walks toward newer entries.
			pf.navigateAvailableFolderHistory(targetPanel, h, pos, -1)
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
				pf.cmdLine.InsertString(path)
				menu.Close()
				return true
			}
			if shift {
				search.cleanup()
				menu.Close()
				if targetPanel := pf.getInactivePanel(); targetPanel != nil {
					pf.navigateAvailableFolderHistory(targetPanel, h, historyPos, -1)
				}
				return true
			}
			gotoActive(historyPos)
			return true
		}

		if (e.VirtualKeyCode == vtinput.VK_DELETE || e.VirtualKeyCode == vtinput.VK_BACK) && shift {
			// Delete item
			if search.deleteSelected() {
				richFolders = append([]HistoryRecord(nil), search.all...)
				h = extractNames(richFolders)
				if folderHP != nil {
					saveFolderHistoryRecords(folderHP, richFolders)
				} else {
					vtui.GlobalHistoryProvider.SaveHistory("folders", h)
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
			if folderHP != nil {
				confirmAndClearRichHistory(Msg("History.FoldersTitle"), "folders", &richFolders, func() {
					h = extractNames(richFolders)
					folderHP.SaveHistory("folders", h)
					pf.cmdLine.Edit.HistoryPos = -1
				}, search, menu)
			} else {
				confirmAndClearHistory(Msg("History.FoldersTitle"), "folders", &h, func() {
					pf.cmdLine.Edit.HistoryPos = -1
				}, search, menu)
			}
			return true
		}

		// Ctrl+C / Ctrl+Ins: copy the selected entry to the clipboard.
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			setClipboardAsync(path)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
}

func actionCommandHistory(pf *PanelsFrame) {
	h := pf.cmdLine.Edit.History
	if len(h) == 0 {
		vtui.ShowMessage(Msg("History.Title"), Msg("History.EmptyCommands"), []string{Msg("vtui.Ok")})
		return
	}

	var richCmds []HistoryRecord
	hp, isF4 := vtui.GlobalHistoryProvider.(*F4HistoryProvider)
	if isF4 {
		richCmds = hp.LoadRichHistory("cmdline")
	} else {
		for _, c := range h {
			richCmds = append(richCmds, HistoryRecord{Name: c})
		}
	}
	if len(richCmds) == 0 && len(h) > 0 {
		richCmds = recordsFromNames(h)
	}
	// The tab/workspace branch originally stored command directories in a
	// parallel history. Fold those records into upstream's richer history
	// model so existing sessions retain their paths after the merge.
	legacyPaths := loadCommandHistoryPaths(h)
	for i := range richCmds {
		if richCmds[i].directory() == "" && i < len(legacyPaths) {
			richCmds[i].Dir = legacyPaths[i]
		}
	}

	menu := vtui.NewVMenu(Msg("History.CommandsTitle"))
	menu.SetHelp("History")
	search := newHistorySearch(menu, richCmds, Msg("History.CommandsHint"))
	search.supportsLocks = isF4
	search.showDetails = true
	search.showTimes = true
	search.timeMode = AppConfig.HistoryShowTimes[historyTypeCommands]
	search.showDirPrefix = true
	search.dirPrefixLen = AppConfig.HistoryDirsPrefixLen
	search.onTimesChanged = func(mode int) {
		AppConfig.HistoryShowTimes[historyTypeCommands] = mode
		SaveConfig()
	}
	search.onPrefixChanged = func(width int) {
		AppConfig.HistoryDirsPrefixLen = width
		SaveConfig()
	}
	search.showSecond = search.hasSecondary()
	search.secondWidth = 24
	search.applyFilter()

	search.onLockToggled = func() {
		if isF4 {
			hp.SaveRichHistory("cmdline", search.all)
		}
	}
	search.onCtrlF10 = func(rec HistoryRecord) {
		if dir := rec.directory(); dir != "" {
			search.cleanup()
			menu.Close()
			if targetPanel := pf.getActivePanel(); targetPanel != nil {
				pf.NavigateToPath(targetPanel, dir)
			}
		}
	}
	search.onDetails = func(rec HistoryRecord) {
		showCommandHistoryDetails(pf, rec, search, menu)
	}

	// Shared "paste selected command" path used by Enter and mouse click.
	// The record is passed in because VMenu.Close restores its initial
	// selection, which would otherwise make Enter paste a different row.
	pasteRecord := func(rec HistoryRecord) {
		search.cleanup()
		pf.cmdLine.Edit.SetText(rec.Name)
		pf.cmdLine.Edit.HistoryPos = -1
	}
	// VMenu.ProcessMouse calls SetExitCode after OnAction, so click closes
	// the menu automatically — pasteRecord only does the side effect.
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
					pf.insertPathToCmdLine(path)
				}
				return true
			}
			menu.Close()
			pasteRecord(rec)
			return true
		}

		if e.VirtualKeyCode == vtinput.VK_NEXT && ctrl && !shift && !alt {
			if path != "" {
				if targetPanel := pf.getActivePanel(); targetPanel != nil && pf.NavigateToPath(targetPanel, path) {
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
				h = extractNames(search.all)
				pf.cmdLine.Edit.History = h
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
			confirmAndClearRichHistory(Msg("History.CommandsTitle"), "cmdline", &richCmds, func() {
				h = extractNames(richCmds)
				pf.cmdLine.Edit.History = h
				pf.cmdLine.Edit.HistoryPos = -1
				if !isF4 && vtui.GlobalHistoryProvider != nil {
					vtui.GlobalHistoryProvider.SaveHistory("cmdline", h)
				}
			}, search, menu)
			return true
		}

		// Ctrl+C / Ctrl+Ins: copy the selected entry to the clipboard.
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			setClipboardAsync(rec.Name)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
}

func showCommandHistoryDetails(pf *PanelsFrame, rec HistoryRecord, search *historySearch, menu *vtui.VMenu) {
	dateText := "None"
	timeText := "None"
	if !rec.Timestamp.IsZero() {
		dateText = rec.Timestamp.Format("2006-01-02")
		timeText = rec.Timestamp.Format("15:04:05")
	}
	dir := rec.directory()
	message := fmt.Sprintf("Command: %s\nDirectory: %s\nDate: %s\nTime: %s", rec.Name, dir, dateText, timeText)
	buttons := []string{"&Close"}
	if dir != "" {
		buttons = append(buttons, "&ChDir", "&Run-up")
	}
	dlg := vtui.ShowMessage(Msg("History.CommandsTitle"), message, buttons)
	dlg.OnResult = func(code int) {
		if code == 0 || dir == "" {
			return
		}
		search.cleanup()
		menu.Close()
		if code == 1 {
			if panel := pf.getActivePanel(); panel != nil {
				pf.NavigateToPath(panel, dir)
			}
			return
		}
		if code == 2 {
			pf.cmdLine.Edit.SetText(rec.Name)
			pf.cmdLine.Edit.HistoryPos = -1
		}
	}
}

// confirmAndClearHistory shows a Yes/No dialog, and on Yes wipes the
// history file identified by providerName, invokes localReset (used for
// state kept outside the provider, e.g. cmdLine.Edit.History), and
// closes the history menu. Mirrors far2l's Del handler in history.cpp
// with the "confirm history clear" option always on.
func confirmAndClearHistory(title, providerName string, h *[]string, localReset func(), search *historySearch, menu *vtui.VMenu) {
	buttons := []string{Msg("vtui.Ok"), Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(title, Msg("History.ConfirmClearAll"), buttons)
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
func confirmAndClearRichHistory(title, providerName string, h *[]HistoryRecord, localReset func(), search *historySearch, menu *vtui.VMenu) {
	buttons := []string{Msg("vtui.Ok"), Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(title, Msg("History.ConfirmClearAll"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		kept := make([]HistoryRecord, 0)
		for _, r := range *h {
			if r.Lock {
				kept = append(kept, r)
			}
		}
		*h = kept
		if localReset != nil {
			localReset()
		}
		if hp, ok := vtui.GlobalHistoryProvider.(*F4HistoryProvider); ok {
			hp.SaveRichHistory(providerName, kept)
		}
		search.setItems(kept)
		if len(kept) == 0 {
			search.cleanup()
			menu.Close()
		}
	}
}
func confirmAndPruneMissingFolderHistory(h *[]string, rich *[]HistoryRecord, hp *F4HistoryProvider, search *historySearch, menu *vtui.VMenu) {
	buttons := []string{Msg("vtui.Ok"), Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(Msg("History.FoldersTitle"), Msg("History.ConfirmPruneMissing"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		kept := make([]HistoryRecord, 0, len(*rich))
		for _, record := range *rich {
			p := record.Name
			if record.Lock {
				kept = append(kept, record)
				continue
			}
			if isPersistentURIPath(p) || vfs.FindStandaloneProvider(context.Background(), nil, p) != nil {
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
		*h = extractNames(kept)
		if hp != nil {
			saveFolderHistoryRecords(hp, kept)
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

func actionSortMenu(pf *PanelsFrame) {
	actionSortMenuForPanel(pf, pf.getActivePanel())
}

func actionSortMenuForPanel(pf *PanelsFrame, fsp *FileSystemPanel) {
	if fsp == nil {
		return
	}

	entries := []struct {
		mode     SortMode
		labelKey string
		shortcut string
	}{
		{mode: SortName, labelKey: "Menu.SortName", shortcut: "Ctrl+F3"},
		{mode: SortExt, labelKey: "Menu.SortExt", shortcut: "Ctrl+F4"},
		{mode: SortTime, labelKey: "Menu.SortTime", shortcut: "Ctrl+F5"},
		{mode: SortSize, labelKey: "Menu.SortSize", shortcut: "Ctrl+F6"},
		{mode: SortUnsorted, labelKey: "Menu.SortUnsorted", shortcut: "Ctrl+F7"},
	}

	menu := vtui.NewVMenu(Msg("Sort.Title"))
	selected := 0
	for idx, entry := range entries {
		prefix := "  "
		if entry.mode == fsp.sortMode {
			prefix = "✓ "
			selected = idx
		}
		menu.AddItem(vtui.MenuItem{
			Text:     prefix + Msg(entry.labelKey),
			Shortcut: entry.shortcut,
		})
	}
	menu.SetSelectPos(selected)
	menu.OnAction = func(idx int) {
		if idx < 0 || idx >= len(entries) {
			return
		}
		fsp.SetSortMode(entries[idx].mode)
		pf.updateMenuCheckmarks()
		vtui.FrameManager.Redraw()
	}

	w, h := 36, len(entries)+2
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

func actionEditFileExternal(pf *PanelsFrame, v vfs.VFS, path string, size int64) {
	rememberViewerEditorHistory(v, path, historyModeEdit)
	cmdStr := AppConfig.ExternalEditorCommand
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
		vtui.ShowMessage(Msg("Error.Title"), fmt.Sprintf(Msg("ExtEdit.TempError"), err), []string{Msg("vtui.Ok")})
		return
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close() // Will be reopened by VFS/editor

	pf.RunProgressTask(" Downloading... ", "Preparing to download...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		src, err := v.Open(ctx, path)
		if err != nil {
			// If file does not exist, it's a new file. Just create an empty temp file.
			if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "file does not exist") {
				return nil
			}
			return err
		}
		defer src.Close()

		dst, err := os.Create(tmpPath)
		if err != nil {
			return err
		}
		closeDst := closeOnce(dst)
		defer closeDst()

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
			os.Remove(tmpPath)
			return
		}
		if err == context.Canceled {
			os.Remove(tmpPath)
			return
		}

		stBefore, err := os.Stat(tmpPath)
		if err != nil {
			os.Remove(tmpPath)
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
				defer src.Close()

				dst, err := v.Create(ctx, path)
				if err != nil {
					return err
				}
				closeDst := closeOnce(dst)
				defer closeDst()

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
				os.Remove(tmpPath)
				if err != nil && err != context.Canceled {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to upload file:\n%v", err), []string{"&Ok"})
				}
				pf.RefreshAll()
			})
		} else {
			os.Remove(tmpPath)
			pf.RefreshAll()
		}
	})
}

func runExternalEditor(pf *PanelsFrame, cmdStr, path string) {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return
	}

	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if fsp := pf.getActivePanel(); fsp != nil {
		if _, isLocal := fsp.vfs.(*vfs.OSVFS); isLocal {
			cmd.Dir = fsp.vfs.GetPath()
		}
	}

	vtui.Suspend()
	err := cmd.Run()
	vtui.Resume()

	if err != nil {
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Editor exited with error:\n%v", err), []string{"&Ok"})
		})
	}
	vtui.FrameManager.PostTask(func() {
		pf.RefreshAll()
	})
}

func showEditor(pf *PanelsFrame, v vfs.VFS, path string, f vfs.ReadAtCloser) {
	var pt *piecetable.PieceTable
	var buf *AsyncBuffer
	var mapped *MappedFile
	cpID := AppConfig.EditorDefaultCodePage
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

		cpID = vfs.DetectEncoding(header, AppConfig.EditorAutodetectCodePage, AppConfig.EditorDefaultCodePage)
		if remembered, ok := rememberedCodepage(v, path); ok {
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
			// Everything else — remote, empty, or a mapping the kernel
			// refused — keeps the lazily fetched chunk buffer.
			if AppConfig.EditorMemoryMap {
				var mapErr error
				mapped, mapErr = MapEditorFileWithOffset(v, f, dataOffset)
				if mapErr != nil && mapErr != errNotMappable {
					vtui.DebugLog("EDITOR: memory mapping %s failed, reading lazily instead: %v", path, mapErr)
				}
			}
			if mapped != nil {
				pt = piecetable.New(mapped.Bytes())
			} else {
				buf = NewAsyncBufferWithOffset(context.Background(), f, dataOffset)
				buf.prewarm()
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
	// else — an empty buffer, or a file decoded into memory — has its index
	// built with it, as it always has.
	var editor *EditorView
	if mapped != nil || buf != nil {
		editor = NewEditorViewIndexedLater(pt, v, path)
	} else {
		editor = NewEditorView(pt, v, path)
	}
	editor.Codepage = cpID
	editor.binaryFile = binary
	editor.utf8BOM = cpID == 65001 && dataOffset != 0
	// The decode view's processor mode comes off the header read above,
	// like the codepage: the buffer behind a lazily loaded file may not
	// have its first bytes when the mode is first wanted.
	editor.DisasmMode = detectX86Mode(header)
	// StartIndexing skips hex, so binary files open without a line scan.
	if _, isDisks := v.(*vfs.DisksVFS); isDisks || binary {
		editor.HexMode = true
	}
	// A saved position is a line number, meaningless for a hex view.
	if GlobalFileState != nil && path != "" && !binary {
		if state := GlobalFileState.GetState(FileStateKey(v, path)); state != nil {
			editor.WordWrap = state.EditorWrap
			editor.targetLine = state.EditorLine
			editor.targetPos = state.EditorPos
			editor.targetTopRow = state.EditorTopRow
			editor.targetLeft = state.EditorLeft
		}
	}
	editor.file = f
	editor.asyncBuf = buf
	editor.mapped = mapped
	editor.ResizeConsole(pf.lastW, pf.lastH)
	editor.StartIndexing()

	vtui.FrameManager.AddScreen(editor)
}

func findOpenedEditor(v vfs.VFS, path string) (*EditorView, int) {
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
			if ev, ok := f.(*EditorView); ok && !ev.IsDone() {
				if isLocal && ev.vfs != nil {
					if evOSVFS, evOk := ev.vfs.(*vfs.OSVFS); evOk {
						evAbsPath, _ := evOSVFS.Abs(ev.filePath)
						if evAbsPath == absPath {
							return ev, i
						}
					}
				} else {
					if ev.filePath == path {
						return ev, i
					}
				}
			}
		}
	}
	return nil, -1
}

func actionOpenEditor(pf *PanelsFrame, v vfs.VFS, path string) {
	rememberViewerEditorHistory(v, path, historyModeEdit)
	existingEditor, screenIdx := findOpenedEditor(v, path)
	if existingEditor != nil {
		var buttons []string
		if existingEditor.modified {
			buttons = []string{Msg("FileOp.BtnCurrent"), Msg("FileOp.BtnNewInstance"), Msg("vtui.Cancel")}
		} else {
			buttons = []string{Msg("FileOp.BtnCurrent"), Msg("FileOp.BtnReload"), Msg("FileOp.BtnNewInstance"), Msg("vtui.Cancel")}
		}

		vtui.FrameManager.PostTask(func() {
			// This is a choice dialog ("switch / reload / new instance / cancel"),
			// not a warning — render on the neutral dialog palette. See #379.
			dlg := vtui.ShowMessageEx(Msg("FileOp.AlreadyOpenedTitle"), fmt.Sprintf(Msg("FileOp.AlreadyOpened"), vtui.TruncateMiddle(v.Base(path), 40)), buttons, vtui.MessageInfo)
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

func openEditorInternal(pf *PanelsFrame, v vfs.VFS, path string) {
	if AppConfig.EditorHighlighter == "Colorer" && !SchemasExist() {
		// Read on the goroutine that starts this work, not inside it: the
		// work outlives the call, and reading the global from it races
		// anything that reassigns vtui.FrameManager meanwhile.
		uiFrames := vtui.FrameManager
		go func() {
			msg := "Colorer syntax highlighting schemas are missing.\nWould you like to download them from elfmz/far2l GitHub?"
			if pf.Message(" Download Colorer Schemas ", msg, []string{"&Yes", "&No"}) == 0 {
				DownloadColorerSchemas(pf, func(success bool) {
					uiFrames.PostTask(func() {
						if !success {
							AppConfig.EditorHighlighter = "Chroma"
							SaveConfig()
						}
						openEditorInternal(pf, v, path)
					})
				})
			} else {
				uiFrames.PostTask(func() {
					AppConfig.EditorHighlighter = "Chroma"
					openEditorInternal(pf, v, path)
				})
			}
		}()
		return
	}
	if isLocalOSVFS(v) {
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
	pf.runProgressTaskAfter(openingProgressDelay, " Opening... ", "Preparing to edit file...", false, func(ctx context.Context, update func(msg string, percent int)) error {
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

func findOpenedViewer(v vfs.VFS, path string) (*ViewerView, int) {
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
			if vv, ok := f.(*ViewerView); ok && !vv.IsDone() {
				if isLocal && vv.vfs != nil {
					if vvOSVFS, evOk := vv.vfs.(*vfs.OSVFS); evOk {
						vvAbsPath, _ := vvOSVFS.Abs(vv.path)
						if vvAbsPath == absPath {
							return vv, i
						}
					}
				} else {
					if vv.path == path {
						return vv, i
					}
				}
			}
		}
	}
	return nil, -1
}

func showViewer(pf *PanelsFrame, viewer *ViewerView, path string) {
	if GlobalFileState != nil && path != "" {
		if state := GlobalFileState.GetState(FileStateKey(viewer.vfs, path)); state != nil {
			viewer.TopOffset = state.ViewerOffset
			if viewer.TopOffset > viewer.backend.Size() {
				viewer.TopOffset = viewer.backend.Size() - 1
			}
			if viewer.TopOffset < 0 {
				viewer.TopOffset = 0
			}
			viewer.WrapMode = state.ViewerWrap
			// The saved flag is what the user left the file in last time,
			// so it outranks the binary check the same way an F4 does.
			viewer.HexMode = state.ViewerHex
			viewer.hexAuto = false
		}
	}
	viewer.ResizeConsole(pf.lastW, pf.lastH)
	vtui.FrameManager.AddScreen(viewer)
}

func actionOpenViewer(pf *PanelsFrame, v vfs.VFS, path string) {
	rememberViewerEditorHistory(v, path, historyModeView)
	existingViewer, screenIdx := findOpenedViewer(v, path)
	if existingViewer != nil {
		vtui.FrameManager.PostTask(func() {
			// Same as actionOpenEditor above — this is a choice
			// dialog, render on the neutral dialog palette. See #379.
			dlg := vtui.ShowMessageEx(Msg("FileOp.AlreadyViewedTitle"), fmt.Sprintf(Msg("FileOp.AlreadyViewed"), vtui.TruncateMiddle(v.Base(path), 40)), []string{Msg("FileOp.BtnCurrent"), Msg("FileOp.BtnReload"), Msg("FileOp.BtnNewInstance"), Msg("vtui.Cancel")}, vtui.MessageInfo)
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
func actionSwitchEditorToViewer(ev *EditorView) {
	if ev == nil || ev.filePath == "" || ev.vfs == nil {
		return
	}

	doSwitch := func() {
		targetOffset := int64(0)
		if ev.HexMode || ev.DecodeMode {
			targetOffset = int64(ev.HexTopOffset)
		} else if ev.li != nil && ev.CursorLine >= 0 {
			// The index owns the answer to "where is line N", and on a file
			// that is still being scanned it may not have reached the cursor
			// yet — which used to open the viewer at the top of the file
			// instead of where the editor was.
			ev.ensureIndexedToLine(ev.CursorLine)
			if ev.CursorLine < ev.li.LineCount() {
				targetOffset = int64(ev.li.GetLineOffset(ev.CursorLine))
			}
		}

		ctx := context.Background()
		viewer, err := NewViewerView(ctx, ev.vfs, ev.filePath)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file in viewer:\n%v", err), []string{"&Ok"})
			return
		}

		// NewViewerView normally follows the saved per-file override, but the
		// editor can have a just-selected or conversion codepage that is not in
		// file_states yet. Rebuild the viewer backend so the displayed bytes and
		// the Codepage label cannot diverge.
		viewer.ReloadWithCodepage(ev.Codepage)
		viewer.HexMode = ev.HexMode
		viewer.DecodeMode = ev.DecodeMode
		// A decided mode travels with the switch, whether the header or
		// the user decided it; an undecided one must not undo the
		// viewer's own detection.
		if ev.DisasmMode != 0 {
			viewer.DisasmMode = ev.DisasmMode
		}
		viewer.WrapMode = ev.WordWrap
		if viewer.HexMode {
			viewer.TopOffset = targetOffset &^ 0xF
		} else {
			viewer.TopOffset = targetOffset
		}

		w := vtui.FrameManager.GetScreenSize()
		h := vtui.FrameManager.GetScreenHeight()
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 25
		}
		viewer.ResizeConsole(w, h)

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
			// here, since switching to the viewer normally happens from the
			// editor screen currently on-screen). Writing straight into
			// Screens[screenIdx].Frames wouldn't be picked up by GetTopFrame(),
			// which reads the live fm.frames slice. Go through
			// RemoveFrame()/Push() instead for the active-screen case, since
			// those mutate fm.frames directly; keep the direct Screens[] +
			// SwitchScreen() combo for background screens, where
			// SwitchScreen() does perform the swap.
			if screenIdx == vtui.FrameManager.ActiveIdx {
				vtui.FrameManager.RemoveFrame(ev)
				vtui.FrameManager.Push(viewer)
			} else {
				vtui.FrameManager.Screens[screenIdx].Frames = []vtui.Frame{viewer}
				vtui.FrameManager.SwitchScreen(screenIdx)
			}
		} else if vtui.FrameManager != nil {
			vtui.FrameManager.AddScreen(viewer)
		}
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
		rememberViewerEditorHistory(ev.vfs, ev.filePath, historyModeView)
	}

	if ev.modified {
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

func actionSwitchViewerToEditor(vv *ViewerView) {
	if vv == nil || vv.path == "" || vv.vfs == nil {
		return
	}

	if stat, err := vv.vfs.Stat(context.Background(), vv.path); err == nil && stat.IsDir {
		vtui.ShowMessage(" Error ", "Cannot edit a directory.", []string{"&Ok"})
		return
	}

	ctx := context.Background()
	f, err := vv.vfs.Open(ctx, vv.path)
	if err != nil {
		if err == os.ErrInvalid {
			vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets).", []string{"&Ok"})
		} else {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file for editing:\n%v", err), []string{"&Ok"})
		}
		return
	}

	var pt *piecetable.PieceTable
	var buf *AsyncBuffer
	var mapped *MappedFile
	cpID := vv.Codepage
	dataOffset := int64(0)
	if vv.backend != nil {
		dataOffset = vv.backend.dataOffset
	}

	if cpID == 65001 {
		if AppConfig.EditorMemoryMap {
			var mapErr error
			mapped, mapErr = MapEditorFileWithOffset(vv.vfs, f, dataOffset)
			if mapErr != nil && mapErr != errNotMappable {
				vtui.DebugLog("EDITOR: memory mapping %s failed, reading lazily instead: %v", vv.path, mapErr)
			}
		}
		if mapped != nil {
			pt = piecetable.New(mapped.Bytes())
		} else {
			buf = NewAsyncBufferWithOffset(ctx, f, dataOffset)
			buf.prewarm()
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
	// indexed on the way in, or switching to the editor pays the whole file's
	// scan on the UI thread before it appears — twenty seconds of it on the
	// 8 GB test file.
	var editor *EditorView
	if mapped != nil || buf != nil {
		editor = NewEditorViewIndexedLater(pt, vv.vfs, vv.path)
	} else {
		editor = NewEditorView(pt, vv.vfs, vv.path)
	}
	editor.file = f
	editor.asyncBuf = buf
	editor.mapped = mapped
	editor.Codepage = cpID
	editor.binaryFile = vv.HexMode
	editor.utf8BOM = cpID == 65001 && vv.backend != nil && vv.backend.dataOffset != 0
	editor.WordWrap = vv.WrapMode
	editor.HexMode = vv.HexMode
	editor.DecodeMode = vv.DecodeMode
	editor.DisasmMode = vv.DisasmMode

	targetOff := int(vv.TopOffset)
	if editor.HexMode || editor.DecodeMode {
		editor.HexTopOffset = targetOff &^ 0xF
		editor.CursorLine = editor.li.GetLineAtOffset(targetOff)
		editor.CursorPos = targetOff - editor.li.GetLineOffset(editor.CursorLine)
	} else {
		line, pos := 0, 0
		if !editor.awaitOffset(targetOff) {
			line = editor.CursorLine
			pos = editor.CursorPos
		} else {
			// The file has not been read that far — a chunk of a lazily
			// loaded one is still on its way — so the offset has no line yet.
			// The editor opens at the top and the scan puts the cursor where
			// the viewer was when it reads past it, rather than guessing now.
			vtui.DebugLog("EDITOR: viewer offset %d is past the index; the scan will place it",
				targetOff)
		}
		editor.CursorLine = line
		editor.CursorPos = pos
		editor.targetLine = line
		editor.targetPos = pos
		editor.targetTopRow = editor.engine.GetRowOffset(line)
		editor.targetLeft = 0
		editor.ScrollTopRow = editor.targetTopRow
	}
	editor.StartIndexing()

	w := vtui.FrameManager.GetScreenSize()
	h := vtui.FrameManager.GetScreenHeight()
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 25
	}
	editor.ResizeConsole(w, h)

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
			vtui.FrameManager.Push(editor)
		} else {
			vtui.FrameManager.Screens[screenIdx].Frames = []vtui.Frame{editor}
			vtui.FrameManager.SwitchScreen(screenIdx)
		}
	} else if vtui.FrameManager != nil {
		vtui.FrameManager.AddScreen(editor)
	}
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
	rememberViewerEditorHistory(vv.vfs, vv.path, historyModeEdit)
}

// tryOpenImageViewer opens the picture viewer when the file looks like an
// image and the backend can actually show one. It returns false to let the
// ordinary viewer handle the file.
// tryOpenVideoPlayer offers to play a video. It comes before the picture
// viewer because a file is one or the other, and before the text viewer
// because a hex dump of an mp4 is not what anybody asked for.
func tryOpenVideoPlayer(pf *PanelsFrame, v vfs.VFS, path string) bool {
	if pf == nil || !IsVideoFile(path) {
		return false
	}
	// Video is a local business: the frames of it never fit down a
	// terminal, and the player draws into a window of f4's own on the
	// screen the terminal is on.
	if sharedTTYXSession() == nil {
		return false
	}
	if !toolMPV.Available() {
		vtui.ShowMessage(" Video ", toolMPV.MissingMessage(), []string{"&Ok"})
		return true
	}

	vv, err := NewVideoView(v, path)
	if err != nil {
		vtui.DebugLog("VIDEO: %v", err)
		return false
	}
	vv.ResizeConsole(pf.lastW, pf.lastH)
	vtui.FrameManager.AddScreen(vv)
	return true
}

func tryOpenImageViewer(pf *PanelsFrame, v vfs.VFS, path string) bool {
	if pf == nil || !IsImageFile(path) {
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
	fsp := pf.getActivePanel()
	picked := make(map[string]bool)
	if fsp != nil && v != nil {
		for _, sibling := range siblings {
			if fsp.IsNameSelected(v.Base(sibling)) {
				picked[sibling] = true
			}
		}
	}

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		iv, err := NewImageView(ctx.Context, v, path)
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
			iv.ResizeConsole(pf.lastW, pf.lastH)
			vtui.FrameManager.AddScreen(iv)
		})
	})
	return true
}

// imageSiblingPaths lists the pictures next to this one, in the order the
// active panel shows them. A panel looking somewhere else has nothing to say
// about this file, and then the viewer simply shows one picture.
func imageSiblingPaths(pf *PanelsFrame, v vfs.VFS, path string) ([]string, int) {
	if pf == nil || v == nil {
		return nil, -1
	}
	fsp := pf.getActivePanel()
	if fsp == nil || fsp.vfs == nil {
		return nil, -1
	}
	dir := v.Dir(path)
	if fsp.vfs.GetPath() != dir {
		return nil, -1
	}

	names, index := fsp.ImageSiblings()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, v.Join(dir, name))
	}
	return paths, index
}

func openViewerInternal(pf *PanelsFrame, v vfs.VFS, path string) {
	if tryOpenVideoPlayer(pf, v, path) {
		return
	}
	if tryOpenImageViewer(pf, v, path) {
		return
	}
	if isLocalOSVFS(v) {
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			if v != nil {
				if stat, err := v.Stat(ctx.Context, path); err == nil && stat.IsDir {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", "Cannot view a directory.", []string{"&Ok"})
					})
					return
				}
			}

			viewer, err := NewViewerView(ctx.Context, v, path)
			ctx.RunOnUI(func() {
				if err == nil {
					showViewer(pf, viewer, path)
				} else {
					vtui.DebugLog("PANELS: Failed to open viewer for %s: %v", path, err)
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

	var viewer *ViewerView
	pf.runProgressTaskAfter(openingProgressDelay, " Opening... ", "Preparing to open file...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		update("Opening file...", -1)
		ctx = context.WithValue(ctx, vfs.ProgressKey, vfs.ProgressCallback(update))
		var err error
		viewer, err = NewViewerView(ctx, v, path)
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
		showViewer(pf, viewer, path)
	})
}

func actionViewerSearch(vv *ViewerView) {
	actionViewerSearchDirection(vv, false)
}

func actionViewerSearchDirection(vv *ViewerView, reverse bool) {
	dlgW, dlgH := 66, 15
	dlg := vtui.NewCenteredDialog(dlgW, dlgH, Msg("Viewer.SearchTitle"))
	dlg.ShowClose = true

	lblPrompt := vtui.NewLabel(0, 0, Msg("Search.Prompt"), nil)
	editPattern := vtui.NewEdit(0, 0, 40, vv.lastSearch)
	attachHistoryUseLast(editPattern, searchTextHistoryID)
	editPattern.SelectAll()
	lblPrompt.FocusLink = editPattern
	dlg.SetFocusedItem(editPattern)

	chkCase := vtui.NewCheckbox(0, 0, Msg("Search.CaseSensitive"), false)
	if vv.lastSearchCase {
		chkCase.State = 1
	}
	chkWholeWord := vtui.NewCheckbox(0, 0, Msg("Search.WholeWords"), false)
	if vv.lastSearchWholeWord {
		chkWholeWord.State = 1
	}
	chkReverse := vtui.NewCheckbox(0, 0, Msg("Search.Reverse"), false)
	if vv.lastSearchReverse || reverse {
		chkReverse.State = 1
	}
	chkRegexp := vtui.NewCheckbox(0, 0, Msg("Search.Regex"), false)
	if vv.lastSearchRegexp {
		chkRegexp.State = 1
	}

	btnFind := vtui.NewButton(0, 0, Msg("Search.BtnFind"))
	btnFind.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		optionsChanged := pattern != vv.lastSearch ||
			caseSensitive != vv.lastSearchCase ||
			searchReverse != vv.lastSearchReverse ||
			useRegexp != vv.lastSearchRegexp ||
			wholeWord != vv.lastSearchWholeWord
		if optionsChanged {
			vv.lastSearchFound = false
		}
		commitHistory(editPattern, pattern)
		vv.lastSearch = pattern
		vv.lastSearchCase = caseSensitive
		vv.lastSearchReverse = searchReverse
		vv.lastSearchRegexp = useRegexp
		vv.lastSearchWholeWord = wholeWord
		dlg.Close()
		runViewerSearch(vv, pattern, searchReverse)
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}

func actionViewerSearchAgain(vv *ViewerView, reverse bool) {
	if vv.lastSearch == "" {
		actionViewerSearchDirection(vv, reverse)
		return
	}
	runViewerSearch(vv, vv.lastSearch, reverse)
}

func runViewerSearch(vv *ViewerView, pattern string, reverse bool) {
	vtui.FrameManager.PostTask(func() {
		runSearchWithProgress(pattern, func(ctx *vtui.TaskContext, dlg *vtui.Window) {
			start := vv.TopOffset + 1
			if reverse {
				start = vv.TopOffset
			}
			if vv.lastSearchFound && vv.TopOffset == vv.lastSearchTopOffset {
				start = vv.lastSearchOffset + 1
				if reverse {
					start = vv.lastSearchOffset
				}
			}
			foundOffset, matchLen, searchErr := viewerSearchMatch(ctx.Context, vv.backend, pattern, start, viewerSearchOptions{
				caseSensitive: vv.lastSearchCase,
				reverse:       reverse,
				regexp:        vv.lastSearchRegexp,
				wholeWord:     vv.lastSearchWholeWord,
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
					if vv.lastSearchRegexp {
						vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid regular expression:\n%v", searchErr), []string{"&Ok"})
					} else {
						vtui.ShowMessage(" Error ", "Failed to read file buffer.", []string{"&Ok"})
					}
					return
				}
				if foundOffset != -1 {
					vv.TopOffset = vv.backend.FindLineStart(foundOffset)
					vv.lastSearchOffset = foundOffset
					vv.lastSearchTopOffset = vv.TopOffset
					vv.lastSearchMatchLen = int64(matchLen)
					vv.lastSearchFound = true
					vtui.FrameManager.Redraw()
				} else {
					vtui.ShowMessage(" Search ", "Pattern not found.", []string{"&Ok"})
				}
			})
		})
	})
}

func viewerSearchOffset(ctx context.Context, backend *ViewerBackend, pattern string, start int64, reverse bool, progress func(int)) int64 {
	offset, _, _ := viewerSearchMatch(ctx, backend, pattern, start, viewerSearchOptions{reverse: reverse}, progress)
	return offset
}

type viewerSearchOptions struct {
	caseSensitive bool
	reverse       bool
	regexp        bool
	wholeWord     bool
}

// viewerSearchMatch returns the byte offset and byte length of the next match.
// Literal searches retain the viewer's streaming behavior; regex and
// whole-word searches use one snapshot so matches crossing read boundaries
// have the same semantics as editor search.
func viewerSearchMatch(ctx context.Context, backend *ViewerBackend, pattern string, start int64, options viewerSearchOptions, progress func(int)) (int64, int, error) {
	if backend == nil || pattern == "" {
		return -1, 0, nil
	}
	fileSize := backend.Size()
	if fileSize <= 0 {
		return -1, 0, nil
	}
	if start < 0 {
		start = 0
	}
	if start > fileSize {
		start = fileSize
	}

	if options.regexp || options.wholeWord {
		data, err := readViewerSearchData(ctx, backend, progress)
		if err != nil {
			return -1, 0, err
		}
		if int64(len(data)) < start {
			start = int64(len(data))
		}
		found, matchLen, err := findMatch(data, pattern, options.caseSensitive, options.reverse, options.regexp, options.wholeWord, false, int(start))
		return int64(found), matchLen, err
	}

	patternLower := strings.ToLower(pattern)
	chunkSize := int64(256 * 1024)
	if int64(len(patternLower)) > chunkSize {
		chunkSize = int64(len(patternLower))
	}
	overlap := int64(len(patternLower) - 1)

	if options.reverse {
		if !options.caseSensitive {
			if at, searched := backend.SearchBefore(ctx, pattern, start); searched {
				return at, len(pattern), nil
			}
		}
		end := start
		for end > 0 {
			if ctx.Err() != nil {
				return -1, 0, ctx.Err()
			}
			begin := end - chunkSize
			if begin < 0 {
				begin = 0
			}
			if progress != nil {
				progress(int(((fileSize - end) * 100) / fileSize))
			}
			data, err := backend.ReadAt(begin, int(end-begin))
			if err == piecetable.ErrLoading {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err != nil || len(data) == 0 {
				return -1, 0, nil
			}
			if idx, matchLen, matchErr := findMatch(data, pattern, options.caseSensitive, true, false, false, false, len(data)); matchErr != nil {
				return -1, 0, matchErr
			} else if idx >= 0 {
				return begin + int64(idx), matchLen, nil
			}
			if begin == 0 {
				break
			}
			end = begin + overlap
		}
		return -1, 0, nil
	}

	if !options.caseSensitive {
		if at, searched := backend.SearchFrom(ctx, pattern, start); searched {
			return at, len(pattern), nil
		}
	}
	current := start
	for current < fileSize {
		if ctx.Err() != nil {
			return -1, 0, ctx.Err()
		}
		if progress != nil {
			progress(int((current * 100) / fileSize))
		}
		data, err := backend.ReadAt(current, int(chunkSize))
		if err == piecetable.ErrLoading {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil || len(data) == 0 {
			break
		}
		if idx, matchLen, matchErr := findMatch(data, pattern, options.caseSensitive, false, false, false, false, 0); matchErr != nil {
			return -1, 0, matchErr
		} else if idx >= 0 {
			return current + int64(idx), matchLen, nil
		}
		advance := int64(len(data)) - overlap
		if advance < 1 {
			advance = 1
		}
		current += advance
	}
	return -1, 0, nil
}

// readViewerSearchData materializes the decoded viewer stream for searches
// whose match rules can span arbitrary read boundaries. ViewerBackend still
// fetches it in bounded windows, so remote VFSes remain cancelable and do not
// allocate one request per file chunk.
func readViewerSearchData(ctx context.Context, backend *ViewerBackend, progress func(int)) ([]byte, error) {
	fileSize := backend.Size()
	capacity := 0
	if fileSize <= int64(int(^uint(0)>>1)) {
		capacity = int(fileSize)
	}
	data := make([]byte, 0, capacity)
	const chunkSize = int64(256 * 1024)
	for current := int64(0); current < fileSize; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		length := chunkSize
		if remaining := fileSize - current; remaining < length {
			length = remaining
		}
		chunk, err := backend.ReadAt(current, int(length))
		if err == piecetable.ErrLoading {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(chunk) == 0 {
			break
		}
		data = append(data, chunk...)
		current += int64(len(chunk))
		if progress != nil {
			progress(int((current * 100) / fileSize))
		}
	}
	return data, nil
}

// openPlayerPanel is the player when it is open on the passive side, which
// is the only side it can be on while a file panel is active.
func openPlayerPanel(pf *PanelsFrame) *PlayerPanel {
	if pf == nil || !pf.showPanels || pf.activeIdx < 0 || pf.activeIdx > 1 {
		return nil
	}
	player, _ := pf.altPanels[1-pf.activeIdx].(*PlayerPanel)
	return player
}

// tryPlayInPlayerPanel is Enter on a recording while the player panel is
// open: the file plays there at once, the file panel keeps the cursor, and
// the rest of the panel's audio files become the queue. Without the player
// open, Enter keeps its usual meaning — associations, then the system
// opener — so the rule costs nobody anything they did not ask for.
func tryPlayInPlayerPanel(pf *PanelsFrame, v vfs.VFS, path string) bool {
	player := openPlayerPanel(pf)
	if player == nil || !IsAudioFile(path) {
		return false
	}
	osv, isLocal := v.(*vfs.OSVFS)
	if !isLocal {
		vtui.ShowMessage(Msg("Player.Title"), Msg("Player.LocalOnly"), []string{Msg("vtui.Ok")})
		return true
	}
	fsp := pf.getActivePanel()
	if fsp == nil {
		return false
	}
	dir := v.Dir(path)
	names, index := fsp.AudioSiblings()
	if index < 0 || fsp.vfs.GetPath() != dir {
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

func actionExecute(pf *PanelsFrame, v vfs.VFS, dir, name, path string) {
	// User-defined file associations for Enter (mirrors far2l F9 →
	// Commands → File associations). A matching association intercepts
	// before the runnable / xdg-open fallback; no match → default flow.
	if tryFileAssociation(pf, AssocExecute) {
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
				pf.addCommandHistory(historyCmd)
				pf.cmdLine.Edit.HistoryPos = -1

				useDir := isOS || isPty
				actualDir := ""
				if useDir {
					actualDir = dir
				}

				if pf.shellMode == ShellModeSimpleInline {
					pf.runSimpleInlineCommand(actualDir, historyCmd)
					return
				}
				if pf.shellMode == ShellModeSimpleCaptured {
					pf.runSimpleCapturedCommand(actualDir, historyCmd)
					return
				}

				activePty := pf.getActivePTY()
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
						// Используем OSC 133 для уведомления терминала о начале и конце выполнения.
						if actualDir != "" {
							sqDir := strings.ReplaceAll(actualDir, "'", "'\\''")
							cmdToWire = fmt.Sprintf(" set +H; cd '%s' && { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; ./'%s' ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqDir, sqCmd)
						} else {
							cmdToWire = fmt.Sprintf(" set +H; { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; ./'%s' ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqCmd)
						}
					}
					vtui.DebugLog("ACTIONS: Sending to PTY: %q", cmdToWire)

					cleanCmd := "./" + cmd
					if isWindowsShell {
						cleanCmd = cmd
					}
					if !isWindowsShell {
						pf.termView.PrintCleanCommand(cleanCmd)
					}

					// Only the Unix template above wraps the command in an
					// OSC 133 C/D pair; cmd.exe reports completion through
					// the prompt marker instead.
					if isWindowsShell {
						pf.beginPromptDrivenExecution()
					} else {
						pf.beginManagedExecution()
					}
					pf.returnToPanels = true

					if !isWindowsShell {
						pf.termView.SetMuted(true)
					}
					pf.writePTY(activePty, []byte(cmdToWire))
					if isWindowsShell {
						if isBatchCommand(historyCmd) {
							pf.cmdSession.noteBatchExecution()
						}
						pf.noteLocalShellLineSent(activePty)
					}
					pf.showPanels = false
				}
			})
		} else {
			if _, isLocal := v.(*vfs.OSVFS); !isLocal {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(" Error ", "Cannot execute non-runnable files on a remote file system.", []string{"&Ok"})
				})
				return
			}
			command, args, ok := associatedFileCommand(path)
			if ok {
				workingDir := ""
				if _, isLocal := v.(*vfs.OSVFS); isLocal {
					workingDir = dir
				}
				vtui.DebugLog("ACTIONS: Executing external command: %s %q", command, args)
				err := pf.runExternalUICommand(command, args, workingDir)
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

func actionNewFile(pf *PanelsFrame) {
	if fsp := pf.getActivePanel(); fsp != nil {
		dir := fsp.vfs.GetPath()
		if dispatchPanelAction(pf, vfs.PanelActionCreate, []string{dir}) {
			return
		}
		activeVfs := fsp.vfs
		var nameEdit *vtui.Edit
		dlg := vtui.InputBox(Msg("Edit.NewFileTitle"), Msg("Edit.NewFilePrompt"), "", func(name string) {
			// Record what was actually typed, before the fallback below
			// turns an empty prompt into a placeholder name.
			commitHistory(nameEdit, name)
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
			if AppConfig.UseExternalEditor {
				actionEditFileExternal(pf, activeVfs, path, 0)
				return
			}
			actionOpenEditor(pf, activeVfs, path)
		})
		inputBoxEdit(dlg).PathHintsEnabled = true
		// Plain DIF_HISTORY, as in far2l's dlgOpenEditor: the prompt opens
		// empty rather than on the last file that was created this way.
		nameEdit = attachHistory(inputBoxEdit(dlg), newEditHistoryID)
	}
}

func actionViewTerminalLog(pf *PanelsFrame) {
	v := &TerminalLogVFS{tv: pf.termView, fallback: pf.hostConsoleLogFallback()}
	actionOpenViewer(pf, v, "Terminal Log")
}

func actionEditTerminalLog(pf *PanelsFrame) {
	v := &TerminalLogVFS{tv: pf.termView, fallback: pf.hostConsoleLogFallback()}
	actionOpenEditor(pf, v, "Terminal Log")
}

// hostConsoleLogFallback selects the data source for F3/F4 (Terminal.ViewLog/
// EditLog). pf.termView only ever sees bytes that came through a real PTY;
// under ShellModeSimpleInline (issue #513 / WINE.md -- Wine has no usable
// ConPTY) commands run with inherited stdio straight into the host console
// buffer, never touching termView, so its log is permanently empty there.
// Read the host console buffer itself instead. Every other shell mode keeps
// using termView exactly as before (nil here).
func (pf *PanelsFrame) hostConsoleLogFallback() func() []byte {
	if pf.shellMode != ShellModeSimpleInline || !consoleOverlayUsesWinAPI() {
		return nil
	}
	lines := pf.overlayLines()
	return func() []byte { return readHostConsoleFullText(lines) }
}

func actionViewFile(pf *PanelsFrame) {
	if fsp := pf.getActivePanel(); fsp != nil {
		idx := fsp.GetCursorIndex()
		if idx < 0 || idx >= len(fsp.entries) {
			return
		}
		if fsp.entries[idx].IsDir {
			actionCalcDirSize(pf, fsp, idx)
			return
		}
		// A matching View association intercepts before the built-in
		// viewer, so users can wire F3 to feh, less, or anything else.
		if tryFileAssociation(pf, AssocView) {
			return
		}
		name := fsp.GetSelectedName()
		path := fsp.vfs.Join(fsp.vfs.GetPath(), name)
		actionOpenViewer(pf, fsp.vfs, path)
	}
}

func actionCalcDirSize(pf *PanelsFrame, fsp *FileSystemPanel, idx int) {
	entry := fsp.entries[idx]
	name := entry.Name
	// ".." is a panel navigation row, not a directory owned by this VFS.
	// Trying to scan it from an archive crosses the virtual root and produces
	// misleading path-escape errors (issue #510).
	if name == ".." {
		return
	}
	basePath := fsp.vfs.GetPath()

	var targetPath = fsp.vfs.Join(basePath, name)

	opDlg := NewFileOpProgressDialog(" Calculating Size... ")
	var taskCtx *vtui.TaskContext
	opDlg.btnCancel.OnClick = func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	}

	vtui.FrameManager.PostTask(func() {
		vtui.FrameManager.AddScreenHeadless(opDlg)
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		var totalStats vfs.OpStats
		lastScanUpdate := time.Now()
		totalStats, scanErr := vfs.CalculateStats(ctx.Context, fsp.vfs, targetPath, []string{""}, func(currentPath string, stats vfs.OpStats) {
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
				if fsp.sortMode == SortSize {
					fsp.sortEntries()
					// Keep cursor on the same item after re-sorting
					for i, e := range fsp.entries {
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

func actionEditFile(pf *PanelsFrame) {
	if fsp := pf.getActivePanel(); fsp != nil {
		if dispatchPanelAction(pf, vfs.PanelActionEdit, selectedPanelActionPaths(fsp)) {
			return
		}
		idx := fsp.GetCursorIndex()
		if idx < 0 || idx >= len(fsp.entries) {
			return
		}
		if fsp.entries[idx].IsDir {
			actionFileAttributes(pf)
			return
		}
		// A matching Edit association intercepts before the built-in
		// editor (or the external one if UseExternalEditor is on).
		if tryFileAssociation(pf, AssocEdit) {
			return
		}
		name := fsp.GetSelectedName()
		path := fsp.vfs.Join(fsp.vfs.GetPath(), name)

		if AppConfig.UseExternalEditor {
			actionEditFileExternal(pf, fsp.vfs, path, fsp.entries[idx].Size)
			return
		}

		actionOpenEditor(pf, fsp.vfs, path)
	}
}

func actionCopyMove(pf *PanelsFrame, isMove bool) {
	fspSrc := pf.getActivePanel()
	fspDst := pf.getInactivePanel()
	if fspSrc == nil || fspDst == nil {
		return
	}

	names := fspSrc.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	title := Msg("Copy.Title")
	prompt := Msg("Copy.Prompt")
	if isMove {
		title = Msg("Move.Title")
		prompt = Msg("Move.Prompt")
	}

	srcVfs, dstVfs := fspSrc.vfs, fspDst.vfs
	srcBasePath := srcVfs.GetPath()
	if player, ok := pf.altPanels[1-pf.activeIdx].(*PlayerPanel); ok {
		// The player panel is a playlist, not a place: F5 adds
		// references, F6 is refused rather than moving music around.
		if isMove {
			vtui.ShowMessage(Msg("Player.Title"), Msg("Player.MoveRefused"), []string{Msg("vtui.Ok")})
			return
		}
		osv, isLocal := srcVfs.(*vfs.OSVFS)
		if !isLocal {
			vtui.ShowMessage(Msg("Player.Title"), Msg("Player.LocalOnly"), []string{Msg("vtui.Ok")})
			return
		}
		paths := make([]string, 0, len(names))
		for _, n := range names {
			if abs, err := osv.Abs(filepath.Join(srcBasePath, n)); err == nil {
				paths = append(paths, abs)
			}
		}
		if player.AddPaths(paths) == 0 {
			vtui.ShowMessage(Msg("Player.Title"), Msg("Player.NothingAdded"), []string{Msg("vtui.Ok")})
			return
		}
		fspSrc.selectedItems = make(map[string]bool)
		for _, entry := range fspSrc.entries {
			entry.Selected = false
		}
		vtui.FrameManager.Redraw()
		return
	}
	if temp, ok := dstVfs.(*TempPanelVFS); ok {
		// A temporary panel contains references, not copies. Keep F5/F6
		// useful for it, but never remove the real source on F6: the
		// reference list is intentionally non-destructive.
		if err := temp.AddReferences(context.Background(), srcVfs, names); err != nil {
			vtui.ShowMessage(Msg("Error.Title"), fmt.Sprintf(Msg("TempPanel.AddError"), err), []string{Msg("vtui.Ok")})
		}
		fspSrc.selectedItems = make(map[string]bool)
		for _, entry := range fspSrc.entries {
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
			if fsp := pf.getActivePanel(); fsp != nil {
				fsp.selectedItems = make(map[string]bool)
				for _, e := range fsp.entries {
					e.Selected = false
				}
			}
			pf.RefreshAll()
		}
	}

	if isMove && !AppConfig.ConfirmMove {
		go ExecuteFileOpAt(pf, srcVfs, dstVfs, srcBasePath, names, initialDest, isMove, AppConfig.DefaultFileOpMode, onCompleteWithClear)
		return
	}

	if !isMove && !AppConfig.ConfirmCopy {
		go ExecuteFileOpAt(pf, srcVfs, dstVfs, srcBasePath, names, initialDest, isMove, AppConfig.DefaultFileOpMode, onCompleteWithClear)
		return
	}

	dlg := vtui.NewCenteredDialog(50, 11, title)
	dlg.ShowClose = true

	promptLbl := vtui.NewLabel(0, 0, fmt.Sprintf(prompt, len(names)), nil)
	dlg.AddItem(promptLbl)

	editDest := vtui.NewEdit(0, 0, 10, initialDest)
	editDest.PathHintsEnabled = true
	attachHistoryUseLast(editDest, copyDestHistoryID)
	dlg.AddItem(editDest)

	modes := []string{Msg("Op.Queue"), Msg("Op.Background"), Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	defMode := AppConfig.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnOk := vtui.NewButton(0, 0, Msg("Copy.Btn"))
	if isMove {
		btnOk = vtui.NewButton(0, 0, Msg("Move.Btn"))
	}
	btnOk.IsDefault = true

	btnOk.OnClick = func() {
		dest := editDest.GetText()
		mode := comboMode.Menu.SelectPos
		dlg.Close()
		if dest != "" {
			commitHistory(editDest, dest)
			go ExecuteFileOpAt(pf, srcVfs, dstVfs, srcBasePath, names, dest, isMove, mode, onCompleteWithClear)
		}
	}
	dlg.AddItem(btnOk)

	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.AddItem(btnCancel)
	dlg.AddItem(comboMode)

	// Layout Engine
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, 11-4)
	vbox.Add(promptLbl, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editDest, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	// Keep the action row above the mode selector. ComboBox.Open() places its
	// popup below the field, so the popup cannot cover these buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()
	dlg.SetFocusedItem(editDest)

	vtui.FrameManager.Push(dlg)
}
func actionRename(pf *PanelsFrame) {
	fsp := pf.getActivePanel()
	if fsp == nil {
		return
	}

	name := fsp.getRawSelectedName()
	if name == "" || name == ".." {
		return
	}

	vtui.InputBox(Msg("Dialog.RenameTitle"), fmt.Sprintf(Msg("Dialog.RenamePrompt"), name), name, func(newName string) {
		if newName == "" || newName == name {
			return
		}
		oldPath := fsp.vfs.Join(fsp.vfs.GetPath(), name)
		newPath := fsp.vfs.Join(fsp.vfs.GetPath(), newName)

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			// The rename dialog never asks for overwrite confirmation. Carry an
			// atomic no-replace decision so remote providers cannot silently
			// destroy an entry that already has the requested name.
			err := fsp.vfs.Rename(vfs.WithDestinationOverwrite(ctx.Context, false), oldPath, newPath)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to rename:\n%v", err), []string{"&Ok"})
					fsp.pendingSelection = name
				} else {
					// Clear cache to ensure the new name is visible immediately
					delete(fsp.dirCache, fsp.cacheKey(fsp.vfs.GetPath()))
					fsp.pendingSelection = newName
				}
				pf.RefreshAll()
			})
		})
	})
}
func actionCreateLink(pf *PanelsFrame) {
	fspSrc := pf.getActivePanel()
	fspDst := pf.getInactivePanel()
	if fspSrc == nil || fspDst == nil {
		return
	}

	names := fspSrc.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	prompt := fmt.Sprintf(Msg("Link.Prompt"), names[0])
	if len(names) > 1 {
		prompt = fmt.Sprintf(Msg("Link.PromptMultiple"), len(names))
	}

	initialDest := fspDst.vfs.GetPath()
	if initialDest != "" && !strings.HasSuffix(initialDest, "/") && !strings.HasSuffix(initialDest, "\\") {
		sep := "/"
		if _, isOS := fspDst.vfs.(*vfs.OSVFS); isOS && runtime.GOOS == "windows" {
			sep = "\\"
		}
		initialDest += sep
	}

	dlg := vtui.NewCenteredDialog(52, 11, Msg("Link.Title"))
	dlg.ShowClose = true

	promptLbl := vtui.NewLabel(0, 0, prompt, nil)
	dlg.AddItem(promptLbl)

	editDest := vtui.NewEdit(0, 0, 10, initialDest)
	editDest.PathHintsEnabled = true
	dlg.AddItem(editDest)

	linkTypes := []string{
		Msg("Link.TypeSymlink"),
		Msg("Link.TypeJunction"),
		Msg("Link.TypeHardlink"),
	}
	comboType := vtui.NewComboBox(0, 0, 32, linkTypes)
	comboType.DropdownOnly = true
	comboType.Menu.SetSelectPos(0)
	comboType.Edit.SetText(linkTypes[0])
	lblType := vtui.NewLabel(0, 0, Msg("Link.Type"), comboType)

	btnOk := vtui.NewButton(0, 0, Msg("Link.Btn"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	btnOk.OnClick = func() {
		dest := editDest.GetText()
		linkType := comboType.Menu.SelectPos
		dlg.Close()
		if dest == "" {
			return
		}

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			srcVfs := fspSrc.vfs
			dstVfs := fspDst.vfs
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
func actionCopyInPlace(pf *PanelsFrame) {
	fsp := pf.getActivePanel()
	if fsp == nil {
		return
	}

	name := fsp.getRawSelectedName()
	if name == "" || name == ".." {
		return
	}

	sourceVFS := fsp.vfs
	sourceBasePath := sourceVFS.GetPath()
	vtui.InputBox(" Copy ", "Copy '"+name+"' to:", name, func(newName string) {
		if newName == "" || newName == name {
			return
		}
		newPath := sourceVFS.Join(sourceBasePath, newName)

		onCompleteWithClear := func() {
			if pf != nil {
				if fsp := pf.getActivePanel(); fsp != nil {
					fsp.selectedItems = make(map[string]bool)
					for _, e := range fsp.entries {
						e.Selected = false
					}
				}
				pf.RefreshAll()
			}
		}

		go ExecuteFileOpAt(pf, sourceVFS, sourceVFS, sourceBasePath, []string{name}, newPath, false, AppConfig.DefaultFileOpMode, onCompleteWithClear)
	})
}
func actionEditorSettings(pf *PanelsFrame) {
	// Height sized so the 3×2 checkbox grid stacks tight (no blank
	// rows between rows of the grid). See #298.
	width, height := 78, 27
	checkCaptions := []string{
		Msg("EditorSettings.AutoIndent"),
		Msg("EditorSettings.CursorBeyondEOL"),
		Msg("EditorSettings.UseEditorConfig"),
		Msg("EditorSettings.AutoComplete"),
		Msg("EditorSettings.Crosshair"),
		Msg("EditorSettings.ColorerBg"),
		Msg("EditorSettings.SyntaxAnimation"),
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

	// The external editor row is a checkbox, a label and an input field on
	// one line. How much of it the two captions take depends on the
	// language, so the field is sized from what they leave rather than from
	// a constant that happens to fit in English: a translation whose
	// captions are wider (Bengali, for one) pushed the field onto the
	// dialog frame. When even a usable field does not fit, the row is
	// stacked over three lines instead, and the dialog grows to match.
	captionWidth := func(key string) int {
		clean, _, _ := vtui.ParseAmpersandString(Msg(key))
		return vtui.StringWidth(clean)
	}
	const minExternalCommandWidth = 20
	extCheckWidth := 4 + captionWidth("EditorSettings.UseExternalEditor")
	extLabelWidth := captionWidth("EditorSettings.ExternalCommand")
	// The row spends, left to right: the checkbox, its right margin, the
	// layout's spacing, the label's left margin, the label, its right
	// margin, the spacing again; whatever is left over is the field.
	const extRowSpacing = 1
	extCmdWidth := (width - 4) - extCheckWidth - 1 - extRowSpacing - 2 - extLabelWidth - 1 - extRowSpacing
	stackExternalRow := extCmdWidth < minExternalCommandWidth
	if stackExternalRow {
		extCmdWidth = width - 4
		height += 2
	}
	dlg := vtui.NewCenteredDialog(width, height, Msg("EditorSettings.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	comboExpand := vtui.NewComboBox(0, 0, 40, []string{
		Msg("EditorSettings.TabExpandNone"),
		Msg("EditorSettings.TabExpandNew"),
		Msg("EditorSettings.TabExpandAll"),
	})
	comboExpand.DropdownOnly = true
	if AppConfig.EditorExpandTabs >= 0 && AppConfig.EditorExpandTabs <= 2 {
		comboExpand.Menu.SetSelectPos(AppConfig.EditorExpandTabs)
		comboExpand.Edit.SetText(comboExpand.Menu.Items[AppConfig.EditorExpandTabs].Text)
	}
	lblExpand := vtui.NewLabel(0, 0, Msg("EditorSettings.ExpandTabs"), comboExpand)
	engines := []string{"Chroma", "Colorer", "None"}
	selectedEngine := 0
	for i, eng := range engines {
		if strings.EqualFold(eng, AppConfig.EditorHighlighter) {
			selectedEngine = i
			break
		}
	}
	comboHighlighter := vtui.NewComboBox(0, 0, 40, engines)
	comboHighlighter.DropdownOnly = true
	comboHighlighter.Menu.SetSelectPos(selectedEngine)
	comboHighlighter.Edit.SetText(engines[selectedEngine])
	lblHighlighter := vtui.NewLabel(0, 0, Msg("EditorSettings.Highlighter"), comboHighlighter)
	schemeNames := []string{""}
	schemeItems := []string{Msg("ColorerSettings.BuiltIn")}
	for _, scheme := range ListColorerSchemes() {
		schemeNames = append(schemeNames, scheme.Name)
		schemeItems = append(schemeItems, colorerSchemeLabel(scheme))
	}
	selectedScheme := 0
	for i := 1; i < len(schemeNames); i++ {
		if strings.EqualFold(schemeNames[i], AppConfig.EditorColorerScheme) {
			selectedScheme = i
			break
		}
	}
	comboScheme := vtui.NewComboBox(0, 0, 40, schemeItems)
	comboScheme.DropdownOnly = true
	comboScheme.Menu.SetSelectPos(selectedScheme)
	comboScheme.Edit.SetText(schemeItems[selectedScheme])
	lblScheme := vtui.NewLabel(0, 0, Msg("EditorSettings.ColorerStyle"), comboScheme)

	editTabSize := vtui.NewEdit(0, 0, 4, fmt.Sprintf("%d", AppConfig.EditorTabSize))
	editTabSize.ClearSelection()
	lblTabSize := vtui.NewLabel(0, 0, Msg("EditorSettings.TabSize"), editTabSize)

	editorCodepageIDs, editorCodepageLabels := codepageSettingChoices()
	comboEditorCodepage := vtui.NewComboBox(0, 0, 40, editorCodepageLabels)
	comboEditorCodepage.DropdownOnly = true
	editorCodepagePos := codepageChoiceIndex(editorCodepageIDs, AppConfig.EditorDefaultCodePage)
	comboEditorCodepage.Menu.SetSelectPos(editorCodepagePos)
	comboEditorCodepage.Edit.SetText(editorCodepageLabels[editorCodepagePos])
	lblEditorCodepage := vtui.NewLabel(0, 0, Msg("EditorSettings.DefaultCodePage"), comboEditorCodepage)

	chkEditorAutodetect := vtui.NewCheckbox(0, 0, Msg("EditorSettings.AutodetectCodePage"), false)
	if AppConfig.EditorAutodetectCodePage {
		chkEditorAutodetect.State = 1
	}

	chkAutoIndent := vtui.NewCheckbox(0, 0, Msg("EditorSettings.AutoIndent"), false)
	if AppConfig.EditorAutoIndent {
		chkAutoIndent.State = 1
	}

	chkCursorEOL := vtui.NewCheckbox(0, 0, Msg("EditorSettings.CursorBeyondEOL"), false)
	if AppConfig.EditorCursorBeyondEOL {
		chkCursorEOL.State = 1
	}

	chkEditorConfig := vtui.NewCheckbox(0, 0, Msg("EditorSettings.UseEditorConfig"), false)
	if AppConfig.EditorUseEditorConfig {
		chkEditorConfig.State = 1
	}

	chkAuto := vtui.NewCheckbox(0, 0, Msg("EditorSettings.AutoComplete"), false)
	if AppConfig.EditorAutoComplete {
		chkAuto.State = 1
	}

	chkCrosshair := vtui.NewCheckbox(0, 0, Msg("EditorSettings.Crosshair"), false)
	if AppConfig.EditorCrosshair {
		chkCrosshair.State = 1
	}
	chkColorerBg := vtui.NewCheckbox(0, 0, Msg("EditorSettings.ColorerBg"), false)
	if AppConfig.EditorColorerBackground {
		chkColorerBg.State = 1
	}

	chkSyntaxAnimation := vtui.NewCheckbox(0, 0, Msg("EditorSettings.SyntaxAnimation"), false)
	if AppConfig.EditorSyntaxAnimation {
		chkSyntaxAnimation.State = 1
	}

	editMask := vtui.NewEdit(0, 0, 56, AppConfig.EditorAutoCompleteMask)
	lblMask := vtui.NewLabel(0, 0, Msg("EditorSettings.Mask"), editMask)

	chkExtEdit := vtui.NewCheckbox(0, 0, Msg("EditorSettings.UseExternalEditor"), false)
	if AppConfig.UseExternalEditor {
		chkExtEdit.State = 1
	}

	editExtCmd := vtui.NewEdit(0, 0, extCmdWidth, AppConfig.ExternalEditorCommand)
	editExtCmd.PathHintsEnabled = true
	attachHistory(editExtCmd, externalEditorHistoryID)
	lblExtCmd := vtui.NewLabel(0, 0, Msg("EditorSettings.ExternalCommand"), editExtCmd)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
	dlg.AddItem(chkColorerBg)
	dlg.AddItem(chkSyntaxAnimation)
	dlg.AddItem(lblMask)
	dlg.AddItem(editMask)
	dlg.AddItem(chkExtEdit)
	dlg.AddItem(lblExtCmd)
	dlg.AddItem(editExtCmd)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	dlg.AddLink(chkExtEdit, editExtCmd, vtui.LinkEnableIfChecked)

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
			chkAuto, chkCrosshair, chkColorerBg, chkSyntaxAnimation,
		} {
			checkColumn.Add(check, vtui.Margins{}, vtui.AlignLeft)
		}
		vbox.Add(checkColumn, vtui.Margins{Top: 1}, vtui.AlignFill)
	} else {
		col1 := vtui.NewVBoxLayout(0, 0, (width-4)/2, checkRows)
		col1.Add(chkAutoIndent, vtui.Margins{}, vtui.AlignLeft)
		col1.Add(chkEditorConfig, vtui.Margins{}, vtui.AlignLeft)
		col1.Add(chkColorerBg, vtui.Margins{}, vtui.AlignLeft)

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

	if stackExternalRow {
		vbox.Add(chkExtEdit, vtui.Margins{Top: 1}, vtui.AlignLeft)
		vbox.Add(lblExtCmd, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(editExtCmd, vtui.Margins{}, vtui.AlignFill)
	} else {
		rowExt := vtui.NewHBoxLayout(0, 0, width-4, 1)
		rowExt.Add(chkExtEdit, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowExt.Add(lblExtCmd, vtui.Margins{Right: 1, Left: 2}, vtui.AlignLeft)
		rowExt.Add(editExtCmd, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(rowExt, vtui.Margins{Top: 1}, vtui.AlignFill)
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
		AppConfig.EditorHighlighter = comboHighlighter.Menu.Items[comboHighlighter.Menu.SelectPos].Text
		AppConfig.EditorColorerScheme = ""
		if pos := comboScheme.Menu.SelectPos; pos > 0 && pos < len(schemeNames) {
			AppConfig.EditorColorerScheme = schemeNames[pos]
		}
		SetColorerScheme(AppConfig.EditorColorerScheme)
		AppConfig.EditorExpandTabs = comboExpand.Menu.SelectPos
		AppConfig.EditorAutodetectCodePage = chkEditorAutodetect.State == 1
		if pos := comboEditorCodepage.Menu.SelectPos; pos >= 0 && pos < len(editorCodepageIDs) {
			AppConfig.EditorDefaultCodePage = editorCodepageIDs[pos]
		}
		fmt.Sscanf(editTabSize.GetText(), "%d", &AppConfig.EditorTabSize)
		if AppConfig.EditorTabSize <= 0 {
			AppConfig.EditorTabSize = 8
		}

		AppConfig.EditorAutoIndent = chkAutoIndent.State == 1
		AppConfig.EditorCursorBeyondEOL = chkCursorEOL.State == 1
		AppConfig.EditorUseEditorConfig = chkEditorConfig.State == 1
		AppConfig.EditorAutoComplete = chkAuto.State == 1
		AppConfig.EditorCrosshair = chkCrosshair.State == 1
		AppConfig.EditorColorerBackground = chkColorerBg.State == 1
		AppConfig.EditorSyntaxAnimation = chkSyntaxAnimation.State == 1
		AppConfig.EditorAutoCompleteMask = editMask.GetText()
		AppConfig.UseExternalEditor = chkExtEdit.State == 1
		AppConfig.ExternalEditorCommand = editExtCmd.GetText()
		commitHistory(editExtCmd, AppConfig.ExternalEditorCommand)
		SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}

// stopPlayerForDelete lets go of a file the player is reading when it is
// about to be deleted, so the delete succeeds on Windows and the player does
// not stay on a file that is gone. Nothing happens for other files.
func stopPlayerForDelete(pf *PanelsFrame, v vfs.VFS, basePath string, names []string) {
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
func actionDelete(pf *PanelsFrame) {
	disposition := vfs.DeletePermanently
	if AppConfig.UseTrash {
		disposition = vfs.DeleteToTrash
	}
	actionDeleteWithDisposition(pf, disposition, false)
}

// actionDeletePermanent is bound to Shift+Del/Shift+NumDel and intentionally
// ignores the global trash preference.
func actionDeletePermanent(pf *PanelsFrame) {
	actionDeleteWithDisposition(pf, vfs.DeletePermanently, true)
}

func actionDeleteWithDisposition(pf *PanelsFrame, disposition vfs.DeleteDisposition, explicitPermanent bool) {
	fsp := pf.getActivePanel()
	if fsp == nil {
		return
	}

	activeVfs := fsp.vfs
	basePath := activeVfs.GetPath()
	names := fsp.GetSelectedNames()
	if len(names) == 0 {
		return
	}
	if dispatchPanelAction(pf, vfs.PanelActionDelete, selectedPanelActionPaths(fsp)) {
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

	if !AppConfig.ConfirmDelete {
		fsp.pendingSelection = fsp.GetSuccessorName()
		stopPlayerForDelete(pf, activeVfs, basePath, names)
		go ExecuteDeleteOpWithDispositionAt(pf, activeVfs, basePath, names, AppConfig.DefaultFileOpMode, disposition, pf.RefreshAll)
		return
	}

	msgName := names[0]
	if len(names) > 1 {
		msgName = fmt.Sprintf(Msg("Delete.Items"), len(names))
	}

	title := Msg(titleKey)
	msg := fmt.Sprintf(Msg(confirmKey), msgName)
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

	modes := []string{Msg("Op.Queue"), Msg("Op.Background"), Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	defMode := AppConfig.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnDel := vtui.NewButton(0, 0, Msg(buttonKey))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	if AppConfig.DeleteCancelFocused {
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
		fsp.pendingSelection = fsp.GetSuccessorName()
		dlg.Close()
		stopPlayerForDelete(pf, activeVfs, basePath, names)
		go ExecuteDeleteOpWithDispositionAt(pf, activeVfs, basePath, names, mode, disposition, pf.RefreshAll)
	}

	if AppConfig.DeleteCancelFocused {
		dlg.SetFocusedItem(btnCancel)
	} else {
		dlg.SetFocusedItem(btnDel)
	}

	vtui.FrameManager.Push(dlg)
}

func actionMkDir(pf *PanelsFrame) {
	panel := pf.getActivePanel()
	if panel == nil {
		return
	}
	if temp, isTempPanel := panel.vfs.(*TempPanelVFS); isTempPanel {
		// F7 removes references from TempPanel. Do this at the concrete F7
		// action boundary: PanelActionCreate is also used by Shift+F4 for
		// creating a new file and must keep its ordinary meaning.
		if temp.removePanelReferences(selectedPanelActionPaths(panel)) {
			pf.RefreshAll()
			return
		}
	}

	activeVfs := panel.vfs

	dlg := vtui.NewCenteredDialog(40, 11, Msg("MakeFolder.Title"))
	dlg.ShowClose = true

	editName := vtui.NewEdit(0, 0, 10, "")
	editName.PathHintsEnabled = true
	attachHistoryUseLast(editName, newFolderHistoryID)
	lblPrompt := vtui.NewLabel(0, 0, Msg("MakeFolder.Prompt"), editName)
	dlg.AddItem(lblPrompt)
	dlg.AddItem(editName)

	modes := []string{Msg("Op.Queue"), Msg("Op.Background"), Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 30, modes)
	comboMode.DropdownOnly = true
	defMode := AppConfig.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(choiceText(modes, defMode))

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
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
		commitHistory(editName, name)
		fullPath := activeVfs.Join(activeVfs.GetPath(), name)

		desc := fmt.Sprintf("Create folder %s", name)
		runFunc := func(ctx context.Context, reporter TaskReporter, anchor vtui.Frame) error {
			reporter.UpdateTransfer("Creating", name, 100, "Folder", 100, "")
			err := activeVfs.MkDir(ctx, fullPath)
			return err
		}

		if mode == 0 { // Queue
			rk := getResourceKey(activeVfs)
			var keys []string
			if rk != "" {
				keys = append(keys, rk)
			}
			task := &QueueTask{
				Type:    "MkDir",
				Desc:    desc,
				ResKeys: keys,
				Run:     runFunc,
				OnComplete: func() {
					panel.pendingSelection = name
					pf.RefreshAll()
				},
			}
			GlobalQueueManager.Enqueue(task)
		} else { // Background / Foreground
			taskCtx := vtui.RunAsync(func(ctx *vtui.TaskContext) {
				err := runFunc(ctx.Context, &DummyReporter{}, nil)
				ctx.RunOnUI(func() {
					if err != nil {
						vtui.ShowMessage(" Error ", fmt.Sprintf(Msg("Operation.Error"), err.Error()), []string{"&Ok"})
					}
					panel.pendingSelection = name
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
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return false
	}
	fsp := pf.getActivePanel()
	if fsp == nil {
		return false
	}
	_, ok := fsp.vfs.(vfs.DuplicateFinder)
	return ok
}

// actionFindDuplicates asks the file system for files with identical
// content. Only a file system that can do the work on its own side offers
// it: doing it from here would mean reading every candidate over the
// network, which costs more than the answer is worth.
func actionFindDuplicates(pf *PanelsFrame) {
	fsp := pf.getActivePanel()
	if fsp == nil {
		return
	}
	finder, ok := fsp.vfs.(vfs.DuplicateFinder)
	if !ok {
		// Reachable through a key binding or a macro, which the menu's
		// visibility rule does not cover.
		vtui.ShowMessage(" Find Duplicates ",
			"This file system cannot search for duplicates.\nOnly a remote one that can hash its own files offers it.",
			[]string{"&Ok"})
		return
	}

	v := fsp.vfs
	root := v.GetPath()
	opDlg := NewFileOpProgressDialog(" Searching for duplicates... ")
	var taskCtx *vtui.TaskContext
	// detached is read and written on the UI thread only, which is where
	// both the buttons and the progress updates run.
	detached := false
	opDlg.btnCancel.OnClick = func() {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
		opDlg.Close()
	}
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
		job := GlobalBackgroundJobs.StartOn(sessionKeyOf(v), "Duplicates in "+root, ctx.Cancel)
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
		var found []FoundFile
		if err == nil {
			for _, group := range groups {
				for _, p := range group {
					item, statErr := v.Stat(ctx.Context, p)
					if statErr != nil {
						item = vfs.VFSItem{Name: v.Base(p)}
					}
					found = append(found, FoundFile{Path: p, Item: item})
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

func actionFindFile(pf *PanelsFrame) {
	activePanel := pf.getActivePanel()
	if activePanel == nil {
		return
	}

	const width, height = 78, 20
	dlg := vtui.NewCenteredDialog(width, height, Msg("FindFile.Title"))
	dlg.ShowClose = true

	lblMask := vtui.NewLabel(0, 0, Msg("FindFile.MaskPrompt"), nil)
	editMask := vtui.NewEdit(0, 0, 20, LastFindFileMask)
	attachHistory(editMask, fileMasksHistoryID)
	lblMask.FocusLink = editMask
	dlg.SetFocusedItem(editMask)

	lblText := vtui.NewLabel(0, 0, Msg("FindFile.TextPrompt"), nil)
	editText := vtui.NewEdit(0, 0, 20, LastFindFileText)
	attachHistory(editText, searchTextHistoryID)
	lblText.FocusLink = editText

	chkCase := vtui.NewCheckbox(0, 0, Msg("FindFile.CaseSensitive"), false)
	chkCase.State = boolToCheckboxState(LastFindFileCaseSensitive)
	chkWhole := vtui.NewCheckbox(0, 0, Msg("FindFile.WholeWords"), false)
	chkWhole.State = boolToCheckboxState(LastFindFileWholeWords)
	chkRegexp := vtui.NewCheckbox(0, 0, Msg("FindFile.Regexp"), false)
	chkRegexp.State = boolToCheckboxState(LastFindFileRegexp)
	chkNotContaining := vtui.NewCheckbox(0, 0, Msg("FindFile.NotContaining"), false)
	chkNotContaining.State = boolToCheckboxState(LastFindFileNotContaining)
	chkFolders := vtui.NewCheckbox(0, 0, Msg("FindFile.Folders"), false)
	chkFolders.State = boolToCheckboxState(LastFindFileFolders)
	chkSymlinks := vtui.NewCheckbox(0, 0, Msg("FindFile.Symlinks"), false)
	chkSymlinks.State = boolToCheckboxState(LastFindFileSymlinks)

	btnFind := vtui.NewButton(0, 0, Msg("FindFile.BtnFind"))
	btnFind.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		commitHistory(editMask, LastFindFileMask)
		commitHistory(editText, LastFindFileText)
		LastFindFileCaseSensitive = chkCase.State == 1
		LastFindFileWholeWords = chkWhole.State == 1
		LastFindFileRegexp = chkRegexp.State == 1
		LastFindFileNotContaining = chkNotContaining.State == 1
		LastFindFileFolders = chkFolders.State == 1
		LastFindFileSymlinks = chkSymlinks.State == 1
		SaveSession()
		dlg.Close()
		if LastFindFileMask != "" {
			ExecuteFindFile(pf, activePanel.vfs, activePanel.vfs.GetPath(), LastFindFileMask, LastFindFileText, FindFileOptions{
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
func actionSaveSettings(pf *PanelsFrame) {
	const width, height = 54, 11
	dlg := vtui.NewCenteredDialog(width, height, Msg("SaveSettings.Title"))
	dlg.ShowClose = true

	question := vtui.NewText(0, 0, Msg("SaveSettings.Question"), 0)
	chkGeneral := vtui.NewCheckbox(0, 0, Msg("SaveSettings.General"), false)
	chkPanel := vtui.NewCheckbox(0, 0, Msg("SaveSettings.Panel"), false)
	chkWindow := vtui.NewCheckbox(0, 0, Msg("SaveSettings.Window"), false)
	chkGeneral.State = 1
	chkPanel.State = 1
	chkWindow.State = 1

	btnSave := vtui.NewButton(0, 0, Msg("SaveSettings.Save"))
	btnSave.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		showToast(Msg("SaveSettings.Done"), 2*time.Second)
	}

	vtui.FrameManager.PushToFrameScreen(pf, dlg)
}

func actionAutoSaveSettings(pf *PanelsFrame) {
	const width, height = 66, 13
	dlg := vtui.NewCenteredDialog(width, height, Msg("PanelSettings.AutoSaveDetails"))
	dlg.ShowClose = true

	question := vtui.NewText(0, 0, Msg("SaveSettings.Question"), 0)
	chkDialog := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AutoSave.Dialog"), false)
	chkPanel := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AutoSave.Panel"), false)
	chkCurrent := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AutoSave.Current"), false)
	chkWindow := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AutoSave.GUI"), false)
	chkDialog.State = boolToCheckboxState(AppConfig.AutoSaveDialogSettings)
	chkPanel.State = boolToCheckboxState(AppConfig.AutoSavePanelSettings)
	chkCurrent.State = boolToCheckboxState(AppConfig.AutoSaveCurrentPanel)
	chkWindow.State = boolToCheckboxState(AppConfig.AutoSaveGUIWindow)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
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
		AppConfig.AutoSaveDialogSettings = chkDialog.State == 1
		AppConfig.AutoSavePanelSettings = chkPanel.State == 1
		AppConfig.AutoSaveCurrentPanel = chkCurrent.State == 1
		AppConfig.AutoSaveGUIWindow = chkWindow.State == 1
		syncAutoSaveMaster()
		// Changing the autosave policy is an explicit settings action. Persist
		// the policy itself even when the new policy disables future writes.
		saveConfigWithWindowSize(false)
		dlg.Close()
	}

	vtui.FrameManager.PushToFrameScreen(pf, dlg)
}

func actionPanelSettings(pf *PanelsFrame) {
	// Keep the frequently used panel and navigation options in a compact
	// dialog. The less frequently changed performance, console, and operation
	// display options live in actionPanelAdditionalSettings below. Keeping both
	// pages as ordinary dialogs means they remain usable on a 25-row terminal
	// without introducing a second scrolling container for interactive items.
	dlg := vtui.NewCenteredDialog(60, 24, Msg("PanelSettings.Title"))
	dlg.ShowClose = true

	chkHidden := vtui.NewCheckbox(0, 0, Msg("PanelSettings.ShowHidden"), false)
	chkHidden.State = 0
	if AppConfig.ShowHiddenFiles {
		chkHidden.State = 1
	}

	chkDirPrefix := vtui.NewCheckbox(0, 0, Msg("PanelSettings.ShowDirPrefix"), false)
	chkDirPrefix.State = 0
	if AppConfig.ShowDirPrefix {
		chkDirPrefix.State = 1
	}

	chkHighlightMarks := vtui.NewCheckbox(0, 0, Msg("PanelSettings.ShowHighlightMarks"), false)
	chkHighlightMarks.State = 0
	if AppConfig.ShowHighlightMarks {
		chkHighlightMarks.State = 1
	}

	chkSeparateExtensions := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SeparateExtensions"), false)
	if AppConfig.SeparateFileExtensions {
		chkSeparateExtensions.State = 1
	}
	chkFileInfo := vtui.NewCheckbox(0, 0, Msg("PanelSettings.ShowFileInfo"), false)
	if AppConfig.ShowPanelFileInfo {
		chkFileInfo.State = 1
	}

	scrollbarModes := []string{
		Msg("PanelSettings.ScrollbarsOff"),
		Msg("PanelSettings.ScrollbarsMinimal"),
		Msg("PanelSettings.ScrollbarsFull"),
	}
	comboScrollbars := vtui.NewComboBox(0, 0, 24, scrollbarModes)
	comboScrollbars.DropdownOnly = true
	comboScrollbars.Menu.SetSelectPos(int(AppConfig.PanelScrollbarMode))
	comboScrollbars.Edit.SetText(scrollbarModes[AppConfig.PanelScrollbarMode])
	lblScrollbars := vtui.NewLabel(0, 0, Msg("PanelSettings.Scrollbars"), comboScrollbars)

	chkPaths := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SavePaths"), false)
	chkPaths.State = 0
	if AppConfig.SavePanelPaths {
		chkPaths.State = 1
	}

	chkAutoSave := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AutoSave"), false)
	if AppConfig.AutoSaveSettings {
		chkAutoSave.State = 1
	}
	btnAutoSaveDetails := vtui.NewButton(0, 0, Msg("PanelSettings.AutoSaveDetails"))

	chkUseTrash := vtui.NewCheckbox(0, 0, Msg("PanelSettings.UseTrash"), false)
	if AppConfig.UseTrash {
		chkUseTrash.State = 1
	}
	chkCmdAc := vtui.NewCheckbox(0, 0, Msg("PanelSettings.CommandLineAutoComplete"), false)
	chkCmdAc.State = 0
	if AppConfig.CommandLineAutoComplete {
		chkCmdAc.State = 1
	}

	lblNavigation := vtui.NewText(0, 0, Msg("PanelSettings.Navigation"), 0)
	navigation := vtui.NewRadioGroup(0, 0, 1, []string{
		Msg("PanelSettings.NavigationClassic"),
		Msg("PanelSettings.NavigationVim"),
		Msg("PanelSettings.NavigationSearch"),
	})
	navigation.Selected = int(AppConfig.NavigationMode)
	// RadioGroup does not expose its keyboard focus index, so advance it to
	// the selected row to make Space operate on the visibly selected mode.
	for i := 0; i < navigation.Selected; i++ {
		navigation.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	}
	chkStayFocused := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SearchStayFocused"), false)
	if AppConfig.SearchCommandStayFocused {
		chkStayFocused.State = 1
	}
	chkStayFocused.SetDisabled(AppConfig.NavigationMode != NavigationSearchFirst)
	navigation.OnChange = func(selected int) {
		chkStayFocused.SetDisabled(PanelNavigationMode(selected) != NavigationSearchFirst)
	}

	btnAdditional := vtui.NewButton(0, 0, Msg("PanelSettings.Additional"))
	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
	// First checkbox cluster — stack tight, no blank rows between.
	vbox.Add(chkHidden, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDirPrefix, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkHighlightMarks, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkSeparateExtensions, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkFileInfo, vtui.Margins{}, vtui.AlignLeft)
	// Blank row before the scrollbar combo — transition to a different
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
	// Navigation radio group — its own visual island.
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
		AppConfig.ShowHiddenFiles = chkHidden.State == 1
		AppConfig.ShowDirPrefix = chkDirPrefix.State == 1
		AppConfig.ShowHighlightMarks = chkHighlightMarks.State == 1
		AppConfig.SeparateFileExtensions = chkSeparateExtensions.State == 1
		AppConfig.ShowPanelFileInfo = chkFileInfo.State == 1
		AppConfig.PanelScrollbarMode = PanelScrollbarMode(comboScrollbars.Menu.SelectPos)
		AppConfig.SavePanelPaths = chkPaths.State == 1
		if chkAutoSave.State == 0 {
			AppConfig.AutoSaveDialogSettings = false
			AppConfig.AutoSavePanelSettings = false
			AppConfig.AutoSaveCurrentPanel = false
			AppConfig.AutoSaveGUIWindow = false
		} else if !AppConfig.AutoSaveDialogSettings && !AppConfig.AutoSavePanelSettings && !AppConfig.AutoSaveCurrentPanel && !AppConfig.AutoSaveGUIWindow {
			AppConfig.AutoSaveDialogSettings = true
			AppConfig.AutoSavePanelSettings = true
			AppConfig.AutoSaveCurrentPanel = true
			AppConfig.AutoSaveGUIWindow = true
		}
		syncAutoSaveMaster()
		AppConfig.UseTrash = chkUseTrash.State == 1
		AppConfig.CommandLineAutoComplete = chkCmdAc.State == 1
		pf.cmdLine.Edit.PathHintsEnabled = AppConfig.CommandLineAutoComplete
		AppConfig.NavigationMode = PanelNavigationMode(navigation.Selected)
		AppConfig.SearchCommandStayFocused = chkStayFocused.State == 1
		pf.applyNavigationMode()
		SaveConfig()
		dlg.Close()
		pf.ResizeConsole(pf.lastW, pf.lastH)
		pf.RefreshAll()
	}
	btnAdditional.OnClick = func() { actionPanelAdditionalSettings(pf) }
	btnAutoSaveDetails.OnClick = func() { actionAutoSaveSettings(pf) }

	vtui.FrameManager.Push(dlg)
}

func actionPanelAdditionalSettings(pf *PanelsFrame) {
	// This page contains the options that are changed less often than the
	// panel/navigation controls. Keep it below 25 rows even when the host
	// console notice needs a separate line.
	consoleModeUnavailable := probeGUIBackend() != "" || !probeHostTTY()
	dlg := vtui.NewCenteredDialog(60, 24, Msg("PanelSettings.AdvancedTitle"))
	dlg.ShowClose = true

	chkSync := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SyncPanelLoad"), false)
	if AppConfig.SyncPanelLoad {
		chkSync.State = 1
	}
	editApplyWorkers := vtui.NewEdit(0, 0, 12, strconv.Itoa(AppConfig.ApplyCommandParallelism))
	lblApplyWorkers := vtui.NewLabel(0, 0, Msg("PanelSettings.ApplyWorkers"), editApplyWorkers)

	chkAlwaysMenu := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AlwaysShowMenuBar"), false)
	if AppConfig.AlwaysShowMenuBar {
		chkAlwaysMenu.State = 1
	}
	chkCPUGPU := vtui.NewCheckbox(0, 0, Msg("PanelSettings.InfoPanelCPUGPU"), false)
	if AppConfig.InfoPanelCPUGPU {
		chkCPUGPU.State = 1
	}
	chkEscToggle := vtui.NewCheckbox(0, 0, Msg("PanelSettings.EscTogglePanels"), false)
	if AppConfig.EscTogglePanels {
		chkEscToggle.State = 1
	}
	chkTerminalCtrlN := vtui.NewCheckbox(0, 0, Msg("PanelSettings.TerminalCtrlNWorkspace"), false)
	if AppConfig.TerminalCtrlNWorkspace {
		chkTerminalCtrlN.State = 1
	}
	chkExactSearch := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SearchExactOnHit"), false)
	if AppConfig.SearchExactOnHit {
		chkExactSearch.State = 1
	}

	lblConsoleMode := vtui.NewText(0, 0, Msg("PanelSettings.ConsoleMode"), 0)
	radioConsoleMode := vtui.NewRadioGroup(0, 0, 1, []string{
		Msg("PanelSettings.ConsoleModeOwn"),
		Msg("PanelSettings.ConsoleModeHost"),
	})
	if strings.EqualFold(AppConfig.ConsoleMode, "host") {
		radioConsoleMode.Selected = 1
	}
	for i := 0; i < radioConsoleMode.Selected; i++ {
		radioConsoleMode.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	}
	chkOverlay := vtui.NewCheckbox(0, 0, Msg("PanelSettings.ConsoleOverlayUI"), false)
	if AppConfig.ConsoleOverlayUI {
		chkOverlay.State = 1
	}
	chkOverlay.SetDisabled(radioConsoleMode.Selected != 1)
	var lblConsoleUnavailable *vtui.Text
	if consoleModeUnavailable {
		lblConsoleUnavailable = vtui.NewText(0, 0, Msg("PanelSettings.ConsoleModeUnavailable"), 0)
	}
	lblConsoleNote := vtui.NewText(0, 0, Msg("PanelSettings.ConsoleModeNote"), 0)
	radioConsoleMode.OnChange = func(selected int) {
		chkOverlay.SetDisabled(selected != 1)
	}

	modes := []string{Msg("Op.Queue"), Msg("Op.Background"), Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 24, modes)
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(AppConfig.DefaultFileOpMode)
	comboMode.Edit.SetText(modes[AppConfig.DefaultFileOpMode])
	lblMode := vtui.NewLabel(0, 0, Msg("PanelSettings.DefaultMode"), comboMode)

	pathModes := []string{Msg("Op.PathNameOnly"), Msg("Op.PathFullPath"), Msg("Op.PathSrcDst")}
	comboPath := vtui.NewComboBox(0, 0, 24, pathModes)
	comboPath.DropdownOnly = true
	comboPath.Menu.SetSelectPos(AppConfig.FileOpPathDisplay)
	comboPath.Edit.SetText(pathModes[AppConfig.FileOpPathDisplay])
	lblPath := vtui.NewLabel(0, 0, Msg("PanelSettings.PathDisplay"), comboPath)

	macroModes := []string{"key_macros.ini (Legacy)", "Macros/scripts/*.lua"}
	comboMacro := vtui.NewComboBox(0, 0, 24, macroModes)
	comboMacro.DropdownOnly = true
	comboMacro.Menu.SetSelectPos(AppConfig.MacroRecordFormat)
	comboMacro.Edit.SetText(macroModes[AppConfig.MacroRecordFormat])
	lblMacro := vtui.NewLabel(0, 0, Msg("PanelSettings.RecordMacrosTo"), comboMacro)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
			vtui.ShowMessageOn(dlg, Msg("ApplyCommand.InvalidWorkersTitle"), Msg("ApplyCommand.InvalidWorkers"), []string{Msg("vtui.Ok")})
			return
		}
		AppConfig.SyncPanelLoad = chkSync.State == 1
		AppConfig.ApplyCommandParallelism = applyWorkers
		AppConfig.AlwaysShowMenuBar = chkAlwaysMenu.State == 1
		AppConfig.InfoPanelCPUGPU = chkCPUGPU.State == 1
		AppConfig.EscTogglePanels = chkEscToggle.State == 1
		AppConfig.TerminalCtrlNWorkspace = chkTerminalCtrlN.State == 1
		AppConfig.SearchExactOnHit = chkExactSearch.State == 1
		if radioConsoleMode.Selected == 1 {
			AppConfig.ConsoleMode = "host"
		} else {
			AppConfig.ConsoleMode = "own"
		}
		AppConfig.ConsoleOverlayUI = chkOverlay.State == 1
		AppConfig.DefaultFileOpMode = comboMode.Menu.SelectPos
		AppConfig.FileOpPathDisplay = comboPath.Menu.SelectPos
		AppConfig.MacroRecordFormat = comboMacro.Menu.SelectPos
		SaveConfig()
		dlg.Close()
		pf.ResizeConsole(pf.lastW, pf.lastH)
		pf.RefreshAll()
	}

	vtui.FrameManager.Push(dlg)
}

func actionConfirmationsSettings(pf *PanelsFrame) {
	const width, height = 56, 15
	dlg := vtui.NewCenteredDialog(width, height, Msg("ConfirmationsSettings.Title"))
	dlg.ShowClose = true

	chkCopy := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.Copy"), false)
	chkCopy.State = 0
	if AppConfig.ConfirmCopy {
		chkCopy.State = 1
	}

	chkMove := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.Move"), false)
	chkMove.State = 0
	if AppConfig.ConfirmMove {
		chkMove.State = 1
	}

	chkDelete := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.Delete"), false)
	chkDelete.State = 0
	if AppConfig.ConfirmDelete {
		chkDelete.State = 1
	}

	chkExit := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.Exit"), false)
	chkExit.State = 0
	if AppConfig.ConfirmExit {
		chkExit.State = 1
	}

	chkDelFocus := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.DeleteCancelFocused"), false)
	chkDelFocus.State = 0
	if AppConfig.DeleteCancelFocused {
		chkDelFocus.State = 1
	}

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		AppConfig.ConfirmCopy = chkCopy.State == 1
		AppConfig.ConfirmMove = chkMove.State == 1
		AppConfig.ConfirmDelete = chkDelete.State == 1
		AppConfig.ConfirmExit = chkExit.State == 1
		AppConfig.DeleteCancelFocused = chkDelFocus.State == 1
		SaveConfig()
		dlg.Close()
		pf.RefreshAll()
	}

	vtui.FrameManager.Push(dlg)
}

func actionMouseWheelSettings(pf *PanelsFrame) {
	const width, height = 44, 22
	dlg := vtui.NewCenteredDialog(width, height, Msg("MouseWheel.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	lblHint := vtui.NewText(0, 0, Msg("MouseWheel.Hint"), 0)

	newWheelRow := func(upVal, downVal int) (lblUp, lblDown *vtui.Text, editUp, editDown *vtui.Edit) {
		editUp = vtui.NewEdit(0, 0, 5, strconv.Itoa(upVal))
		editDown = vtui.NewEdit(0, 0, 5, strconv.Itoa(downVal))
		lblUp = vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editUp)
		lblDown = vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editDown)
		return
	}
	lblPanelUp, lblPanelDown, editPanelUp, editPanelDown := newWheelRow(AppConfig.WheelPanelUp, AppConfig.WheelPanelDown)
	lblEditorUp, lblEditorDown, editEditorUp, editEditorDown := newWheelRow(AppConfig.WheelEditorUp, AppConfig.WheelEditorDown)
	lblViewerUp, lblViewerDown, editViewerUp, editViewerDown := newWheelRow(AppConfig.WheelViewerUp, AppConfig.WheelViewerDown)
	lblMenuUp, lblMenuDown, editMenuUp, editMenuDown := newWheelRow(AppConfig.WheelMenuUp, AppConfig.WheelMenuDown)
	lblTableUp, lblTableDown, editTableUp, editTableDown := newWheelRow(AppConfig.WheelTableUp, AppConfig.WheelTableDown)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		gb := vtui.NewGroupBox(0, 0, width-5, 2, Msg(titleKey))
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
		AppConfig.WheelPanelUp = parseWheel(editPanelUp)
		AppConfig.WheelPanelDown = parseWheel(editPanelDown)
		AppConfig.WheelEditorUp = parseWheel(editEditorUp)
		AppConfig.WheelEditorDown = parseWheel(editEditorDown)
		AppConfig.WheelViewerUp = parseWheel(editViewerUp)
		AppConfig.WheelViewerDown = parseWheel(editViewerDown)
		AppConfig.WheelMenuUp = parseWheel(editMenuUp)
		AppConfig.WheelMenuDown = parseWheel(editMenuDown)
		AppConfig.WheelTableUp = parseWheel(editTableUp)
		AppConfig.WheelTableDown = parseWheel(editTableDown)
		applyWheelSettings()
		SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}

func actionPathHintSettings(pf *PanelsFrame) {
	const width, height = 56, 19
	dlg := vtui.NewCenteredDialog(width, height, Msg("PathHints.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	chkFullPath := vtui.NewCheckbox(0, 0, Msg("PathHints.FullPath"), false)
	if AppConfig.PathHintFullPath {
		chkFullPath.State = 1
	}

	sources := []string{Msg("PathHints.SourceActive"), Msg("PathHints.SourcePassive"), Msg("PathHints.SourceBoth")}
	comboSource := vtui.NewComboBox(0, 0, 24, sources)
	comboSource.DropdownOnly = true
	if AppConfig.PathHintSource >= 0 && AppConfig.PathHintSource < len(sources) {
		comboSource.Menu.SetSelectPos(AppConfig.PathHintSource)
		comboSource.Edit.SetText(sources[AppConfig.PathHintSource])
	}
	lblSource := vtui.NewLabel(0, 0, Msg("PathHints.Source"), comboSource)

	editTimeout := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.PathHintTimeout))
	lblTimeout := vtui.NewLabel(0, 0, Msg("PathHints.Timeout"), editTimeout)

	editMaxVisible := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.PathHintMaxVisible))
	lblMaxVisible := vtui.NewLabel(0, 0, Msg("PathHints.MaxVisible"), editMaxVisible)

	chkPerCategory := vtui.NewCheckbox(0, 0, Msg("PathHints.PerCategory"), false)
	if AppConfig.PathHintPerCategory {
		chkPerCategory.State = 1
	}

	chkDialogAutoComplete := vtui.NewCheckbox(0, 0, Msg("PathHints.DialogAutoComplete"), false)
	if AppConfig.DialogAutoComplete {
		chkDialogAutoComplete.State = 1
	}

	lblNote := vtui.NewText(0, 0, Msg("PathHints.MarkersNote"), 0)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		AppConfig.PathHintFullPath = chkFullPath.State == 1
		AppConfig.PathHintSource = comboSource.Menu.SelectPos
		timeout := 2
		fmt.Sscanf(editTimeout.GetText(), "%d", &timeout)
		if timeout < 1 {
			timeout = 1
		}
		AppConfig.PathHintTimeout = timeout
		maxVisible := 5
		fmt.Sscanf(editMaxVisible.GetText(), "%d", &maxVisible)
		if maxVisible < 1 {
			maxVisible = 1
		}
		AppConfig.PathHintMaxVisible = maxVisible
		AppConfig.PathHintPerCategory = chkPerCategory.State == 1
		AppConfig.DialogAutoComplete = chkDialogAutoComplete.State == 1
		applyPathHintSettings()
		SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}
func actionUpdateSettings(pf *PanelsFrame) {
	width, height := 54, 11
	dlg := vtui.NewCenteredDialog(width, height, Msg("UpdateSettings.Title"))
	dlg.ShowClose = true

	channels := []string{Msg("UpdateSettings.ChannelStable"), Msg("UpdateSettings.ChannelNightly")}
	comboChannel := vtui.NewComboBox(0, 0, 24, channels)
	comboChannel.DropdownOnly = true
	if AppConfig.UpdateChannel >= 0 && AppConfig.UpdateChannel < len(channels) {
		comboChannel.Menu.SetSelectPos(AppConfig.UpdateChannel)
		comboChannel.Edit.SetText(channels[AppConfig.UpdateChannel])
	}
	lblChannel := vtui.NewLabel(0, 0, Msg("UpdateSettings.Channel"), comboChannel)

	intervals := []string{Msg("UpdateSettings.IntervalNever"), Msg("UpdateSettings.IntervalStart"), Msg("UpdateSettings.IntervalDaily"), Msg("UpdateSettings.IntervalWeekly")}
	comboInterval := vtui.NewComboBox(0, 0, 24, intervals)
	comboInterval.DropdownOnly = true
	if AppConfig.UpdateInterval >= 0 && AppConfig.UpdateInterval < len(intervals) {
		comboInterval.Menu.SetSelectPos(AppConfig.UpdateInterval)
		comboInterval.Edit.SetText(intervals[AppConfig.UpdateInterval])
	}
	lblInterval := vtui.NewLabel(0, 0, Msg("UpdateSettings.Interval"), comboInterval)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCheck := vtui.NewButton(0, 0, Msg("UpdateSettings.BtnCheck"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		AppConfig.UpdateChannel = comboChannel.Menu.SelectPos
		AppConfig.UpdateInterval = comboInterval.Menu.SelectPos
		SaveConfig()
		dlg.Close()
	}
	btnCheck.OnClick = func() {
		AppConfig.UpdateChannel = comboChannel.Menu.SelectPos
		AppConfig.UpdateInterval = comboInterval.Menu.SelectPos
		SaveConfig()
		dlg.Close()
		// Do not hold the UI mouse-dispatch loop while the manual update
		// check waits for GitHub. The dialog is already closed, so the
		// network request can safely continue in the background and post
		// its result back through FrameManager when it completes.
		go CheckForUpdates(pf, true)
	}

	vtui.FrameManager.Push(dlg)
}

func decodeFar2lTime(hexStr string) (time.Time, error) {
	if len(hexStr) != 16 {
		return time.Time{}, fmt.Errorf("invalid time length")
	}
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return time.Time{}, err
	}
	val := binary.LittleEndian.Uint64(b)
	// FILETIME's maximum uint64 value becomes at most 1.85e12 seconds
	// after division, far below int64's limit.
	// #nosec G115 -- division by 10,000,000 bounds the result to int64.
	sec := int64(val / 10000000)
	nsec := int64(val%10000000) * 100
	sec -= 11644473600
	return time.Unix(sec, nsec), nil
}

func unescapeFar2lString(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

func importFar2lHistory(path string) ([]HistoryRecord, error) {
	ini := LoadIni(path)
	linesStr := ini.GetString("SavedHistory", "Lines", "")
	extrasStr := ini.GetString("SavedHistory", "Extras", "")
	locksStr := ini.GetString("SavedHistory", "Locks", "")
	timesStr := ini.GetString("SavedHistory", "Times", "")

	if linesStr == "" {
		return nil, fmt.Errorf("no Lines found in %s", path)
	}

	linesStr = unescapeFar2lString(linesStr)
	extrasStr = unescapeFar2lString(extrasStr)

	lines := strings.Split(linesStr, "\n")
	var extras []string
	if extrasStr != "" {
		extras = strings.Split(extrasStr, "\n")
	}
	times := strings.Fields(timesStr)

	var res []HistoryRecord
	for i, name := range lines {
		rec := HistoryRecord{Name: name}
		if i < len(extras) {
			rec.Extra = extras[i]
		}
		if i < len(locksStr) && locksStr[i] != '0' {
			rec.Lock = true
		}
		if i < len(times) {
			if t, err := decodeFar2lTime(times[i]); err == nil {
				rec.Timestamp = t
			}
		}
		res = append(res, rec)
	}

	return res, nil
}

func actionImportFar2lHistory(pf *PanelsFrame) {
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
			recs, err := importFar2lHistory(far2lConfig)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to import history:\n%v", err), []string{"&Ok"})
					return
				}

				hp, isF4 := vtui.GlobalHistoryProvider.(*F4HistoryProvider)
				if !isF4 {
					vtui.ShowMessage(" Error ", "Incompatible history provider.", []string{"&Ok"})
					return
				}

				current := hp.LoadRichHistory("cmdline")
				seen := make(map[string]bool)
				var merged []HistoryRecord

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

				limit := pf.cmdLine.Edit.HistoryLimit
				if limit <= 0 {
					limit = 100
				}
				if len(merged) > limit {
					merged = merged[:limit]
				}

				hp.SaveRichHistory("cmdline", merged)

				h := extractNames(merged)
				pf.cmdLine.Edit.History = h

				showToast(fmt.Sprintf("Imported %d new commands from far2l.", len(merged)-len(current)), 3*time.Second)
			})
		})
	}
}

func actionAppearanceSettings(pf *PanelsFrame) {
	const width, height = 64, 30
	dlg := vtui.NewCenteredDialog(width, height, Msg("AppearanceSettings.Title"))
	dlg.ShowClose = true
	// Snapshot the whole palette (not just the style name) so a
	// Cancel restores every runtime tweak — farcolors.ini overrides
	// loaded at startup, Colorer editor-background pushes, anything
	// else that touched vtui.Palette. Re-applying originalStyle
	// alone would wipe those.
	originalPalette := append([]uint64(nil), vtui.Palette...)

	styles := AvailableColorStyles()
	names := make([]string, len(styles))
	selected := 0
	for i, style := range styles {
		names[i] = style.Name
		if strings.EqualFold(style.Name, AppConfig.ColorStyle) {
			selected = i
		}
	}

	comboStyle := vtui.NewComboBox(0, 0, 24, names)
	comboStyle.DropdownOnly = true
	if len(names) > 0 {
		comboStyle.Menu.SetSelectPos(selected)
		comboStyle.Edit.SetText(names[selected])
	}
	lblStyle := vtui.NewText(0, 0, Msg("AppearanceSettings.Style"), 0)
	lblStyle.FocusLink = comboStyle
	defaultMenuAction := comboStyle.Menu.OnAction
	comboStyle.Menu.OnAction = func(idx int) {
		defaultMenuAction(idx)
		if idx >= 0 && idx < len(names) {
			if err := ApplyColorStyle(names[idx]); err == nil {
				vtui.FrameManager.Redraw()
			}
		}
	}
	fontChoices := guiFontDisplayChoices(AppConfig.Language, AppConfig.GuiFont)
	comboFont := vtui.NewComboBox(0, 0, 30, fontChoices)
	comboFont.Edit.SetText(guiFontCurrentDisplayName(AppConfig.Language, AppConfig.GuiFont))
	comboFont.Edit.SelectAll()
	configureGuiFontCombo(comboFont, fontChoices)
	lblFont := vtui.NewLabel(0, 0, Msg("AppearanceSettings.Font"), comboFont)
	chkSystemMonospace := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.UseSystemMonospace"), false)
	if AppConfig.GuiUseSystemMonospace {
		chkSystemMonospace.State = 1
	}
	updateFontEditor := func() {
		usePlatformFont := chkSystemMonospace.State == 1 && (runtime.GOOS == "windows" || runtime.GOOS == "darwin")
		lblFont.SetDisabled(usePlatformFont)
		comboFont.SetDisabled(usePlatformFont)
	}
	chkSystemMonospace.OnChange = func(int) { updateFontEditor() }
	updateFontEditor()

	editSize := vtui.NewEdit(0, 0, 6, fmt.Sprintf("%d", AppConfig.GuiFontSize))
	editSize.Validator = &vtui.IntRangeValidator{Min: 6, Max: 72}
	lblSize := vtui.NewLabel(0, 0, Msg("AppearanceSettings.FontSize"), editSize)

	editTitle := vtui.NewEdit(0, 0, 30, AppConfig.ConsoleTitleTemplate)
	lblTitle := vtui.NewLabel(0, 0, Msg("AppearanceSettings.TitleTemplate"), editTitle)
	chkFullPathTitle := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.DisplayFullPathInTitle"), false)
	if AppConfig.DisplayFullPathInTitle {
		chkFullPathTitle.State = 1
	}

	workspaceTabModes := []string{
		Msg("AppearanceSettings.WorkspaceTabsAlways"),
		Msg("AppearanceSettings.WorkspaceTabsMultiple"),
		Msg("AppearanceSettings.WorkspaceTabsCtrl"),
		Msg("AppearanceSettings.WorkspaceTabsNever"),
	}
	comboWorkspaceTabs := vtui.NewComboBox(0, 0, 30, workspaceTabModes)
	comboWorkspaceTabs.DropdownOnly = true
	workspaceTabSelection := AppConfig.WorkspaceTabMode
	if workspaceTabSelection < 0 || workspaceTabSelection >= len(workspaceTabModes) {
		workspaceTabSelection = int(vtui.WorkspaceTabsMultiple)
	}
	comboWorkspaceTabs.Menu.SetSelectPos(workspaceTabSelection)
	comboWorkspaceTabs.Edit.SetText(choiceText(workspaceTabModes, workspaceTabSelection))
	lblWorkspaceTabs := vtui.NewLabel(0, 0, Msg("AppearanceSettings.WorkspaceTabs"), comboWorkspaceTabs)
	chkWorkspaceTabsOverlay := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.WorkspaceTabsOverlay"), AppConfig.WorkspaceTabsOverlay)
	if AppConfig.WorkspaceTabsOverlay {
		chkWorkspaceTabsOverlay.State = 1
	}

	ctrlTabModes := []string{
		Msg("AppearanceSettings.CtrlTabDirect"),
		Msg("AppearanceSettings.CtrlTabMenu"),
	}
	comboCtrlTab := vtui.NewComboBox(0, 0, 30, ctrlTabModes)
	comboCtrlTab.DropdownOnly = true
	ctrlTabSelection := 0
	if AppConfig.CtrlTabShowsMenu {
		ctrlTabSelection = 1
	}
	comboCtrlTab.Menu.SetSelectPos(ctrlTabSelection)
	comboCtrlTab.Edit.SetText(ctrlTabModes[ctrlTabSelection])
	lblCtrlTab := vtui.NewLabel(0, 0, Msg("AppearanceSettings.CtrlTab"), comboCtrlTab)

	chkAltNumberTabs := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.AltNumberTabs"), AppConfig.AltNumberSwitchesTabs)
	if AppConfig.AltNumberSwitchesTabs {
		chkAltNumberTabs.State = 1
	}
	chkRestoreWorkspaceTabs := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.RestoreWorkspaceTabs"), AppConfig.RestoreWorkspaceTabs)
	if AppConfig.RestoreWorkspaceTabs {
		chkRestoreWorkspaceTabs.State = 1
	}
	workspaceNumberingModes := []string{
		Msg("AppearanceSettings.WorkspaceNumbersAlways"),
		Msg("AppearanceSettings.WorkspaceNumbersSession"),
		Msg("AppearanceSettings.WorkspaceNumbersOrder"),
	}
	comboWorkspaceNumbering := vtui.NewComboBox(0, 0, 30, workspaceNumberingModes)
	comboWorkspaceNumbering.DropdownOnly = true
	workspaceNumberingSelection := int(AppConfig.WorkspaceTabNumbering)
	if workspaceNumberingSelection < 0 || workspaceNumberingSelection >= len(workspaceNumberingModes) {
		workspaceNumberingSelection = int(WorkspaceTabNumbersAlways)
	}
	comboWorkspaceNumbering.Menu.SetSelectPos(workspaceNumberingSelection)
	comboWorkspaceNumbering.Edit.SetText(choiceText(workspaceNumberingModes, workspaceNumberingSelection))
	lblWorkspaceNumbering := vtui.NewLabel(0, 0, Msg("AppearanceSettings.WorkspaceNumbers"), comboWorkspaceNumbering)

	chkCursor := vtui.NewCheckbox(0, 0, Msg("PanelSettings.KeepCursor"), false)
	chkCursor.State = 0
	if AppConfig.KeepTerminalCursor {
		chkCursor.State = 1
	}

	chkContrast := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.ColorCorrection"), false)
	chkContrast.State = 0
	if AppConfig.EnforceColorCorrection {
		chkContrast.State = 1
	}

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnExport := vtui.NewButton(0, 0, Msg("AppearanceSettings.ExportBtn"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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
		colorsPath := filepath.Join(GetF4ConfigDir(), "farcolors.ini")
		err := ExportColors(colorsPath)
		if err != nil {
			vtui.ShowMessageOn(dlg, " Error ", fmt.Sprintf("Failed to export colors:\n%v", err), []string{"&Ok"})
		} else {
			customIdx := -1
			for i, item := range comboStyle.Menu.Items {
				if strings.EqualFold(item.Text, customColorStyleName) {
					customIdx = i
					break
				}
			}
			if customIdx < 0 {
				names = append(names, customColorStyleName)
				comboStyle.Menu.AddItem(vtui.MenuItem{Text: customColorStyleName})
				customIdx = len(names) - 1
			}
			comboStyle.Menu.SetSelectPos(customIdx)
			comboStyle.Menu.OnAction(customIdx)
			vtui.ShowMessageOn(dlg, " Info ", "Current colors successfully exported to:\n"+colorsPath+"\n\nYou can edit this file to customize your palette.", []string{"&Ok"})
		}
	}
	btnOk.OnClick = func() {
		if len(names) > 0 {
			AppConfig.ColorStyle = names[comboStyle.Menu.SelectPos]
		}
		useSystemMonospace := chkSystemMonospace.State == 1
		fontValue := guiFontValueForDisplay(AppConfig.Language, AppConfig.GuiFont, comboFont.Edit.GetText())
		fontChanged := AppConfig.GuiUseSystemMonospace != useSystemMonospace || AppConfig.GuiFont != fontValue || fmt.Sprintf("%d", AppConfig.GuiFontSize) != editSize.GetText()

		AppConfig.ConsoleTitleTemplate = editTitle.GetText()
		AppConfig.DisplayFullPathInTitle = chkFullPathTitle.State == 1
		AppConfig.GuiUseSystemMonospace = useSystemMonospace
		AppConfig.GuiFont = fontValue
		fmt.Sscanf(editSize.GetText(), "%d", &AppConfig.GuiFontSize)
		if AppConfig.GuiFontSize <= 0 {
			AppConfig.GuiFontSize = defaultGuiFontSize(runtime.GOOS)
		}
		AppConfig.KeepTerminalCursor = chkCursor.State == 1
		vtui.ManageCursorStyle = !AppConfig.KeepTerminalCursor
		AppConfig.EnforceColorCorrection = chkContrast.State == 1
		AppConfig.WorkspaceTabMode = comboWorkspaceTabs.Menu.SelectPos
		AppConfig.WorkspaceTabsOverlay = chkWorkspaceTabsOverlay.State == 1
		AppConfig.CtrlTabShowsMenu = comboCtrlTab.Menu.SelectPos == 1
		AppConfig.AltNumberSwitchesTabs = chkAltNumberTabs.State == 1
		AppConfig.RestoreWorkspaceTabs = chkRestoreWorkspaceTabs.State == 1
		AppConfig.WorkspaceTabNumbering = WorkspaceTabNumberingMode(comboWorkspaceNumbering.Menu.SelectPos)
		if AppConfig.WorkspaceTabNumbering == WorkspaceTabNumbersOrder {
			renumberWorkspaceScreens()
		}
		SaveConfig()

		dlg.SetExitCode(1)
		ctrlTabMode := vtui.WorkspaceCtrlTabDirect
		if AppConfig.CtrlTabShowsMenu {
			ctrlTabMode = vtui.WorkspaceCtrlTabMenu
		}
		vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabMode(AppConfig.WorkspaceTabMode), ctrlTabMode)
		vtui.FrameManager.ConfigureWorkspaceTabOverlay(AppConfig.WorkspaceTabsOverlay)
		vtui.FrameManager.ConfigureWorkspaceAltNumberSwitch(AppConfig.AltNumberSwitchesTabs)

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

func actionManagePlugins(pf *PanelsFrame) {
	width, height := 60, 16
	btnAdd := vtui.NewButton(0, 0, Msg("Plugins.BtnAdd"))
	btnDel := vtui.NewButton(0, 0, Msg("Plugins.BtnRemove"))
	btnPerms := vtui.NewButton(0, 0, Msg("Plugins.BtnPermissions"))
	btnClose := vtui.NewButton(0, 0, Msg("Plugins.BtnClose"))
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

	dlg := vtui.NewCenteredDialog(width, height, Msg("Plugins.Title"))
	dlg.ShowClose = true

	lb := vtui.NewListBox(0, 0, width-4, 10, AppConfig.RegisteredPlugins)

	btnPerms.OnClick = func() { actionPluginPermissions(PluginPermissions()) }

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
		if fsp := pf.getActivePanel(); fsp != nil {
			if _, ok := fsp.vfs.(*vfs.OSVFS); ok {
				startPath = fsp.vfs.GetPath()
			}
		}
		showPluginFileDialog(dlg, startPath, func(path string) {
			if path != "" {
				AppConfig.RegisteredPlugins = append(AppConfig.RegisteredPlugins, path)
				SaveConfig()
				lb.Items = AppConfig.RegisteredPlugins
				lb.UpdateRows()
				vtui.FrameManager.Redraw()
				if GlobalPluginManager != nil {
					GlobalPluginManager.LoadExternalPlugin(path)
				}
			}
		})
	}

	btnDel.OnClick = func() {
		idx := lb.SelectPos
		if idx >= 0 && idx < len(AppConfig.RegisteredPlugins) {
			pluginPath := AppConfig.RegisteredPlugins[idx]
			confirm := vtui.ShowMessageOn(dlg, " Confirm ", "Remove plugin:\n"+vtui.TruncateMiddle(pluginPath, 40)+"?", []string{"&Remove", "Cancel"})
			confirm.OnResult = func(code int) {
				if code == 0 {
					AppConfig.RegisteredPlugins = append(AppConfig.RegisteredPlugins[:idx], AppConfig.RegisteredPlugins[idx+1:]...)
					SaveConfig()
					lb.Items = AppConfig.RegisteredPlugins
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
	dlg := vtui.NewCenteredDialog(w, h, Msg("Plugins.AddTitle"))
	dlg.ShowClose = true

	lbl := vtui.NewLabel(0, 0, Msg("Plugins.SelectFilePrompt"), nil)
	edit := vtui.NewEdit(0, 0, w-4, startPath)
	edit.PathHintsEnabled = true
	lb := vtui.NewListBox(0, 0, w-4, h-10, nil)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

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

func actionFileAttributes(pf *PanelsFrame) {
	fsp := pf.getActivePanel()
	if fsp == nil || fsp.vfs == nil {
		return
	}

	names := fsp.GetSelectedNames()
	if len(names) == 0 {
		return
	}

	paths := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" && name != ".." {
			paths = append(paths, fsp.vfs.Join(fsp.vfs.GetPath(), name))
		}
	}
	if len(paths) == 0 {
		return
	}

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		targets := make([]attributesTarget, 0, len(paths))
		for _, path := range paths {
			item, err := vfs.Lstat(ctx.Context, fsp.vfs, path)
			if err != nil {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
				})
				return
			}
			targets = append(targets, attributesTarget{path: path, item: item})
		}
		ctx.RunOnUI(func() {
			showAttributesDialogForTargets(pf, fsp.vfs, targets)
		})
	})
}

type langInfo struct {
	code string
	name string
}

func listAvailableUILanguages() []langInfo {
	langs := []langInfo{{"en", "English"}}
	seen := map[string]bool{"en": true}

	// Every .lng shipped with f4 is embedded in the binary (langPackFS), and
	// InitLang loads the configured language from there even when no lang/
	// directory exists on disk. The dialog has to enumerate the same set:
	// built from disk alone it offered English only, silently misrepresenting
	// a configured non-English language as "English" (selection fell back to
	// item 0) with no way to see or change the real setting.
	if entries, err := langPackFS.ReadDir("lang"); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lng") {
				continue
			}
			code := strings.TrimSuffix(e.Name(), ".lng")
			if seen[code] {
				continue
			}
			data, err := langPackFS.ReadFile("lang/" + e.Name())
			if err != nil {
				continue
			}
			ini := ParseIni(strings.NewReader(string(data)))
			langs = append(langs, langInfo{code: code, name: ini.GetString("Language", "Name", code)})
			seen[code] = true
		}
	}

	// Packs on disk extend the embedded set (user-supplied translations).
	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "lang")
	dirs := []string{filepath.Join(exeDir, "lang"), userDir, "lang"}

	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".lng") {
				code := strings.TrimSuffix(e.Name(), ".lng")
				if !seen[code] {
					ini := LoadIni(filepath.Join(d, e.Name()))
					name := ini.GetString("Language", "Name", code)
					langs = append(langs, langInfo{code: code, name: name})
					seen[code] = true
				}
			}
		}
	}
	return langs
}

func listAvailableHelpLanguages() []langInfo {
	langs := []langInfo{{"en", "English"}}
	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "help")
	dirs := []string{filepath.Join(exeDir, "help"), userDir, "help"}
	seen := map[string]bool{"en": true}

	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".hlf") {
				code := strings.TrimSuffix(e.Name(), ".hlf")
				if !seen[code] {
					name := getLanguageName(code)
					langs = append(langs, langInfo{code: code, name: name})
					seen[code] = true
				}
			}
		}
	}
	return langs
}

func actionLanguage(pf *PanelsFrame) {
	uiLangs := listAvailableUILanguages()
	helpLangs := listAvailableHelpLanguages()

	width, height := 54, 13
	dlg := vtui.NewCenteredDialog(width, height, Msg("LanguageSettings.Title"))
	dlg.ShowClose = true

	uiNames := make([]string, len(uiLangs))
	selectedUI := 0
	for i, l := range uiLangs {
		uiNames[i] = l.name
		if l.code == AppConfig.Language {
			selectedUI = i
		}
	}
	comboUI := vtui.NewComboBox(0, 0, 24, uiNames)
	comboUI.DropdownOnly = true
	comboUI.Menu.SetSelectPos(selectedUI)
	comboUI.Edit.SetText(uiNames[selectedUI])
	lblUI := vtui.NewLabel(0, 0, Msg("LanguageSettings.Primary"), comboUI)

	helpNames := make([]string, len(helpLangs))
	selectedHelp := 0
	for i, l := range helpLangs {
		helpNames[i] = l.name
		if l.code == AppConfig.HelpLanguage {
			selectedHelp = i
		}
	}
	comboHelp := vtui.NewComboBox(0, 0, 24, helpNames)
	comboHelp.DropdownOnly = true
	comboHelp.Menu.SetSelectPos(selectedHelp)
	comboHelp.Edit.SetText(helpNames[selectedHelp])
	lblHelp := vtui.NewLabel(0, 0, Msg("HelpLanguage.Title")+":", comboHelp)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblUI)
	dlg.AddItem(comboUI)
	dlg.AddItem(lblHelp)
	dlg.AddItem(comboHelp)
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
		suggestFontChoice := false
		if idx := comboUI.Menu.SelectPos; idx >= 0 && idx < len(uiLangs) {
			if AppConfig.Language != uiLangs[idx].code {
				suggestFontChoice = shouldSuggestFontForLanguage(uiLangs[idx].code, AppConfig.GuiFont)
				AppConfig.Language = uiLangs[idx].code
				uiChanged = true
			}
		}
		if idx := comboHelp.Menu.SelectPos; idx >= 0 && idx < len(helpLangs) {
			if AppConfig.HelpLanguage != helpLangs[idx].code {
				AppConfig.HelpLanguage = helpLangs[idx].code
				helpChanged = true
			}
		}
		if uiChanged || helpChanged {
			SaveConfig()
			InitLang()
			InitHelpSystem()
			vtui.FrameManager.PostTask(func() {
				if uiChanged && suggestFontChoice {
					actionAppearanceSettings(pf)
				} else if uiChanged {
					vtui.ShowMessage(Msg("Info.Title"), Msg("Language.Changed"), []string{Msg("vtui.Ok")})
				}
				vtui.FrameManager.Redraw()
			})
		}
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
}

func actionHelpLanguage(pf *PanelsFrame) {
	actionLanguage(pf)
}

func getLanguageName(code string) string {
	if code == "en" || code == "eng" {
		return "English"
	}
	if !safeLanguageCode(code) {
		return strings.ToUpper(code)
	}
	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "lang")
	candidates := []string{
		filepath.Join(userDir, code+".lng"),
		filepath.Join(exeDir, "lang", code+".lng"),
		filepath.Join("lang", code+".lng"),
	}
	for _, cand := range candidates {
		// #nosec G703 -- safeLanguageCode rejects separators and ".." before code is used as a path component.
		if _, err := os.Stat(cand); err == nil {
			ini := LoadIni(cand)
			if name := ini.GetString("Language", "Name", ""); name != "" {
				return name
			}
		}
	}
	return strings.ToUpper(code)
}
