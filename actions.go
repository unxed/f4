package main

import (
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

var (
	LastFindFileMask = "*"
	LastFindFileText = ""
	LastLeftPath     = ""
	LastRightPath    = ""
	LastLeftCursor   = ""
	LastRightCursor  = ""
	LastActivePanel  = 1
	LastWidePanel    = -1

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
func actionFoldersHistory(pf *PanelsFrame) {
	if vtui.GlobalHistoryProvider == nil {
		return
	}
	h := vtui.GlobalHistoryProvider.LoadHistory("folders")
	if len(h) == 0 {
		vtui.ShowMessage(Msg("History.Title"), Msg("History.EmptyFolders"), []string{Msg("vtui.Ok")})
		return
	}

	menu := vtui.NewVMenu(Msg("History.FoldersTitle"))
	menu.SetHelp("HistoryFolders")

	var richFolders []HistoryRecord
	for _, path := range h {
		richFolders = append(richFolders, HistoryRecord{Name: path})
	}
	search := newHistorySearch(menu, richFolders, Msg("History.FoldersHint"))

	if activePanel := pf.getActivePanel(); activePanel != nil {
		currentPath := activePanel.vfs.GetPath()
		for historyPos, path := range h {
			if sameFolderHistoryPath(path, currentPath) {
				search.selectOriginalIndex(historyPos)
				break
			}
		}
	}

	// Shared "cd on active panel" path used by bare Enter and by mouse click.
	gotoActive := func() {
		historyPos, _, ok := search.selected()
		if !ok {
			return
		}
		search.cleanup()
		if targetPanel := pf.getActivePanel(); targetPanel != nil {
			// The menu is oldest → newest. If the selected path disappeared,
			// navigateAvailableFolderHistory walks toward newer entries.
			pf.navigateAvailableFolderHistory(targetPanel, h, historyPos, -1)
		}
	}
	// VMenu.ProcessMouse calls SetExitCode after OnAction, so click closes
	// the menu automatically — gotoActive only does the side effect.
	menu.OnAction = func(int) { gotoActive() }

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
			confirmAndPruneMissingFolderHistory(&h, search, menu)
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
			menu.Close()
			gotoActive()
			return true
		}

		if (e.VirtualKeyCode == vtinput.VK_DELETE || e.VirtualKeyCode == vtinput.VK_BACK) && shift {
			// Delete item
			search.deleteSelected()
			h = extractNames(search.all)
			vtui.GlobalHistoryProvider.SaveHistory("folders", h)
			if len(search.all) == 0 {
				search.cleanup()
				menu.Close()
			}
			return true
		}

		// Del (no modifiers): clear the whole history with a confirmation.
		if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift {
			confirmAndClearHistory(Msg("History.FoldersTitle"), "folders", &h, func() {
				pf.cmdLine.Edit.HistoryPos = -1
			}, search, menu)
			return true
		}

		// Ctrl+C / Ctrl+Ins: copy the selected entry to the clipboard.
		if (e.VirtualKeyCode == vtinput.VK_C || e.VirtualKeyCode == vtinput.VK_INSERT) && ctrl && !alt && !shift {
			go vtui.SetClipboard(path)
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
	// The tab/workspace branch originally stored command directories in a
	// parallel history. Fold those records into upstream's richer history
	// model so existing sessions retain their paths after the merge.
	legacyPaths := loadCommandHistoryPaths(h)
	for i := range richCmds {
		if richCmds[i].Extra == "" && i < len(legacyPaths) {
			richCmds[i].Extra = legacyPaths[i]
		}
	}

	menu := vtui.NewVMenu(Msg("History.CommandsTitle"))
	menu.SetHelp("History")
	search := newHistorySearch(menu, richCmds, Msg("History.CommandsHint"))
	search.showSecond = search.hasSecondary()
	search.secondWidth = 24

	search.onLockToggled = func() {
		if isF4 {
			hp.SaveRichHistory("cmdline", search.all)
		}
	}
	search.onCtrlF10 = func(rec HistoryRecord) {
		if rec.Extra != "" {
			search.cleanup()
			menu.Close()
			if targetPanel := pf.getActivePanel(); targetPanel != nil {
				pf.NavigateToPath(targetPanel, rec.Extra)
			}
		}
	}

	// Shared "paste selected command" path used by Enter and mouse click.
	pasteCurrent := func() {
		_, rec, ok := search.selected()
		if !ok {
			return
		}
		search.cleanup()
		pf.cmdLine.Edit.SetText(rec.Name)
		pf.cmdLine.Edit.HistoryPos = -1
	}
	// VMenu.ProcessMouse calls SetExitCode after OnAction, so click closes
	// the menu automatically — pasteCurrent only does the side effect.
	menu.OnAction = func(int) { pasteCurrent() }

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
			pasteCurrent()
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
			go vtui.SetClipboard(rec.Name)
			return true
		}
		return false
	}

	vtui.FrameManager.Push(menu)
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

// confirmAndPruneMissingFolderHistory implements the far2l Ctrl+R
// handler in folder history: prompt, then keep only paths that still
// exist on disk. Locked-entry semantics don't apply here — f4 has no
// per-entry lock flag yet.
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
func confirmAndPruneMissingFolderHistory(h *[]string, search *historySearch, menu *vtui.VMenu) {
	buttons := []string{Msg("vtui.Ok"), Msg("vtui.Cancel")}
	dlg := vtui.ShowMessage(Msg("History.FoldersTitle"), Msg("History.ConfirmPruneMissing"), buttons)
	dlg.OnResult = func(code int) {
		if code != 0 {
			return
		}
		kept := make([]string, 0, len(*h))
		for _, p := range *h {
			if isPersistentURIPath(p) || vfs.FindStandaloneProvider(context.Background(), nil, p) != nil {
				kept = append(kept, p)
				continue
			}
			if _, err := os.Stat(p); err == nil {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(*h) {
			return
		}
		*h = kept
		if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory("folders", kept)
		}
		var richFolders []HistoryRecord
		for _, p := range kept {
			richFolders = append(richFolders, HistoryRecord{Name: p})
		}
		search.setItems(richFolders)
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
	cpID := AppConfig.EditorDefaultCodePage

	if f != nil {
		size := f.Size()
		detectLen := 16 * 1024
		if int64(detectLen) > size {
			detectLen = int(size)
		}
		header := make([]byte, detectLen)
		_, _ = f.ReadAt(context.Background(), header, 0)

		cpID = vfs.DetectEncoding(header, AppConfig.EditorAutodetectCodePage, AppConfig.EditorDefaultCodePage)

		if cpID == 65001 {
			buf = NewAsyncBuffer(context.Background(), f)
			buf.prewarm()
			pt = piecetable.NewWithBuffer(buf)
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

	editor := NewEditorView(pt, v, path)
	editor.Codepage = cpID
	if GlobalFileState != nil && path != "" {
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
		go func() {
			msg := "Colorer syntax highlighting schemas are missing.\nWould you like to download them from elfmz/far2l GitHub?"
			if pf.Message(" Download Colorer Schemas ", msg, []string{"&Yes", "&No"}) == 0 {
				DownloadColorerSchemas(pf, func(success bool) {
					vtui.FrameManager.PostTask(func() {
						if !success {
							AppConfig.EditorHighlighter = "Chroma"
							SaveConfig()
						}
						openEditorInternal(pf, v, path)
					})
				})
			} else {
				vtui.FrameManager.PostTask(func() {
					AppConfig.EditorHighlighter = "Chroma"
					openEditorInternal(pf, v, path)
				})
			}
		}()
		return
	}
	if _, isLocal := v.(*vfs.OSVFS); isLocal {
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
					vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets, Devices).", []string{"&Ok"})
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
			viewer.HexMode = state.ViewerHex
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
				if res == 0 {
					vtui.FrameManager.SwitchScreen(screenIdx)
				} else if res == 1 { // Reload
					existingViewer.Close()
					openViewerInternal(pf, v, path)
				} else if res == 2 { // New instance
					openViewerInternal(pf, v, path)
				}
			}
		})
		return
	}
	openViewerInternal(pf, v, path)
}

// tryOpenImageViewer opens the picture viewer when the file looks like an
// image and the backend can actually show one. It returns false to let the
// ordinary viewer handle the file.
func tryOpenImageViewer(pf *PanelsFrame, v vfs.VFS, path string) bool {
	if pf == nil || !IsImageFile(path) {
		return false
	}
	scr := vtui.FrameManager.Screen()
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
	if tryOpenImageViewer(pf, v, path) {
		return
	}
	if _, isLocal := v.(*vfs.OSVFS); isLocal {
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
						vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets, Devices).", []string{"&Ok"})
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
					vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets, Devices).", []string{"&Ok"})
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
	vtui.InputBox(Msg("Viewer.SearchTitle"), "Search for:", vv.lastSearch, func(pattern string) {
		if pattern == "" {
			return
		}
		if pattern != vv.lastSearch {
			vv.lastSearchFound = false
		}
		vv.lastSearch = pattern
		runViewerSearch(vv, pattern, reverse)
	})
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
			foundOffset := viewerSearchOffset(ctx.Context, vv.backend, pattern, start, reverse, func(percent int) {
				ctx.RunOnUI(func() { dlg.SetProgress(percent) })
			})

			ctx.RunOnUI(func() {
				canceled := ctx.Err() != nil
				dlg.Close()
				if canceled {
					return
				}
				if foundOffset != -1 {
					vv.TopOffset = vv.backend.FindLineStart(foundOffset)
					vv.lastSearchOffset = foundOffset
					vv.lastSearchTopOffset = vv.TopOffset
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
	if backend == nil || pattern == "" {
		return -1
	}
	fileSize := backend.Size()
	if fileSize <= 0 {
		return -1
	}
	if start < 0 {
		start = 0
	}
	if start > fileSize {
		start = fileSize
	}
	patternLower := strings.ToLower(pattern)
	chunkSize := int64(256 * 1024)
	if int64(len(patternLower)) > chunkSize {
		chunkSize = int64(len(patternLower))
	}
	overlap := int64(len(patternLower) - 1)

	if reverse {
		if at, searched := backend.SearchBefore(ctx, pattern, start); searched {
			return at
		}
		end := start
		for end > 0 {
			if ctx.Err() != nil {
				return -1
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
				return -1
			}
			if idx := strings.LastIndex(strings.ToLower(string(data)), patternLower); idx >= 0 {
				return begin + int64(idx)
			}
			if begin == 0 {
				break
			}
			end = begin + overlap
		}
		return -1
	}

	if at, searched := backend.SearchFrom(ctx, pattern, start); searched {
		return at
	}
	current := start
	for current < fileSize {
		if ctx.Err() != nil {
			return -1
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
		if idx := strings.Index(strings.ToLower(string(data)), patternLower); idx >= 0 {
			return current + int64(idx)
		}
		advance := int64(len(data)) - overlap
		if advance < 1 {
			advance = 1
		}
		current += advance
	}
	return -1
}

func actionExecute(pf *PanelsFrame, v vfs.VFS, dir, name, path string) {
	// User-defined file associations for Enter (mirrors far2l F9 →
	// Commands → File associations). A matching association intercepts
	// before the runnable / xdg-open fallback; no match → default flow.
	if tryFileAssociation(pf, AssocExecute) {
		return
	}
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		if _, isLocal := v.(*vfs.OSVFS); isLocal {
			if fi, err := os.Stat(path); err == nil {
				if fi.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice|os.ModeCharDevice) != 0 {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", "Cannot open special files (Named Pipes, Sockets, Devices).", []string{"&Ok"})
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

				activePty := pf.getActivePTY()
				if activePty != nil {
					cmd := name
					var cmdToWire string

					useDir := isOS || isPty

					actualDir := ""
					if useDir {
						actualDir = dir
					}

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

					pf.executing = true
					pf.returnToPanels = true

					if !isWindowsShell {
						pf.termView.SetMuted(true)
					}
					pf.writePTY(activePty, []byte(cmdToWire))
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
		vtui.InputBox(Msg("Edit.NewFileTitle"), Msg("Edit.NewFilePrompt"), "", func(name string) {
			if name == "" {
				name = "newfile.txt"
			}
			path := activeVfs.Join(dir, name)
			if AppConfig.UseExternalEditor {
				actionEditFileExternal(pf, activeVfs, path, 0)
				return
			}
			actionOpenEditor(pf, activeVfs, path)
		})
	}
}

func actionViewTerminalLog(pf *PanelsFrame) {
	v := &TerminalLogVFS{tv: pf.termView}
	actionOpenViewer(pf, v, "Terminal Log")
}

func actionEditTerminalLog(pf *PanelsFrame) {
	v := &TerminalLogVFS{tv: pf.termView}
	actionOpenEditor(pf, v, "Terminal Log")
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
	basePath := fsp.vfs.GetPath()

	var targetPath string
	if name == ".." {
		targetPath = fsp.vfs.Dir(basePath)
	} else {
		targetPath = fsp.vfs.Join(basePath, name)
	}

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
	dlg.AddItem(editDest)

	modes := []string{Msg("Op.Queue"), Msg("Op.Background"), Msg("Op.Foreground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	defMode := AppConfig.DefaultFileOpMode
	if defMode < 0 || defMode >= len(modes) {
		defMode = 0
	}
	comboMode.Menu.SetSelectPos(defMode)
	comboMode.Edit.SetText(modes[defMode])
	dlg.AddItem(comboMode)

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
			go ExecuteFileOpAt(pf, srcVfs, dstVfs, srcBasePath, names, dest, isMove, mode, onCompleteWithClear)
		}
	}
	dlg.AddItem(btnOk)

	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	btnCancel.OnClick = func() { dlg.Close() }
	dlg.AddItem(btnCancel)

	// Layout Engine
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, 11-4)
	vbox.Add(promptLbl, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editDest, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
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
	width, height := 78, 25
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

	editMask := vtui.NewEdit(0, 0, 56, AppConfig.EditorAutoCompleteMask)
	lblMask := vtui.NewLabel(0, 0, Msg("EditorSettings.Mask"), editMask)

	chkExtEdit := vtui.NewCheckbox(0, 0, Msg("EditorSettings.UseExternalEditor"), false)
	if AppConfig.UseExternalEditor {
		chkExtEdit.State = 1
	}

	editExtCmd := vtui.NewEdit(0, 0, 20, AppConfig.ExternalEditorCommand)
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
	dlg.AddItem(chkAutoIndent)
	dlg.AddItem(chkCursorEOL)
	dlg.AddItem(chkEditorConfig)
	dlg.AddItem(chkAuto)
	dlg.AddItem(chkCrosshair)
	dlg.AddItem(chkColorerBg)
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

	col1 := vtui.NewVBoxLayout(0, 0, (width-4)/2, 3)
	col1.Add(chkAutoIndent, vtui.Margins{}, vtui.AlignLeft)
	col1.Add(chkEditorConfig, vtui.Margins{}, vtui.AlignLeft)
	col1.Add(chkColorerBg, vtui.Margins{}, vtui.AlignLeft)

	col2 := vtui.NewVBoxLayout(0, 0, (width-4)/2, 3)
	col2.Add(chkCursorEOL, vtui.Margins{}, vtui.AlignLeft)
	col2.Add(chkAuto, vtui.Margins{}, vtui.AlignLeft)
	col2.Add(chkCrosshair, vtui.Margins{}, vtui.AlignLeft)

	rowChecks := vtui.NewHBoxLayout(0, 0, width-4, 3)
	rowChecks.Add(col1, vtui.Margins{}, vtui.AlignFill)
	rowChecks.Add(col2, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(lblMask, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editMask, vtui.Margins{}, vtui.AlignFill)

	rowExt := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowExt.Add(chkExtEdit, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowExt.Add(lblExtCmd, vtui.Margins{Right: 1, Left: 2}, vtui.AlignLeft)
	rowExt.Add(editExtCmd, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowExt, vtui.Margins{Top: 1}, vtui.AlignFill)

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
		AppConfig.EditorHighlighter = comboHighlighter.Menu.Items[comboHighlighter.Menu.SelectPos].Text
		AppConfig.EditorColorerScheme = ""
		if pos := comboScheme.Menu.SelectPos; pos > 0 && pos < len(schemeNames) {
			AppConfig.EditorColorerScheme = schemeNames[pos]
		}
		SetColorerScheme(AppConfig.EditorColorerScheme)
		AppConfig.EditorExpandTabs = comboExpand.Menu.SelectPos
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
		AppConfig.EditorAutoCompleteMask = editMask.GetText()
		AppConfig.UseExternalEditor = chkExtEdit.State == 1
		AppConfig.ExternalEditorCommand = editExtCmd.GetText()
		SaveConfig()
		dlg.Close()
	}

	vtui.FrameManager.Push(dlg)
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

	if AppConfig.ConfirmDelete == false {
		fsp.pendingSelection = fsp.GetSuccessorName()
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
	// Delete is destructive — render on the red WarnDialog palette
	// so the confirmation reads as an alarm, not a neutral question.
	dlg.IsWarning = true
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
	comboMode.Edit.SetText(modes[defMode])
	dlg.AddItem(comboMode)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)

	btnDel := vtui.NewButton(0, 0, Msg(buttonKey))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	if AppConfig.DeleteCancelFocused {
		btnCancel.IsDefault = true
	} else {
		btnDel.IsDefault = true
	}

	dlg.AddItem(btnDel)
	dlg.AddItem(btnCancel)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnDel, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnDel.OnClick = func() {
		mode := comboMode.Menu.SelectPos
		fsp.pendingSelection = fsp.GetSuccessorName()
		dlg.Close()
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

	activeVfs := panel.vfs

	dlg := vtui.NewCenteredDialog(40, 11, Msg("MakeFolder.Title"))
	dlg.ShowClose = true

	editName := vtui.NewEdit(0, 0, 10, "")
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
	comboMode.Edit.SetText(modes[defMode])
	dlg.AddItem(comboMode)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 40-4, 11-4)
	vbox.Add(lblPrompt, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editName, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)

	hbox := vtui.NewHBoxLayout(0, 0, 40-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
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

func actionFindFile(pf *PanelsFrame) {
	activePanel := pf.getActivePanel()
	if activePanel == nil {
		return
	}

	dlg := vtui.NewCenteredDialog(54, 13, Msg("FindFile.Title"))
	dlg.ShowClose = true

	lblMask := vtui.NewLabel(0, 0, Msg("FindFile.MaskPrompt"), nil)
	editMask := vtui.NewEdit(0, 0, 20, LastFindFileMask)
	lblMask.FocusLink = editMask
	dlg.SetFocusedItem(editMask)

	lblText := vtui.NewLabel(0, 0, Msg("FindFile.TextPrompt"), nil)
	editText := vtui.NewEdit(0, 0, 20, LastFindFileText)
	lblText.FocusLink = editText

	btnFind := vtui.NewButton(0, 0, Msg("FindFile.BtnFind"))
	btnFind.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblMask)
	dlg.AddItem(editMask)
	dlg.AddItem(lblText)
	dlg.AddItem(editText)
	dlg.AddItem(btnFind)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 54-4, 13-4)
	vbox.Add(lblMask, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editMask, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(lblText, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editText, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, 54-4, 1)
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
		SaveSession()
		dlg.Close()
		if LastFindFileMask != "" {
			ExecuteFindFile(pf, activePanel.vfs, activePanel.vfs.GetPath(), LastFindFileMask, LastFindFileText)
		}
	}

	vtui.FrameManager.Push(dlg)
}
func actionPanelSettings(pf *PanelsFrame) {
	// Height sized so consecutive checkbox rows stack tightly without
	// blank lines between them (see #298). Blank rows are kept only at
	// transitions between widget kinds (checkbox↔combo↔radio↔button)
	// so groups still read as groups.
	const dialogHeight = 37
	dlg := vtui.NewCenteredDialog(60, dialogHeight, Msg("PanelSettings.Title"))
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

	chkSync := vtui.NewCheckbox(0, 0, Msg("PanelSettings.SyncPanelLoad"), false)
	chkSync.State = 0
	if AppConfig.SyncPanelLoad {
		chkSync.State = 1
	}
	editApplyWorkers := vtui.NewEdit(0, 0, 12, strconv.Itoa(AppConfig.ApplyCommandParallelism))
	lblApplyWorkers := vtui.NewLabel(0, 0, Msg("PanelSettings.ApplyWorkers"), editApplyWorkers)

	chkAlwaysMenu := vtui.NewCheckbox(0, 0, Msg("PanelSettings.AlwaysShowMenuBar"), false)
	chkAlwaysMenu.State = 0
	if AppConfig.AlwaysShowMenuBar {
		chkAlwaysMenu.State = 1
	}

	chkCPUGPU := vtui.NewCheckbox(0, 0, Msg("PanelSettings.InfoPanelCPUGPU"), false)
	chkCPUGPU.State = 0
	if AppConfig.InfoPanelCPUGPU {
		chkCPUGPU.State = 1
	}

	chkEscToggle := vtui.NewCheckbox(0, 0, Msg("PanelSettings.EscTogglePanels"), false)
	chkEscToggle.State = 0
	if AppConfig.EscTogglePanels {
		chkEscToggle.State = 1
	}

	chkTerminalCtrlN := vtui.NewCheckbox(0, 0, Msg("PanelSettings.TerminalCtrlNWorkspace"), false)
	if AppConfig.TerminalCtrlNWorkspace {
		chkTerminalCtrlN.State = 1
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

	dlg.AddItem(chkHidden)
	dlg.AddItem(chkDirPrefix)
	dlg.AddItem(chkHighlightMarks)
	dlg.AddItem(chkSeparateExtensions)
	dlg.AddItem(chkFileInfo)
	dlg.AddItem(lblScrollbars)
	dlg.AddItem(comboScrollbars)
	dlg.AddItem(chkPaths)
	dlg.AddItem(chkCmdAc)
	dlg.AddItem(lblNavigation)
	dlg.AddItem(navigation)
	dlg.AddItem(chkStayFocused)
	dlg.AddItem(chkSync)
	dlg.AddItem(lblApplyWorkers)
	dlg.AddItem(editApplyWorkers)
	dlg.AddItem(chkAlwaysMenu)
	dlg.AddItem(chkCPUGPU)
	dlg.AddItem(chkEscToggle)
	dlg.AddItem(chkTerminalCtrlN)
	dlg.AddItem(lblMode)
	dlg.AddItem(comboMode)
	dlg.AddItem(lblPath)
	dlg.AddItem(comboPath)
	dlg.AddItem(lblMacro)
	dlg.AddItem(comboMacro)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 56, dialogHeight-4)
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
	vbox.Add(rowScrollbars, vtui.Margins{Top: 1}, vtui.AlignFill)
	// Second checkbox cluster.
	vbox.Add(chkPaths, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkCmdAc, vtui.Margins{}, vtui.AlignLeft)
	// Navigation radio group — its own visual island.
	vbox.Add(lblNavigation, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(navigation, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkStayFocused, vtui.Margins{Left: 2}, vtui.AlignLeft)
	// Third checkbox cluster.
	vbox.Add(chkSync, vtui.Margins{Top: 1}, vtui.AlignLeft)
	rowApplyWorkers := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowApplyWorkers.Add(lblApplyWorkers, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowApplyWorkers.Add(editApplyWorkers, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowApplyWorkers, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(chkAlwaysMenu, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(chkCPUGPU, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkEscToggle, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkTerminalCtrlN, vtui.Margins{}, vtui.AlignLeft)

	rowMode := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowMode.Add(lblMode, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowMode.Add(comboMode, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowMode, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowPath := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowPath.Add(lblPath, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowPath.Add(comboPath, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowPath, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowMacro := vtui.NewHBoxLayout(0, 0, 56, 1)
	rowMacro.Add(lblMacro, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowMacro.Add(comboMacro, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowMacro, vtui.Margins{Top: 1}, vtui.AlignFill)

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
		AppConfig.ShowHiddenFiles = chkHidden.State == 1
		AppConfig.ShowDirPrefix = chkDirPrefix.State == 1
		AppConfig.ShowHighlightMarks = chkHighlightMarks.State == 1
		AppConfig.SeparateFileExtensions = chkSeparateExtensions.State == 1
		AppConfig.ShowPanelFileInfo = chkFileInfo.State == 1
		AppConfig.PanelScrollbarMode = PanelScrollbarMode(comboScrollbars.Menu.SelectPos)
		AppConfig.SavePanelPaths = chkPaths.State == 1
		AppConfig.CommandLineAutoComplete = chkCmdAc.State == 1
		AppConfig.NavigationMode = PanelNavigationMode(navigation.Selected)
		AppConfig.SearchCommandStayFocused = chkStayFocused.State == 1
		AppConfig.SyncPanelLoad = chkSync.State == 1
		AppConfig.ApplyCommandParallelism = applyWorkers
		AppConfig.AlwaysShowMenuBar = chkAlwaysMenu.State == 1
		AppConfig.InfoPanelCPUGPU = chkCPUGPU.State == 1
		AppConfig.EscTogglePanels = chkEscToggle.State == 1
		AppConfig.TerminalCtrlNWorkspace = chkTerminalCtrlN.State == 1
		AppConfig.DefaultFileOpMode = comboMode.Menu.SelectPos
		AppConfig.FileOpPathDisplay = comboPath.Menu.SelectPos
		AppConfig.MacroRecordFormat = comboMacro.Menu.SelectPos
		pf.applyNavigationMode()
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

	chkUseTrash := vtui.NewCheckbox(0, 0, Msg("ConfirmationsSettings.UseTrash"), false)
	if AppConfig.UseTrash {
		chkUseTrash.State = 1
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
	dlg.AddItem(chkUseTrash)
	dlg.AddItem(chkExit)
	dlg.AddItem(chkDelFocus)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(chkCopy, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkMove, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkDelete, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(chkUseTrash, vtui.Margins{}, vtui.AlignLeft)
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
		AppConfig.UseTrash = chkUseTrash.State == 1
		AppConfig.ConfirmExit = chkExit.State == 1
		AppConfig.DeleteCancelFocused = chkDelFocus.State == 1
		SaveConfig()
		dlg.Close()
		pf.RefreshAll()
	}

	vtui.FrameManager.Push(dlg)
}
func actionMouseWheelSettings(pf *PanelsFrame) {
	const width, height = 56, 17
	dlg := vtui.NewCenteredDialog(width, height, Msg("MouseWheel.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	lblHint := vtui.NewText(0, 0, Msg("MouseWheel.Hint"), 0)

	lblPanels := vtui.NewText(0, 0, Msg("MouseWheel.Panels"), 0)
	editPanelUp := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelPanelUp))
	editPanelDown := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelPanelDown))
	lblPanelUp := vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editPanelUp)
	lblPanelDown := vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editPanelDown)

	lblEditor := vtui.NewText(0, 0, Msg("MouseWheel.Editor"), 0)
	editEditorUp := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelEditorUp))
	editEditorDown := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelEditorDown))
	lblEditorUp := vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editEditorUp)
	lblEditorDown := vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editEditorDown)

	lblViewer := vtui.NewText(0, 0, Msg("MouseWheel.Viewer"), 0)
	editViewerUp := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelViewerUp))
	editViewerDown := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelViewerDown))
	lblViewerUp := vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editViewerUp)
	lblViewerDown := vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editViewerDown)

	lblMenus := vtui.NewText(0, 0, Msg("MouseWheel.Menus"), 0)
	editMenuUp := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelMenuUp))
	editMenuDown := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelMenuDown))
	lblMenuUp := vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editMenuUp)
	lblMenuDown := vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editMenuDown)

	lblTables := vtui.NewText(0, 0, Msg("MouseWheel.Tables"), 0)
	editTableUp := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelTableUp))
	editTableDown := vtui.NewEdit(0, 0, 5, strconv.Itoa(AppConfig.WheelTableDown))
	lblTableUp := vtui.NewLabel(0, 0, Msg("MouseWheel.Up"), editTableUp)
	lblTableDown := vtui.NewLabel(0, 0, Msg("MouseWheel.Down"), editTableDown)

	btnOk := vtui.NewButton(0, 0, Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	// 2. Add to Dialog
	dlg.AddItem(lblHint)
	dlg.AddItem(lblPanels)
	dlg.AddItem(lblPanelUp)
	dlg.AddItem(editPanelUp)
	dlg.AddItem(lblPanelDown)
	dlg.AddItem(editPanelDown)
	dlg.AddItem(lblEditor)
	dlg.AddItem(lblEditorUp)
	dlg.AddItem(editEditorUp)
	dlg.AddItem(lblEditorDown)
	dlg.AddItem(editEditorDown)
	dlg.AddItem(lblViewer)
	dlg.AddItem(lblViewerUp)
	dlg.AddItem(editViewerUp)
	dlg.AddItem(lblViewerDown)
	dlg.AddItem(editViewerDown)
	dlg.AddItem(lblMenus)
	dlg.AddItem(lblMenuUp)
	dlg.AddItem(editMenuUp)
	dlg.AddItem(lblMenuDown)
	dlg.AddItem(editMenuDown)
	dlg.AddItem(lblTables)
	dlg.AddItem(lblTableUp)
	dlg.AddItem(editTableUp)
	dlg.AddItem(lblTableDown)
	dlg.AddItem(editTableDown)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	// 3. Layout Configuration
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(lblHint, vtui.Margins{}, vtui.AlignLeft)

	addWheelRow := func(area *vtui.Text, lblUp, lblDown *vtui.Text, editUp, editDown *vtui.Edit) {
		row := vtui.NewHBoxLayout(0, 0, width-4, 1)
		row.Add(area, vtui.Margins{Right: 2}, vtui.AlignLeft)
		row.Add(lblUp, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(editUp, vtui.Margins{Right: 2}, vtui.AlignLeft)
		row.Add(lblDown, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(editDown, vtui.Margins{}, vtui.AlignLeft)
		vbox.Add(row, vtui.Margins{Top: 1}, vtui.AlignFill)
	}
	addWheelRow(lblPanels, lblPanelUp, lblPanelDown, editPanelUp, editPanelDown)
	addWheelRow(lblEditor, lblEditorUp, lblEditorDown, editEditorUp, editEditorDown)
	addWheelRow(lblViewer, lblViewerUp, lblViewerDown, editViewerUp, editViewerDown)
	addWheelRow(lblMenus, lblMenuUp, lblMenuDown, editMenuUp, editMenuDown)
	addWheelRow(lblTables, lblTableUp, lblTableDown, editTableUp, editTableDown)

	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

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
	const width, height = 56, 18
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
		CheckForUpdates(pf, true)
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

				vtui.ShowToast(fmt.Sprintf("Imported %d new commands from far2l.", len(merged)-len(current)), 3*time.Second)
			})
		})
	}
}

