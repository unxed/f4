package multiarc

import (
	"os"
	"sync"

	"github.com/unxed/f4/vfs"
)

// tempDirs tracks every directory Open extracted a member into (vfs.go),
// so the plugin can clean them up on Close instead of leaving them for the
// OS's own temp-directory housekeeping. Removing one earlier, on the
// MultiArcVFS.Close of whichever clone happened to extract it, would be
// wrong: a viewer or editor session, or another clone's directory listing
// racing it, can still be reading the extracted file.
var (
	tempDirsMu sync.Mutex
	tempDirs   []string
)

func registerTempDir(dir string) {
	tempDirsMu.Lock()
	tempDirs = append(tempDirs, dir)
	tempDirsMu.Unlock()
}

func closeSharedMultiArcTempDirs() {
	tempDirsMu.Lock()
	dirs := tempDirs
	tempDirs = nil
	tempDirsMu.Unlock()
	for _, dir := range dirs {
		_ = os.RemoveAll(dir)
	}
}

// Plugin registers multiarc's VFS provider. It is the lite build's
// replacement for plugins/archive.ArchivePlugin (f4#1178, part 2): see
// plugins/multiarc's own doc comment and Provider's for what it covers and
// why it is scoped to local-disk archives only.
type Plugin struct{}

func (p *Plugin) Init(api vfs.HostAPI) error {
	api.RegisterVFSProvider(&Provider{})
	return nil
}

func (p *Plugin) Close() error {
	closeSharedMultiArcTempDirs()
	return nil
}

func (p *Plugin) GetName() string { return "MultiArc (CLI archivers)" }
