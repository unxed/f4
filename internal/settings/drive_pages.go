package settings

import (
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtui"
)

// The drive menu's F9 window shows the Drive chooser category as separate
// pages, Drive options, Bookmarks and Links (f4#1148), instead of one long page
// with a five-line list in the middle. The category itself, in the main
// Settings window, is left as it is: these are only the pages of a copy of the
// catalog made for that one window.
const (
	driveOptionsPage   = "drives.options"
	driveToolsPage     = "drives.tools"
	driveBookmarksPage = "drives.bookmarks"
	driveLinksPage     = "drives.links"
)

var driveChooserPages = []f4settings.Category{
	{ID: driveOptionsPage, Label: f4settings.Text{Key: "SettingsCenter.DriveTab.Options", English: "Drive options"}},
	{ID: driveToolsPage, Label: f4settings.Text{English: "Tools"}},
	{ID: driveBookmarksPage, Label: f4settings.Text{Key: "SettingsCenter.DriveTab.Bookmarks", English: "Bookmarks"}},
	{ID: driveLinksPage, Label: f4settings.Text{Key: "SettingsCenter.DriveTab.Links", English: "Links"}},
}

func driveChooserPageIDs() []string {
	ids := make([]string, len(driveChooserPages))
	for i, page := range driveChooserPages {
		ids[i] = page.ID
	}
	return ids
}

func driveChooserPageOf(collection string) string {
	switch collection {
	case "bookmarks":
		return driveBookmarksPage
	case "drive-links":
		return driveLinksPage
	}
	return driveOptionsPage
}

// splitDriveChooser moves what the sessions keep in the "drives" category onto
// the pages above. The catalogs are copied, never edited in place.
func splitDriveChooser(sessions []*settingsSession) {
	for _, s := range sessions {
		cat := s.catalog
		cat.Categories = nil
		for _, c := range s.catalog.Categories {
			if c.ID == "drives" {
				cat.Categories = append(cat.Categories, driveChooserPages...)
				continue
			}
			cat.Categories = append(cat.Categories, c)
		}
		cat.Fields = append([]f4settings.Field(nil), s.catalog.Fields...)
		for i := range cat.Fields {
			if cat.Fields[i].Category == "drives" {
				cat.Fields[i].Category = driveOptionsPage
			}
		}
		cat.Collections = nil
		for _, col := range s.catalog.Collections {
			// The tools have a page of their own here; their list under the
			// drive options repeated it (mitek1977-coder, f4#1148).
			if col.Category == "drives" && col.ID == "drive-tools" {
				continue
			}
			if col.Category == "drives" {
				col.Category = driveChooserPageOf(col.ID)
			}
			cat.Collections = append(cat.Collections, col)
		}
		cat.Commands = append([]f4settings.Command(nil), s.catalog.Commands...)
		for i := range cat.Commands {
			if cat.Commands[i].Category == "drives" {
				cat.Commands[i].Category = driveOptionsPage
			}
		}
		s.catalog = cat
	}
}

func showDriveChooser(sessions []*settingsSession) bool {
	tools, err := newDriveToolsSession()
	if err != nil {
		for _, s := range sessions {
			s.draft.Close()
		}
		vtui.ShowMessage(Phrase("Settings"), err.Error(), []string{i18n.Msg("vtui.Ok")})
		return true
	}
	sessions = append(sessions, tools)
	splitDriveChooser(sessions)
	c := newSettingsCenter(sessions)
	c.fullLists = true
	c.fieldTitle = Phrase("Drive chooser")
	c.restrictTo(driveChooserPageIDs()...)
	// Opened by F9 in the drive menu: the menu under the window shows
	// what Apply wrote without the window closing (f4#1148).
	c.onApplied = func() { panel.RefreshDriveMenuBelow(c) }
	c.navigate(driveOptionsPage, "", "", false)
	c.ResizeConsole(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	vtui.FrameManager.Push(c)
	// No refreshSchemeChoices: the window shows only the drive pages, never
	// the editor's colour scheme, and the background scheme listing outlived
	// the window -- on CI (12-10-2026) it raced with the next test and wrote
	// colorer's cache into a temp HOME being removed.
	return true
}

// settingsCheckboxBoxOnly is true for the check boxes of the Tools page: a
// click on a tool's name selects it, the way a list row is selected, and
// only a click on its "[x]" shows or hides the tool (f4#1148).
func settingsCheckboxBoxOnly(id string) bool {
	return strings.HasPrefix(id, driveToolFieldPrefix)
}

// settingsFieldIndent is how far in a check box sits under the one it depends
// on: SortToolsByHotkey only matters while ShowTools is on, and is drawn four
// columns in under it (mitek1977-coder, f4#1148).
func settingsFieldIndent(id string) int {
	if id == driveMenuOptionFieldID("Drive.SortToolsByHotkey") {
		return 4
	}
	return 0
}
