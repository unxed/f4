package ide

import (
	"errors"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

type fakeRegistration struct{ unregistered bool }

func (r *fakeRegistration) Unregister() { r.unregistered = true }

// fakeContributionHost implements vfs.ContributionHost, recording every
// vfs.PluginCommand it is asked to register. failID, when non-empty, makes
// RegisterPluginCommand fail for that one command ID, so tests can exercise
// Init's rollback path.
type fakeContributionHost struct {
	vfs.HostAPI
	commands []vfs.PluginCommand
	regs     []*fakeRegistration
	failID   string
}

func (h *fakeContributionHost) RegisterPluginCommand(c vfs.PluginCommand) (vfs.Registration, error) {
	if h.failID != "" && c.ID == h.failID {
		return nil, errors.New("boom")
	}
	h.commands = append(h.commands, c)
	reg := &fakeRegistration{}
	h.regs = append(h.regs, reg)
	return reg, nil
}

func (h *fakeContributionHost) RegisterQuickViewProvider(vfs.QuickViewProvider) (vfs.Registration, error) {
	return &fakeRegistration{}, nil
}

func (h *fakeContributionHost) RegisterCommandPrefix(string, string, func(vfs.App, string)) (vfs.CommandPrefixRegistration, error) {
	return nil, nil
}

func (h *fakeContributionHost) RegisterMacroCallProvider(vfs.MacroCallProvider) (vfs.Registration, error) {
	return &fakeRegistration{}, nil
}

// bareHost implements vfs.HostAPI only, without vfs.ContributionHost, so
// Init's type assertion on it fails.
type bareHost struct{ vfs.HostAPI }

// fakeApp implements just enough of vfs.App to observe Message calls from
// the placeholder Run handlers. The handlers notify from a worker goroutine
// (calling blocking Message on the UI thread deadlocks, unxed/f4#1893), so
// calls arrive over a channel instead of plain fields.
type messageCall struct {
	title, msg string
	buttons    []string
}

type fakeApp struct {
	vfs.App
	calls chan messageCall
}

func (a *fakeApp) Message(title, msg string, buttons []string) int {
	a.calls <- messageCall{title, msg, buttons}
	return 0
}

func TestGetName(t *testing.T) {
	if got := NewPlugin().GetName(); got != "IDE" {
		t.Fatalf("GetName() = %q, want %q", got, "IDE")
	}
}

func TestInitRejectsNilHostAPI(t *testing.T) {
	if err := NewPlugin().Init(nil); err == nil {
		t.Fatal("Init(nil) should fail")
	}
}

func TestInitRejectsHostWithoutContributions(t *testing.T) {
	if err := NewPlugin().Init(&bareHost{}); err == nil {
		t.Fatal("Init should fail when the host cannot take plugin commands")
	}
}

func TestInitRegistersThreeCommands(t *testing.T) {
	host := &fakeContributionHost{}
	p := NewPlugin()
	if err := p.Init(host); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wantIDs := map[string]bool{buildCommandID: true, runCommandID: true, testCommandID: true}
	if len(host.commands) != len(wantIDs) {
		t.Fatalf("registered %d commands, want %d", len(host.commands), len(wantIDs))
	}
	for _, c := range host.commands {
		if !wantIDs[c.ID] {
			t.Errorf("unexpected command ID %q", c.ID)
		}
		if c.Location != vfs.PluginCommandPanel {
			t.Errorf("command %q: Location = %v, want PluginCommandPanel", c.ID, c.Location)
		}
		if c.Run == nil {
			t.Errorf("command %q: Run is nil", c.ID)
		}
	}
}

func TestInitTwiceFails(t *testing.T) {
	host := &fakeContributionHost{}
	p := NewPlugin()
	if err := p.Init(host); err != nil {
		t.Fatalf("first Init failed: %v", err)
	}
	if err := p.Init(host); err == nil {
		t.Fatal("second Init should fail")
	}
}

func TestInitRollsBackOnRegistrationFailure(t *testing.T) {
	host := &fakeContributionHost{failID: testCommandID}
	p := NewPlugin()
	if err := p.Init(host); err == nil {
		t.Fatal("Init should fail when a command registration fails")
	}
	for _, r := range host.regs {
		if !r.unregistered {
			t.Error("Init did not roll back an earlier registration after a later one failed")
		}
	}
}

func TestCommandRunReportsNotImplementedYet(t *testing.T) {
	host := &fakeContributionHost{}
	if err := NewPlugin().Init(host); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	for _, c := range host.commands {
		app := &fakeApp{calls: make(chan messageCall, 1)}
		c.Run(app)
		select {
		case call := <-app.calls:
			if call.title != "IDE" {
				t.Errorf("%s: Message title = %q, want %q", c.ID, call.title, "IDE")
			}
			if call.msg == "" {
				t.Errorf("%s: Message body is empty", c.ID)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: no Message call arrived", c.ID)
		}
	}
}

func TestCloseUnregistersCommands(t *testing.T) {
	host := &fakeContributionHost{}
	p := NewPlugin()
	if err := p.Init(host); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	for _, r := range host.regs {
		if !r.unregistered {
			t.Error("Close did not unregister a command")
		}
	}
	// Close should be idempotent-safe to call again without panicking.
	if err := p.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}
