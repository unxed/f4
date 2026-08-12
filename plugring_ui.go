package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/vtui"
)

type plugRingRow struct {
	item   PlugRingItem
	status string
	// header is set on a category heading, which is a row with no plugin
	// behind it.
	header string
	// note says why this entry cannot be used here, and takes the place of
	// the description when it does.
	note string
}

func (r plugRingRow) GetCellText(col int) string {
	if r.header != "" {
		if col == 0 {
			return r.header
		}
		return ""
	}
	switch col {
	case 0:
		return r.item.Name
	case 1:
		return r.item.Version
	case 2:
		return r.status
	case 3:
		return r.item.Author
	case 4:
		text := r.item.Description
		if r.note != "" {
			text = r.note
		}
		return runewidth.Truncate(text, 40, "...")
	}
	return ""
}

func (r plugRingRow) GetCellAttr(col int, def uint64) uint64 {
	switch {
	case r.header != "":
		return themedForeground(def, vtui.ColDialogHighlightText)
	case r.note != "":
		return vtui.DimColor(def)
	case r.status == "Update":
		return themedForeground(def, vtui.ColDialogHighlightText)
	case r.status == "Installed":
		return themedForeground(def, vtui.ColDialogText)
	}
	return def
}

// BuildPlugRingRows lays a catalog out as a table: a heading per category,
// then its plugins.
//
// It returns a parallel slice saying which plugin each row belongs to, with a
// nil where a heading is. The table is indexed by position, so without that
// slice pressing Enter on the "Archives" heading would install whichever
// plugin happened to sit at the same index.
func BuildPlugRingRows(items []PlugRingItem, installed map[string]PlugRingItem) ([]vtui.TableRow, []*PlugRingItem) {
	order, grouped := GroupPlugRingByCategory(items)

	rows := make([]vtui.TableRow, 0, len(items)+len(order))
	selectable := make([]*PlugRingItem, 0, len(items)+len(order))

	for _, category := range order {
		rows = append(rows, plugRingRow{header: PlugRingCategoryTitle(category)})
		selectable = append(selectable, nil)

		for _, item := range grouped[category] {
			entry := item

			status := "Not installed"
			if inst, ok := installed[entry.ID]; ok {
				if inst.Version != entry.Version {
					status = "Update"
				} else {
					status = "Installed"
				}
			}

			note := ""
			if ok, reason := PlugRingItemRunsHere(entry); !ok {
				status = "Unavailable"
				note = reason
			}

			rows = append(rows, plugRingRow{item: entry, status: status, note: note})
			selectable = append(selectable, &entry)
		}
	}
	return rows, selectable
}

func actionPlugRing(pf *PanelsFrame) {
	w, h := 76, 22

	btnInstall := vtui.NewButton(0, 0, Msg("PlugRing.BtnInstall"))
	btnRemove := vtui.NewButton(0, 0, Msg("PlugRing.BtnRemove"))
	btnRefresh := vtui.NewButton(0, 0, Msg("PlugRing.BtnRefresh"))
	btnClose := vtui.NewButton(0, 0, Msg("PlugRing.BtnClose"))

	dlg, table := vtui.NewTableDialog(w, h, Msg("PlugRing.Title"), []vtui.TableColumn{
		{Title: "Name", Width: 16},
		{Title: "Version", Width: 8},
		{Title: "Status", Width: 13},
		{Title: "Author", Width: 10},
		{Title: "Description", Width: 0},
	}, btnInstall, btnRemove, btnRefresh, btnClose)
	useDialogTableColors(table)
	table.Sortable = true    // click a column header to sort, again to reverse
	table.QuickSearch = true // type to fuzzy-filter (Myers bit-vector)
	table.ShowScrollBar = true

	btnClose.OnClick = func() { dlg.Close() }

	var items []PlugRingItem
	// shown[i] is the plugin on row i, or nil when row i is a category
	// heading.
	var shown []*PlugRingItem

	refresh := func() {
		table.SetRows(nil)
		vtui.FrameManager.Redraw()

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			fetched, err := FetchCatalog(ctx.Context)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessageOn(dlg, " Error ", fmt.Sprintf("Failed to fetch catalog:\n%v", err), []string{"&Ok"})
					return
				}
				items = fetched
				var rows []vtui.TableRow
				rows, shown = BuildPlugRingRows(items, GetInstalledPlugRingItems())
				table.SetRows(rows)
				vtui.FrameManager.Redraw()
			})
		})
	}

	btnRefresh.OnClick = refresh

	// selected is nil on a category heading, which is not a plugin.
	selected := func() *PlugRingItem {
		idx := table.SelectPos
		if idx >= 0 && idx < len(shown) {
			return shown[idx]
		}
		return nil
	}

	btnInstall.OnClick = func() {
		if item := selected(); item != nil {
			actionInstallPlugRingItem(pf, dlg, *item, refresh)
		}
	}
	btnRemove.OnClick = func() {
		if item := selected(); item != nil {
			actionRemovePlugRingItem(pf, dlg, *item, refresh)
		}
	}

	table.OnAction = func(idx int) {
		btnInstall.OnClick()
	}

	vtui.FrameManager.Push(dlg)
	refresh()
}

