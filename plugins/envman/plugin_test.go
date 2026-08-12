package envman

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type envManTestRegistration struct {
	mu           sync.Mutex
	unregistered int
}

func (registration *envManTestRegistration) Unregister() {
	registration.mu.Lock()
	registration.unregistered++
	registration.mu.Unlock()
}

type envManTestPrefixRegistration struct {
	envManTestRegistration
	prefix string
}

func (registration *envManTestPrefixRegistration) SetPrefix(prefix string) error {
	registration.prefix = prefix
	return nil
}

type envManTestHost struct {
	commands []vfs.PluginCommand
	prefix   *envManTestPrefixRegistration
	macro    vfs.MacroCallProvider
	regs     []*envManTestRegistration
	logs     []string

	envMu      sync.Mutex
	generation uint64
	variables  map[string]string
	applied    [][]vfs.ProcessEnvironmentChange
}

func newEnvManTestHost(environment ...string) *envManTestHost {
	host := &envManTestHost{variables: make(map[string]string)}
	state, _ := newEnvironmentState(environment, runtimeOptions())
	for _, key := range state.order {
		item := state.values[key]
		host.variables[item.name] = item.value
	}
	return host
}

func (host *envManTestHost) registration() *envManTestRegistration {
	registration := &envManTestRegistration{}
	host.regs = append(host.regs, registration)
	return registration
}

func (*envManTestHost) GetVersion() string                           { return "test" }
func (host *envManTestHost) Log(message string)                      { host.logs = append(host.logs, message) }
func (*envManTestHost) Message(string)                               {}
func (*envManTestHost) RegisterHighlighter(vtui.HighlighterProvider) {}
func (*envManTestHost) RegisterVFSProvider(vfs.VFSProvider)          {}
func (*envManTestHost) RegisterURIProvider(vfs.URIProvider) error    { return nil }
func (*envManTestHost) RegisterDrive(string, func() vfs.VFS)         {}
func (*envManTestHost) RegisterGlobalHotkey(uint16, vtinput.ControlKeyState, func(vfs.App)) {
}
func (*envManTestHost) RegisterPluginMenuItem(string, func(vfs.App)) {}
func (*envManTestHost) RunAction(string) bool                        { return false }

func (host *envManTestHost) RegisterQuickViewProvider(vfs.QuickViewProvider) (vfs.Registration, error) {
	return host.registration(), nil
}

func (host *envManTestHost) RegisterPluginCommand(command vfs.PluginCommand) (vfs.Registration, error) {
	host.commands = append(host.commands, command)
	return host.registration(), nil
}

func (host *envManTestHost) RegisterCommandPrefix(_ string, prefix string, _ func(vfs.App, string)) (vfs.CommandPrefixRegistration, error) {
	host.prefix = &envManTestPrefixRegistration{prefix: prefix}
	host.regs = append(host.regs, &host.prefix.envManTestRegistration)
	return host.prefix, nil
}

func (host *envManTestHost) RegisterMacroCallProvider(provider vfs.MacroCallProvider) (vfs.Registration, error) {
	host.macro = provider
	return host.registration(), nil
}

func (host *envManTestHost) SnapshotProcessEnvironment() vfs.ProcessEnvironmentSnapshot {
	host.envMu.Lock()
	defer host.envMu.Unlock()
	return host.snapshotLocked()
}

func (host *envManTestHost) snapshotLocked() vfs.ProcessEnvironmentSnapshot {
	names := make([]string, 0, len(host.variables))
	for name := range host.variables {
		names = append(names, name)
	}
	sort.Strings(names)
	variables := make([]vfs.ProcessEnvironmentVariable, 0, len(names))
	for _, name := range names {
		variables = append(variables, vfs.ProcessEnvironmentVariable{Name: name, Value: host.variables[name]})
	}
	return vfs.ProcessEnvironmentSnapshot{Generation: host.generation, Variables: variables}
}

func (host *envManTestHost) ApplyProcessEnvironment(changes []vfs.ProcessEnvironmentChange) (vfs.ProcessEnvironmentSnapshot, error) {
	host.envMu.Lock()
	defer host.envMu.Unlock()
	host.applied = append(host.applied, append([]vfs.ProcessEnvironmentChange(nil), changes...))
	for _, change := range changes {
		if change.Unset {
			delete(host.variables, change.Name)
		} else {
			host.variables[change.Name] = change.Value
		}
	}
	host.generation++
	return host.snapshotLocked(), nil
}

func (host *envManTestHost) changeOutside(name, value string) {
	host.envMu.Lock()
	host.variables[name] = value
	host.generation++
	host.envMu.Unlock()
}

