package settings

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/sdk/f4settings"
)

type settingsRecordStore struct {
	collection f4settings.Collection
	path       string
	load       func() ([]f4settings.Record, error)
	save       func([]f4settings.Record) error
	validate   func([]f4settings.Record) error
	afterSave  func([]f4settings.Record)
}
type coreRecordSettingsProvider struct{ stores []settingsRecordStore }

func recordField(id, label, description string, kind f4settings.Kind) f4settings.Field {
	return f4settings.Field{ID: id, Label: f4settings.Text{English: label, Key: "SettingsCenter." + id + ".Label"}, Description: f4settings.Text{English: description, Key: "SettingsCenter." + id + ".Description"}, Kind: kind, Timing: "Apply"}
}
func recordCollection(id, category, label, description, name string, fields []f4settings.Field) f4settings.Collection {
	return f4settings.Collection{ID: id, Category: category, Group: label, Label: f4settings.Text{English: label}, Description: f4settings.Text{English: description}, NameField: name, Fields: fields, Ordered: true}
}
func settingsFileRevision(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func newCoreRecordSettingsProvider() coreRecordSettingsProvider {
	p := coreRecordSettingsProvider{}
	assoc := recordCollection("associations", "associations", "File associations", "Ordered filename-mask rules. Changes are staged until Apply.", "assoc.Description", []f4settings.Field{recordField("assoc.Mask", "Filename mask", "Select filenames for which this association is considered.", f4settings.String), recordField("assoc.Description", "Description", "Name shown when several associations match.", f4settings.String)})
	for i, name := range panel.AssocKeyName {
		enabled := recordField("assoc."+name+"Enabled", "Enable "+name, "Enable this association command slot. An empty command is skipped.", f4settings.Boolean)
		command := recordField("assoc."+name, name+" command", "Command dispatched for this slot, using the existing user-menu substitution machinery.", f4settings.Multiline)
		if i%2 == 1 {
			enabled.Unavailable = "Reserved compatibility slot; no current dispatcher."
			command.Unavailable = enabled.Unavailable
		}
		assoc.Fields = append(assoc.Fields, enabled, command)
	}
	path := panel.AssociationsFilePath()
	p.stores = append(p.stores, settingsRecordStore{collection: assoc, path: path, load: func() ([]f4settings.Record, error) {
		items, err := panel.LoadAssociations(path)
		var rows []f4settings.Record
		for i, item := range items {
			values := map[string]string{"assoc.Mask": item.Mask, "assoc.Description": item.Description}
			for j, name := range panel.AssocKeyName {
				values["assoc."+name] = item.Commands[j]
				values["assoc."+name+"Enabled"] = strconv.FormatBool(item.Enabled[j])
			}
			rows = append(rows, f4settings.Record{ID: fmt.Sprintf("association:%d", i), Values: values})
		}
		return rows, err
	}, save: func(rows []f4settings.Record) error {
		var items []panel.FileAssoc
		for _, r := range rows {
			item := panel.FileAssoc{Mask: r.Values["assoc.Mask"], Description: r.Values["assoc.Description"]}
			for i, name := range panel.AssocKeyName {
				item.Commands[i] = r.Values["assoc."+name]
				item.Enabled[i] = r.Values["assoc."+name+"Enabled"] == "true"
			}
			items = append(items, item)
		}
		return panel.SaveAssociations(path, items)
	}})
	bookmarkPath := panel.BookmarksFilePath()
	bookmarks := recordCollection("bookmarks", "drives", "Folder bookmark slots", "Ten numbered directory slots. Reordering changes the digit shortcut. Existing Far plugin metadata is retained.", "bookmark.Name", []f4settings.Field{recordField("bookmark.Path", "Folder path", "Directory path or expandable path expression for this numbered slot.", f4settings.Path)})
	bookmarks.Fixed = true
	p.stores = append(p.stores, settingsRecordStore{collection: bookmarks, path: bookmarkPath, load: func() ([]f4settings.Record, error) {
		items, err := panel.LoadBookmarks(bookmarkPath)
		var rows []f4settings.Record
		for i, item := range items {
			rows = append(rows, f4settings.Record{ID: fmt.Sprintf("bookmark:%d", i), Values: map[string]string{"bookmark.Name": fmt.Sprintf("%d: %s", i, item.Path), "bookmark.Path": item.Path, "Plugin": item.Plugin, "PluginData": item.PluginData, "PluginFile": item.PluginFile}})
		}
		return rows, err
	}, save: func(rows []f4settings.Record) error {
		if len(rows) != 10 {
			return settingsError("folder bookmarks require ten slots")
		}
		var items panel.BookmarkSet
		for i, r := range rows {
			items[i] = panel.Bookmark{Path: r.Values["bookmark.Path"], Plugin: r.Values["Plugin"], PluginData: r.Values["PluginData"], PluginFile: r.Values["PluginFile"]}
		}
		return panel.SaveBookmarks(bookmarkPath, items)
	}})
	linkPath := panel.DriveBookmarksFilePath()
	links := recordCollection("drive-links", "drives", "Drive links", "Named links shown in the drive chooser.", "link.Name", []f4settings.Field{recordField("link.Name", "Link name", "Display name in the drive chooser.", f4settings.String), recordField("link.Path", "Link path", "Directory or provider path opened by the link.", f4settings.Path), recordField("link.Hotkey", "Shortcut", "Far-style shortcut spelling, such as Q or CtrlF5.", f4settings.String)})
	p.stores = append(p.stores, settingsRecordStore{collection: links, path: linkPath, load: func() ([]f4settings.Record, error) {
		items, err := panel.LoadDriveBookmarks(linkPath)
		var rows []f4settings.Record
		for i, item := range items {
			rows = append(rows, f4settings.Record{ID: fmt.Sprintf("drive-link:%d", i), Values: map[string]string{"link.Name": item.Name, "link.Path": item.Path, "link.Hotkey": item.Hotkey}})
		}
		return rows, err
	}, save: func(rows []f4settings.Record) error {
		var items []panel.DriveBookmark
		for _, r := range rows {
			items = append(items, panel.DriveBookmark{Name: r.Values["link.Name"], Path: r.Values["link.Path"], Hotkey: r.Values["link.Hotkey"]})
		}
		return panel.SaveDriveBookmarks(linkPath, items)
	}})
	p.addUserMenuStores()
	return p
}

func (p *coreRecordSettingsProvider) addUserMenuStores() {
	sources := []settingsMenuSource{{"main", "Global user menu", panel.MainMenuFilePath(), panel.MenuModeMain}}
	if exe, err := os.Executable(); err == nil {
		sources = append(sources, settingsMenuSource{"binary", "Executable user menu", filepath.Join(filepath.Dir(exe), panel.FarMenuFileName), panel.MenuModeFar})
	}
	dir, _ := os.Getwd()
	if pf := panel.FindPanelsFrameAnyScreen(); pf != nil {
		if fsp, ok := pf.Panels[pf.ActiveIdx].(*panel.FileSystemPanel); ok && fsp.Vfs != nil {
			if st, err := os.Stat(fsp.Vfs.GetPath()); err == nil && st.IsDir() {
				dir = fsp.Vfs.GetPath()
			}
		}
	}
	local, found := panel.FindLocalFarMenu(dir)
	if !found {
		local = filepath.Join(dir, panel.FarMenuFileName)
	}
	sources = append(sources, settingsMenuSource{"local", "Local/ancestor user menu", local, panel.MenuModeLocal})
	for _, src := range sources {
		p.stores = append(p.stores, newUserMenuSettingsStore(src))
	}
}

type settingsMenuSource struct {
	id, label, path string
	mode            panel.MenuMode
}

func newUserMenuSettingsStore(src settingsMenuSource) settingsRecordStore {
	prefix := "menu." + src.id + "."
	fields := []f4settings.Field{recordField(prefix+"Label", "Label", "Visible command or submenu name.", f4settings.String), recordField(prefix+"HotKey", "Activation key", "Menu activation key. Use -- for a separator.", f4settings.String), recordField(prefix+"Submenu", "Is a submenu", "Group child entries under this item instead of executing commands.", f4settings.Boolean), recordField(prefix+"Parent", "Parent entry ID", "Empty places the item at the root. Otherwise select the ID of a submenu entry.", f4settings.String), recordField(prefix+"Commands", "Commands", "Multiline shell commands using existing user-menu substitutions.", f4settings.Multiline)}
	col := recordCollection("usermenu."+src.id, "menus", src.label, "Source: %s. This source is captured when Settings Center opens; changing panels does not redirect saves.", prefix+"Label", fields)
	col.Description.Args = []any{src.path}
	return settingsRecordStore{collection: col, path: src.path, load: func() ([]f4settings.Record, error) {
		var items []panel.UserMenuItem
		var err error
		if src.mode == panel.MenuModeMain {
			items, err = panel.LoadMainMenu(src.path)
		} else {
			items, err = panel.LoadFarMenuFile(src.path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		var rows []f4settings.Record
		var walk func([]panel.UserMenuItem, string)
		walk = func(items []panel.UserMenuItem, parent string) {
			for _, item := range items {
				id := fmt.Sprintf("%s:%d", src.id, len(rows))
				rows = append(rows, f4settings.Record{ID: id, Values: map[string]string{prefix + "Label": item.Label, prefix + "HotKey": item.HotKey, prefix + "Commands": strings.Join(item.Commands, "\n"), prefix + "Submenu": strconv.FormatBool(item.Submenu != nil), prefix + "Parent": parent}})
				walk(item.Submenu, id)
			}
		}
		walk(items, "")
		return rows, err
	}, validate: func(rows []f4settings.Record) error { _, err := settingsMenuTree(rows, prefix); return err }, save: func(rows []f4settings.Record) error {
		items, err := settingsMenuTree(rows, prefix)
		if err != nil {
			return err
		}
		return panel.SaveRootForMode(src.mode, src.path, items)
	}}
}

func settingsMenuTree(rows []f4settings.Record, prefix string) ([]panel.UserMenuItem, error) {
	byID := map[string]f4settings.Record{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, r := range rows {
		seen := map[string]bool{r.ID: true}
		parent := r.Values[prefix+"Parent"]
		for parent != "" {
			p, ok := byID[parent]
			if !ok || p.Values[prefix+"Submenu"] != "true" {
				return nil, settingsError("%s: parent must be an existing submenu", r.Values[prefix+"Label"])
			}
			if seen[parent] {
				return nil, settingsError("submenu cycle")
			}
			seen[parent] = true
			parent = p.Values[prefix+"Parent"]
		}
	}
	var build func(string) []panel.UserMenuItem
	build = func(parent string) []panel.UserMenuItem {
		items := []panel.UserMenuItem{}
		for _, r := range rows {
			if r.Values[prefix+"Parent"] != parent {
				continue
			}
			item := panel.UserMenuItem{Label: r.Values[prefix+"Label"], HotKey: r.Values[prefix+"HotKey"]}
			if r.Values[prefix+"Submenu"] == "true" {
				item.Submenu = build(r.ID)
			} else {
				if commands := r.Values[prefix+"Commands"]; commands != "" {
					item.Commands = strings.Split(commands, "\n")
				}
			}
			items = append(items, item)
		}
		return items
	}
	return build(""), nil
}
func (p coreRecordSettingsProvider) Catalog() f4settings.Catalog {
	c := f4settings.Catalog{ID: "core-records", Categories: Categories, Background: true}
	for _, s := range p.stores {
		c.Collections = append(c.Collections, s.collection)
	}
	return c
}
func (p coreRecordSettingsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	records := map[string][]f4settings.Record{}
	revisions := map[string]string{}
	for _, s := range p.stores {
		rows, err := s.load()
		if err != nil {
			return nil, err
		}
		records[s.collection.ID] = rows
		rev, err := settingsFileRevision(s.path)
		if err != nil {
			return nil, err
		}
		revisions[s.collection.ID] = rev
	}
	d := f4settings.NewDraft(nil, records)
	d.ValidateFunc = func(d *f4settings.Draft) map[string]error {
		failures := map[string]error{}
		for _, s := range p.stores {
			id := s.collection.ID
			if !d.Dirty(id) {
				continue
			}
			revision, err := settingsFileRevision(s.path)
			if err != nil {
				failures[id] = err
			} else if revision != revisions[id] {
				failures[id] = settingsError("source changed outside Settings Center: %s", s.path)
			}
			if s.validate != nil {
				if err := s.validate(d.Records[id]); err != nil {
					failures[id] = err
				}
			}
			for _, r := range d.Records[id] {
				for _, f := range s.collection.Fields {
					if f.Kind != f4settings.Multiline {
						if err := settingsSingleLine(r.Values[f.ID]); err != nil {
							failures[id] = err
						}
					}
				}
			}
		}
		return failures
	}
	d.CommitFunc = func(ctx context.Context, d *f4settings.Draft) f4settings.Result {
		result := f4settings.Result{Errors: d.Validate(), Records: map[string]f4settings.Record{}}
		if len(result.Errors) > 0 {
			return result
		}
		for _, s := range p.stores {
			id := s.collection.ID
			if !d.Dirty(id) {
				continue
			}
			if err := ctx.Err(); err != nil {
				result.Errors[id] = err
				break
			}
			if rev, err := settingsFileRevision(s.path); err != nil || rev != revisions[id] {
				if err == nil {
					err = settingsError("source changed before saving: %s", s.path)
				}
				result.Errors[id] = err
				continue
			}
			rows := d.Records[id]
			if strings.HasPrefix(id, "usermenu.") {
				rows = normalizedMenuDraftRows(d, id)
			}
			if err := s.save(rows); err != nil {
				result.Errors[id] = err
				continue
			}
			if s.afterSave != nil {
				RunOnUI(ctx, func() { s.afterSave(rows) })
			}
			for _, row := range rows {
				result.Records[row.ID] = row
			}
			result.Applied = append(result.Applied, id)
			revisions[id], _ = settingsFileRevision(s.path)
		}
		return result
	}
	return d, nil
}

func normalizedMenuDraftRows(d *f4settings.Draft, id string) []f4settings.Record {
	rows := f4settings.NewDraft(nil, map[string][]f4settings.Record{id: d.Records[id]}).Records[id]
	old := map[string]f4settings.Record{}
	for _, r := range d.BaselineRecords[id] {
		old[r.ID] = r
	}
	for _, r := range rows {
		for key, value := range r.Values {
			if !strings.HasSuffix(key, ".Commands") || value == old[r.ID].Values[key] {
				continue
			}
			var commands []string
			for _, line := range strings.Split(value, "\n") {
				line = strings.TrimRight(line, " \t\r")
				if line == "" && len(commands) == 0 {
					continue
				}
				commands = append(commands, line)
			}
			for len(commands) > 0 && strings.TrimSpace(commands[len(commands)-1]) == "" {
				commands = commands[:len(commands)-1]
			}
			r.Values[key] = strings.Join(commands, "\n")
		}
	}
	return rows
}
