package netfox

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSettingsRenamePreservesOptionsAndCredentials(t *testing.T) {
	store := NewNetFoxVFS(filepath.Join(t.TempDir(), "NetFox.json"))
	ctx := context.Background()
	if err := store.SaveConfig("original", NetFoxConfig{Type: "sftp", Host: "example.test", Pass: "secret", Path: "/work", Options: map[string]string{"future": "keep"}}); err != nil {
		t.Fatal(err)
	}
	p := &settingsProvider{store: store}
	d, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, name := range []string{"renamed", "again"} {
		d.Records["netfox.connections"][0].Values["netfox.Name"] = name
		if r := d.Commit(ctx); len(r.Errors) > 0 {
			t.Fatal(r.Errors)
		}
		cfg := store.getConfigs()[name]
		if cfg.Options["future"] != "keep" || cfg.Pass != "secret" || cfg.Path != "/work" {
			t.Fatal("rename lost hidden options, credentials, or start folder")
		}
	}
	if err := store.SaveConfig("other", NetFoxConfig{Type: "ftp", Host: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	d.Records["netfox.connections"][0].Values["netfox.Host"] = "changed"
	if r := d.Commit(ctx); len(r.Errors) == 0 || !d.Dirty("netfox.connections") {
		t.Fatal("concurrent update was overwritten")
	}
}
