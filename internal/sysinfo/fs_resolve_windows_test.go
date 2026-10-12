//go:build windows

package sysinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f4#1835: a folder reached through a junction resolves to the junction's
// target, as one reached through a symlink does.
func TestFinalPathFollowsAJunction(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	// #nosec G204 -- fixed command, paths from the test's own temp folder
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	want, err := finalPath(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := finalPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("finalPath(junction) = %q, want the target %q", got, want)
	}
	if strings.HasPrefix(got, `\\?\`) {
		t.Fatalf("verbatim prefix left in %q", got)
	}
}

func TestTrimVerbatim(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\Users\me`:        `C:\Users\me`,
		`\\?\UNC\server\share\x`: `\\server\share\x`,
		`C:\plain`:               `C:\plain`,
	} {
		if got := trimVerbatim(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
