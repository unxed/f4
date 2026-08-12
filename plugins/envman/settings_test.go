package envman

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStoreMissingSaveReloadAndSecondReplacement(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, DefaultConfig()) {
		t.Fatalf("initial snapshot = %#v", got)
	}
	wantPath := filepath.Join(directory, settingsDirectory, settingsFileName)
	if store.Path() != wantPath {
		t.Fatalf("path = %q, want %q", store.Path(), wantPath)
	}

	first := testConfig("first", "A=1")
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	first.Entries[0].Name = "mutated caller"
	first.Entries[0].Variables[0] = "A=mutated"
	if got := store.Snapshot().Entries[0].Name; got != "first" {
		t.Fatalf("snapshot aliases caller: %q", got)
	}

	second := testConfig("second", "B=2")
	second.AlwaysUseEditor = true
	if err := store.Save(second); err != nil {
		t.Fatalf("second replacement failed: %v", err)
	}
	loaded, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot(); !reflect.DeepEqual(got, second) {
		t.Fatalf("reloaded snapshot = %#v, want %#v", got, second)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(store.Path()), ".envman-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files remain: %#v", temporary)
	}
}

func TestStoreCorruptFileReturnsUsableDefaultStore(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, settingsDirectory, settingsFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStoreWithOptions(directory, EngineOptions{})
	if err == nil || store == nil {
		t.Fatalf("store, error = %#v, %v; want usable store and error", store, err)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, DefaultConfig()) {
		t.Fatalf("fallback snapshot = %#v", got)
	}
	valid := testConfig("recovered", "A=1")
	if err := store.Save(valid); err != nil {
		t.Fatalf("fallback store could not overwrite corrupt file: %v", err)
	}
	reopened, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil || !reflect.DeepEqual(reopened.Snapshot(), valid) {
		t.Fatalf("reopened = %#v, %v", reopened.Snapshot(), err)
	}
}

func TestStoreReloadFailureRetainsSnapshot(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := testConfig("stable", "A=1")
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("reload error = %v", err)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed reload changed snapshot = %#v", got)
	}
}

func TestStoreInvalidSaveDoesNotPublish(t *testing.T) {
	store, err := NewStoreWithOptions(t.TempDir(), EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	valid := testConfig("valid", "A=1")
	if err := store.Save(valid); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Version++
	if err := store.Save(invalid); err == nil {
		t.Fatal("invalid config was saved")
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(store.Snapshot(), valid) {
		t.Fatal("invalid save changed file or snapshot")
	}
}

func TestStoreConcurrentSavesPublishOneCompleteConfiguration(t *testing.T) {
	store, err := NewStoreWithOptions(t.TempDir(), EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	configs := make([]Config, writers)
	for index := range configs {
		configs[index] = testConfig(
			"writer-"+string(rune('A'+index)),
			"VALUE="+string(rune('a'+index)),
		)
	}
	start := make(chan struct{})
	errors := make(chan error, writers)
	var wait sync.WaitGroup
	for index := range configs {
		wait.Add(1)
		go func(config Config) {
			defer wait.Done()
			<-start
			errors <- store.Save(config)
		}(configs[index])
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := NewStoreWithOptions(filepath.Dir(filepath.Dir(store.Path())), EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot()
	for _, config := range configs {
		if reflect.DeepEqual(got, config) {
			return
		}
	}
	t.Fatalf("concurrent saves published a torn or unknown configuration: %#v", got)
}

func TestIndependentStoresUseAtomicLastWriterWins(t *testing.T) {
	directory := t.TempDir()
	first, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	configs := []Config{testConfig("first instance", "A=one"), testConfig("second instance", "B=two")}
	start := make(chan struct{})
	errors := make(chan error, 2)
	go func() { <-start; errors <- first.Save(configs[0]) }()
	go func() { <-start; errors <- second.Save(configs[1]) }()
	close(start)
	for range configs {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := NewStoreWithOptions(directory, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot()
	if !reflect.DeepEqual(got, configs[0]) && !reflect.DeepEqual(got, configs[1]) {
		t.Fatalf("independent stores published a torn configuration: %#v", got)
	}
}

func testConfig(name, assignment string) Config {
	return Config{
		Version: CurrentConfigVersion,
		Entries: []Entry{{
			Kind: KindProfile, Name: name, Enabled: true, Variables: []string{assignment},
		}},
	}
}
