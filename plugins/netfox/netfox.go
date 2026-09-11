package netfox

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
)

type ioCtxReader struct {
	r   io.Reader
	ctx context.Context
}

func (cr *ioCtxReader) Read(p []byte) (int, error) {
	if cr.ctx.Err() != nil {
		return 0, cr.ctx.Err()
	}
	return cr.r.Read(p)
}

const (
	netFoxEditConnectionCommandID = "netfox.edit-connection"
	netFoxAddConnectionCommandID  = "netfox.add-connection"
)

type NetFoxPlugin struct {
	registrations []vfs.Registration
}

type netFoxVFSWrapper struct {
	*NetFoxVFS
}

func (w *netFoxVFSWrapper) Clone() vfs.VFS {
	return &netFoxVFSWrapper{w.NetFoxVFS.Clone().(*NetFoxVFS)}
}

// ctxReader wraps vfs.ReadAtCloser to implement standard io.Reader
type ctxReader struct {
	r   vfs.ReadAtCloser
	ctx context.Context
}

func (cr ctxReader) Read(p []byte) (int, error) {
	return cr.r.Read(cr.ctx, p)
}

func (w *netFoxVFSWrapper) ProcessPanelKey(app vfs.App, e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}

	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	noMods := !shift && !ctrl && !alt

	// F4 -> Edit existing connection
	if e.VirtualKeyCode == vtinput.VK_F4 && noMods {
		editNetFoxConnection(app)
		return true
	}

	// Shift+F4 -> Add new connection
	if e.VirtualKeyCode == vtinput.VK_F4 && shift && !ctrl && !alt {
		addNetFoxConnection(app)
		return true
	}

	// Enter on <Add connection>
	if e.VirtualKeyCode == vtinput.VK_RETURN && noMods {
		if app.GetSelectedName() == "<Add connection>" {
			addNetFoxConnection(app)
			return true
		}
	}

	// Protect <Add connection> from deletion via F8
	if e.VirtualKeyCode == vtinput.VK_F8 && noMods {
		names := app.GetSelectedNames()
		if len(names) == 1 && names[0] == "<Add connection>" {
			return true // Swallow to prevent core from deleting it
		}
	}

	return false
}

func activeNetFoxVFS(app vfs.App) (*NetFoxVFS, bool) {
	if app == nil {
		return nil, false
	}
	wrapper, ok := app.GetActivePanelVFS().(*netFoxVFSWrapper)
	if !ok || wrapper == nil || wrapper.NetFoxVFS == nil {
		return nil, false
	}
	return wrapper.NetFoxVFS, true
}

func netFoxCommandsVisible(app vfs.App) bool {
	_, ok := activeNetFoxVFS(app)
	return ok
}

func editNetFoxConnection(app vfs.App) {
	netFoxVFS, ok := activeNetFoxVFS(app)
	if !ok {
		return
	}
	name := app.GetSelectedName()
	if name == "" || name == ".." || name == "<Add connection>" {
		return
	}
	showConnectionDialog(app, netFoxVFS, name)
}

func addNetFoxConnection(app vfs.App) {
	netFoxVFS, ok := activeNetFoxVFS(app)
	if !ok {
		return
	}
	showConnectionDialog(app, netFoxVFS, "")
}

func (p *NetFoxPlugin) Init(api vfs.HostAPI) error {
	registrations := make([]vfs.Registration, 0, 2)
	rollback := func(err error) error {
		for index := len(registrations) - 1; index >= 0; index-- {
			registrations[index].Unregister()
		}
		return err
	}
	if contributions, ok := api.(vfs.ContributionHost); ok {
		editRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
			ID:             netFoxEditConnectionCommandID,
			Location:       vfs.PluginCommandPanel,
			Label:          "Edit connection",
			LabelKey:       "NetFox.Command.EditConnection",
			Description:    "Edit the selected NetFox connection",
			DescriptionKey: "NetFox.Command.EditConnection.Desc",
			SearchKeys:     []string{"NetFox.ConnectionTitle"},
			Shortcut:       "F4",
			Visible:        netFoxCommandsVisible,
			Run:            editNetFoxConnection,
		})
		if err != nil {
			return rollback(fmt.Errorf("NetFox: register edit-connection command: %w", err))
		}
		registrations = append(registrations, editRegistration)

		addRegistration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
			ID:             netFoxAddConnectionCommandID,
			Location:       vfs.PluginCommandPanel,
			Label:          "Add connection",
			LabelKey:       "NetFox.Command.AddConnection",
			Description:    "Create a new NetFox connection",
			DescriptionKey: "NetFox.Command.AddConnection.Desc",
			SearchKeys:     []string{"NetFox.ConnectionTitle"},
			Shortcut:       "Shift+F4",
			Visible:        netFoxCommandsVisible,
			Run:            addNetFoxConnection,
		})
		if err != nil {
			return rollback(fmt.Errorf("NetFox: register add-connection command: %w", err))
		}
		registrations = append(registrations, addRegistration)
	}

	// sftp:// as a string, for every caller that has no stored connection
	// to point at: the mount command line, an fstab line, a script.
	if err := api.RegisterURIProvider(&sftpURIProvider{}); err != nil {
		return rollback(fmt.Errorf("NetFox: register sftp URI provider: %w", err))
	}
	api.RegisterDrive("NetFox", func() vfs.VFS {
		cfgDir := vfs.CustomConfigDir
		if cfgDir == "" {
			sysDir, _ := os.UserConfigDir()
			cfgDir = filepath.Join(sysDir, "f4")
		}
		return &netFoxVFSWrapper{NewNetFoxVFS(filepath.Join(cfgDir, "NetFox.json"))}
	})
	if host, ok := api.(vfs.SettingsContributionHost); ok {
		reg, err := host.RegisterSettingsProvider(newSettingsProvider())
		if err != nil {
			return rollback(err)
		}
		registrations = append(registrations, reg)
	}
	p.registrations = append(p.registrations, registrations...)
	return nil
}

func (p *NetFoxPlugin) Close() error {
	registrations := p.registrations
	p.registrations = nil
	for index := len(registrations) - 1; index >= 0; index-- {
		registrations[index].Unregister()
	}
	return nil
}
func (p *NetFoxPlugin) GetName() string { return "NetFox" }