func TestPluginRegistersContributionsAppliesProfilesAndClosesIdempotently(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Entries = []Entry{{Kind: KindProfile, Name: "active", Enabled: true, Variables: []string{"A=two", "B=added"}}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}

	host := newEnvManTestHost("A=one")
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if len(host.commands) != 2 || host.commands[0].ID != panelCommandID || host.commands[1].ID != configCommandID {
		t.Fatalf("commands = %#v", host.commands)
	}
	if host.prefix == nil || host.prefix.prefix != commandPrefix {
		t.Fatalf("prefix = %#v", host.prefix)
	}
	wantMacroIDs := []string{macroID, macroAlias, legacyMacroID, legacyMacroHex}
	if len(host.macro.IDs) != len(wantMacroIDs) {
		t.Fatalf("macro IDs = %#v", host.macro.IDs)
	}
	for index := range wantMacroIDs {
		if host.macro.IDs[index] != wantMacroIDs[index] {
			t.Fatalf("macro IDs = %#v", host.macro.IDs)
		}
	}
	snapshot := host.SnapshotProcessEnvironment()
	got := snapshotLines(snapshot)
	if len(got) != 2 || got[0] != "A=two" || got[1] != "B=added" {
		t.Fatalf("startup environment = %#v", got)
	}
	if len(host.applied) != 1 {
		t.Fatalf("Apply calls = %d", len(host.applied))
	}

	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	for index, registration := range host.regs {
		registration.mu.Lock()
		count := registration.unregistered
		registration.mu.Unlock()
		if count != 1 {
			t.Errorf("registration %d unregistered %d times", index, count)
		}
	}
}

func TestCurrentDriftIgnoresConfiguredNames(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.IgnoredVariables = []string{"IGNORED"}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	host := newEnvManTestHost("A=one", "IGNORED=old")
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plugin.Close() })

	host.changeOutside("IGNORED", "new")
	_, drift, err := plugin.currentDrift()
	if err != nil {
		t.Fatal(err)
	}
	if !drift.Empty() {
		t.Fatalf("ignored drift = %#v", drift)
	}
	host.changeOutside("A", "external")
	_, drift, err = plugin.currentDrift()
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Changed) != 1 || drift.Changed[0].Name != "A" {
		t.Fatalf("managed drift = %#v", drift)
	}
}

type blockingEnvManHost struct {
	*envManTestHost
	entered chan struct{}
	release chan struct{}
}

type failingContributionHost struct {
	*envManTestHost
	failAt int
	step   int
}

func (host *failingContributionHost) shouldFail() bool {
	host.step++
	return host.step == host.failAt
}

func (host *failingContributionHost) RegisterPluginCommand(command vfs.PluginCommand) (vfs.Registration, error) {
	if host.shouldFail() {
		return nil, errors.New("injected registration failure")
	}
	return host.envManTestHost.RegisterPluginCommand(command)
}

func (host *failingContributionHost) RegisterCommandPrefix(id, prefix string, handler func(vfs.App, string)) (vfs.CommandPrefixRegistration, error) {
	if host.shouldFail() {
		return nil, errors.New("injected registration failure")
	}
	return host.envManTestHost.RegisterCommandPrefix(id, prefix, handler)
}

func (host *failingContributionHost) RegisterMacroCallProvider(provider vfs.MacroCallProvider) (vfs.Registration, error) {
	if host.shouldFail() {
		return nil, errors.New("injected registration failure")
	}
	return host.envManTestHost.RegisterMacroCallProvider(provider)
}

func (host *blockingEnvManHost) ApplyProcessEnvironment(changes []vfs.ProcessEnvironmentChange) (vfs.ProcessEnvironmentSnapshot, error) {
	close(host.entered)
	<-host.release
	return host.envManTestHost.ApplyProcessEnvironment(changes)
}

func TestPluginCloseWaitsForActiveEnvironmentOperation(t *testing.T) {
	directory := t.TempDir()
	host := &blockingEnvManHost{
		envManTestHost: newEnvManTestHost("A=old"),
		entered:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	plugin := NewPlugin(directory)
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Entries = []Entry{{Kind: KindProfile, Name: "active", Enabled: true, Variables: []string{"A=new"}}}
	if err := plugin.saveConfig(config); err != nil {
		t.Fatal(err)
	}
	applyDone := make(chan error, 1)
	go func() { applyDone <- plugin.applyStoredConfig() }()
	select {
	case <-host.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("environment operation did not enter the host")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- plugin.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the active operation completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(host.release)
	if err := <-applyDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if got, _ := snapshotVariableForTest(host.SnapshotProcessEnvironment(), "A"); got != "new" {
		t.Fatalf("completed environment value = %q", got)
	}
}

func TestPluginRegistrationFailureRollsBackEveryEarlierContribution(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			host := &failingContributionHost{envManTestHost: newEnvManTestHost(), failAt: failAt}
			plugin := NewPlugin(t.TempDir())
			if err := plugin.Init(host); err == nil {
				t.Fatal("Init succeeded despite injected registration failure")
			}
			if plugin.initialized || plugin.api != nil || plugin.store != nil || plugin.envHost != nil {
				t.Fatalf("failed Init retained plugin state: %#v", plugin)
			}
			if len(host.regs) != failAt-1 {
				t.Fatalf("successful registrations = %d, want %d", len(host.regs), failAt-1)
			}
			for index, registration := range host.regs {
				registration.mu.Lock()
				count := registration.unregistered
				registration.mu.Unlock()
				if count != 1 {
					t.Errorf("registration %d rollback count = %d", index, count)
				}
			}
		})
	}
}

func snapshotVariableForTest(snapshot vfs.ProcessEnvironmentSnapshot, name string) (string, bool) {
	for _, variable := range snapshot.Variables {
		if variable.Name == name {
			return variable.Value, true
		}
	}
	return "", false
}

var _ vfs.HostAPI = (*envManTestHost)(nil)
var _ vfs.ContributionHost = (*envManTestHost)(nil)
var _ vfs.ProcessEnvironmentHost = (*envManTestHost)(nil)
