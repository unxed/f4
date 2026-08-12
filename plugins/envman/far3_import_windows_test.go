//go:build windows

package envman

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtui"
	"golang.org/x/sys/windows/registry"
)

func TestReadFar3RegistryCandidate(t *testing.T) {
	path := fmt.Sprintf(`Software\f4-tests\EnvMan-%d-%d`, os.Getpid(), time.Now().UnixNano())
	root, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, path+`\1`)
		_ = registry.DeleteKey(registry.CURRENT_USER, path+`\0`)
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\f4-tests`)
	})
	if err := root.SetStringValue("IgnoredVariables", "KEEP; Path"); err != nil {
		root.Close()
		t.Fatal(err)
	}
	if err := root.SetDWordValue("AlwaysUseEditor", 1); err != nil {
		root.Close()
		t.Fatal(err)
	}
	profile, _, err := registry.CreateKey(root, "0", registry.ALL_ACCESS)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	if err := profile.SetStringValue("", "Far profile"); err != nil {
		profile.Close()
		root.Close()
		t.Fatal(err)
	}
	if err := profile.SetStringsValue("Vars", []string{"FAR_TEST=one", `PATH=%PATH%;C:\Far`}); err != nil {
		profile.Close()
		root.Close()
		t.Fatal(err)
	}
	if err := profile.SetDWordValue("Enabled", 1); err != nil {
		profile.Close()
		root.Close()
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		root.Close()
		t.Fatal(err)
	}
	separator, _, err := registry.CreateKey(root, "1", registry.ALL_ACCESS)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	if err := separator.SetStringValue("", "-"); err != nil {
		separator.Close()
		root.Close()
		t.Fatal(err)
	}
	if err := separator.Close(); err != nil {
		root.Close()
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	candidate, found, err := readFar3RegistryCandidate(path, far3RegistryView{label: far3SourceRegistry64})
	if err != nil {
		t.Fatal(err)
	}
	if !found || candidate.Source != far3SourceRegistry64 {
		t.Fatalf("candidate found/source = %v/%q", found, candidate.Source)
	}
	if len(candidate.Entries) != 2 || candidate.Entries[0].Name != "Far profile" || !candidate.Entries[0].Enabled || candidate.Entries[1].Kind != KindSeparator {
		t.Fatalf("candidate entries = %#v", candidate.Entries)
	}
	if got := strings.Join(candidate.Entries[0].Variables, "|"); got != `FAR_TEST=one|PATH=%PATH%;C:\Far` {
		t.Fatalf("candidate variables = %q", got)
	}
	if strings.Join(candidate.IgnoredVariables, ",") != "KEEP,Path" || !candidate.HasIgnoredVariables || !candidate.HasAlwaysUseEditor || !candidate.AlwaysUseEditor {
		t.Fatalf("candidate settings = %#v", candidate)
	}
}

func TestReadFar3RegistryCandidateUsesLegacyDefaults(t *testing.T) {
	path := fmt.Sprintf(`Software\f4-tests\EnvMan-defaults-%d-%d`, os.Getpid(), time.Now().UnixNano())
	root, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\f4-tests`)
	})

	candidate, found, err := readFar3RegistryCandidate(path, far3RegistryView{label: far3SourceRegistry64})
	if err != nil {
		t.Fatal(err)
	}
	if !found || len(candidate.Entries) != 0 {
		t.Fatalf("candidate found/entries = %v/%#v", found, candidate.Entries)
	}
	if !candidate.HasIgnoredVariables || strings.Join(candidate.IgnoredVariables, ",") != "FARENV_EXPORT_HWND" {
		t.Fatalf("legacy ignored default = %#v", candidate.IgnoredVariables)
	}
	if !candidate.HasAlwaysUseEditor || candidate.AlwaysUseEditor {
		t.Fatalf("legacy editor default = present %v, value %v", candidate.HasAlwaysUseEditor, candidate.AlwaysUseEditor)
	}
}

func TestFar3ImportConfigButtonAppendsProfiles(t *testing.T) {
	initEnvManTestUI()
	plugin := NewPlugin(t.TempDir())
	store, err := NewStoreWithOptions(plugin.configDir, plugin.options)
	if err != nil {
		t.Fatal(err)
	}
	initial := DefaultConfig()
	initial.Entries = []Entry{{Kind: KindProfile, Name: "current", Variables: []string{"CURRENT=one"}}}
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	plugin.store = store
	oldLoader := far3ImportLoader
	far3ImportLoader = func() ([]far3ImportCandidate, error) {
		return []far3ImportCandidate{{
			Source:              far3SourceRegistry64,
			Entries:             []Entry{{Kind: KindProfile, Name: "Far", Enabled: true, Variables: []string{"FAR_TEST=one"}}},
			IgnoredVariables:    []string{"FAR_IGNORED"},
			HasIgnoredVariables: true,
		}}, nil
	}
	t.Cleanup(func() { far3ImportLoader = oldLoader })

	dialog := plugin.openConfigDialog(&envManEditorTestApp{}, false)
	var importButton *vtui.Button
	for _, child := range dialog.GetChildren() {
		if button, ok := child.(*vtui.Button); ok && strings.Contains(button.GetCaption(), "Far Manager 3") {
			importButton = button
			break
		}
	}
	if importButton == nil {
		t.Fatal("Far Manager 3 import button was not found")
	}
	importButton.OnClick()
	prompt, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok || prompt == dialog {
		t.Fatalf("import confirmation = %T", vtui.FrameManager.GetTopFrame())
	}
	prompt.SetExitCode(0)

	got := store.Snapshot()
	if len(got.Entries) != 2 || got.Entries[0].Name != "current" || got.Entries[1].Name != "Far" {
		t.Fatalf("imported entries = %#v", got.Entries)
	}
	if strings.Join(got.IgnoredVariables, ",") != "FAR_IGNORED" {
		t.Fatalf("imported ignored variables = %#v", got.IgnoredVariables)
	}
}
