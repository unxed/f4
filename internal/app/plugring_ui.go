package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/unpack"
	"github.com/unxed/vtui"
)

type plugRingRow struct {
	item   plughost.PlugRingItem
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
		return theme.ThemedForeground(def, vtui.ColDialogHighlightText)
	case r.note != "":
		return vtui.DimColor(def)
	case r.status == "Update":
		return theme.ThemedForeground(def, vtui.ColDialogHighlightText)
	case r.status == "Installed":
		return theme.ThemedForeground(def, vtui.ColDialogText)
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
func BuildPlugRingRows(items []plughost.PlugRingItem, installed map[string]plughost.PlugRingItem) ([]vtui.TableRow, []*plughost.PlugRingItem) {
	order, grouped := plughost.GroupPlugRingByCategory(items)

	rows := make([]vtui.TableRow, 0, len(items)+len(order))
	selectable := make([]*plughost.PlugRingItem, 0, len(items)+len(order))

	for _, category := range order {
		rows = append(rows, plugRingRow{header: plughost.PlugRingCategoryTitle(category)})
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
			if ok, reason := plughost.PlugRingItemRunsHere(entry); !ok {
				status = "Unavailable"
				note = reason
			}

			rows = append(rows, plugRingRow{item: entry, status: status, note: note})
			selectable = append(selectable, &entry)
		}
	}
	return rows, selectable
}

// plugRingCatalog is the catalog read the dialog performs in the background.
// It is a variable so a test that only wants the dialog's layout can keep the
// fetch from escaping: the goroutine outlives the test that started it, and
// plughost.FetchCatalog reads package state that another test writes.
//
// It is plughost.FetchPlugRingCatalog, not plughost.FetchCatalog directly, so
// the dialog shows f4's first-party native plugins (cloudfox, f4#1178 part 3)
// alongside the community catalog, from one combined list.
//
// refresh reads this on the goroutine that starts the task, not on the one that
// runs it, so replacing it is safe while a refresh is in flight.
var plugRingCatalog = plughost.FetchPlugRingCatalog

func actionPlugRing(pf *panel.PanelsFrame) {
	actionPlugRingFocused(pf, "")
}