func actionAppearanceSettings(pf *PanelsFrame) {
	const width, height = 64, 28
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
	editFont := vtui.NewEdit(0, 0, 30, AppConfig.GuiFont)
	lblFont := vtui.NewLabel(0, 0, Msg("AppearanceSettings.Font"), editFont)
	chkSystemMonospace := vtui.NewCheckbox(0, 0, Msg("AppearanceSettings.UseSystemMonospace"), false)
	if AppConfig.GuiUseSystemMonospace {
		chkSystemMonospace.State = 1
	}
	updateFontEditor := func() {
		usePlatformFont := chkSystemMonospace.State == 1 && (runtime.GOOS == "windows" || runtime.GOOS == "darwin")
		lblFont.SetDisabled(usePlatformFont)
		editFont.SetDisabled(usePlatformFont)
	}
	chkSystemMonospace.OnChange = func(int) { updateFontEditor() }
	updateFontEditor()

	editSize := vtui.NewEdit(0, 0, 6, fmt.Sprintf("%d", AppConfig.GuiFontSize))
	editSize.Validator = &vtui.IntRangeValidator{Min: 6, Max: 72}
	lblSize := vtui.NewLabel(0, 0, Msg("AppearanceSettings.FontSize"), editSize)

	editTitle := vtui.NewEdit(0, 0, 30, AppConfig.ConsoleTitleTemplate)
	lblTitle := vtui.NewLabel(0, 0, Msg("AppearanceSettings.TitleTemplate"), editTitle)

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
	comboWorkspaceTabs.Edit.SetText(workspaceTabModes[workspaceTabSelection])
	lblWorkspaceTabs := vtui.NewLabel(0, 0, Msg("AppearanceSettings.WorkspaceTabs"), comboWorkspaceTabs)

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
	comboWorkspaceNumbering.Edit.SetText(workspaceNumberingModes[workspaceNumberingSelection])
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
	dlg.AddItem(editFont)
	dlg.AddItem(lblSize)
	dlg.AddItem(editSize)
	dlg.AddItem(lblTitle)
	dlg.AddItem(editTitle)
	dlg.AddItem(lblWorkspaceTabs)
	dlg.AddItem(comboWorkspaceTabs)
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
	vbox.Add(editFont, vtui.Margins{}, vtui.AlignFill)

	rowSize := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowSize.Add(lblSize, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowSize.Add(editSize, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowSize, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Add(lblTitle, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editTitle, vtui.Margins{}, vtui.AlignFill)

	rowWorkspaceTabs := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowWorkspaceTabs.Add(lblWorkspaceTabs, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowWorkspaceTabs.Add(comboWorkspaceTabs, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowWorkspaceTabs, vtui.Margins{Top: 1}, vtui.AlignFill)

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
			vtui.ShowMessageOn(dlg, " Info ", "Current colors successfully exported to:\n"+colorsPath+"\n\nYou can edit this file to customize your palette.", []string{"&Ok"})
		}
	}
	btnOk.OnClick = func() {
		if len(names) > 0 {
			AppConfig.ColorStyle = names[comboStyle.Menu.SelectPos]
		}
		useSystemMonospace := chkSystemMonospace.State == 1
		fontChanged := AppConfig.GuiUseSystemMonospace != useSystemMonospace || AppConfig.GuiFont != editFont.GetText() || fmt.Sprintf("%d", AppConfig.GuiFontSize) != editSize.GetText()

		AppConfig.ConsoleTitleTemplate = editTitle.GetText()
		AppConfig.GuiUseSystemMonospace = useSystemMonospace
		AppConfig.GuiFont = editFont.GetText()
		fmt.Sscanf(editSize.GetText(), "%d", &AppConfig.GuiFontSize)
		if AppConfig.GuiFontSize <= 0 {
			AppConfig.GuiFontSize = defaultGuiFontSize(runtime.GOOS)
		}
		AppConfig.KeepTerminalCursor = chkCursor.State == 1
		vtui.ManageCursorStyle = !AppConfig.KeepTerminalCursor
		AppConfig.EnforceColorCorrection = chkContrast.State == 1
		AppConfig.WorkspaceTabMode = comboWorkspaceTabs.Menu.SelectPos
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
	dlg := vtui.NewCenteredDialog(width, height, Msg("Plugins.Title"))
	dlg.ShowClose = true

	lb := vtui.NewListBox(0, 0, 56, 10, AppConfig.RegisteredPlugins)

	btnAdd := vtui.NewButton(0, 0, Msg("Plugins.BtnAdd"))
	btnDel := vtui.NewButton(0, 0, Msg("Plugins.BtnRemove"))
	btnPerms := vtui.NewButton(0, 0, Msg("Plugins.BtnPermissions"))
	btnPerms.OnClick = func() { actionPluginPermissions(PluginPermissions()) }
	btnClose := vtui.NewButton(0, 0, Msg("Plugins.BtnClose"))

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
	if fsp == nil {
		return
	}

	name := fsp.getRawSelectedName()
	if name == "" || name == ".." {
		return
	}

	fullPath := fsp.vfs.Join(fsp.vfs.GetPath(), name)

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		item, err := fsp.vfs.Stat(ctx.Context, fullPath)
		ctx.RunOnUI(func() {
			if err != nil {
				vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
				return
			}
			ShowAttributesDialog(pf, fsp.vfs, fullPath, item)
		})
	})
}

