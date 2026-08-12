package envman

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestProfileCommandsAffectFirstExactDuplicate(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Entries = []Entry{
		{Kind: KindProfile, Name: "duplicate", Variables: []string{"A=first"}},
		{Kind: KindProfile, Name: "duplicate", Variables: []string{"A=second"}},
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	host := newEnvManTestHost("BASE=kept")
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plugin.Close() })

	result, err := plugin.callNonInteractive(context.Background(), vfs.NewOSVFS(directory), directory, []any{"+duplicate"})
	if err != nil || !reflect.DeepEqual(result, []any{true}) {
		t.Fatalf("enable result = %#v, %v", result, err)
	}
	updated := plugin.snapshotConfig()
	if !updated.Entries[0].Enabled || updated.Entries[1].Enabled {
		t.Fatalf("duplicate profile states = %#v", updated.Entries)
	}
	if value, ok := snapshotVariableForTest(host.SnapshotProcessEnvironment(), "A"); !ok || value != "first" {
		t.Fatalf("applied first duplicate A = %q/%v", value, ok)
	}
	if _, err := plugin.callNonInteractive(context.Background(), vfs.NewOSVFS(directory), directory, []any{"*duplicate"}); err != nil {
		t.Fatal(err)
	}
	if plugin.snapshotConfig().Entries[0].Enabled {
		t.Fatal("toggle did not disable the first duplicate")
	}
}

func TestMacroArgumentAndInteractiveErrorsAreClear(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	tests := []struct {
		name      string
		arguments []any
		contains  string
	}{
		{name: "missing", contains: "expects one string"},
		{name: "type", arguments: []any{42}, contains: "must be a string"},
		{name: "manager", arguments: []any{""}, contains: "interactive"},
		{name: "editor", arguments: []any{"e"}, contains: "interactive"},
		{name: "obsolete raw export", arguments: []any{"}"}, contains: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := plugin.callNonInteractive(context.Background(), vfs.NewOSVFS(t.TempDir()), "", test.arguments)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestMacroVFSImportExportAndExportRefresh(t *testing.T) {
	directory := t.TempDir()
	filesystem := vfs.NewOSVFS(directory)
	host := newEnvManTestHost("A=old", "Z=remove")
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plugin.Close() })

	if err := os.WriteFile(filepath.Join(directory, "import.env"), []byte("B=two\nA=new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := plugin.callNonInteractive(context.Background(), filesystem, directory, []any{"<import.env"}); err != nil || !reflect.DeepEqual(result, []any{true}) {
		t.Fatalf("import result = %#v, %v", result, err)
	}
	snapshot := host.SnapshotProcessEnvironment()
	if got, _ := snapshotVariableForTest(snapshot, "A"); got != "new" {
		t.Fatalf("imported A = %q", got)
	}
	if got, _ := snapshotVariableForTest(snapshot, "B"); got != "two" {
		t.Fatalf("imported B = %q", got)
	}
	if _, exists := snapshotVariableForTest(snapshot, "Z"); exists {
		t.Fatal("omitted import variable Z was not removed")
	}

	if _, err := plugin.callNonInteractive(context.Background(), filesystem, directory, []any{">export.env"}); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(filepath.Join(directory, "export.env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(exported) != "A=new\nB=two\n" {
		t.Fatalf("deterministic export = %q", exported)
	}

	app := &envManEditorTestApp{active: filesystem}
	plugin.runFileCommand(app, '>', "ui-export.env")
	if app.refresh != 1 {
		t.Fatalf("successful UI export refresh count = %d", app.refresh)
	}
	if _, err := os.Stat(filepath.Join(directory, "ui-export.env")); err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plugin.callNonInteractive(canceled, filesystem, directory, []any{"<import.env"}); err == nil {
		t.Fatal("canceled VFS import succeeded")
	}
}
