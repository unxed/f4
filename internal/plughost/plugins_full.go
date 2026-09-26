//go:build !lite

package plughost

import (
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/plugins/archive"
	"github.com/unxed/f4/plugins/cloudfox"
	"github.com/unxed/f4/plugins/netfox"
)

// optionalVFSPlugins are the VFS providers a lite build cuts down or drops
// (f4#1178): the native-library archive plugin and cloud storage go
// entirely, and netfox keeps only FISH+ (over a subprocess ssh dialer
// instead of this build's golang.org/x/crypto/ssh one) in place of the
// FTP/SFTP/FISH+ trio here. See plugins_lite.go for the other half of this
// build tag's single point of truth.
func optionalVFSPlugins() []Plugin {
	return []Plugin{
		&archive.ArchivePlugin{},
		cloudfox.NewPlugin(cloudfox.Options{
			ConfigDir: config.GetF4ConfigDir(),
			Portable:  config.IsPortableProfile(),
		}),
		&netfox.NetFoxPlugin{},
	}
}
