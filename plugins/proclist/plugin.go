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

// Plugin exposes the process list as an in-process f4 panel plugin.
type Plugin struct {
	mu           sync.Mutex
	registration vfs.Registration
	initialized  bool
}

// NewPlugin constructs the built-in ProcList plugin.
func NewPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) GetName() string { return "ProcList" }

// Init registers the panel provider. On a platform Supported() reports
// false for, it does nothing and returns nil: v1 is Linux-only by design
// (f4#312 part 1 of 4), which is not a load failure elsewhere -- the same
// way plugins/ios's core_access_stub.go leaves a capability quietly absent
// on a platform it does not cover yet.
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

	registration, err := host.RegisterPanelProvider(vfs.PanelProvider{
		ID:          panelProviderID,
		Title:       "ProcList",
		Description: "Live list of running processes: PID, name, memory and CPU%",
		Open:        newProcListPanel,
	})
	if err != nil {
		return fmt.Errorf("ProcList: register panel provider: %w", err)
	}

	p.mu.Lock()
	p.registration = registration
	p.initialized = true
	p.mu.Unlock()
	return nil
}

func (p *Plugin) Close() error {
	p.mu.Lock()
	registration := p.registration
	p.registration = nil
	p.initialized = false
	p.mu.Unlock()
	if registration != nil {
		registration.Unregister()
	}
	return nil
}
