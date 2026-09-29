package update

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestExecutableBackupRestore(t *testing.T) {
	tmpDir := t.TempDir()
	executable := filepath.Join(tmpDir, "f4")
	original := []byte("working f4")
	if err := os.WriteFile(executable, original, 0755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}

	oldExecutable := Executable
	Executable = func() (string, error) { return executable, nil }
	defer func() { Executable = oldExecutable }()

	backup, err := BackupExecutable()
	if err != nil {
		t.Fatalf("BackupExecutable() error: %v", err)
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != string(original) {
		t.Fatalf("backup contents = %q, %v; want %q", got, err, original)
	}
	if err := os.WriteFile(executable, []byte("broken f4"), 0755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}

	if err := RestoreExecutable(backup); err != nil {
		t.Fatalf("RestoreExecutable() error: %v", err)
	}
	if got, err := os.ReadFile(executable); err != nil || string(got) != string(original) {
		t.Fatalf("restored contents = %q, %v; want %q", got, err, original)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup still exists: %v", err)
	}
}

// The backup is made in the temp directory, often another filesystem than
// the installation, where rename fails with EXDEV. The executable has been
// removed by then, so the restore has to copy, or no f4 is left.
func TestRestoreExecutableCopiesAcrossFilesystems(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "f4")
	if err := os.WriteFile(executable, []byte("working f4"), 0o755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}
	oldExecutable, oldRename := Executable, renameFile
	Executable = func() (string, error) { return executable, nil }
	renameFile = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EXDEV}
	}
	t.Cleanup(func() { Executable, renameFile = oldExecutable, oldRename })

	backup, err := BackupExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("broken f4"), 0o755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}
	if err := RestoreExecutable(backup); err != nil {
		t.Fatalf("RestoreExecutable() = %v", err)
	}
	if got, err := os.ReadFile(executable); err != nil || string(got) != "working f4" {
		t.Fatalf("restored = %q, %v; want the working build", got, err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("backup left behind: %v", err)
	}
}

// An install that failed before it reached the executable left it alone,
// and the restore leaves it alone too: on Windows the running f4.exe cannot
// be removed, and trying used to ask for elevation.
func TestRestoreExecutableLeavesAnUntouchedExecutable(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "f4")
	if err := os.WriteFile(executable, []byte("working f4"), 0o755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}
	oldExecutable := Executable
	Executable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { Executable = oldExecutable })

	before, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := BackupExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreExecutable(backup); err != nil {
		t.Fatalf("RestoreExecutable() = %v", err)
	}
	after, err := os.Stat(executable)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the untouched executable was replaced (%v)", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("backup left behind: %v", err)
	}
}

// Through a symlink the update replaced the file it points at, and that file
// is what the restore puts back; the symlink stays a symlink.
func TestRestoreExecutableRestoresTheSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "f4")
	link := filepath.Join(dir, "f4-link")
	if err := os.WriteFile(target, []byte("working f4"), 0o755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	oldExecutable := Executable
	Executable = func() (string, error) { return link, nil }
	t.Cleanup(func() { Executable = oldExecutable })

	backup, err := BackupExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("broken f4"), 0o755); err != nil { // #nosec G306 -- test executable fixture.
		t.Fatal(err)
	}
	if err := RestoreExecutable(backup); err != nil {
		t.Fatalf("RestoreExecutable() = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "working f4" {
		t.Errorf("target = %q, want the working build", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced by a file (%v)", err)
	}
}
