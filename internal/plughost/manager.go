package plughost

import (
	"path/filepath"
	"sync"

	"github.com/unxed/f4/internal/config"
	androidfs "github.com/unxed/f4/plugins/android"
	"github.com/unxed/f4/plugins/chroma"
	"github.com/unxed/f4/plugins/dummy_internal"
	"github.com/unxed/f4/plugins/envman"
	"github.com/unxed/f4/plugins/id3editor"
	iosfs "github.com/unxed/f4/plugins/ios"
	"github.com/unxed/f4/plugins/mediainfo"
	sqliteplugin "github.com/unxed/f4/plugins/sqlite"
	"github.com/unxed/f4/plugins/visren"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// Plugin represents a loaded module.
type Plugin interface {
	Init(api vfs.HostAPI) error
	Close() error
	GetName() string
}
type PluginManager struct {
	mu           sync.Mutex
	api          vfs.HostAPI
	plugins      []Plugin
	closed       bool
	internalOnce sync.Once
	externalOnce sync.Once
	// externalLoader is a test seam for the startup phase. Production managers
	// leave it nil and use the configured/PlugRing discovery below.
	externalLoader func()
}

var GlobalPluginManager *PluginManager

func NewPluginManager(api vfs.HostAPI) *PluginManager {
	return &PluginManager{api: api}
}

func (pm *PluginManager) LoadAll() {
	vtui.DebugLog("--- Loading Plugins ---")
	pm.LoadInternal()
	pm.LoadExternal()
}

// LoadInternal initializes built-in plugins synchronously. Their Init methods
// only register local capabilities, so panels can restore URI-backed sessions
// without waiting for external plugin discovery or subprocess startup.
func (pm *PluginManager) LoadInternal() {
	pm.internalOnce.Do(pm.loadInternal)
}

// LoadExternal loads configured and PlugRing plugins. Startup calls this only
// after the initial desktop and panels have been constructed: external plugin
// initialization may ask for permissions or synchronously call back into the
// UI, neither of which can complete before FrameManager.Run starts consuming
// posted tasks.
func (pm *PluginManager) LoadExternal() {
	pm.externalOnce.Do(func() {
		if pm.externalLoader != nil {
			pm.externalLoader()
			return
		}
		for _, path := range config.App.RegisteredPlugins {
			pm.LoadExternalPlugin(path)
		}
		pm.loadPlugRing()
	})
}

// StartExternal schedules the potentially interactive external phase without
// holding up startup. Built-in plugins are loaded separately and synchronously
// so their URI providers are already present when the saved panel paths are
// restored.
func (pm *PluginManager) StartExternal() {
	if pm == nil {
		return
	}
	pm.mu.Lock()
	closed := pm.closed
	pm.mu.Unlock()
	if closed {
		return
	}
	go pm.LoadExternal()
}

func (pm *PluginManager) keepPlugin(p Plugin) bool {
	pm.mu.Lock()
	if pm.closed {
		pm.mu.Unlock()
		_ = p.Close()
		return false
	}
	pm.plugins = append(pm.plugins, p)
	pm.mu.Unlock()
	return true
}

func (pm *PluginManager) LoadExternalPlugin(path string) {
	p := newPluginForEntrypoint("", path)
	if err := p.Init(pm.api); err == nil {
		if pm.keepPlugin(p) {
			vtui.DebugLog("Loaded plugin: %s", p.GetName())
		}
	} else {
		vtui.DebugLog("Failed plugin %s: %v", path, err)
	}
}
func (pm *PluginManager) loadPlugRing() {
	installed := GetInstalledPlugRingItems()
	plugringDir := filepath.Join(config.GetF4ConfigDir(), "plugring")
	for id, item := range installed {
		if item.Entrypoint != "" {
			// We build a pseudo-path that NewRPCPlugin will handle specifically later if needed,
			// but since NewRPCPlugin uses exec.Command directly, we pass the command.
			// The RPCPlugin execution logic will need to handle splitting by spaces if it's a shell command.
			p := newPluginForPlugRingItem(filepath.Join(plugringDir, id), item)
			if err := p.Init(pm.api); err == nil {
				if pm.keepPlugin(p) {
					vtui.DebugLog("Loaded PlugRing RPC plugin: %s", p.GetName())
				}
			} else {
				vtui.DebugLog("Failed PlugRing RPC plugin %s: %v", id, err)
			}
		}
	}
}
func (pm *PluginManager) LoadSinglePlugRingItem(item PlugRingItem) {
	if item.Entrypoint == "" {
		return
	}
	plugringDir := filepath.Join(config.GetF4ConfigDir(), "plugring")
	pluginDir := filepath.Join(plugringDir, item.ID)

	p := newPluginForPlugRingItem(pluginDir, item)
	if err := p.Init(pm.api); err == nil {
		if pm.keepPlugin(p) {
			vtui.DebugLog("Hot-loaded PlugRing RPC plugin: %s", p.GetName())
		}
	} else {
		vtui.DebugLog("Failed to hot-load PlugRing RPC plugin %s: %v", item.ID, err)
	}
}

func (pm *PluginManager) loadInternal() {
	plugins := []Plugin{
		&chroma.Plugin{},
		&dummy_internal.InternalDummyPlugin{},
		androidfs.NewPlugin(),
		iosfs.NewPlugin(),
		&visren.Plugin{},
		&id3editor.ID3EditorPlugin{},
		envman.NewPlugin(config.GetF4ConfigDir()),
		mediainfo.NewPlugin(config.GetF4ConfigDir()),
		sqliteplugin.NewPlugin(),
	}
	// archive, cloudfox (cloud services) and netfox (ftp/sftp/scp) are the
	// heavy VFS providers f4#1178 excludes from a lite build; see
	// plugins_lite.go/plugins_full.go, the single point of truth for which
	// build tag gets which set. A later slice replaces them with plugins
	// that wrap CLI archivers and ssh/scp/sftp instead (f4#1178, part 2).
	plugins = append(plugins, optionalVFSPlugins()...)

	for _, p := range plugins {
		if err := p.Init(pm.api); err == nil {
			if pm.keepPlugin(p) {
				vtui.DebugLog("Loaded internal plugin: %s", p.GetName())
			}
		} else {
			vtui.DebugLog("Failed to init internal plugin %T: %v", p, err)
		}
	}
}

func (pm *PluginManager) CloseAll() {
	pm.mu.Lock()
	pm.closed = true
	plugins := append([]Plugin(nil), pm.plugins...)
	pm.plugins = nil
	pm.mu.Unlock()
	// Close outside the manager lock so a plugin can finish callbacks without
	// deadlocking on registry or manager work. Reverse order mirrors startup.
	for i := len(plugins) - 1; i >= 0; i-- {
		if err := plugins[i].Close(); err != nil {
			vtui.DebugLog("Failed to close plugin %s: %v", plugins[i].GetName(), err)
		}
	}
}

// Names lists the loaded plugins by GetName, in load order; it is what
// f4:about reports. The lock is held only to copy the list, so a GetName
// that takes its time cannot hold up a plugin being loaded meanwhile.
func (pm *PluginManager) Names() []string {
	if pm == nil {
		return nil
	}
	pm.mu.Lock()
	plugins := append([]Plugin(nil), pm.plugins...)
	pm.mu.Unlock()
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.GetName())
	}
	return names
}
