package main

import (
	"path/filepath"
	"sync"

	androidfs "github.com/unxed/f4/plugins/android"
	"github.com/unxed/f4/plugins/archive"
	"github.com/unxed/f4/plugins/chroma"
	"github.com/unxed/f4/plugins/dummy_internal"
	"github.com/unxed/f4/plugins/envman"
	"github.com/unxed/f4/plugins/id3editor"
	iosfs "github.com/unxed/f4/plugins/ios"
	"github.com/unxed/f4/plugins/netfox"
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
type PluginMenuItem struct {
	Label   string
	Handler func(app vfs.App)
}

var PluginMenuItems []PluginMenuItem

func RegisterPluginMenuItem(label string, handler func(app vfs.App)) {
	pluginRegistryMu.Lock()
	PluginMenuItems = append(PluginMenuItems, PluginMenuItem{Label: label, Handler: handler})
	pluginRegistryMu.Unlock()
}

func pluginMenuItemsSnapshot() []PluginMenuItem {
	pluginRegistryMu.RLock()
	defer pluginRegistryMu.RUnlock()
	return append([]PluginMenuItem(nil), PluginMenuItems...)
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

func NewPluginManager() *PluginManager {
	return &PluginManager{
		api: &coreAPI{},
	}
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
		for _, path := range AppConfig.RegisteredPlugins {
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
	plugringDir := filepath.Join(GetF4ConfigDir(), "plugring")
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
func (pm *PluginManager) loadSinglePlugRingItem(item PlugRingItem) {
	if item.Entrypoint == "" {
		return
	}
	plugringDir := filepath.Join(GetF4ConfigDir(), "plugring")
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
		&archive.ArchivePlugin{},
		androidfs.NewPlugin(),
		iosfs.NewPlugin(),
		&netfox.NetFoxPlugin{},
		&visren.Plugin{},
		&id3editor.ID3EditorPlugin{},
		envman.NewPlugin(GetF4ConfigDir()),
	}

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