// entrypointNeedsInterpreterOnPath reports whether the first word of an
// entrypoint is a command that must already exist on the user's PATH. A bare
// .lua or .wasm entrypoint names a file f4 runs itself, so there is nothing to
// look up, and warning that "notes.lua" is missing would send the user looking
// for a package that does not exist.
func entrypointNeedsInterpreterOnPath(entrypoint string) bool {
	if IsLuaEntrypoint(entrypoint) || IsWasmEntrypoint(entrypoint) {
		return false
	}
	fields := strings.Fields(entrypoint)
	if len(fields) == 0 {
		return false
	}
	interpreter := fields[0]
	return !strings.ContainsAny(interpreter, "/\\") && !strings.HasPrefix(interpreter, ".")
}

func actionInstallPlugRingItem(pf *PanelsFrame, parent *vtui.Window, item PlugRingItem, refresh func()) {
	// 0. The distribution policy, enforced rather than merely documented.
	// An entry that breaks it is not installed silently; the user is told
	// exactly what is wrong and may insist, because the catalog in the wild
	// predates the rule.
	if problem := PlugRingItemProblem(item); problem != "" {
		msg := fmt.Sprintf("This catalog entry does not meet f4's distribution policy:\n\n%s\n\nSee PLUGRING.md. Installing anyway is your decision.", problem)
		if pf.Message(" Policy Warning ", msg, []string{"&Install Anyway", "Cancel"}) != 0 {
			return
		}
	}
	if ok, reason := PlugRingItemRunsHere(item); !ok {
		msg := fmt.Sprintf("This plugin %s.\n\nIt will install, but f4 will not be able to run it.", reason)
		if pf.Message(" Cannot Run Here ", msg, []string{"&Install Anyway", "Cancel"}) != 0 {
			return
		}
	}
	// 1. Implicit dependency check from Entrypoint interpreter
	if entrypointNeedsInterpreterOnPath(item.Entrypoint) {
		interpreter := strings.Fields(item.Entrypoint)[0]
		if _, err := exec.LookPath(interpreter); err != nil {
			msg := fmt.Sprintf("Warning: This plugin requires '%s' to run, but it was not found in your system's PATH.\n\nPlease install '%s' first, or the plugin might fail to load.", interpreter, interpreter)
			if pf.Message(" Missing Dependency ", msg, []string{"&Install Anyway", "Cancel"}) != 0 {
				return
			}
		}
	}

	// 2. Explicit dependencies check
	for _, dep := range item.Dependencies {
		if _, err := exec.LookPath(dep); err != nil {
			msg := fmt.Sprintf("Warning: This plugin requires '%s', but it was not found in your system's PATH.\n\nPlease install it, or the plugin might fail to load.", dep)
			if pf.Message(" Missing Dependency ", msg, []string{"&Install Anyway", "Cancel"}) != 0 {
				return
			}
		}
	}

	url := ResolveAssetURL(item.URL)
	isTarGz := strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz")
	isArchive := isTarGz || strings.HasSuffix(url, ".zip")

	plugringDir := filepath.Join(GetF4ConfigDir(), "plugring")
	pluginDir := filepath.Join(plugringDir, item.ID)

	pf.RunProgressTask(" Installing Plugin ", "Downloading "+item.Name+"...", false, func(ctx context.Context, update func(msg string, percent int)) error {
		var archiveBytes []byte
		if strings.HasPrefix(url, "file://") {
			localPath := strings.TrimPrefix(url, "file://")
			data, err := os.ReadFile(localPath)
			if err != nil {
				return fmt.Errorf("failed to read local test file %s: %w", localPath, err)
			}
			archiveBytes = data
		} else {
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return err
			}
			req.Header.Set("User-Agent", "f4-plugring")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				return fmt.Errorf("download failed with status %d", resp.StatusCode)
			}

			contentLength := resp.ContentLength
			var archiveData bytes.Buffer
			buf := make([]byte, 32*1024)
			var downloaded int64

			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					archiveData.Write(buf[:n])
					downloaded += int64(n)
					pct := 0
					if contentLength > 0 {
						pct = int((downloaded * 100) / contentLength)
					}
					update("Downloading...", pct)
				}
				if readErr != nil {
					if readErr == io.EOF {
						break
					}
					return readErr
				}
			}
			archiveBytes = archiveData.Bytes()
		}

		update("Extracting files...", -1)

		os.RemoveAll(pluginDir)
		os.MkdirAll(pluginDir, 0755)

		if isArchive {
			var err error
			if isTarGz {
				err = extractTarGzToDir(archiveBytes, pluginDir)
			} else {
				err = extractZipToDir(archiveBytes, pluginDir)
			}
			if err != nil {
				os.RemoveAll(pluginDir)
				return fmt.Errorf("failed to extract archive: %w", err)
			}
		} else {
			filename := filepath.Base(url)
			err := os.WriteFile(filepath.Join(pluginDir, filename), archiveBytes, 0755)
			if err != nil {
				os.RemoveAll(pluginDir)
				return fmt.Errorf("failed to save plugin file: %w", err)
			}
		}

		// setup_cmd used to be run here: an arbitrary shell command, with the
		// user's privileges, at install time, from a catalog entry nobody
		// reads. That is worse than shipping a binary, and no confirmation
		// dialog makes it acceptable, so it is not run at all. A plugin that
		// needs a build step does not belong in this catalog.
		if item.SetupCmd != "" {
			vtui.DebugLog("PLUGRING: ignoring setup_cmd of %q: %s", item.ID, item.SetupCmd)
		}

		manifestData, _ := json.MarshalIndent(item, "", "  ")
		os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifestData, 0644)

		return nil
	}, func(err error) {
		if err != nil {
			if err != context.Canceled {
				vtui.ShowMessageOn(parent, " Error ", fmt.Sprintf("Installation failed:\n%v", err), []string{"&Ok"})
			}
		} else {
			if GlobalPluginManager != nil {
				GlobalPluginManager.loadSinglePlugRingItem(item)
			}
			vtui.ShowMessageOn(parent, " Success ", "Plugin installed and loaded successfully!", []string{"&Ok"})
			refresh()
		}
	})
}

