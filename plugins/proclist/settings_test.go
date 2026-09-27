package proclist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettingsIsValid(t *testing.T) {
	if err := DefaultSettings().validate(); err != nil {
		t.Fatalf("DefaultSettings() is invalid: %v", err)
	}
	if got := DefaultSettings().refreshInterval(); got != defaultRefreshInterval {
		t.Fatalf("DefaultSettings().refreshInterval() = %v, want %v", got, defaultRefreshInterval)
	}
}

func TestSettingsValidateRejectsNoVisibleColumns(t *testing.T) {
	s := DefaultSettings()
	s.VisibleColumns = []string{"bogus"}
	if err := s.validate(); err == nil {
		t.Fatal("validate should reject a settings with no known visible column")
	}
}

func TestSettingsValidateRejectsAnOutOfRangeInterval(t *testing.T) {
	for _, ms := range []int{0, 1, int(maxRefreshInterval.Milliseconds()) + 1} {
		s := DefaultSettings()
		s.RefreshIntervalMS = ms
		if err := s.validate(); err == nil {
			t.Errorf("validate should reject a %dms refresh interval", ms)
		}
	}
}

func TestNormalizeSettingsTrimsLowercasesAndDedupsColumns(t *testing.T) {
	got := normalizeSettings(Settings{VisibleColumns: []string{" PID ", "pid", "Mem", ""}})
	want := []string{"pid", "mem"}
	if len(got.VisibleColumns) != len(want) {
		t.Fatalf("normalizeSettings columns = %#v, want %#v", got.VisibleColumns, want)
	}
	for i, k := range want {
		if got.VisibleColumns[i] != k {
			t.Fatalf("normalizeSettings columns = %#v, want %#v", got.VisibleColumns, want)
		}
	}
}

func TestSettingsStoreSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := newSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.snapshot(); len(got.VisibleColumns) != len(validColumnKeys) {
		t.Fatalf("a fresh store's snapshot = %#v, want DefaultSettings", got)
	}

	next := Settings{VisibleColumns: []string{"pid", "cpu"}, RefreshIntervalMS: 1000}
	if err := store.save(next); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := store.snapshot(); len(got.VisibleColumns) != 2 || got.RefreshIntervalMS != 1000 {
		t.Fatalf("snapshot after save = %#v, want %#v", got, next)
	}

	reopened, err := newSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.snapshot()
	if got.RefreshIntervalMS != 1000 || len(got.VisibleColumns) != 2 {
		t.Fatalf("reopened settings = %#v, want the saved values", got)
	}
}

func TestSettingsStoreSaveRejectsInvalidSettings(t *testing.T) {
	store, err := newSettingsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := store.snapshot()
	if err := store.save(Settings{VisibleColumns: []string{"bogus"}}); err == nil {
		t.Fatal("save should reject a settings with no known visible column")
	}
	if got := store.snapshot(); got.RefreshIntervalMS != before.RefreshIntervalMS {
		t.Fatal("a rejected save must not change the in-memory settings")
	}
}

func TestSettingsStoreSaveOnANilStoreFails(t *testing.T) {
	var store *settingsStore
	if err := store.save(DefaultSettings()); err == nil {
		t.Fatal("save on a nil store should fail")
	}
	if got := store.snapshot(); len(got.VisibleColumns) != len(validColumnKeys) {
		t.Fatalf("snapshot on a nil store = %#v, want DefaultSettings", got)
	}
}

func TestNewSettingsStoreFallsBackToDefaultsOnAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugins", "proclist.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := newSettingsStore(dir)
	if err == nil {
		t.Fatal("newSettingsStore should report a decode error for invalid JSON")
	}
	if got := store.snapshot(); len(got.VisibleColumns) != len(validColumnKeys) {
		t.Fatalf("snapshot after a decode error = %#v, want DefaultSettings", got)
	}
}

func TestSettingsStoreSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	store, err := newSettingsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.save(Settings{VisibleColumns: []string{"pid"}, RefreshIntervalMS: 750}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" || e.Name() == "proclist.json" {
			t.Fatalf("save left a stray file behind: %s", e.Name())
		}
	}
}
