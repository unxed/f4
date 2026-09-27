package multiarc

import (
	"context"
	"os/exec"
	"strings"
	"sync"
)

// gnuTarByPath caches, per resolved tar binary, whether it is GNU tar.
var gnuTarByPath sync.Map

// platformToolArgs adds --force-local to a GNU tar command. GNU tar reads
// an archive name holding a colon before its first slash as host:path on a
// remote machine, which every absolute Windows path ("C:\...") is; it then
// fails with "Cannot connect to C: resolve failed". GNU tar is what Git for
// Windows and MSYS2 put on PATH; the tar.exe Windows ships is bsdtar, which
// has no such option and needs none.
func platformToolArgs(ctx context.Context, name string, args []string) []string {
	if name != "tar" || (len(args) == 1 && args[0] == "--version") {
		return args
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return args
	}
	gnu, ok := gnuTarByPath.Load(path)
	if !ok {
		out, _ := exec.CommandContext(ctx, path, "--version").Output() // #nosec G204 -- tar's own path, as PATH resolves it.
		gnu = strings.Contains(string(out), "GNU tar")
		gnuTarByPath.Store(path, gnu)
	}
	if gnu.(bool) {
		return append([]string{"--force-local"}, args...)
	}
	return args
}
