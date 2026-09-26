//go:build lite

package plughost

import (
	"github.com/unxed/f4/plugins/multiarc"
	"github.com/unxed/f4/plugins/netfox"
)

// optionalVFSPlugins carries multiarc and netfox in a lite build (f4#1178):
// a CLI-tool-wrapper archive provider (tar/unzip/7z/gzip, whichever the host
// has) in place of the full build's native-library one (part 2), and NetFox
// cut down to FISH+ over a subprocess ssh dialer in place of the full
// build's FTP/SFTP/FISH+ trio (part 3) -- no cloud VFS provider at all. None
// of the cloud SDKs jlaffaye/ftp, pkg/sftp, kbolino/pageant or
// golang.org/x/crypto/ssh, nor the archive libraries multiarc's tool-wrapper
// replaces, are even imported; cmd/f4's TestLiteBuildExcludesHeavyNetworkDependencies
// checks this mechanically, from `go list -deps`, rather than trusting this
// comment to stay accurate. See plugins_full.go for the full list and
// manager.go for where this is called.
//
// netfox.NetFoxPlugin (netfox.go) is the same plugin type the full build
// registers: it stores connections and offers the same "Add/Edit
// connection" dialog, but plugins/netfox's own files are individually
// //go:build lite/!lite (ftp_vfs.go, sftp_vfs.go, sftp_uri.go, ssh_dial.go,
// ssh_known_hosts.go, ssh_pty.go, ssh_agent_*.go and ssh_fish_dialer.go stay
// out; fish_dialer_lite.go's console-ssh-subprocess dialer takes over for
// FISH+), so a lite build's connection dialog only ever offers fish+ as a
// protocol -- registry.go's handler map has nothing else registered to
// offer. See fish_dialer_lite.go's own comment for what that dialer leaves
// out (password auth, an explicit HTTP/SOCKS5 proxy, and any host-key
// handling of its own beyond the system ssh's) and why.
func optionalVFSPlugins() []Plugin {
	return []Plugin{&multiarc.Plugin{}, &netfox.NetFoxPlugin{}}
}
