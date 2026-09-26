package multiarc

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/unxed/f4/vfs"
)

// Provider is the vfs.VFSProvider the lite build registers in place of the
// full build's plugins/archive.ArchiveProvider (f4#1178, part 2). It only
// ever claims an archive that already sits on the local disk: every backend
// shells out to a command-line tool that needs a real path to open, and a
// lite build carries none of the materialize-a-remote-or-nested-member
// machinery the native archive plugin uses to stage one first. A .tar.gz
// fetched onto a router's flash and browsed from the local panel -- the
// case f4#609 asked for -- is exactly what this covers; an archive inside
// another archive, or one sitting on a FISH+/SFTP panel, is left to the
// full build.
type Provider struct{}

func (p *Provider) Name() string  { return "multiarc" }
func (p *Provider) Priority() int { return 10 }

// PanelEnterAllowed keeps Enter and double-click browsing every archive
// this provider recognizes, mirroring far2l's own default of opening
// anything its multiarc plugin claims. The full build's exclude-mask
// setting (ArchiveEnterExcludeMask) is deliberately not reimplemented
// here: it is one more piece of config surface for a build meant to be
// small, and Ctrl+PgDn is unaffected either way.
func (p *Provider) PanelEnterAllowed(ctx context.Context, parent vfs.VFS, path string) bool {
	return true
}

func (p *Provider) CanOpen(ctx context.Context, parent vfs.VFS, path string) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	osvfs, ok := parent.(*vfs.OSVFS)
	if !ok {
		return false
	}
	name := path
	if base := parent.Base(path); base != "" {
		name = base
	}
	if _, _, ok := detectFormat(name); !ok {
		return false
	}
	localPath, err := osvfs.Abs(path)
	if err != nil {
		return false
	}
	fi, err := os.Stat(localPath)
	return err == nil && fi.Mode().IsRegular()
}

func (p *Provider) Open(ctx context.Context, parent vfs.VFS, pth string) (vfs.VFS, error) {
	osvfs, ok := parent.(*vfs.OSVFS)
	if !ok {
		return nil, errors.New("multiarc: only archives on the local disk are supported in this build")
	}
	name := pth
	if base := parent.Base(pth); base != "" {
		name = base
	}
	b, id, ok := detectFormat(name)
	if !ok {
		return nil, fmt.Errorf("multiarc: no CLI archiver on PATH understands %q", name)
	}
	localPath, err := osvfs.Abs(pth)
	if err != nil {
		return nil, err
	}
	return NewMultiArcVFS(parent, localPath, name, b, id), nil
}

var (
	_ vfs.VFSProvider              = (*Provider)(nil)
	_ vfs.PanelEnterPolicyProvider = (*Provider)(nil)
)
