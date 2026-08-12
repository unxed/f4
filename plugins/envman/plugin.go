package envman

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
)

const (
	panelCommandID  = "f4.envman.open"
	configCommandID = "f4.envman.configure"
	prefixID        = "f4.envman.prefix"
	macroID         = "f4.envman"
	macroAlias      = "envman"
	legacyMacroID   = "4D766E45"
	legacyMacroHex  = "0x4D766E45"
	commandPrefix   = "envman"
)

// Plugin is the in-process Environment Manager port. The Store owns durable
// profiles, Engine derives their target environment, and ProcessEnvironmentHost
// is the sole mutation boundary for f4 and its local shell workspaces.
type Plugin struct {
	mu   sync.Mutex
	opMu sync.Mutex

	configDir string
	options   EngineOptions
	api       vfs.HostAPI
	store     *Store
	engine    *Engine
	envHost   vfs.ProcessEnvironmentHost

	registrations []vfs.Registration
	prefix        vfs.CommandPrefixRegistration
	lastApplied   vfs.ProcessEnvironmentSnapshot
	initialized   bool
}

func NewPlugin(configDir string) *Plugin {
	return &Plugin{configDir: configDir, options: OptionsForGOOS(runtime.GOOS)}
}

func (plugin *Plugin) GetName() string {
	return plugin.text("EnvMan.Name", "Environment Manager", "Менеджер окружения")
}

func (plugin *Plugin) Init(api vfs.HostAPI) error {
	if api == nil {
		return errors.New("Environment Manager: nil host API")
	}
	contributions, ok := api.(vfs.ContributionHost)
	if !ok {
		return errors.New("Environment Manager: this host does not support plugin contributions")
	}
	environmentHost, ok := api.(vfs.ProcessEnvironmentHost)
	if !ok {
		return errors.New("Environment Manager: this host does not expose its process environment")
	}
	plugin.mu.Lock()
	if !plugin.options.WindowsCaseFold && !plugin.options.ExpandDollarSyntax && len(plugin.options.ReservedNames) == 0 {
		plugin.options = OptionsForGOOS(runtime.GOOS)
	}
	plugin.mu.Unlock()

	plugin.mu.Lock()
	if plugin.initialized || plugin.api != nil {
		plugin.mu.Unlock()
		return errors.New("Environment Manager: plugin is already initialized")
	}
	plugin.api = api
	plugin.envHost = environmentHost
	plugin.mu.Unlock()

	store, loadErr := NewStoreWithOptions(plugin.settingsDirectory(), plugin.options)
	if store == nil {
		plugin.resetAfterFailedInit()
		return fmt.Errorf("Environment Manager: open settings: %w", loadErr)
	}
	if loadErr != nil {
		api.Log(loadErr.Error() + "; using defaults")
	}
	initial := environmentHost.SnapshotProcessEnvironment()
	engine, err := NewEngine(snapshotLines(initial), plugin.options)
	if err != nil {
		plugin.resetAfterFailedInit()
		return fmt.Errorf("Environment Manager: capture baseline environment: %w", err)
	}
	plugin.mu.Lock()
	plugin.store = store
	plugin.engine = engine
	plugin.lastApplied = cloneEnvironmentSnapshot(initial)
	plugin.mu.Unlock()

	registrations := make([]vfs.Registration, 0, 4)
	rollback := func(cause error) error {
		for index := len(registrations) - 1; index >= 0; index-- {
			registrations[index].Unregister()
		}
		plugin.resetAfterFailedInit()
		return cause
	}

	panelRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
		ID:       panelCommandID,
		Location: vfs.PluginCommandPanel,
		Label:    plugin.text("EnvMan.Menu", "&Environment Manager", "&Менеджер окружения"),
		Run:      plugin.openFromMenu,
	})
	if err != nil {
		return rollback(fmt.Errorf("Environment Manager: register panel command: %w", err))
	}
	registrations = append(registrations, panelRegistration)

	configRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
		ID:       configCommandID,
		Location: vfs.PluginCommandConfig,
		Label:    plugin.text("EnvMan.ConfigMenu", "Environment Manager", "Менеджер окружения"),
		Run:      plugin.configure,
	})
	if err != nil {
		return rollback(fmt.Errorf("Environment Manager: register configuration command: %w", err))
	}
	registrations = append(registrations, configRegistration)

	prefixRegistration, err := contributions.RegisterCommandPrefix(prefixID, commandPrefix, plugin.handlePrefix)
	if err != nil {
		return rollback(fmt.Errorf("Environment Manager: register command prefix: %w", err))
	}
	registrations = append(registrations, prefixRegistration)
	plugin.mu.Lock()
	plugin.prefix = prefixRegistration
	plugin.mu.Unlock()

	macroRegistration, err := contributions.RegisterMacroCallProvider(vfs.MacroCallProvider{
		IDs:  []string{macroID, macroAlias, legacyMacroID, legacyMacroHex},
		Call: plugin.callMacro,
	})
	if err != nil {
		return rollback(fmt.Errorf("Environment Manager: register macro provider: %w", err))
	}
	registrations = append(registrations, macroRegistration)

	if err := plugin.applyStoredConfig(); err != nil {
		return rollback(fmt.Errorf("Environment Manager: apply startup profiles: %w", err))
	}
	plugin.mu.Lock()
	plugin.registrations = registrations
	plugin.initialized = true
	plugin.mu.Unlock()
	return nil
}