func actionLanguage(pf *PanelsFrame) {
	type langInfo struct {
		code string
		name string
	}
	langs := []langInfo{{"en", "English"}}

	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "lang")

	// We add "lang" to support running f4 via "go run ." from the project root
	dirs := []string{filepath.Join(exeDir, "lang"), userDir, "lang"}
	seen := map[string]bool{"en": true}

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
					langs = append(langs, langInfo{code, name})
					seen[code] = true
				}
			}
		}
	}

	menu := vtui.NewVMenu(Msg("Language.Title"))
	currIdx := 0
	for i, l := range langs {
		menu.AddItem(vtui.MenuItem{Text: l.name})
		if l.code == AppConfig.Language {
			currIdx = i
		}
	}

	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	w, h := 30, len(langs)+2
	if h > 15 {
		h = 15
	}
	x := (scrW - w) / 2
	y := (scrH - h) / 2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	menu.SetSelectPos(currIdx)

	menu.OnAction = func(idx int) {
		AppConfig.Language = langs[idx].code
		SaveConfig()
		InitLang()
		// Key binding help topics are generated from the action
		// registry and must be rebuilt in the new language.
		InitHelpSystem()
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(Msg("Info.Title"), Msg("Language.Changed"), []string{Msg("vtui.Ok")})
			vtui.FrameManager.Redraw()
		})
	}

	vtui.FrameManager.Push(menu)
}