// actionPlugRingFocused is actionPlugRing plus one thing: once the catalog
// (community + first-party, f4#1178 part 3) has loaded, it selects and
// scrolls to the row whose PlugRingItem.ID equals focusItemID, instead of
// leaving the table wherever SetRows put it (row 0). An empty focusItemID
// behaves exactly like actionPlugRing.
//
// This is f4#1178, part 4 of 4: a lite build's "CloudFox" drive entry
// (cloud_storage_lite.go) opens PlugRing landed directly on the "cloudfox"
// row it now carries, rather than a generic browse-everything dialog the
// user has to search through themselves.
func actionPlugRingFocused(pf *panel.PanelsFrame, focusItemID string) {
	w, h := 76, 22

	btnInstall := vtui.NewButton(0, 0, i18n.Msg("PlugRing.BtnInstall"))
	btnRemove := vtui.NewButton(0, 0, i18n.Msg("PlugRing.BtnRemove"))
	btnRefresh := vtui.NewButton(0, 0, i18n.Msg("PlugRing.BtnRefresh"))
	btnClose := vtui.NewButton(0, 0, i18n.Msg("PlugRing.BtnClose"))

	dlg, table := vtui.NewTableDialog(w, h, i18n.Msg("PlugRing.Title"), []vtui.TableColumn{
		{Title: i18n.Msg("PlugRing.ColName"), Width: 16},
		{Title: i18n.Msg("PlugRing.ColVersion"), Width: 8},
		{Title: i18n.Msg("PlugRing.ColStatus"), Width: 13},
		{Title: i18n.Msg("PlugRing.ColAuthor"), Width: 10},
		{Title: i18n.Msg("PlugRing.ColDescription"), Width: 0},
	}, btnInstall, btnRemove, btnRefresh, btnClose)
	theme.UseTableColors(table)
	table.Sortable = true    // click a column header to sort, again to reverse
	table.QuickSearch = true // type to fuzzy-filter (Myers bit-vector)
	table.ShowScrollBar = true

	btnClose.OnClick = func() { dlg.Close() }

	var items []plughost.PlugRingItem
	// shown[i] is the plugin on row i, or nil when row i is a category
	// heading.
	var shown []*plughost.PlugRingItem
	var refreshTask *vtui.TaskContext
	dlg.OnResult = func(int) {
		if refreshTask != nil {
			refreshTask.Cancel()
		}
	}

	refresh := func() {
		if refreshTask != nil {
			refreshTask.Cancel()
		}
		table.SetRows(nil)
		vtui.FrameManager.Redraw()

		fetch := plugRingCatalog
		refreshTask = vtui.RunAsync(func(ctx *vtui.TaskContext) {
			fetched, err := fetch(ctx.Context)
			if ctx.Err() != nil {
				return
			}
			ctx.RunOnUI(func() {
				if ctx.Err() != nil || dlg.IsDone() {
					return
				}
				if err != nil {
					vtui.ShowMessageOn(dlg, " Error ", fmt.Sprintf("Failed to fetch catalog:\n%v", err), []string{"&Ok"})
					return
				}
				items = fetched
				var rows []vtui.TableRow
				rows, shown = BuildPlugRingRows(items, plughost.GetInstalledPlugRingItems())
				table.SetRows(rows)
				if focusItemID != "" {
					for i, entry := range shown {
						if entry != nil && entry.ID == focusItemID {
							table.SelectPos = i
							table.EnsureVisible()
							break
						}
					}
				}
				vtui.FrameManager.Redraw()
			})
		})
	}

	btnRefresh.OnClick = refresh

	// selected is nil on a category heading, which is not a plugin.
	selected := func() *plughost.PlugRingItem {
		idx := table.SelectPos
		if idx >= 0 && idx < len(shown) {
			return shown[idx]
		}
		return nil
	}

	btnInstall.OnClick = func() {
		if item := selected(); item != nil {
			// The installer may use PanelsFrame.Message for a synchronous
			// confirmation. Run that orchestration off the UI goroutine: Message
			// posts the dialog to the UI queue and waits for its answer, so calling
			// it here would wait for the same goroutine that has to show the
			// dialog (f4#1710).
			go actionInstallPlugRingItem(pf, dlg, *item, refresh)
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
	if plughost.IsLuaEntrypoint(entrypoint) || plughost.IsWasmEntrypoint(entrypoint) {
		return false
	}
	fields := strings.Fields(entrypoint)
	if len(fields) == 0 {
		return false
	}
	interpreter := fields[0]
	return !strings.ContainsAny(interpreter, "/\\") && !strings.HasPrefix(interpreter, ".")
}

func actionInstallPlugRingItem(pf *panel.PanelsFrame, parent *vtui.Window, item plughost.PlugRingItem, refresh func()) {
	if !safePlugRingID(item.ID) {
		vtui.ShowMessageOn(parent, " Error ", "Plugin catalog contains an invalid ID.", []string{"&Ok"})
		return
	}
	// 0. The distribution policy, enforced rather than merely documented.
	// An entry that breaks it is not installed silently; the user is told
	// exactly what is wrong and may insist, because the catalog in the wild
	// predates the rule.
	if problem := plughost.PlugRingItemProblem(item); problem != "" {
		msg := fmt.Sprintf("This catalog entry does not meet f4's distribution policy:\n\n%s\n\nSee PLUGRING.md. Installing anyway is your decision.", problem)
		if pf.Message(" Policy Warning ", msg, []string{"&Install Anyway", "Cancel"}) != 0 {
			return
		}
	}
	if ok, reason := plughost.PlugRingItemRunsHere(item); !ok {
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

	url := plughost.ResolveAssetURL(item.URL)
	isTarGz := strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz")
	isArchive := isTarGz || strings.HasSuffix(url, ".zip")

	plugringDir := filepath.Join(config.GetF4ConfigDir(), "plugring")
	pluginDir := filepath.Join(plugringDir, item.ID)

	pf.RunProgressTask(" Installing Plugin ", "Downloading "+item.Name+"...", false, func(ctx context.Context, updateProgress func(msg string, percent int)) error {
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

			resp, err := netproxy.HTTPClient(0).Do(req)
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
					updateProgress("Downloading...", pct)
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

		updateProgress("Extracting files...", -1)

		if err := os.RemoveAll(pluginDir); err != nil {
			return fmt.Errorf("failed to replace existing plugin: %w", err)
		}
		if err := os.MkdirAll(pluginDir, 0700); err != nil {
			return fmt.Errorf("failed to create plugin directory: %w", err)
		}

		if isArchive {
			var err error
			if isTarGz {
				err = unpack.TarGz(archiveBytes, pluginDir)
			} else {
				err = unpack.Zip(archiveBytes, pluginDir)
			}
			if err != nil {
				os.RemoveAll(pluginDir)
				return fmt.Errorf("failed to extract archive: %w", err)
			}
		} else {
			filename := filepath.Base(url)
			// #nosec G306 G703 -- downloaded plugins may be native executables; mode 0700 is owner-only, safePlugRingID rejected traversal, and filename is reduced with filepath.Base.
			err := os.WriteFile(filepath.Join(pluginDir, filename), archiveBytes, 0700)
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
			finishPlugRingInstall(parent, item, refresh)
		}
	})
}

// loadPlugRingItem starts a just-installed plugin. It is a variable so a test
// can stand in for a plugin that takes its time.
var loadPlugRingItem = func(item plughost.PlugRingItem) {
	if plughost.GlobalPluginManager != nil {
		plughost.GlobalPluginManager.LoadSinglePlugRingItem(item)
	}
}

// finishPlugRingInstall loads the installed plugin and reports the result.
//
// It is called on the UI goroutine, and loading must not run there (f4#1710):
// it starts the plugin process and asks the user's permission to run it, and
// that prompt is a dialog which only the UI goroutine can show. Loading on it
// meant waiting for an answer that the waiting itself kept from being asked,
// so the window froze for the whole two-minute prompt timeout and could not
// even be closed. The load runs in the background and the message comes back
// to the UI goroutine afterwards.
func finishPlugRingInstall(parent *vtui.Window, item plughost.PlugRingItem, refresh func()) {
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		loadPlugRingItem(item)
		ctx.RunOnUI(func() {
			vtui.ShowMessageOn(parent, " Success ", "Plugin installed and loaded successfully!", []string{"&Ok"})
			refresh()
		})
	})
}

func actionRemovePlugRingItem(pf *panel.PanelsFrame, parent *vtui.Window, item plughost.PlugRingItem, refresh func()) {
	if !safePlugRingID(item.ID) {
		vtui.ShowMessageOn(parent, " Error ", "Plugin catalog contains an invalid ID.", []string{"&Ok"})
		return
	}
	plugringDir := filepath.Join(config.GetF4ConfigDir(), "plugring")
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
			if err := plughost.PluginPermissions().Forget(item.ID); err != nil {
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

// safePlugRingID accepts only a single, clean path element. The ID arrives from
// a remote catalog and names the directory install replaces and remove deletes,
// so anything that could resolve somewhere else is refused rather than cleaned.
// "." is the trap the obvious check misses: it holds no separator and no "..",
// yet it names the plugring directory itself, so removing it would take every
// installed plugin with it.
func safePlugRingID(id string) bool {
	if id == "" || id == "." {
		return false
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, "/\\\x00") {
		return false
	}
	return filepath.Clean(id) == id
}
