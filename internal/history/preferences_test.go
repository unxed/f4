package history

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyDisabledBucketsDisablesAndRestoresPersistedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	hp := NewProviderAtPath(path)
	hp.SaveHistory("cmdline", []string{"echo old"})
	hp.SaveHistory("folders", []string{"/old"})

	ApplyDisabledBuckets(hp, "cmdline; folders")
	if got := hp.LoadHistory("cmdline"); got != nil {
		t.Fatalf("disabled command history = %#v", got)
	}
	hp.SaveHistory("cmdline", []string{"echo new"})
	ApplyDisabledBuckets(hp, "folders")
	if got := hp.LoadHistory("cmdline"); !reflect.DeepEqual(got, []string{"echo old"}) {
		t.Fatalf("re-enabled command history = %#v", got)
	}
	if got := hp.LoadHistory("folders"); got != nil {
		t.Fatalf("disabled folder history = %#v", got)
	}
}
