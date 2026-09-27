package proclist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This file has no platform build tag, unlike panel.go/config_dialog.go
// (linux || windows || darwin): plugin.go, which loads settings through it,
// has none either (internal/plughost/manager.go constructs
// proclist.NewPlugin unconditionally, on every platform this module builds
// for, and gates the rest at runtime through Supported()), so every symbol
// it references -- including these -- must exist everywhere, even where
// Supported() is false and Init never actually reaches them. validColumnKeys
// below is this file's own, minimal copy of panel.go's allColumnSpecs' key
// column for exactly that reason: allColumnSpecs itself carries vtui column
// widths and cell formatters this file has no business needing, and is not
// available on a platform panel.go itself does not build for.

// validColumnKeys are Settings.VisibleColumns' recognized values, in
// panel.go's allColumnSpecs' own order. columns_sync_test.go (tagged like
// panel.go) asserts the two stay in lockstep.
var validColumnKeys = []string{"pid", "name", "mem", "cpu"}

// defaultColumnKeys is Settings.VisibleColumns' default: every column.
func defaultColumnKeys() []string {
	return append([]string(nil), validColumnKeys...)
}

// columnKeysValid reports whether keys names at least one column this
// package still knows about. Settings.validate uses it to refuse saving a
// configuration that would leave nothing visible; columnSpecsForKeys'
// (panel.go) own, more forgiving fallback exists for a settings.json this
// check already kept from being written in the first place -- a hand-edited
// file, or one from a future version with columns this build predates.
func columnKeysValid(keys []string) bool {
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		for _, v := range validColumnKeys {
			if v == k {
				return true
			}
		}
	}
	return false
}

// defaultRefreshInterval is v1's own fixed refresh rate (f4#312 parts 1-3),
// now Settings.RefreshIntervalMS' default and loop()'s (panel.go) fallback
// if a settings.json ever produced a non-positive interval -- validate below
// already refuses to save one, but a hand-edited file on disk is not bound
// by that.
const defaultRefreshInterval = 500 * time.Millisecond

// minRefreshInterval/maxRefreshInterval bound ProcList.Config's refresh
// interval field. Below the minimum, the background collector (collect,
// collector_linux.go/collector_windows.go/collector_darwin.go) becomes a
// meaningful background load for no real benefit -- nothing on a terminal UI
// needs sub-100ms process stats. Above the maximum, "live" stops being an
// honest description of the panel.
const (
	minRefreshInterval = 100 * time.Millisecond
	maxRefreshInterval = 60 * time.Second
)

// Settings are ProcList's own user preferences (f4#312 part 4 of 4): which
// table columns panel.go shows, and how often it refreshes. Kill's
// confirmation and the priority/suspend gestures (actions.go) stay
// unconditional -- see that file's own comment -- so they are deliberately
// not configurable here.
type Settings struct {
	// VisibleColumns names allColumnSpecs' keys (panel.go) that should be
	// shown, in any order -- columnSpecsForKeys always renders them back in
	// allColumnSpecs' own fixed order regardless of what order they are
	// listed here.
	VisibleColumns []string `json:"visibleColumns"`
	// RefreshIntervalMS is stored as a plain millisecond count, not a
	// time.Duration (which encoding/json would marshal as an opaque
	// nanosecond count), so a settings.json a person opens by hand reads as
	// the same unit ProcList.Config's own dialog field shows.
	RefreshIntervalMS int `json:"refreshIntervalMS"`
}

// DefaultSettings is v1 through part 3's own fixed behavior: every column,
// refreshed every defaultRefreshInterval.
func DefaultSettings() Settings {
	return Settings{
		VisibleColumns:    defaultColumnKeys(),
		RefreshIntervalMS: int(defaultRefreshInterval / time.Millisecond),
	}
}

// refreshInterval converts RefreshIntervalMS to a time.Duration for loop
// (panel.go) to use directly.
func (s Settings) refreshInterval() time.Duration {
	return time.Duration(s.RefreshIntervalMS) * time.Millisecond
}

func (s Settings) validate() error {
	if !columnKeysValid(s.VisibleColumns) {
		return errors.New("at least one column must stay visible")
	}
	if d := s.refreshInterval(); d < minRefreshInterval || d > maxRefreshInterval {
		return fmt.Errorf("refresh interval must be between %d and %d ms",
			minRefreshInterval.Milliseconds(), maxRefreshInterval.Milliseconds())
	}
	return nil
}

// normalizeSettings trims, lowercases and dedups VisibleColumns' entries
// (a settings.json is free-form JSON before it is ever validated). It does
// not drop a key this build does not recognize -- columnSpecsForKeys already
// tolerates that at render time, and dropping it here would lose a newer
// version's column the next time that same file round-trips through an older
// build.
func normalizeSettings(s Settings) Settings {
	seen := make(map[string]bool, len(s.VisibleColumns))
	keys := make([]string, 0, len(s.VisibleColumns))
	for _, k := range s.VisibleColumns {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	s.VisibleColumns = keys
	return s
}

// settingsStore persists Settings to <configDir>/plugins/proclist.json,
// the same per-plugin-JSON-file convention plugins/mediainfo/settings.go
// established (mediainfo.json alongside it). A nil *settingsStore is valid
// everywhere this package reads one (snapshot below): every existing test
// that has no reason to exercise settings just passes nil, and gets
// DefaultSettings() back.
type settingsStore struct {
	mu      sync.RWMutex
	saveMu  sync.Mutex
	path    string
	current Settings
}

func newSettingsStore(configDir string) (*settingsStore, error) {
	store := &settingsStore{
		path:    filepath.Join(configDir, "plugins", "proclist.json"),
		current: DefaultSettings(),
	}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, fmt.Errorf("read ProcList settings: %w", err)
	}
	settings := DefaultSettings()
	if err := json.Unmarshal(data, &settings); err != nil {
		return store, fmt.Errorf("decode ProcList settings: %w", err)
	}
	settings = normalizeSettings(settings)
	if err := settings.validate(); err != nil {
		return store, fmt.Errorf("validate ProcList settings: %w", err)
	}
	store.current = settings
	return store, nil
}

func (store *settingsStore) snapshot() Settings {
	if store == nil {
		return DefaultSettings()
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.current
}

// save validates, then atomically replaces both the on-disk file and the
// in-memory snapshot (create-temp + Sync + rename, the same sequence
// plugins/mediainfo/settings.go's own save uses) so a reader never observes
// a half-written file, and a failed save leaves the previous settings
// exactly as they were.
func (store *settingsStore) save(settings Settings) error {
	if store == nil {
		return errors.New("ProcList settings store is unavailable")
	}
	store.saveMu.Lock()
	defer store.saveMu.Unlock()

	settings = normalizeSettings(settings)
	if err := settings.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ProcList settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return fmt.Errorf("create ProcList settings directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".proclist-*.json")
	if err != nil {
		return fmt.Errorf("create ProcList settings file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write ProcList settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync ProcList settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close ProcList settings: %w", err)
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace ProcList settings: %w", err)
	}
	committed = true

	store.mu.Lock()
	store.current = settings
	store.mu.Unlock()
	return nil
}
