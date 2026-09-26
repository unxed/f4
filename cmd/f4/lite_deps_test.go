package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
)

// TestLiteBuildExcludesHeavyNetworkDependencies is the mechanical half of
// f4#1178 part 3: FISH+ over a subprocess ssh dialer came back into the lite
// build (plugins/netfox, gated file-by-file with //go:build lite/!lite --
// see internal/plughost/plugins_lite.go), and this is what keeps it from
// quietly dragging FTP, SFTP or Pageant support back in with it. Each of
// those links a library -tags lite exists to shed: github.com/jlaffaye/ftp,
// github.com/pkg/sftp, github.com/kbolino/pageant and
// golang.org/x/crypto/ssh (and its ssh/agent, ssh/knownhosts).
//
// This asks the toolchain rather than grepping source: a forbidden import
// reintroduced through a different file, or a new dependency of fishplus
// itself, is caught the same way an already-tagged one would be.
func TestLiteBuildExcludesHeavyNetworkDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	forbidden := []string{
		"github.com/jlaffaye/ftp",
		"github.com/pkg/sftp",
		"github.com/kbolino/pageant",
		"golang.org/x/crypto/ssh",
	}

	deps := liteBuildDeps(t)

	var offenders []string
	for _, imported := range deps {
		for _, bad := range forbidden {
			if imported == bad || strings.HasPrefix(imported, bad+"/") {
				offenders = append(offenders, imported)
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("a -tags lite build of ./cmd/f4 still depends on:\n\t%s", strings.Join(offenders, "\n\t"))
	}
}

// TestLiteBuildStillIncludesFishPlus is the other side of the same check: an
// overzealous exclusion that dropped FISH+ itself back out of the lite build
// would pass the test above for the wrong reason. fishplus has no dependency
// beyond the standard library, so its presence here does not reintroduce any
// of the weight -tags lite sheds.
func TestLiteBuildStillIncludesFishPlus(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	deps := liteBuildDeps(t)
	const fishplus = "github.com/unxed/f4/plugins/netfox/fishplus"
	for _, imported := range deps {
		if imported == fishplus {
			return
		}
	}
	t.Fatalf("a -tags lite build of ./cmd/f4 no longer depends on %s", fishplus)
}

func liteBuildDeps(t *testing.T) []string {
	t.Helper()
	command := exec.Command("go", "list", "-tags", "lite", "-deps", "./cmd/f4")
	command.Dir = testutil.ModuleRootDir(t)
	out, err := command.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		t.Fatalf("go list -tags lite -deps ./cmd/f4: %v\n%s", err, stderr)
	}
	return strings.Fields(string(out))
}
