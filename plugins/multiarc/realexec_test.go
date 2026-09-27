package multiarc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
)

// The *_realexec_test.go files in this package run the actual archivers
// against real archives, with runTool/lookupTool left as production has
// them. Each one skips when the tool it needs is not on PATH, which is what
// lets the same files run on every CI cell: a Linux runner has GNU tar, zip
// and unzip, a macOS runner has bsdtar, and a Windows runner has bsdtar as
// tar.exe and usually nothing else. The helpers below are shared by them.

// requireRealTool skips the test unless every one of names is on PATH.
func requireRealTool(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH", name)
		}
		if toolCannotStart(name) {
			t.Skipf("%s is on PATH but cannot start", name)
		}
	}
}

// toolCannotStart reports whether name is found on PATH but Windows cannot
// load it: exit status 0xC0000135, STATUS_DLL_NOT_FOUND. The Windows
// runners have such an unzip, without the DLL it was built against. Any
// other outcome, a usage error included, means the tool runs.
func toolCannotStart(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, name).Run() // #nosec G204 -- test fixture: a known archiver's name.
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && uint32(exitErr.ExitCode()) == 0xC0000135 // #nosec G115 -- an NTSTATUS, compared bit for bit.
}

// writeTree creates files under root. A key ending in "/" makes an empty
// directory; any other key is a file holding its value.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(name, "/")))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", full, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// runReal runs a tool in dir to build a test fixture, failing the test if
// the tool fails. It execs directly rather than through runTool, so a
// fixture never depends on the code it is there to test.
func runReal(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	// The same Windows fix-up multiarc gives its own commands: GNU tar needs
	// --force-local to take "C:\..." as a file.
	cmd := exec.Command(name, platformToolArgs(context.Background(), name, args)...) // #nosec G204 -- test fixture: a known archiver and paths under t.TempDir.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v (%s)", name, args, err, out)
	}
}

// openReal opens the archive at localPath the way the provider would: by
// its name, through whichever backend detectFormat picks from the real PATH.
func openReal(t *testing.T, localPath string) *MultiArcVFS {
	t.Helper()
	b, id, ok := detectFormat(filepath.Base(localPath))
	if !ok {
		t.Fatalf("detectFormat(%s): no backend", localPath)
	}
	return NewMultiArcVFS(vfs.NewOSVFS(filepath.Dir(localPath)), localPath, filepath.Base(localPath), b, id)
}

// memberPaths lists every member the archive holds, sorted, directories
// marked with a trailing "/", read back from a fresh listing rather than
// from any state a MultiArcVFS might have cached. A member stored twice
// (a tar -r that did not delete first) shows up twice.
func memberPaths(t *testing.T, localPath string) []string {
	t.Helper()
	b, _, ok := detectFormat(filepath.Base(localPath))
	if !ok {
		t.Fatalf("detectFormat(%s): no backend", localPath)
	}
	entries, err := b.list(context.Background(), localPath)
	if err != nil {
		t.Fatalf("list %s: %v", localPath, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		p := e.Path
		if e.IsDir {
			p += "/"
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// writeMember stores content as the member p through Create, the way the
// copy engine and the editor do.
func writeMember(t *testing.T, v *MultiArcVFS, p, content string) {
	t.Helper()
	w, err := v.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create %s: %v", p, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("Write %s: %v", p, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close %s: %v", p, err)
	}
}

// pathOnly replaces PATH, for the rest of the test, with a directory
// holding just the named tools, each a symlink to the real binary found on
// the current PATH under the target name (so "tar": "bsdtar" puts bsdtar on
// PATH as tar). Name lookup only ever sees that directory, so a tool this
// map leaves out stays unreachable. Windows also walks PATH, last, to find
// a loaded module's own DLLs, and GetModuleFileName on a symlinked exe
// reports the symlink's directory, not the real one, which has no copy of
// the companion DLL; each target's real directory is appended after bin so
// that fallback search still finds it, while name resolution keeps picking
// bin's symlink first. It skips the test when a tool is missing, cannot
// start, or symlinks cannot be made (Windows without the privilege).
func pathOnly(t *testing.T, tools map[string]string) {
	t.Helper()
	bin := t.TempDir()
	dirs := []string{bin}
	seen := map[string]bool{}
	for name, target := range tools {
		real, err := exec.LookPath(target)
		if err != nil {
			t.Skipf("%s is not on PATH", target)
		}
		if toolCannotStart(real) {
			t.Skipf("%s is on PATH but cannot start", target)
		}
		link := filepath.Join(bin, name+filepath.Ext(real))
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("cannot symlink %s: %v", real, err)
		}
		if dir := filepath.Dir(real); !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// realTarFlavor is what the tar on the real PATH is.
func realTarFlavor(t *testing.T) tarFlavor {
	t.Helper()
	requireRealTool(t, "tar")
	return probeTar(context.Background()).flavor
}

func assertMembers(t *testing.T, localPath string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := memberPaths(t, localPath)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("members of %s =\n\t%q\nwant\n\t%q", filepath.Base(localPath), got, want)
	}
}

// readMember reads one member back through MultiArcVFS.Open.
func readMember(t *testing.T, v *MultiArcVFS, p string) string {
	t.Helper()
	f, err := v.Open(context.Background(), p)
	if err != nil {
		t.Fatalf("Open %s: %v", p, err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, f.Size())
	n, err := f.ReadAt(context.Background(), buf, 0)
	if err != nil && n != len(buf) {
		t.Fatalf("ReadAt %s: %v", p, err)
	}
	return string(buf[:n])
}
