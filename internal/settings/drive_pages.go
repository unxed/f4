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
	{ID: driveToolsPage, Label: f4settings.Text{English: "Tools & Plugins"}},
	{ID: driveBookmarksPage, Label: f4settings.Text{Key: "SettingsCenter.DriveTab.Bookmarks", English: "Bookmarks"}},
	{ID: driveLinksPage, Label: f4settings.Text{Key: "SettingsCenter.DriveTab.Links", English: "Links"}},
}

// lastDriveChooserPage is the page the drive menu's F9 window was closed on;
// the next F9 opens it again (mitek1977-coder, f4#1148). Empty until the
// window has been closed once.
var lastDriveChooserPage string

func isDriveChooserPage(id string) bool {
	for _, page := range driveChooserPages {
		if page.ID == id {
			return true
		}
	}
	return false
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
		cat.Fields = nil
		for _, f := range s.catalog.Fields {
			if f.Category == "drives" {
				if driveChooserLeavesOut(f.ID) {
					continue
				}
				f.Category = driveOptionsPage
				if f.ID == driveMenuOptionFieldID("Drive.ShowBookmarks") {
					// The switch of the Bookmarks section stands over the
					// bookmark list, in the list's own box (f4#1148).
					f.Category = driveBookmarksPage
					f.Group = driveBookmarksGroup
				}
				if f.ID == driveMenuOptionFieldID("Drive.ShowLinks") {
					// The Links switch stands above its list on the Links page.
					f.Category = driveLinksPage
					f.Group = driveLinksGroup
				}
				// "Takes effect: next drive menu" is no news under the menu
				// itself, which Apply redraws (mitek1977-coder, f4#1148).
				if strings.HasPrefix(f.ID, "DriveMenuOptions.") {
					f.Timing = ""
				}
			}
			cat.Fields = append(cat.Fields, f)
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
			if col.ID == "drive-links" {
				col.Fields = driveLinkPageFields(col.Fields)
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

// driveLinkPageFields are the link fields as the Links page edits them: the
// hot key in a one-character field after "Hot key:", as in the link dialog of
// the drive menu, instead of a chord field with Capture (mitek1977-coder,
// f4#1148), and the global hot key in a field that takes the key pressed in it,
// as that dialog's does (f4#2171). The main Settings window keeps the chord
// fields.
func driveLinkPageFields(fields []f4settings.Field) []f4settings.Field {
	out := append([]f4settings.Field(nil), fields...)
	for i := range out {
		switch out[i].ID {
		case "link.Hotkey":
			out[i].Label = f4settings.Text{Key: "DriveLink.Hotkey", English: "Hot key:"}
			out[i].Description = f4settings.Text{Key: "SettingsCenter.DriveLinks.Hotkey", English: "The link's hot key in the drive menu: a letter or a digit; empty for none."}
			out[i].InputWidth = 1
		case "link.GlobalHotkey":
			out[i].Label = f4settings.Text{Key: "DriveLink.GlobalHotkey", English: "Global hot key:"}
			out[i].InputWidth = driveLinkGlobalHotkeyWidth
		}
	}
	return out
}

// driveLinkGlobalHotkeyWidth is the width of the global hot key field of the
// Links page: Ctrl+Alt+Shift+F11 fits.
const driveLinkGlobalHotkeyWidth = 18

// driveBookmarksGroup is the box of the Bookmarks page: the list's own group,
// so that "Show Bookmarks" stands in it above the list.
const driveBookmarksGroup = "Folder bookmark slots"

// driveLinksGroup is the box above the named Links list on the Links page.
const driveLinksGroup = "Drive links"

// driveChooserLeavesOut is true for the drive menu options the F9 window does
// not show (mitek1977-coder, f4#1148): the Tools page already says which tools
// the menu shows and in what order. They stay in the main Settings window.
func driveChooserLeavesOut(id string) bool {
	for _, label := range []string{"Drive.ShowTools", "Drive.SortToolsByHotkey"} {
		if id == driveMenuOptionFieldID(label) {
			return true
		}
	}
	return false
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
	page := driveOptionsPage
	if isDriveChooserPage(lastDriveChooserPage) {
		page = lastDriveChooserPage
	}
	c.navigate(page, "", "", false)
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
// on: SortToolsByHotkey only matters while ShowTools is on, ShowSizeFloat
// while ShowSize is on, and each is drawn four columns in under the other
// (mitek1977-coder, f4#1148).
func settingsFieldIndent(id string) int {
	switch id {
	case driveMenuOptionFieldID("Drive.SortToolsByHotkey"), driveMenuOptionFieldID("Drive.ShowSizeFloat"):
		return 4
	}
	return 0
}