func actionRemovePlugRingItem(pf *PanelsFrame, parent *vtui.Window, item PlugRingItem, refresh func()) {
	plugringDir := filepath.Join(GetF4ConfigDir(), "plugring")
	pluginDir := filepath.Join(plugringDir, item.ID)

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		vtui.ShowMessageOn(parent, " Info ", "Plugin is not installed.", []string{"&Ok"})
		return
	}

	dlg := vtui.ShowMessageOn(parent, " Remove Plugin ", fmt.Sprintf("Do you want to completely remove %s?", item.Name), []string{"&Remove", "Cancel"})
	// Destructive — wipes the plugin directory. Render on the WarnDialog
	// palette so the confirmation reads as an alarm.
	dlg.IsWarning = true
	dlg.OnResult = func(code int) {
		if code == 0 {
			// Grants belong to the plugin, not to its id. Leaving them
			// behind would hand them to whatever is installed here next.
			if err := PluginPermissions().Forget(item.ID); err != nil {
				vtui.DebugLog("PLUGRING: cannot drop the permissions of %q: %v", item.ID, err)
			}
			err := os.RemoveAll(pluginDir)
			if err != nil {
				vtui.ShowMessageOn(parent, " Error ", fmt.Sprintf("Removal failed:\n%v", err), []string{"&Ok"})
			} else {
				vtui.ShowMessageOn(parent, " Success ", "Plugin removed successfully.\nRestart f4 to fully unload.", []string{"&Ok"})
				refresh()
			}
		}
	}
}
