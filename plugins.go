package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/vtui"
)

// Plugin represents a loaded module (Internal, WASM, or Lua).
type Plugin interface {
	Init(api HostAPI) error
	Close() error
	GetName() string
}

type PluginManager struct {
	api     HostAPI
	plugins []Plugin
}

func NewPluginManager() *PluginManager {
	return &PluginManager{
		api: &coreAPI{},
	}
}

func (pm *PluginManager) LoadAll() {
	vtui.DebugLog("--- Loading Plugins ---")

	// 1. Load Internal Plugins
	pm.loadInternal()

	// 2. Load External Plugins (WASM & Lua) from ./plugins dir
	pm.loadExternal("./plugins")
}

func (pm *PluginManager) loadInternal() {
	p := &InternalHelloPlugin{}
	if err := p.Init(pm.api); err == nil {
		pm.plugins = append(pm.plugins, p)
		vtui.DebugLog("Loaded internal plugin: %s", p.GetName())
	}
}

func (pm *PluginManager) loadExternal(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		vtui.DebugLog("Cannot read plugins dir: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// In Far, plugins are usually in subdirectories
			pm.loadExternal(filepath.Join(dir, entry.Name()))
			continue
		}

		path := filepath.Join(dir, entry.Name())

		if strings.HasSuffix(entry.Name(), ".lua") {
			p := NewLuaPlugin(path)
			if err := p.Init(pm.api); err == nil {
				pm.plugins = append(pm.plugins, p)
				vtui.DebugLog("Loaded Lua plugin: %s", p.GetName())
			} else {
				vtui.DebugLog("Failed Lua plugin %s: %v", path, err)
			}
		} else if strings.HasSuffix(entry.Name(), ".wasm") {
			p := NewWasmPlugin(path)
			if err := p.Init(pm.api); err == nil {
				pm.plugins = append(pm.plugins, p)
				vtui.DebugLog("Loaded WASM plugin: %s", p.GetName())
			} else {
				vtui.DebugLog("Failed WASM plugin %s: %v", path, err)
			}
		}
	}
}

func (pm *PluginManager) CloseAll() {
	for _, p := range pm.plugins {
		p.Close()
	}
}
