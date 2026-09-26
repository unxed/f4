package multiarc

import (
	"context"
	"errors"
	"testing"
)

// errNotFoundStub is what a faked lookupTool returns for a binary the test
// wants to pretend is missing from PATH.
var errNotFoundStub = errors.New("multiarc test: not found")

// withFakeTools substitutes runTool/lookupTool for the duration of a test
// and restores the production ones afterward, the same seam
// vfs/mount_linux.go's tests use for runExternalCommand/lookupExecutable
// (f4#415).
func withFakeTools(t *testing.T, lookup func(string) (string, error), run func(ctx context.Context, name string, args ...string) ([]byte, []byte, error)) {
	t.Helper()
	origLookup, origRun := lookupTool, runTool
	if lookup != nil {
		lookupTool = lookup
	}
	if run != nil {
		runTool = run
	}
	t.Cleanup(func() {
		lookupTool = origLookup
		runTool = origRun
	})
}

func TestToolAvailable(t *testing.T) {
	withFakeTools(t, func(name string) (string, error) {
		if name == "tar" {
			return "/usr/bin/tar", nil
		}
		return "", errors.New("not found")
	}, nil)

	if !toolAvailable("tar") {
		t.Error("expected tar to be reported available")
	}
	if toolAvailable("unzip") {
		t.Error("expected unzip to be reported unavailable")
	}
}
