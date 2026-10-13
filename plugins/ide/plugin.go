// Package ide is the scaffold for f4#382's "IDE mode": a plugin, not a core
// feature, built entirely on mechanisms f4 already has rather than a new
// editor/highlighter/runner of its own. See the architecture comment on
// https://github.com/unxed/f4/issues/382 for the full plan and the owner's
// confirmation of it:
//
//   - a panel plugin for "Build & Problems" (#658's VFS-less panel plugins,
//     the same mechanism plugins/proclist already uses for a live process
//     list);
//   - configurable searchable commands (IDE.Build/IDE.Run/IDE.Test, docs/
//     PLUGINS.md section B) instead of hardcoded hotkeys -- the user assigns
//     their own key through the F11 command palette's F4, the same way any
//     other plugin command gets bound;
//   - jumping from a diagnostic to its file:line:col reuses the editor's
//     existing "Find All" navigation (internal/editor/findall.go) rather than
//     inventing a second way to open a file at a position;
//   - long-running build/test/run invocations go through
//     Host.RunProgressTask, like every other plugin's background work;
//   - the first supported language is Go.
//
// This first part (per the owner's decision) is only the plugin's skeleton:
// registration, a minimal command entry per verb, and this documentation of
// what each stub will grow into. None of the three commands parses a
// toolchain's output yet -- they exist so the plugin is visible and
// assignable in the command palette while the Go analysis (parsing `go
// build`/`go vet`, the "Build & Problems" panel itself, and the .f4ide.ini
// project config) lands in later, separate steps.
package ide

import (
	"errors"
	"sync"

	"github.com/unxed/f4/vfs"
)

const (
	// buildCommandID, runCommandID and testCommandID are stable IDs for the
	// three IDE.* searchable commands (docs/PLUGINS.md section B). Stable
	// IDs matter here specifically because the owner asked for user-assigned
	// hotkeys (F11 palette + F4) rather than hardcoded keys: the binding in
	// hotkeys.ini is keyed by command ID, so it must never change once a
	// user has bound something to it.
	buildCommandID = "f4.ide.build"
	runCommandID   = "f4.ide.run"
	testCommandID  = "f4.ide.test"
)

// Plugin is the built-in IDE-mode plugin (f4#382). It is registered the same
// way as plugins/mediainfo and plugins/proclist: as an in-process built-in
// in internal/plughost/manager.go, not a subprocess RPC plugin.
type Plugin struct {
	mu          sync.Mutex
	registrars  []vfs.Registration
	initialized bool
}

// NewPlugin constructs the IDE plugin. It currently takes no arguments;
// once the Go analysis step lands, this is where a per-project config
// directory (for a future settings.json / cached diagnostics) would be
// threaded through, the same way plugins/mediainfo.NewPlugin and
// plugins/proclist.NewPlugin take configDir today.
func NewPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) GetName() string { return "IDE" }

// Init registers the three IDE.* commands. It intentionally does not
// register a panel provider yet: the "Build & Problems" panel is the next
// atomic part (it needs an actual diagnostics list to show), and adding an
// empty panel now would just be UI with nothing behind it.
func (p *Plugin) Init(api vfs.HostAPI) error {
	if api == nil {
		return errors.New("IDE: nil host API")
	}
	host, ok := api.(vfs.ContributionHost)
	if !ok {
		return errors.New("IDE: this host does not support plugin contributions")
	}

	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return errors.New("IDE: plugin is already initialized")
	}
	p.mu.Unlock()

	commands := []vfs.PluginCommand{
		{
			ID:          buildCommandID,
			Location:    vfs.PluginCommandPanel,
			Label:       "IDE.Build",
			Description: "Build the current project (Go first; not implemented yet)",
			SearchTerms: []string{"build", "compile", "go build"},
			Run:         notYetImplemented("IDE.Build"),
		},
		{
			ID:          runCommandID,
			Location:    vfs.PluginCommandPanel,
			Label:       "IDE.Run",
			Description: "Run the current project (Go first; not implemented yet)",
			SearchTerms: []string{"run", "go run"},
			Run:         notYetImplemented("IDE.Run"),
		},
		{
			ID:          testCommandID,
			Location:    vfs.PluginCommandPanel,
			Label:       "IDE.Test",
			Description: "Test the current project (Go first; not implemented yet)",
			SearchTerms: []string{"test", "go test"},
			Run:         notYetImplemented("IDE.Test"),
		},
	}

	registrations := make([]vfs.Registration, 0, len(commands))
	rollback := func(err error) error {
		for i := len(registrations) - 1; i >= 0; i-- {
			registrations[i].Unregister()
		}
		return err
	}

	for _, cmd := range commands {
		reg, err := host.RegisterPluginCommand(cmd)
		if err != nil {
			return rollback(errors.New("IDE: register " + cmd.ID + ": " + err.Error()))
		}
		registrations = append(registrations, reg)
	}

	p.mu.Lock()
	p.registrars = registrations
	p.initialized = true
	p.mu.Unlock()
	return nil
}

// notYetImplemented is a placeholder Run handler shared by all three
// commands in this scaffold step. It exists so IDE.Build/Run/Test are real,
// assignable commands from day one (the owner can bind hotkeys to them
// immediately) while the actual toolchain integration lands separately.
//
// The notice goes out in a worker goroutine, never inline: menu and palette
// dispatch Run synchronously on the UI thread, and app.Message blocks until
// the UI loop shows the dialog and reports the button back — calling it here
// deadlocks the whole app (unxed/f4#1893). Background notifications in other
// plugins (multiarc, intchecker) use the same `go app.Message` shape.
func notYetImplemented(command string) func(vfs.App) {
	return func(app vfs.App) {
		go app.Message("IDE", command+" is not implemented yet (f4#382: this is the plugin scaffold; Go build/run/test integration lands as a follow-up part).", []string{"OK"})
	}
}

func (p *Plugin) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.registrars) - 1; i >= 0; i-- {
		p.registrars[i].Unregister()
	}
	p.registrars = nil
	p.initialized = false
	return nil
}
