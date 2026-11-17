package id3editor

import (
	"errors"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type id3HostMock struct {
	legacyLabel   string
	legacyHandler func(vfs.App)
}

func (*id3HostMock) GetVersion() string                           { return "test" }
func (*id3HostMock) Log(string)                                   {}
func (*id3HostMock) Message(string)                               {}
func (*id3HostMock) RegisterHighlighter(vtui.HighlighterProvider) {}
func (*id3HostMock) RegisterVFSProvider(vfs.VFSProvider)          {}
func (*id3HostMock) RegisterURIProvider(vfs.URIProvider) error    { return nil }
func (*id3HostMock) RegisterDrive(string, func() vfs.VFS)         {}
func (*id3HostMock) RegisterGlobalHotkey(uint16, vtinput.ControlKeyState, func(vfs.App)) {
}
func (host *id3HostMock) RegisterPluginMenuItem(label string, handler func(vfs.App)) {
	host.legacyLabel, host.legacyHandler = label, handler
}
func (*id3HostMock) RunAction(string) bool { return false }

type id3RegistrationMock struct{ unregistered int }

func (registration *id3RegistrationMock) Unregister() {
	registration.unregistered++
}

type id3ContributionHostMock struct {
	*id3HostMock
	commands      []vfs.PluginCommand
	registrations []*id3RegistrationMock
	settingsRoute []string
	err           error
}

func (host *id3ContributionHostMock) OpenSettings(category, collection, record string, create bool) bool {
	host.settingsRoute = []string{category, collection, record}
	return !create
}

func (*id3ContributionHostMock) RegisterQuickViewProvider(vfs.QuickViewProvider) (vfs.Registration, error) {
	return nil, errors.New("unexpected quick-view registration")
}

func (host *id3ContributionHostMock) RegisterPluginCommand(command vfs.PluginCommand) (vfs.Registration, error) {
	host.commands = append(host.commands, command)
	if host.err != nil {
		return nil, host.err
	}
	registration := &id3RegistrationMock{}
	host.registrations = append(host.registrations, registration)
	return registration, nil
}

func (*id3ContributionHostMock) RegisterCommandPrefix(string, string, func(vfs.App, string)) (vfs.CommandPrefixRegistration, error) {
	return nil, errors.New("unexpected command-prefix registration")
}

func (*id3ContributionHostMock) RegisterMacroCallProvider(vfs.MacroCallProvider) (vfs.Registration, error) {
	return nil, errors.New("unexpected macro registration")
}

func TestPluginUsesLegacyMenuForHostsWithoutRichContributions(t *testing.T) {
	host := &id3HostMock{}
	plugin := &ID3EditorPlugin{}
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.legacyLabel == "" || host.legacyHandler == nil {
		t.Fatal("legacy host did not receive an ID3 editor menu item")
	}
}

func TestPluginPrefersRichCommandAndUnregistersIt(t *testing.T) {
	host := &id3ContributionHostMock{id3HostMock: &id3HostMock{}}
	plugin := &ID3EditorPlugin{}
	if err := plugin.Init(host); err != nil {
		t.Fatal(err)
	}
	if host.legacyLabel != "" || host.legacyHandler != nil {
		t.Fatal("rich host also received a duplicate legacy menu item")
	}
	if len(host.commands) != 2 {
		t.Fatalf("registered commands = %#v", host.commands)
	}
	command := host.commands[0]
	if command.ID != "id3editor.edit" || command.Location != vfs.PluginCommandPanel ||
		command.Label != "ID3 Tag &Editor" || command.LabelKey != "ID3Editor.Menu" ||
		command.Description == "" || command.DescriptionKey != "ID3Editor.Command.Edit.Desc" ||
		len(command.SearchKeys) != 3 || command.Run == nil {
		t.Fatalf("rich command metadata = %#v", command)
	}
	configCommand := host.commands[1]
	if configCommand.ID != "id3editor.configure" || configCommand.Location != vfs.PluginCommandConfig ||
		configCommand.LabelKey != "ID3Editor.ConfigMenu" || configCommand.DescriptionKey != "ID3Editor.ConfigDescription" ||
		configCommand.Run == nil {
		t.Fatalf("configuration command metadata = %#v", configCommand)
	}
	configCommand.Run(nil)
	if got := host.settingsRoute; len(got) != 3 || got[0] != "metadata" || got[2] != "id3editor.PreserveModTime" {
		t.Fatalf("configuration command opened settings route %v", got)
	}
	// Enabled dims the menu item/command-palette entry instead of letting
	// it pop an error dialog on a file the editor cannot handle (f4#1356).
	if command.Enabled == nil {
		t.Fatal("rich command has no Enabled predicate")
	}
	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Close(); err != nil {
		t.Fatal(err)
	}
	if len(host.registrations) != 2 || host.registrations[0].unregistered != 1 || host.registrations[1].unregistered != 1 {
		t.Fatalf("unregister calls = %#v", host.registrations)
	}
	if plugin.api != nil || plugin.registration != nil {
		t.Fatal("Close retained host or registration state")
	}
}

func TestPluginDoesNotFallBackToLegacyMenuAfterRichRegistrationFailure(t *testing.T) {
	host := &id3ContributionHostMock{
		id3HostMock: &id3HostMock{},
		err:         errors.New("injected registration failure"),
	}
	plugin := &ID3EditorPlugin{}
	if err := plugin.Init(host); err == nil {
		t.Fatal("Init succeeded despite rich registration failure")
	}
	if host.legacyLabel != "" || host.legacyHandler != nil {
		t.Fatal("failed rich registration silently installed a legacy menu item")
	}
	if plugin.api != nil || plugin.registration != nil {
		t.Fatal("failed Init retained host or registration state")
	}
}

var _ vfs.HostAPI = (*id3HostMock)(nil)
var _ vfs.ContributionHost = (*id3ContributionHostMock)(nil)
