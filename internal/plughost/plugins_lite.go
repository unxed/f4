//go:build lite

package plughost

import "github.com/unxed/f4/plugins/multiarc"

// optionalVFSPlugins carries only multiarc in a lite build (f4#1178, part 2
// of 2): a CLI-tool-wrapper archive provider (tar/unzip/7z/gzip, whichever
// the host has) in place of the full build's native-library one, and no
// cloud or ftp/sftp/scp VFS provider at all -- so none of the cloud SDKs
// jlaffaye/ftp, pkg/sftp, kbolino/pageant or the archive libraries those
// omit are even imported. See plugins_full.go for the full list and
// manager.go for where this is called.
//
// FISH+ (plugins/netfox's fish+ VFS) was investigated for inclusion here
// too, since its own wire-protocol client (plugins/netfox/fishplus) has no
// dependency beyond the standard library and already talks to any duplex
// byte stream, not specifically an SSH library -- architecturally the
// "wrap the console ssh binary, no libraries needed" story f4#609 asked
// for. It did not make it into this build: fish_vfs.go lives in the same
// "netfox" package as the FTP and SFTP backends, sharing their package-level
// init() protocol registration and NetFoxVFS's single dispatcher, so
// importing it at all statically links github.com/jlaffaye/ftp,
// github.com/pkg/sftp, github.com/kbolino/pageant and
// golang.org/x/crypto/ssh (+agent, +knownhosts) right back in -- the exact
// weight this build tag exists to shed. Splitting fish_vfs.go (and its
// dialer, and the tests exercising both) into a package of its own, with a
// subprocess-ssh dialer instead of ssh_dial.go's x/crypto/ssh one, is real,
// separately-scoped work for a later slice, not something to force into
// this one blind and CI-only.
func optionalVFSPlugins() []Plugin {
	return []Plugin{&multiarc.Plugin{}}
}
