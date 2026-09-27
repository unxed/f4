// Package multiarc is the lite build's archive VFS provider (f4#1178, part 2
// of 2). The full build reads archives with native Go libraries (see
// plugins/archive), which is exactly the code weight a lite binary is built
// to avoid. This package instead shells out to whichever command-line
// archiver the host already has -- tar, unzip, 7z/7za/7zr, gzip/gunzip --
// the same trade-off far2l's multiarc plugin makes for the tools it wraps
// (see docs referenced from f4#609).
//
// Like far2l's multiarc it also changes archives through those same tools,
// as far as each one can do it safely: members are added, replaced and
// deleted in place (write.go describes how, and each backend_*_write.go
// says what its tool can do and refuses the rest with the reason), and Add
// to archive (Shift+F1) creates a new archive from the selected files
// (create.go, create_command.go).
package multiarc

import (
	"bytes"
	"context"
	"os/exec"
)

// runToolIn runs name with args in the working directory dir ("" for f4's
// own) and returns its stdout, stderr and error. It is the one seam every
// archiver invocation goes through, a package-level var the same way
// vfs/mount_linux.go's runExternalCommand is (f4#415), so a test can
// substitute it and assert on the exact command multiarc would have run
// without ever executing a real archiver.
//
// The directory matters for writing: zip and 7z store a file under the
// path it was named by, relative to where they run, and neither has a
// "change directory first" option of its own the way tar has -C.
var runToolIn = func(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, platformToolArgs(ctx, name, args)...) // #nosec G204 -- name is one of our own literals, args are our own literals plus paths under f4's control.
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.Bytes(), errBuf.Bytes(), err
}

// runTool is runToolIn in f4's own working directory, which is all listing
// and extracting need: every path they pass is absolute.
func runTool(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	return runToolIn(ctx, "", name, args...)
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
