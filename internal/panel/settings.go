package panel

import "github.com/unxed/vtui"

// OpenSettingsAt is wired by the application to the canonical Settings Center.
var OpenSettingsAt func(category, collection, record string, create bool) bool

func (*PanelsFrame) OpenSettings(category, collection, record string, create bool) bool {
	if OpenSettingsAt == nil {
		return false
	}
	return OpenSettingsAt(category, collection, record, create)
}

// OpenSettingsCategoryOnly opens the Settings Center narrowed to a single
// category. Contextual keys use it so the window they open stays about the
// thing the key belongs to: the drive menu's F9 offers drive-chooser
// settings, not languages, the editor or updates (#1148).
var OpenSettingsCategoryOnly func(category string) bool

// SetSettingsRecordDefault seeds a newly opened record without persisting it.
var SetSettingsRecordDefault func(collection, field, value string)

// MenuSettingsSource captures the scope and draft tree of the active user menu.
type MenuSettingsSource struct {
	Mode                  MenuMode
	RootTitle, SourcePath string
	Path                  []int
	RootItems             []UserMenuItem
	Saved                 func([]UserMenuItem)
	Closed                func(int)
}

var OpenUserMenuSettings func(MenuSettingsSource, *vtui.VMenu, int, bool, bool) bool
