package app

import "github.com/unxed/f4/vfs"

// RegisterViewerProvider exposes the optional viewer contribution to plugins.
func (c *coreAPI) RegisterViewerProvider(provider vfs.ViewerProvider) (vfs.Registration, error) {
	return vfs.RegisterViewerProvider(provider)
}
