//go:build !lite

package netfox

import (
	"fmt"

	"github.com/unxed/f4/vfs"
)

// registerOptionalURIProviders registers the URI-string forms that only the
// full build has a backend for. See netfox_uri_lite.go for the lite build's
// (currently empty) counterpart.
func registerOptionalURIProviders(api vfs.HostAPI) error {
	if err := api.RegisterURIProvider(&sftpURIProvider{}); err != nil {
		return fmt.Errorf("NetFox: register sftp URI provider: %w", err)
	}
	return nil
}
