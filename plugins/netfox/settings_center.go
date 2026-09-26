package netfox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/f4/vfs"
)

type settingsProvider struct{ store *NetFoxVFS }

func newSettingsProvider() *settingsProvider {
	dir := vfs.CustomConfigDir
	if dir == "" {
		base, _ := os.UserConfigDir()
		dir = filepath.Join(base, "f4")
	}
	return &settingsProvider{store: &NetFoxVFS{path: filepath.Join(dir, "NetFox.json")}}
}
func (p *settingsProvider) Catalog() f4settings.Catalog {
	defs := []struct {
		name, label, description string
		kind                     f4settings.Kind
	}{
		{"Name", "Connection name", "Name shown in the NetFox connection list.", f4settings.String},
		{"Type", "Protocol", "FTP, SFTP or FISH connection protocol.", f4settings.ChoiceKind},
		{"Host", "Server", "Hostname or address of the remote server.", f4settings.String},
		{"Port", "Port", "Server port; an empty value uses the protocol default.", f4settings.String},
		{"User", "Username", "Login name sent to the server.", f4settings.String},
		{"Pass", "Password", "Server login password. Values are excluded from search.", f4settings.Secret},
		{"KeyPath", "SSH private key", "Private-key file used for SFTP or FISH authentication.", f4settings.Path},
		{"Timeout", "Connection timeout (seconds)", "Connection timeout in seconds; the default is 15.", f4settings.String},
		{"Codepage", "Filename encoding", "Filename codepage used by FTP and SFTP. FISH does not consume this field.", f4settings.String},
		{"Passive", "FTP passive mode", "Use passive FTP data connections instead of active connections.", f4settings.Boolean},
		{"ProxyMode", "Connection proxy", "Inherit f4's global proxy or override it for this connection.", f4settings.ChoiceKind},
		{"ProxyHost", "Proxy host", "Hostname or address of this connection's explicit proxy.", f4settings.String},
		{"ProxyPort", "Proxy port", "Port of this connection's explicit proxy.", f4settings.String},
		{"ProxyUser", "Proxy username", "Username for proxy authentication.", f4settings.String},
		{"ProxyPass", "Proxy password", "Password for proxy authentication; excluded from search.", f4settings.Secret},
	}
	var fields []f4settings.Field
	for _, def := range defs {
		f := f4settings.Scalar("netfox."+def.name, "network", "NetFox connections", def.label, def.description, def.kind)
		switch def.name {
		case "Port", "ProxyPort":
			f.InputWidth = 6
		case "Type":
			// Filtered by what registry.go actually has a handler for, not
			// hardcoded: a lite build (f4#1178, part 3) registers fish+
			// alone, and offering FTP or SFTP as a choice there would save
			// a connection nothing can open.
			var specs []string
			if _, ok := handlers["ftp"]; ok {
				specs = append(specs, "ftp:FTP")
			}
			if _, ok := handlers["sftp"]; ok {
				specs = append(specs, "sftp:SFTP")
			}
			if _, ok := handlers["fish+"]; ok {
				specs = append(specs, "fish:FISH")
			}
			f.Choices = f4settings.Choices(specs...)
		case "ProxyMode":
			f.Choices = f4settings.Choices("0:Inherit f4", "1:System", "2:Direct", "3:HTTP", "4:SOCKS5")
		case "Codepage":
			f.Default = "65001"
			f.InputWidth = 6
		case "Timeout":
			f.Default = "15"
			f.InputWidth = 10
		case "Passive":
			f.Default = "true"
		}
		fields = append(fields, f)
	}
	return f4settings.Catalog{ID: "netfox", Categories: []f4settings.Category{{ID: "network", Label: f4settings.Text{English: "Network & connections"}}}, Collections: []f4settings.Collection{{ID: "netfox.connections", Category: "network", Group: "NetFox connections", Label: f4settings.Text{English: "NetFox connections"}, Description: f4settings.Text{English: "Saved FTP, SFTP and FISH connections. Proxy overrides are edited inline. Apply saves; opening a connection remains a separate command."}, Fields: fields, NameField: "netfox.Name"}}}
}
func (p *settingsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	p.store.mu.Lock()
	initial, err := p.store.readConfigsLocked()
	p.store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	catalog := p.Catalog()
	fields := catalog.Collections[0].Fields
	var names []string
	for name := range initial {
		names = append(names, name)
	}
	sort.Strings(names)
	records := []f4settings.Record{}
	for _, name := range names {
		cfg := initial[name]
		values := map[string]string{}
		for _, f := range fields {
			if f.ID != "netfox.Name" && f.ID != "netfox.Passive" {
				values[f.ID] = f4settings.StructValue(cfg, f.ID)
			}
		}
		values["netfox.Name"] = name
		values["netfox.Passive"] = "true"
		if cfg.Options["Passive"] == "false" {
			values["netfox.Passive"] = "false"
		}
		records = append(records, f4settings.Record{ID: name, Values: values})
	}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{"netfox.connections": records})
	build := func() (map[string]NetFoxConfig, error) {
		result := map[string]NetFoxConfig{}
		for _, r := range d.Records["netfox.connections"] {
			name := strings.TrimSpace(r.Values["netfox.Name"])
			if name == "" || strings.ContainsAny(name, "/\\\r\n") {
				return nil, f4settings.Error("enter a valid connection name")
			}
			if _, ok := result[name]; ok {
				return nil, f4settings.Error("duplicate connection name %s", name)
			}
			oldName := r.ID
			for _, old := range d.BaselineRecords["netfox.connections"] {
				if old.ID == r.ID {
					oldName = old.Values["netfox.Name"]
					break
				}
			}
			cfg := initial[oldName]
			opts := map[string]string{}
			for k, v := range cfg.Options {
				opts[k] = v
			}
			cfg.Options = opts
			for _, f := range fields {
				if f.ID == "netfox.Name" || f.ID == "netfox.Passive" {
					continue
				}
				if err := f4settings.SetStructValue(&cfg, f.ID, r.Values[f.ID]); err != nil {
					return nil, err
				}
			}
			cfg.Options["Passive"] = r.Values["netfox.Passive"]
			result[name] = cfg
		}
		return result, nil
	}
	d.ValidateFunc = func(*f4settings.Draft) map[string]error {
		_, err := build()
		if err != nil {
			return map[string]error{"netfox.connections": err}
		}
		return nil
	}
	d.CommitFunc = func(ctx context.Context, d *f4settings.Draft) f4settings.Result {
		next, err := build()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = p.store.updateConfigs(func(current map[string]NetFoxConfig) error {
				if !reflect.DeepEqual(current, initial) {
					return f4settings.Error("connections changed outside Settings Center; reopen before saving")
				}
				for k := range current {
					delete(current, k)
				}
				for k, v := range next {
					current[k] = v
				}
				return nil
			})
		}
		if err != nil {
			return f4settings.Result{Errors: map[string]error{"netfox.connections": err}}
		}
		initial = next
		return f4settings.Result{Applied: []string{"netfox.connections"}}
	}
	return d, nil
}
