//go:build lite

package netfox

import "github.com/unxed/f4/vfs"

// registerOptionalURIProviders is a no-op in a lite build: sftp:// has no
// backend here (SFTP itself does not build under -tags lite, see
// sftp_vfs.go's own comment), and FISH+ has no bare-string URI form of its
// own yet. See netfox_uri_full.go for the full build's counterpart.
func registerOptionalURIProviders(api vfs.HostAPI) error {
	return nil
}