func actionFallbackLanguage(pf *PanelsFrame) {
	type langInfo struct {
		code string
		name string
	}
	langs := []langInfo{{"", Msg("LanguageSettings.None")}, {"en", "English"}}

	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "lang")

	dirs := []string{filepath.Join(exeDir, "lang"), userDir, "lang"}
	seen := map[string]bool{"en": true}

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
					langs = append(langs, langInfo{code, name})
					seen[code] = true
				}
			}
		}
	}

	menu := vtui.NewVMenu(Msg("LanguageSettings.Fallback"))
	currIdx := 0
	for i, l := range langs {
		menu.AddItem(vtui.MenuItem{Text: l.name})
		if l.code == AppConfig.FallbackLanguage {
			currIdx = i
		}
	}

	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	w, h := 30, len(langs)+2
	if h > 15 {
		h = 15
	}
	x := (scrW - w) / 2
	y := (scrH - h) / 2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	menu.SetSelectPos(currIdx)

	menu.OnAction = func(idx int) {
		AppConfig.FallbackLanguage = langs[idx].code
		SaveConfig()
		InitLang()
		InitHelpSystem()
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(Msg("Info.Title"), Msg("Language.Changed"), []string{Msg("vtui.Ok")})
			vtui.FrameManager.Redraw()
		})
	}

	vtui.FrameManager.Push(menu)
}
func actionHelpLanguage(pf *PanelsFrame) {
	type langInfo struct {
		code string
		name string
	}
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
					langs = append(langs, langInfo{code, name})
					seen[code] = true
				}
			}
		}
	}

	menu := vtui.NewVMenu(Msg("HelpLanguage.Title"))
	currIdx := 0
	for i, l := range langs {
		menu.AddItem(vtui.MenuItem{Text: l.name})
		if l.code == AppConfig.HelpLanguage {
			currIdx = i
		}
	}

	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	w, h := 30, len(langs)+2
	if h > 15 {
		h = 15
	}
	x := (scrW - w) / 2
	y := (scrH - h) / 2
	menu.SetPosition(x, y, x+w-1, y+h-1)
	menu.SetSelectPos(currIdx)

	menu.OnAction = func(idx int) {
		AppConfig.HelpLanguage = langs[idx].code
		SaveConfig()
		InitHelpSystem()
		vtui.FrameManager.PostTask(func() {
			vtui.ShowMessage(Msg("Info.Title"), Msg("HelpLanguage.Changed"), []string{Msg("vtui.Ok")})
			vtui.FrameManager.Redraw()
		})
	}

	vtui.FrameManager.Push(menu)
}

func getLanguageName(code string) string {
	if code == "en" || code == "eng" {
		return "English"
	}
	exeDir := filepath.Dir(os.Args[0])
	userDir := filepath.Join(GetF4ConfigDir(), "lang")
	candidates := []string{
		filepath.Join(userDir, code+".lng"),
		filepath.Join(exeDir, "lang", code+".lng"),
		filepath.Join("lang", code+".lng"),
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			ini := LoadIni(cand)
			if name := ini.GetString("Language", "Name", ""); name != "" {
				return name
			}
		}
	}
	return strings.ToUpper(code)
}
