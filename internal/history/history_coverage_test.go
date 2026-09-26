package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/unxed/vtui"
)

func TestHistoryRecordDisplayText(t *testing.T) {
	when := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name   string
		record HistoryRecord
		want   string
	}{
		{name: "name only", record: HistoryRecord{Name: "report.txt"}, want: "report.txt"},
		{name: "directory", record: HistoryRecord{Name: "report.txt", Dir: "/tmp/work"}, want: "/tmp/work/ report.txt"},
		{name: "trailing separator", record: HistoryRecord{Name: "report.txt", Extra: `C:\work\`}, want: `C:\work\ report.txt`},
		{name: "legacy directory", record: HistoryRecord{Name: "report.txt", Extra: "/tmp/old"}, want: "/tmp/old/ report.txt"},
		{name: "timestamp", record: HistoryRecord{Name: "report.txt", Timestamp: when}, want: "03:04:05 report.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.record.DisplayText(); got != tc.want {
				t.Errorf("DisplayText() = %q, want %q", got, tc.want)
			}
		})
	}
	if got := (HistoryRecord{Dir: "/new", Extra: "/old"}).Directory(); got != "/new" {
		t.Errorf("Directory prefers legacy Extra: got %q", got)
	}
}

func TestHistoryRecordConversions(t *testing.T) {
	names := []string{"one", "two"}
	records := RecordsFromNames(names)
	if got := ExtractHistoryNames(records); !reflect.DeepEqual(got, names) {
		t.Fatalf("ExtractHistoryNames() = %#v, want %#v", got, names)
	}
	if got := ExtractNames(records); !reflect.DeepEqual(got, names) {
		t.Fatalf("ExtractNames() = %#v, want %#v", got, names)
	}
	if RecordsFromNames(nil) != nil || ExtractHistoryNames(nil) != nil || ExtractNames(nil) != nil {
		t.Fatal("empty history conversions should return nil")
	}
}

func TestMergeHistoryNamesPreservesMetadata(t *testing.T) {
	stamp := time.Unix(123, 0)
	old := []HistoryRecord{{Name: "kept", Dir: "/tmp", Timestamp: stamp, Lock: true}, {Name: "dropped", Extra: "legacy"}}
	got := MergeHistoryNames(old, []string{"kept", "new", "kept"})
	want := []HistoryRecord{{Name: "kept", Dir: "/tmp", Timestamp: stamp, Lock: true}, {}, {Name: "kept", Dir: "/tmp", Timestamp: stamp, Lock: true}}
	want[1].Name = "new"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeHistoryNames() = %#v, want %#v", got, want)
	}
	if MergeHistoryNames(nil, nil) != nil {
		t.Fatal("empty merge should return nil")
	}
}

func TestLimitRichHistoryKeepsLockedEntries(t *testing.T) {
	history := []HistoryRecord{{Name: "a"}, {Name: "locked", Lock: true}, {Name: "b"}, {Name: "c"}}
	got := LimitRichHistory(history, 2)
	want := []HistoryRecord{{Name: "a"}, {Name: "locked", Lock: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LimitRichHistory() = %#v, want %#v", got, want)
	}
	if got := LimitRichHistory(history, 0); !reflect.DeepEqual(got, history) {
		t.Errorf("non-positive limit changed history: %#v", got)
	}
	allLocked := []HistoryRecord{{Name: "one", Lock: true}, {Name: "two", Lock: true}}
	if got := LimitRichHistory(allLocked, 1); len(got) != 2 {
		t.Errorf("locked entries were dropped: %#v", got)
	}
}

func TestProviderPersistsPlainAndRichHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "history.json")
	hp := NewProviderAtPath(path)
	hp.SaveHistory("commands", []string{"one", "two"})
	hp.SaveRichHistory("folders", []HistoryRecord{{Name: "/tmp", Lock: true}})

	reloaded := NewProviderAtPath(path)
	if got := reloaded.LoadHistory("commands"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("plain history = %#v", got)
	}
	if got := reloaded.LoadRichHistory("folders"); len(got) != 1 || !got[0].Lock {
		t.Errorf("rich history = %#v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file was not created: %v", err)
	}
}

func TestProviderMigratesLegacyPlainBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	data, err := json.Marshal(map[string][]string{"folders": {"/one", "/two"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	hp := NewProviderAtPath(path)
	got := hp.LoadRichHistory("folders")
	if len(got) != 2 || got[0].Name != "/one" || got[1].Name != "/two" {
		t.Errorf("migrated rich history = %#v", got)
	}
}

func TestLoadFolderHistoryRecordsSupportsGenericProvider(t *testing.T) {
	provider := stubHistoryProvider{"folders": {"/first", "/second"}}
	got, hp := LoadFolderHistoryRecords(provider)
	if hp != nil {
		t.Fatal("generic provider unexpectedly treated as F4 provider")
	}
	if len(got) != 2 || got[0].Name != "/first" || got[1].Name != "/second" {
		t.Errorf("folder records = %#v", got)
	}
	f4Records, gotHP := LoadFolderHistoryRecords(&F4HistoryProvider{data: map[string][]string{}, rich: map[string][]HistoryRecord{}})
	if gotHP == nil || len(f4Records) != 0 {
		// An empty F4 provider has no visible folders but is still returned so
		// callers can preserve rich metadata on the next write.
		t.Errorf("empty F4 provider returned records=%#v provider=%v", f4Records, gotHP != nil)
	}
}

func TestImportFar2lHistoryDecodesEscapesAndTimes(t *testing.T) {
	ini := stubINI{
		"Lines":  `"one\n二"`,
		"Extras": `"/tmp\nC:\\work\\"`,
		"Locks":  "01",
		"Times":  "0000000000000000 0080B9A4D6DDBF01",
	}
	got, err := ImportFar2lHistory(ini, "saved.ini")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "二" {
		t.Fatalf("decoded records = %#v", got)
	}
	if got[0].Lock || !got[1].Lock || got[0].Directory() != "/tmp" {
		t.Errorf("decoded metadata = %#v", got)
	}
	if got[0].Timestamp.IsZero() || got[1].Timestamp.IsZero() {
		t.Errorf("decoded timestamps = %#v", got)
	}
}

func TestImportFar2lHistoryReportsMissingLines(t *testing.T) {
	if _, err := ImportFar2lHistory(stubINI{}, "missing.ini"); err == nil {
		t.Fatal("missing Lines should be reported")
	}
	ini := stubINI{"Lines": `"one"`, "Times": "not-a-time"}
	got, err := ImportFar2lHistory(ini, "malformed.ini")
	if err != nil || len(got) != 1 || !got[0].Timestamp.IsZero() {
		t.Errorf("malformed time result = %#v, err=%v", got, err)
	}
}

func TestImportFar2lFolderHistoryUsesFolderSection(t *testing.T) {
	ini := sectionINI{section: "SavedFolderHistory", values: map[string]string{
		"Lines": `"/home/user\n/tmp"`,
		"Locks": "01",
		"Times": "0000000000000000 0080B9A4D6DDBF01",
	}}
	got, err := ImportFar2lFolderHistory(ini, "folders.hst")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "/home/user" || got[1].Name != "/tmp" {
		t.Fatalf("decoded folder records = %#v", got)
	}
	if got[0].Lock || !got[1].Lock {
		t.Errorf("decoded folder locks = %#v", got)
	}
}

func TestCommandHistoryPathsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	hp := NewProviderAtPath(path)
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = hp
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	RememberCommandHistoryPath("open", "/tmp/file.txt", []string{"open", "save"})
	if got := LoadCommandHistoryPaths([]string{"save", "open", "missing"}); !reflect.DeepEqual(got, []string{"", "/tmp/file.txt", ""}) {
		t.Errorf("command paths = %#v", got)
	}
	SaveCommandHistoryPaths([]string{"open", "empty"}, []string{"/new", ""})
	if got := LoadCommandHistoryPaths([]string{"open"}); !reflect.DeepEqual(got, []string{"/new"}) {
		t.Errorf("saved command paths = %#v", got)
	}
}

func TestAddPluginFolderHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	hp := NewProviderAtPath(path)
	previous := vtui.GlobalHistoryProvider
	vtui.GlobalHistoryProvider = hp
	t.Cleanup(func() { vtui.GlobalHistoryProvider = previous })

	AddPluginFolderHistory("user@host:/one", "netfox.FishVFS", "ref-one")
	AddPluginFolderHistory("/local/dir", "", "")

	records, _ := LoadFolderHistoryRecords(hp)
	if len(records) != 1 {
		t.Fatalf("expected exactly one plugin entry (the call with empty pluginType/pluginRef must be a no-op), got %#v", records)
	}
	if !records[0].IsPluginEntry() || records[0].PluginType != "netfox.FishVFS" || records[0].PluginRef != "ref-one" {
		t.Fatalf("unexpected plugin entry: %#v", records[0])
	}
	if records[0].Name != "user@host:/one" {
		t.Fatalf("plugin entry Name (display text) = %q", records[0].Name)
	}

	// Re-recording the same (pluginType, pluginRef) updates the display text
	// in place and moves it to the front, rather than duplicating it, mirroring
	// AddFolderHistory's own dedup-and-move-to-top behavior.
	AddPluginFolderHistory("user@host:/two", "netfox.FishVFS", "ref-one")
	AddPluginFolderHistory("other@host:/three", "netfox.SFTPVFS", "ref-two")
	records, _ = LoadFolderHistoryRecords(hp)
	if len(records) != 2 {
		t.Fatalf("expected two entries after re-recording the same ref, got %#v", records)
	}
	if records[0].Name != "other@host:/three" || records[1].Name != "user@host:/two" {
		t.Fatalf("unexpected order/content after dedup: %#v", records)
	}
}

func TestIsPluginEntry(t *testing.T) {
	if (HistoryRecord{Name: "/tmp"}).IsPluginEntry() {
		t.Fatal("a plain path record must not be a plugin entry")
	}
	if (HistoryRecord{Name: "x", PluginType: "T"}).IsPluginEntry() {
		t.Fatal("PluginType alone must not be enough to count as a plugin entry")
	}
	if !(HistoryRecord{Name: "x", PluginType: "T", PluginRef: "R"}).IsPluginEntry() {
		t.Fatal("PluginType and PluginRef together must count as a plugin entry")
	}
}

type stubHistoryProvider map[string][]string

func (s stubHistoryProvider) LoadHistory(id string) []string         { return s[id] }
func (s stubHistoryProvider) SaveHistory(id string, values []string) { s[id] = values }

type stubINI map[string]string

func (s stubINI) GetString(_, key, fallback string) string {
	if value, ok := s[key]; ok {
		return value
	}
	return fallback
}

type sectionINI struct {
	section string
	values  map[string]string
}

func (s sectionINI) GetString(section, key, fallback string) string {
	if section == s.section {
		if value, ok := s.values[key]; ok {
			return value
		}
	}
	return fallback
}