func (plugin *Plugin) Close() error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	plugin.mu.Lock()
	registrations := append([]vfs.Registration(nil), plugin.registrations...)
	plugin.registrations = nil
	plugin.prefix = nil
	plugin.api = nil
	plugin.store = nil
	plugin.engine = nil
	plugin.envHost = nil
	plugin.lastApplied = vfs.ProcessEnvironmentSnapshot{}
	plugin.initialized = false
	plugin.mu.Unlock()
	for index := len(registrations) - 1; index >= 0; index-- {
		registrations[index].Unregister()
	}
	return nil
}

func (plugin *Plugin) resetAfterFailedInit() {
	plugin.mu.Lock()
	plugin.api = nil
	plugin.store = nil
	plugin.engine = nil
	plugin.envHost = nil
	plugin.prefix = nil
	plugin.registrations = nil
	plugin.lastApplied = vfs.ProcessEnvironmentSnapshot{}
	plugin.initialized = false
	plugin.mu.Unlock()
}

func (plugin *Plugin) settingsDirectory() string {
	if directory := strings.TrimSpace(plugin.configDir); directory != "" {
		return directory
	}
	if directory := strings.TrimSpace(vfs.CustomConfigDir); directory != "" {
		return directory
	}
	if directory, err := os.UserConfigDir(); err == nil {
		return filepath.Join(directory, "f4")
	}
	return "."
}

func (plugin *Plugin) snapshotConfig() Config {
	plugin.mu.Lock()
	store := plugin.store
	plugin.mu.Unlock()
	if store == nil {
		return DefaultConfig()
	}
	return store.Snapshot()
}

func (plugin *Plugin) saveConfig(config Config) error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	plugin.mu.Lock()
	store := plugin.store
	plugin.mu.Unlock()
	if store == nil {
		return plugin.notInitializedError()
	}
	return store.Save(config)
}

func (plugin *Plugin) reloadConfig() error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	plugin.mu.Lock()
	store := plugin.store
	plugin.mu.Unlock()
	if store == nil {
		return plugin.notInitializedError()
	}
	return store.Reload()
}

func (plugin *Plugin) mutateConfig(mutate func(Config) (Config, error), apply bool) error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	plugin.mu.Lock()
	store := plugin.store
	plugin.mu.Unlock()
	if store == nil {
		return plugin.notInitializedError()
	}
	next, err := mutate(store.Snapshot())
	if err != nil {
		return err
	}
	if err := store.Save(next); err != nil {
		return err
	}
	if apply {
		return plugin.applyStoredConfigLocked()
	}
	return nil
}

