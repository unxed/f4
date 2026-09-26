// Package multiarc is the lite build's archive VFS provider (f4#1178, part 2
// of 2). The full build reads archives with native Go libraries (see
// plugins/archive), which is exactly the code weight a lite binary is built
// to avoid. This package instead shells out to whichever command-line
// archiver the host already has -- tar, unzip, 7z/7za/7zr, gzip/gunzip --
// the same trade-off far2l's multiarc plugin makes for the tools it wraps
// (see docs referenced from f4#609). Only list and extract are implemented,
// matching multiarc's own scope: a lite build browses and unpacks an
// archive, it does not build or edit one in place.
package multiarc

import (
	"bytes"
	"context"
	"os/exec"
)

// runTool runs name with args and returns its stdout, stderr and error. A
// package-level var, the same seam vfs/mount_linux.go's runExternalCommand
// uses (f4#415), so a test can substitute it and assert on the exact
// command multiarc would have run without ever executing a real archiver.
var runTool = func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name is one of our own literals, args are our own literals plus paths under f4's control.
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.Bytes(), errBuf.Bytes(), err
}

// lookupTool resolves name on PATH. A package-level var for the same
// testability reason as runTool: a test can pretend a given archiver is or
// is not installed without touching the real PATH, which is what lets
// "the tool isn't installed" degrade gracefully rather than hard-fail the
// whole plugin (f4#1178).
var lookupTool = exec.LookPath

// toolAvailable reports whether name resolves on PATH.
func toolAvailable(name string) bool {
	_, err := lookupTool(name)
	return err == nil
}
