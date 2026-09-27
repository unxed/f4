// Package proclist is f4's built-in ProcList plugin: a live list of running
// processes shown as a panel, in the spirit of FAR Manager 3's ProcList
// plugin (Plist.cpp/Pclass.cpp), used as a reference while scoping f4#312.
//
// v1 (f4#312 part 1 of 4) was deliberately narrow: Linux only, view-only
// (PID, name, memory, CPU%), refreshed a few times a second. Part 2 added
// real collectors for Windows (collector_windows.go, Toolhelp32 +
// GetProcessTimes, no WMI) and macOS (collector_darwin.go, sysctl
// kern.proc.all + libproc's proc_pidinfo). The BSDs stayed on the
// collector_other.go stub for part 2 -- see that file's comment for why.
// Part 3 (actions.go, actions_unix.go, actions_windows.go) added process
// management: F8 kill (with confirmation), Shift-F1/F2 priority, and a
// Ctrl+F8 suspend/resume toggle the owner asked for on *nix only, since
// Windows has no supported API for it. FAR3's ProcList also offers rich
// metrics/handles/remote view built on WMI and undocumented NT APIs that
// have no portable equivalent; the owner confirmed (f4#312) this plugin
// should not attempt those at all.
//
// It is also f4's first consumer of vfs.PanelProvider/PanelController
// (vfs/contributions.go, internal/plughost/panel_providers.go): a plugin
// panel that owns its own drawing and key handling, rather than emulating a
// process list through the VFS provider API, which has no natural mapping
// for "a directory entry is a running process".
package proclist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
)

// panelProviderID is also the ID plughost.RegisterPanelProvider derives its
// auto-generated "Open ProcList" command ID from: "panel." + this ID,
// lowercased (internal/plughost/panel_providers.go). internal/app's own
// menu row and hotkey (proclist_actions.go) call that derived command by ID,
// duplicated there as a constant for the same reason sqlite_actions.go
// duplicates plugins/sqlite's command ID rather than importing it.
const panelProviderID = "f4.proclist"

// configCommandID is ProcList.Config's own command, registered into f4's F9
// "Plugin configuration" menu (Settings.PluginConfiguration,
// vfs.PluginCommandConfig) the same way plugins/mediainfo/plugin.go
// registers its own configCommandID.
const configCommandID = "f4.proclist.configure"

// Plugin exposes the process list as an in-process f4 panel plugin.
type Plugin struct {
	mu           sync.Mutex
	configDir    string
	registration vfs.Registration
	configReg    vfs.Registration
	store        *settingsStore
	initialized  bool
}

// NewPlugin constructs the built-in ProcList plugin. configDir is where its
// own settings.json (settings.go, f4#312 part 4 of 4) lives, the same
// per-plugin config directory plugins/mediainfo.NewPlugin and
// plugins/envman.NewPlugin already take -- internal/plughost/manager.go
// passes config.GetF4ConfigDir() for all three.
func NewPlugin(configDir string) *Plugin { return &Plugin{configDir: configDir} }

func (p *Plugin) GetName() string { return "ProcList" }

// Init registers the panel provider and, where the host supports it,
// ProcList.Config's F9 "Plugin configuration" entry. On a platform
// Supported() reports false for, it does nothing and returns nil: v1 is
// Linux-only by design (f4#312 part 1 of 4), which is not a load failure
// elsewhere -- the same way plugins/ios's core_access_stub.go leaves a
// capability quietly absent on a platform it does not cover yet.
func (p *Plugin) Init(api vfs.HostAPI) error {
	if api == nil {
		return errors.New("ProcList: nil host API")
	}
	if !Supported() {
		return nil
	}

	host, ok := api.(vfs.PanelContributionHost)
	if !ok {
		return errors.New("ProcList: host does not support panel contributions")
	}

	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return errors.New("ProcList: plugin is already initialized")
	}
	p.mu.Unlock()

	store, loadErr := newSettingsStore(p.settingsDirectory())
	if loadErr != nil {
		api.Log(loadErr.Error() + "; using defaults")
	}
	p.mu.Lock()
	p.store = store
	p.mu.Unlock()

	registration, err := host.RegisterPanelProvider(vfs.PanelProvider{
		ID:          panelProviderID,
		Title:       "ProcList",
		Description: "Live list of running processes: PID, name, memory and CPU%",
		Open: func(ctx vfs.PanelContext) (vfs.PanelController, error) {
			return newProcListPanel(ctx, p.settingsStore())
		},
	})
	if err != nil {
		p.mu.Lock()
		p.store = nil
		p.mu.Unlock()
		return fmt.Errorf("ProcList: register panel provider: %w", err)
	}

	// ProcList.Config (settings.go) is an optional add-on, not core to the
	// panel working at all: vfs.ContributionHost is a separate, heavier
	// interface (RegisterQuickViewProvider/RegisterPluginCommand/
	// RegisterCommandPrefix/RegisterMacroCallProvider, none of which this
	// plugin otherwise needs) that older host implementations and minimal
	// test doubles are not required to implement -- see its own comment
	// (vfs/contributions.go). A host that lacks it still gets the live
	// process list; it just has no F9 entry to configure it from.
	var configReg vfs.Registration
	if configHost, ok := api.(vfs.ContributionHost); ok {
		reg, err := configHost.RegisterPluginCommand(vfs.PluginCommand{
			ID:       configCommandID,
			Location: vfs.PluginCommandConfig,
			Label:    "ProcList",
			LabelKey: "ProcList.Config.MenuLabel",
			Run:      p.configure,
		})
		if err != nil {
			registration.Unregister()
			p.mu.Lock()
			p.store = nil
			p.mu.Unlock()
			return fmt.Errorf("ProcList: register configuration command: %w", err)
		}
		configReg = reg
	}

	p.mu.Lock()
	p.registration = registration
	p.configReg = configReg
	p.initialized = true
	p.mu.Unlock()
	return nil
}

// settingsStore returns the settings store Init loaded, or nil before Init
// has run (or after Close). configure (config_dialog.go) and the panel
// provider's Open callback above both read it through this getter rather
// than the field directly, matching plugins/mediainfo/plugin.go's own
// settings()/store access pattern.
func (p *Plugin) settingsStore() *settingsStore {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.store
}

func (p *Plugin) Close() error {
	p.mu.Lock()
	registration := p.registration
	configReg := p.configReg
	p.registration = nil
	p.configReg = nil
	p.store = nil
	p.initialized = false
	p.mu.Unlock()
	if configReg != nil {
		configReg.Unregister()
	}
	if registration != nil {
		registration.Unregister()
	}
	return nil
}

// settingsDirectory mirrors plugins/mediainfo/plugin.go's own
// settingsDirectory: configDir if NewPlugin got one, else vfs.CustomConfigDir
// (a portable install's own override), else the OS user-config directory, so
// a test or an unusual embedding that skips NewPlugin's usual configDir
// argument still gets a sane, writable location instead of failing outright.
func (p *Plugin) settingsDirectory() string {
	if strings.TrimSpace(p.configDir) != "" {
		return p.configDir
	}
	if strings.TrimSpace(vfs.CustomConfigDir) != "" {
		return vfs.CustomConfigDir
	}
	if directory, err := os.UserConfigDir(); err == nil {
		return filepath.Join(directory, "f4")
	}
	return "."
}
