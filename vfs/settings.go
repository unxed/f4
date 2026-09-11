package vfs

import "github.com/unxed/f4/sdk/f4settings"

// SettingsContributionHost is optional so existing in-process and RPC plugins
// keep their original host contract. Descriptors contain no frontend widgets.
type SettingsContributionHost interface {
	RegisterSettingsProvider(f4settings.Provider) (Registration, error)
}

// SettingsNavigationHost optionally opens global configuration in the Center.
// In-place record editors should keep their contextual dialog instead of
// redirecting an editing command through this navigation capability.
// Record identifies a stable provider record ID or display name; an empty
// collection opens its category. No preference is written by navigation.
type SettingsNavigationHost interface {
	OpenSettings(category, collection, record string, create bool) bool
}
