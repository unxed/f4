package proclist

import (
	"testing"

	"github.com/unxed/f4/vfs"
)

type fakeRegistration struct{ unregistered bool }

func (r *fakeRegistration) Unregister() { r.unregistered = true }

// fakePanelHost implements vfs.PanelContributionHost only (and embeds a nil
// vfs.HostAPI, matching the fake host pattern plugins/sqlite/plugin_test.go
// already uses for its own vfs.ContributionHost double) -- deliberately not
// vfs.ContributionHost, so it exercises Init's "config command is optional"
// path (plugin.go): a host that cannot take plugin commands still gets a
// working panel provider.
type fakePanelHost struct {
	vfs.HostAPI
	provider vfs.PanelProvider
	reg      *fakeRegistration
}

func (h *fakePanelHost) RegisterPanelProvider(p vfs.PanelProvider) (vfs.Registration, error) {
	h.provider = p
	h.reg = &fakeRegistration{}
	return h.reg, nil
}

// fakeConfigHost adds vfs.ContributionHost on top of fakePanelHost, so
// Init also registers ProcList.Config's command
// (configCommandID/vfs.PluginCommandConfig) into it. Only
// RegisterPluginCommand is exercised by anything in this package; the other
// three methods exist only so this type satisfies the interface, the same
// minimal-implementation pattern plugins/mediainfo/plugin_test.go's
// pluginTestHost uses for the same interface.
type fakeConfigHost struct {
	fakePanelHost
	command vfs.PluginCommand
	cmdReg  *fakeRegistration
}

func (h *fakeConfigHost) RegisterPluginCommand(c vfs.PluginCommand) (vfs.Registration, error) {
	h.command = c
	h.cmdReg = &fakeRegistration{}
	return h.cmdReg, nil
}

func (h *fakeConfigHost) RegisterQuickViewProvider(vfs.QuickViewProvider) (vfs.Registration, error) {
	return &fakeRegistration{}, nil
}

func (h *fakeConfigHost) RegisterCommandPrefix(string, string, func(vfs.App, string)) (vfs.CommandPrefixRegistration, error) {
	return nil, nil
}

func (h *fakeConfigHost) RegisterMacroCallProvider(vfs.MacroCallProvider) (vfs.Registration, error) {
	return &fakeRegistration{}, nil
}

// bareHost implements vfs.HostAPI without vfs.PanelContributionHost, so
// Init's type assertion on it fails on a platform where Supported() is true.
type bareHost struct{ vfs.HostAPI }

func TestInitRejectsNilHostAPI(t *testing.T) {
	if err := NewPlugin(t.TempDir()).Init(nil); err == nil {
		t.Fatal("Init(nil) should fail")
	}
}

func TestInitOnUnsupportedPlatformDoesNothing(t *testing.T) {
	if Supported() {
		t.Skip("this build supports ProcList; see TestInitRegistersPanelProviderWhenSupported")
	}
	host := &fakePanelHost{}
	if err := NewPlugin(t.TempDir()).Init(host); err != nil {
		t.Fatalf("Init returned an error on an unsupported platform: %v", err)
	}
	if host.provider.ID != "" {
		t.Fatalf("Init registered a panel provider on an unsupported platform: %#v", host.provider)
	}
}

func TestInitRegistersPanelProviderWhenSupported(t *testing.T) {
	if !Supported() {
		t.Skip("this build does not support ProcList; see TestInitOnUnsupportedPlatformDoesNothing")
	}

	if err := NewPlugin(t.TempDir()).Init(&bareHost{}); err == nil {
		t.Fatal("Init should reject a host without panel contribution support")
	}

	host := &fakePanelHost{}
	plugin := NewPlugin(t.TempDir())
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.provider.ID != panelProviderID || host.provider.Title == "" || host.provider.Open == nil {
		t.Fatalf("panel provider metadata = %#v", host.provider)
	}

	if err := plugin.Init(host); err == nil {
		t.Fatal("a second Init on an already-initialized plugin should fail")
	}

	controller, err := host.provider.Open(vfs.PanelContext{})
	if err != nil {
		t.Fatalf("Open returned an error: %v", err)
	}
	defer func() { _ = controller.Close() }()

	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	if host.reg == nil || !host.reg.unregistered {
		t.Fatal("Close did not unregister the panel provider")
	}
}

// TestInitRegistersConfigCommandWhenHostSupportsContributions covers f4#312
// part 4 of 4: a host that also implements vfs.ContributionHost gets
// ProcList.Config's F9 "Plugin configuration" entry, and Close unregisters
// it alongside the panel provider.
func TestInitRegistersConfigCommandWhenHostSupportsContributions(t *testing.T) {
	if !Supported() {
		t.Skip("this build does not support ProcList")
	}

	host := &fakeConfigHost{}
	plugin := NewPlugin(t.TempDir())
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.command.ID != configCommandID || host.command.Location != vfs.PluginCommandConfig {
		t.Fatalf("config command = %#v", host.command)
	}
	if host.command.Run == nil {
		t.Fatal("config command has no Run handler")
	}
	// config_dialog_test.go exercises Run itself (it pushes a real dialog
	// onto vtui.FrameManager); this test only covers registration/
	// unregistration, the same split plugins/mediainfo/plugin_test.go and
	// plugins/mediainfo/config_dialog_test.go keep for the same two
	// concerns.

	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	if host.cmdReg == nil || !host.cmdReg.unregistered {
		t.Fatal("Close did not unregister the configuration command")
	}
	if host.reg == nil || !host.reg.unregistered {
		t.Fatal("Close did not unregister the panel provider")
	}
}
