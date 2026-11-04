package update

import (
	"os"
	"path/filepath"
	"testing"
)

// procSelfExe stands in for /proc/self/exe: it names the file under whatever
// name it has now, as the kernel does for a running binary that was renamed.
type procSelfExe struct{ name string }

func (p *procSelfExe) lookup() (string, error) { return p.name, nil }

// #2248: the updater moves f4 to f4.old, writes the new build and deletes
// f4.old. The path asked for after that has to still be the installed f4, not
// the f4.old that is gone.
func TestExecutablePathSurvivesTheUpdateRenamingTheBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "share", "f4", "f4")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old build"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "f4")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	proc := &procSelfExe{name: link}
	asked := snapshotExecutable(proc.lookup)

	oldExecutable := Executable
	t.Cleanup(func() { Executable = oldExecutable })

	// What writeFileSafe does to the installed file, and what the running
	// process then sees of itself.
	if err := os.Rename(exe, exe+".old"); err != nil {
		t.Fatal(err)
	}
	proc.name = exe + ".old"
	if err := os.WriteFile(exe, []byte("new build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(exe + ".old"); err != nil {
		t.Fatal(err)
	}

	Executable = proc.lookup
	if _, err := targetExecutable(); err == nil {
		t.Fatal("a path asked for after the rename should be the missing .old; the test no longer models #2248")
	}

	Executable = asked
	got, err := targetExecutable()
	if err != nil {
		t.Fatalf("targetExecutable() after the update replaced the binary: %v", err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("targetExecutable() = %q, want %q", got, want)
	}
}

func TestStartupExecutableIsFixedAtStart(t *testing.T) {
	first, err := startupExecutable()
	if err != nil {
		t.Skipf("no executable path here: %v", err)
	}
	if second, _ := startupExecutable(); second != first {
		t.Fatalf("startupExecutable() changed: %q then %q", first, second)
	}
}
