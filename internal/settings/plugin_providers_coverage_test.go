package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/sdk/f4settings"
)

func TestPluginSettingsProviderCatalogAndDraft(t *testing.T) {
	provider := pluginSettingsProvider{}
	catalog := provider.Catalog()
	if len(catalog.Collections) != 2 || catalog.Collections[0].ID != "plugins.registered" || catalog.Collections[1].ID != "plugins.permissions" {
		t.Fatalf("plugin catalog collections = %+v", catalog.Collections)
	}

	oldConfig := config.App
	oldDir := config.CachedF4ConfigDir
	config.CachedF4ConfigDir = t.TempDir()
	config.App.RegisteredPlugins = []string{" /one.lua ", "/two.wasm"}
	t.Cleanup(func() { config.App = oldConfig })
	t.Cleanup(func() { config.CachedF4ConfigDir = oldDir })
	draft, err := provider.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Records["plugins.registered"]) != 2 || draft.Records["plugins.registered"][1].Values["plugin.Path"] != "/two.wasm" {
		t.Fatalf("plugin draft = %+v", draft.Records)
	}

	draft.Records["plugins.registered"][0].Values["plugin.Path"] = "  "
	if errs := draft.Validate(); len(errs) != 1 || errs["plugin:0"] == nil {
		t.Fatalf("blank plugin validation = %#v", errs)
	}
	draft.Records["plugins.registered"][0].Values["plugin.Path"] = "line\nbreak"
	if errs := draft.Validate(); errs["plugin:0"] == nil {
		t.Fatalf("multiline plugin validation = %#v", errs)
	}
	draft.Records["plugins.registered"][0].Values["plugin.Path"] = "/one.lua"

	got := draft.Commit(context.Background())
	if len(got.Errors) != 0 || len(got.Applied) != 1 || got.Applied[0] != "plugins.registered" {
		t.Fatalf("plugin draft commit = %+v", got)
	}
}

func TestCatalogProviderRowsReplaceAndActions(t *testing.T) {
	tmp := t.TempDir()
	oldDir := config.CachedF4ConfigDir
	config.CachedF4ConfigDir = tmp
	t.Cleanup(func() { config.CachedF4ConfigDir = oldDir })
	installedDir := filepath.Join(tmp, "plugring", "installed")
	if err := os.MkdirAll(installedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir, "manifest.json"), []byte(`{"id":"installed","version":"1.2"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	provider := &catalogSettingsProvider{}
	items := []plughost.PlugRingItem{{ID: "installed", Name: "Installed", Version: "2.0", Description: "old"}, {ID: "new", Name: "New", Version: "1.0"}}
	rows := provider.rows(items)
	if len(rows) != 2 || !strings.Contains(rows[0].Values["catalog.Status"], "1.2") || rows[1].Values["catalog.Status"] != Phrase("Not installed") {
		t.Fatalf("catalog rows = %+v", rows)
	}
	provider.draft = f4settings.NewDraft(nil, map[string][]f4settings.Record{"plugins.catalog": rows})
	provider.replace(items)
	if len(provider.draft.Records["plugins.catalog"]) != 2 {
		t.Fatalf("replaced rows = %+v", provider.draft.Records)
	}
	provider.closed = true
	provider.replace(nil)
	if len(provider.draft.Records["plugins.catalog"]) != 2 {
		t.Fatal("closed provider was replaced")
	}

	catalog := provider.Catalog()
	if len(catalog.Collections) != 1 || len(catalog.Commands) != 1 || len(catalog.Collections[0].Actions) != 2 {
		t.Fatalf("catalog shape = %+v", catalog)
	}
	for _, action := range catalog.Collections[0].Actions {
		if _, err := action.Run(context.Background(), rows[0]); err == nil {
			t.Errorf("%s action without UI context succeeded", action.ID)
		}
	}
	if _, err := catalog.Collections[0].Actions[0].Run(context.Background(), f4settings.Record{Values: map[string]string{"__item": "{"}}); err == nil {
		t.Error("malformed catalog record succeeded")
	}
}
