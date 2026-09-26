package history

import (
	"encoding/json"
	"github.com/unxed/vtui"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

import "time"

type HistoryRecord struct {
	Name string `json:"name"`
	Dir  string `json:"dir,omitempty"`
	// Extra is the pre-rich-history spelling of Dir. Keep reading and
	// writing it for imported/older records, but use Dir for new records.
	Extra     string    `json:"extra,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Lock      bool      `json:"lock,omitempty"`

	// PluginType and PluginRef, when both set, mark this entry as owned by a
	// panel plugin's VFS (vfs.HistoryPathProvider) rather than a real
	// filesystem path (f4#262): Name is display text the plugin chose, not
	// something safe to open directly, and Dir/Extra are unused. Navigating
	// here means asking an already-open VFS of PluginType to accept
	// PluginRef — never opening a new connection. See
	// panel.NavigateOpenPluginHistoryEntry.
	PluginType string `json:"pluginType,omitempty"`
	PluginRef  string `json:"pluginRef,omitempty"`
}

// IsPluginEntry reports whether this record is owned by a panel plugin's
// VFS, per PluginType/PluginRef, rather than being a real filesystem path.
func (r HistoryRecord) IsPluginEntry() bool {
	return r.PluginType != "" && r.PluginRef != ""
}

func (r HistoryRecord) Directory() string {
	if r.Dir != "" {
		return r.Dir
	}
	return r.Extra
}

func (r HistoryRecord) DisplayText() string {
	res := ""
	if !r.Timestamp.IsZero() {
		res += r.Timestamp.Format("15:04:05 ")
	}
	if extra := r.Directory(); extra != "" {
		if len(extra) > 15 {
			extra = "..." + extra[len(extra)-12:]
		}
		if !strings.HasSuffix(extra, "/") && !strings.HasSuffix(extra, "\\") {
			res += extra + "/ "
		} else {
			res += extra + " "
		}
	}
	res += r.Name
	return res
}

// SamePath reports whether two stored paths denote the same folder. The
// composition root installs the real comparison; the default is exact equality,
// which is right for a plain filesystem path and blind to two spellings of one
// URI. Same shape, and same reason, as action.Localize.
var SamePath = func(a, b string) bool { return a != "" && a == b }

type F4HistoryProvider struct {
	mu   sync.Mutex
	path string
	data map[string][]string
	rich map[string][]HistoryRecord
}

// NewProviderAtPath opens the history stored in one named file, creating an
// empty one in memory when it does not exist yet. Tests use it to keep their
// history in a temporary directory.
func NewProviderAtPath(path string) *F4HistoryProvider {
	hp := &F4HistoryProvider{
		path: path,
		data: make(map[string][]string),
		rich: make(map[string][]HistoryRecord),
	}
	hp.load()
	return hp
}

// NewF4HistoryProvider reads the history stored under configDir. The Directory
// is passed in rather than looked up: this package is a leaf and asking the
// configuration for it would be the one import that stops it being one.
func NewF4HistoryProvider(configDir string) *F4HistoryProvider {
	return NewProviderAtPath(filepath.Join(configDir, "history.json"))
}

func (hp *F4HistoryProvider) load() {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	File, err := os.ReadFile(hp.path)
	if err == nil {
		var wrapper struct {
			Data map[string][]string        `json:"data,omitempty"`
			Rich map[string][]HistoryRecord `json:"rich,omitempty"`
		}
		if err := json.Unmarshal(File, &wrapper); err == nil && (wrapper.Data != nil || wrapper.Rich != nil) {
			if wrapper.Data != nil {
				hp.data = wrapper.Data
			}
			if wrapper.Rich != nil {
				hp.rich = wrapper.Rich
			}
		} else {
			var oldData map[string][]string
			if err := json.Unmarshal(File, &oldData); err == nil {
				hp.data = oldData
			}
		}
	}
	if hp.data == nil {
		hp.data = make(map[string][]string)
	}
	if hp.rich == nil {
		hp.rich = make(map[string][]HistoryRecord)
	}
	// A wrapper written before rich history was introduced still has the
	// command and folder buckets in Data. Promote those buckets lazily while
	// retaining Data as the string-compatible view used by vtui.Edit.
	for _, id := range []string{"cmdline", "folders"} {
		if _, ok := hp.rich[id]; ok {
			continue
		}
		if names, ok := hp.data[id]; ok {
			hp.rich[id] = RecordsFromNames(names)
		}
	}
	for id, records := range hp.rich {
		if _, ok := hp.data[id]; !ok && (id == "cmdline" || id == "folders") {
			hp.data[id] = ExtractHistoryNames(records)
		}
	}
}

func (hp *F4HistoryProvider) save() {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	os.MkdirAll(filepath.Dir(hp.path), 0755)
	wrapper := struct {
		Data map[string][]string        `json:"data,omitempty"`
		Rich map[string][]HistoryRecord `json:"rich,omitempty"`
	}{
		Data: hp.data,
		Rich: hp.rich,
	}
	if len(hp.data) == 0 {
		wrapper.Data = nil
	}
	if len(hp.rich) == 0 {
		wrapper.Rich = nil
	}
	File, err := json.MarshalIndent(wrapper, "", "  ")
	if err == nil {
		// Same as the file states: a history that cannot be written is lost
		// at exit, and the user is not in a position to act on the error.
		_ = os.WriteFile(hp.path, File, 0600)
	}
}

func (hp *F4HistoryProvider) LoadHistory(id string) []string {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	if items, ok := hp.data[id]; ok {
		// Return a copy to avoid concurrent slice modification issues
		res := make([]string, len(items))
		copy(res, items)
		return res
	}
	return nil
}

func (hp *F4HistoryProvider) SaveHistory(id string, history []string) {
	hp.mu.Lock()
	if hp.data == nil {
		hp.data = make(map[string][]string)
	}
	hp.data[id] = append([]string(nil), history...)
	if id == "cmdline" || id == "folders" {
		if hp.rich == nil {
			hp.rich = make(map[string][]HistoryRecord)
		}
		hp.rich[id] = MergeHistoryNames(hp.rich[id], history)
	}
	hp.mu.Unlock()
	hp.save()
}
func (hp *F4HistoryProvider) LoadRichHistory(id string) []HistoryRecord {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	if items, ok := hp.rich[id]; ok {
		res := make([]HistoryRecord, len(items))
		copy(res, items)
		return res
	}
	return nil
}

func (hp *F4HistoryProvider) SaveRichHistory(id string, history []HistoryRecord) {
	hp.mu.Lock()
	if hp.rich == nil {
		hp.rich = make(map[string][]HistoryRecord)
	}
	hp.rich[id] = append([]HistoryRecord(nil), history...)
	if hp.data == nil {
		hp.data = make(map[string][]string)
	}
	if id == "cmdline" || id == "folders" {
		hp.data[id] = ExtractHistoryNames(history)
	}
	hp.mu.Unlock()
	hp.save()
}

func RecordsFromNames(names []string) []HistoryRecord {
	if len(names) == 0 {
		return nil
	}
	records := make([]HistoryRecord, 0, len(names))
	for _, name := range names {
		records = append(records, HistoryRecord{Name: name})
	}
	return records
}

func ExtractHistoryNames(records []HistoryRecord) []string {
	if len(records) == 0 {
		return nil
	}
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.Name)
	}
	return names
}

// MergeHistoryNames updates the string-compatible view without throwing away
// metadata that belongs to an entry which is still present. This is needed
// because vtui.Edit can save a plain []string history after rich history has
// already been loaded.
func MergeHistoryNames(old []HistoryRecord, names []string) []HistoryRecord {
	if len(names) == 0 {
		return nil
	}
	byName := make(map[string]HistoryRecord, len(old))
	for _, record := range old {
		if _, exists := byName[record.Name]; !exists {
			byName[record.Name] = record
		}
	}
	merged := make([]HistoryRecord, 0, len(names))
	for _, name := range names {
		record := byName[name]
		record.Name = name
		merged = append(merged, record)
	}
	return merged
}

func LimitRichHistory(history []HistoryRecord, limit int) []HistoryRecord {
	if limit <= 0 || len(history) <= limit {
		return history
	}
	locked := 0
	for _, record := range history {
		if record.Lock {
			locked++
		}
	}
	unlockedBudget := limit - locked
	if unlockedBudget < 0 {
		unlockedBudget = 0
	}
	kept := make([]HistoryRecord, 0, limit)
	for _, record := range history {
		if record.Lock {
			kept = append(kept, record)
			continue
		}
		if unlockedBudget > 0 {
			kept = append(kept, record)
			unlockedBudget--
		}
	}
	return kept
}

func LoadFolderHistoryRecords(provider vtui.HistoryProvider) ([]HistoryRecord, *F4HistoryProvider) {
	hp, _ := provider.(*F4HistoryProvider)
	plain := provider.LoadHistory("folders")
	if hp == nil {
		records := make([]HistoryRecord, 0, len(plain))
		for _, path := range plain {
			records = append(records, HistoryRecord{Name: path})
		}
		return records, nil
	}
	rich := hp.LoadRichHistory("folders")
	if len(rich) == 0 && len(plain) > 0 {
		rich = RecordsFromNames(plain)
	}
	records := make([]HistoryRecord, 0, len(plain))
	for _, path := range plain {
		var record HistoryRecord
		for _, candidate := range rich {
			if SamePath(candidate.Name, path) {
				record = candidate
				break
			}
		}
		if record.Name == "" {
			record.Name = path
		}
		record.Name = path
		records = append(records, record)
	}
	return records, hp
}

func SaveFolderHistoryRecords(hp *F4HistoryProvider, records []HistoryRecord) {
	if hp == nil {
		return
	}
	hp.SaveRichHistory("folders", records)
	hp.SaveHistory("folders", ExtractNames(records))
}

func AddFolderHistory(path string) {
	if path == "" || path == "." || vtui.GlobalHistoryProvider == nil {
		return
	}
	if records, hp := LoadFolderHistoryRecords(vtui.GlobalHistoryProvider); hp != nil {
		current := HistoryRecord{Name: path, Timestamp: time.Now()}
		newHistory := []HistoryRecord{current}
		for _, record := range records {
			if SamePath(record.Name, path) {
				newHistory[0].Lock = record.Lock
				continue
			}
			newHistory = append(newHistory, record)
		}
		newHistory = LimitRichHistory(newHistory, 100)
		SaveFolderHistoryRecords(hp, newHistory)
		return
	}
	h := vtui.GlobalHistoryProvider.LoadHistory("folders")
	// Deduplicate and move to top
	newHist := []string{path}
	for _, item := range h {
		if !SamePath(item, path) {
			newHist = append(newHist, item)
		}
	}
	// Limit to 100 items
	if len(newHist) > 100 {
		newHist = newHist[:100]
	}
	vtui.GlobalHistoryProvider.SaveHistory("folders", newHist)
}

// AddPluginFolderHistory records a folder-history entry owned by a panel
// plugin's VFS (vfs.HistoryPathProvider) rather than a real filesystem path
// (f4#262). display is the text the plugin chose to show; pluginType and
// pluginRef identify, together, which VFS kind and which opaque reference
// panel.NavigateOpenPluginHistoryEntry should try when the entry is picked.
// It requires the rich (*F4HistoryProvider) history store: a plain
// vtui.HistoryProvider has no room for PluginType/PluginRef, and recording
// the display text alone there would make it indistinguishable from a real
// path, which is exactly the bug this hook exists to avoid.
func AddPluginFolderHistory(display, pluginType, pluginRef string) {
	if display == "" || pluginType == "" || pluginRef == "" || vtui.GlobalHistoryProvider == nil {
		return
	}
	records, hp := LoadFolderHistoryRecords(vtui.GlobalHistoryProvider)
	if hp == nil {
		return
	}
	current := HistoryRecord{Name: display, PluginType: pluginType, PluginRef: pluginRef, Timestamp: time.Now()}
	newHistory := []HistoryRecord{current}
	for _, record := range records {
		if record.PluginType == pluginType && record.PluginRef == pluginRef {
			newHistory[0].Lock = record.Lock
			continue
		}
		newHistory = append(newHistory, record)
	}
	newHistory = LimitRichHistory(newHistory, 100)
	SaveFolderHistoryRecords(hp, newHistory)
}
