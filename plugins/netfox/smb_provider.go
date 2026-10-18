//go:build !lite

package netfox

import (
	"context"
	"os"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// smbProvider opens a saved NetFox connection of type "smb" (f4#188): the same
// SMB browser smb:// gives, with the host, port, user and password taken from
// the connection. The user may be written "DOMAIN;user"; a Share option, when
// present, is the share to start in.
type smbProvider struct{}

func (p *smbProvider) Name() string  { return "NetFox-SMB" }
func (p *smbProvider) Priority() int { return 100 }

func (p *smbProvider) CanOpen(ctx context.Context, parent vfs.VFS, pth string) bool {
	cfg, ok := netFoxConfigAt(ctx, parent, pth)
	return ok && cfg.Type == "smb"
}

func (p *smbProvider) Open(ctx context.Context, parent vfs.VFS, pth string) (vfs.VFS, error) {
	cfg, ok := netFoxConfigAt(ctx, parent, pth)
	if !ok {
		return nil, os.ErrInvalid
	}
	return openSMBConfig(ctx, parent, cfg)
}

// openSMBConfig connects for a saved connection.
func openSMBConfig(ctx context.Context, parent vfs.VFS, cfg NetFoxConfig) (vfs.VFS, error) {
	port := cfg.Port
	if port == "" {
		port = "445"
	}
	domain, user := "", cfg.User
	if d, name, ok := strings.Cut(user, ";"); ok {
		domain, user = d, name
	}
	client, err := dialSMB(ctx, cfg.Host, port, domain, user, cfg.Pass)
	if err != nil {
		return nil, err
	}
	v := newSMBVFS(parent, client, cfg.Host)
	if share := strings.Trim(cfg.Options["Share"], "/"); share != "" {
		if err := v.SetPath("/" + share); err != nil {
			_ = v.Close() // Preserve the invalid-path error.
			return nil, err
		}
	}
	return enterStartPath(v, cfg.Path), nil
}

type smbProtocolHandler struct{}

func (ph *smbProtocolHandler) Prefix() string      { return "smb" }
func (ph *smbProtocolHandler) DefaultPort() string { return "445" }
func (ph *smbProtocolHandler) BuildExtraUI(cfg *NetFoxConfig, x, y, w, h int) (vtui.UIElement, func()) {
	return nil, func() {}
}

func init() {
	vfs.RegisterProvider(&smbProvider{})
	RegisterProtocol(&smbProtocolHandler{})
}