func (plugin *Plugin) applyStoredConfig() error {
	plugin.opMu.Lock()
	defer plugin.opMu.Unlock()
	return plugin.applyStoredConfigLocked()
}

func (plugin *Plugin) applyStoredConfigLocked() error {
	plugin.mu.Lock()
	store := plugin.store
	engine := plugin.engine
	environmentHost := plugin.envHost
	plugin.mu.Unlock()
	if store == nil || engine == nil || environmentHost == nil {
		return plugin.notInitializedError()
	}
	current := environmentHost.SnapshotProcessEnvironment()
	reconciliation, err := engine.Reconcile(store.Snapshot(), snapshotLines(current))
	if err != nil {
		return err
	}
	if reconciliation.Changes.Empty() {
		plugin.setLastApplied(current)
		return nil
	}
	updated, err := environmentHost.ApplyProcessEnvironment(environmentChanges(reconciliation.Changes))
	if err != nil {
		return err
	}
	plugin.setLastApplied(updated)
	return nil
}

func (plugin *Plugin) currentDrift() (vfs.ProcessEnvironmentSnapshot, Diff, error) {
	plugin.mu.Lock()
	environmentHost := plugin.envHost
	last := cloneEnvironmentSnapshot(plugin.lastApplied)
	engine := plugin.engine
	store := plugin.store
	plugin.mu.Unlock()
	if environmentHost == nil || engine == nil || store == nil {
		return vfs.ProcessEnvironmentSnapshot{}, Diff{}, plugin.notInitializedError()
	}
	current := environmentHost.SnapshotProcessEnvironment()
	if current.Generation == last.Generation {
		return current, Diff{}, nil
	}
	reconciliation, err := engine.Reconcile(store.Snapshot(), snapshotLines(current))
	if err != nil {
		return current, Diff{}, err
	}
	return current, reconciliation.Drift, nil
}

func (plugin *Plugin) setLastApplied(snapshot vfs.ProcessEnvironmentSnapshot) {
	plugin.mu.Lock()
	plugin.lastApplied = cloneEnvironmentSnapshot(snapshot)
	plugin.mu.Unlock()
}

func (plugin *Plugin) log(format string, arguments ...any) {
	plugin.mu.Lock()
	api := plugin.api
	plugin.mu.Unlock()
	if api != nil {
		api.Log(fmt.Sprintf(format, arguments...))
	}
}

func snapshotLines(snapshot vfs.ProcessEnvironmentSnapshot) []string {
	lines := make([]string, 0, len(snapshot.Variables))
	for _, variable := range snapshot.Variables {
		lines = append(lines, variable.Name+"="+variable.Value)
	}
	return lines
}

func cloneEnvironmentSnapshot(snapshot vfs.ProcessEnvironmentSnapshot) vfs.ProcessEnvironmentSnapshot {
	copy := snapshot
	copy.Variables = append([]vfs.ProcessEnvironmentVariable(nil), snapshot.Variables...)
	return copy
}

func environmentChanges(diff Diff) []vfs.ProcessEnvironmentChange {
	changes := make([]vfs.ProcessEnvironmentChange, 0, len(diff.Added)+len(diff.Changed)+len(diff.Removed))
	for _, change := range diff.Added {
		changes = append(changes, vfs.ProcessEnvironmentChange{Name: change.Name, Value: change.After})
	}
	for _, change := range diff.Changed {
		changes = append(changes, vfs.ProcessEnvironmentChange{Name: change.Name, Value: change.After})
	}
	for _, change := range diff.Removed {
		changes = append(changes, vfs.ProcessEnvironmentChange{Name: change.Name, Unset: true})
	}
	return changes
}

func (plugin *Plugin) callMacro(ctx context.Context, callContext vfs.MacroCallContext, arguments []any) ([]any, error) {
	return plugin.callNonInteractive(ctx, callContext.Current.VFS, callContext.Current.Dir, arguments)
}
