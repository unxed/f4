//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv stubs UserHomeDir/Getenv/Confirm for one test and restores the
// originals afterward, so RunCLI never touches the real home directory,
// environment or stdin.
func fakeEnv(t *testing.T, home string, env map[string]string, confirm bool) {
	t.Helper()
	oldHome, oldGetenv, oldConfirm := UserHomeDir, Getenv, Confirm
	t.Cleanup(func() { UserHomeDir, Getenv, Confirm = oldHome, oldGetenv, oldConfirm })

	UserHomeDir = func() (string, error) { return home, nil }
	Getenv = func(key string) string { return env[key] }
	Confirm = func(prompt string) bool { return confirm }
}

func writeFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake f4 binary"), 0o755); err != nil { // #nosec G306 -- test fixture executable, needs the exec bit
		t.Fatal(err)
	}
	return path
}

func TestRunCLIInstallsAndReportsAlreadyOnPath(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	fakeEnv(t, home, map[string]string{
		"PATH":  PreferredDir(home) + string(os.PathListSeparator) + "/usr/bin",
		"SHELL": "/bin/bash",
	}, false)

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
	dst := filepath.Join(PreferredDir(home), "f4")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	if string(data) != "fake f4 binary" {
		t.Errorf("installed content = %q", data)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Errorf(".bashrc should be untouched when the dir is already on PATH, stat err = %v", err)
	}
}

func TestRunCLIPromptsAndAppendsOnConfirm(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	fakeEnv(t, home, map[string]string{
		"PATH":  "/usr/bin:/bin",
		"SHELL": "/bin/zsh",
	}, true) // Confirm() answers yes

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
	data, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf(".zshrc not written: %v", err)
	}
	profile, _ := DetectShellProfile(home, "/bin/zsh")
	want := PathLine(profile, home, PreferredDir(home))
	if !strings.Contains(string(data), want) {
		t.Errorf(".zshrc = %q, want it to mention %q", data, want)
	}
}

func TestRunCLIDeclinesWithoutConfirmation(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	fakeEnv(t, home, map[string]string{
		"PATH":  "/usr/bin:/bin",
		"SHELL": "/bin/bash",
	}, false) // Confirm() answers no

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Errorf(".bashrc should be untouched when the user declines, stat err = %v", err)
	}
}

func TestRunCLIAutoConfirmSkipsPrompt(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	promptAsked := false
	fakeEnv(t, home, map[string]string{
		"PATH":  "/usr/bin:/bin",
		"SHELL": "/bin/bash",
	}, false)
	Confirm = func(prompt string) bool { promptAsked = true; return false }

	if got := RunCLI(exe, Options{AutoConfirm: true}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
	if promptAsked {
		t.Errorf("Confirm was called despite --yes")
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatalf(".bashrc not written: %v", err)
	}
	profile, _ := DetectShellProfile(home, "/bin/bash")
	want := PathLine(profile, home, PreferredDir(home))
	if !strings.Contains(string(data), want) {
		t.Errorf(".bashrc = %q, want it to mention %q", data, want)
	}
}

func TestRunCLIRerunIsIdempotent(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	fakeEnv(t, home, map[string]string{
		"PATH":  "/usr/bin:/bin",
		"SHELL": "/bin/bash",
	}, true)

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("first RunCLI = %d, want 0", got)
	}
	first, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}

	// Confirm would now refuse, but re-running should not even need to ask:
	// the profile already has the line, and the binary is just overwritten.
	Confirm = func(prompt string) bool { t.Fatal("Confirm called on a re-run that needed no prompt"); return false }

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("second RunCLI = %d, want 0", got)
	}
	second, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf(".bashrc changed on re-run: first %q, second %q", first, second)
	}
	profile, _ := DetectShellProfile(home, "/bin/bash")
	line := PathLine(profile, home, PreferredDir(home))
	if n := strings.Count(string(second), line); n != 1 {
		t.Errorf("PATH line appears %d times after two runs, want 1: %q", n, second)
	}
}

func TestRunCLIUnknownShellPrintsLineInsteadOfPrompting(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	fakeEnv(t, home, map[string]string{
		"PATH":  "/usr/bin:/bin",
		"SHELL": "/bin/tcsh",
	}, false)
	Confirm = func(prompt string) bool { t.Fatal("Confirm called for an unrecognized shell"); return false }

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
}

func TestRunCLIFallsBackWhenPreferredDirUnavailable(t *testing.T) {
	home := t.TempDir()
	srcDir := t.TempDir()
	exe := writeFakeExecutable(t, srcDir, "f4")

	// Make ~/.local a plain file, so ~/.local/bin can be neither statted as a
	// directory nor created, and ~/bin must be used instead.
	if err := os.WriteFile(filepath.Join(home, ".local"), []byte("x"), 0o644); err != nil { // #nosec G306 -- test fixture, not sensitive
		t.Fatal(err)
	}
	fakeEnv(t, home, map[string]string{
		"PATH":  FallbackDir(home) + string(os.PathListSeparator) + "/usr/bin",
		"SHELL": "/bin/bash",
	}, false)

	if got := RunCLI(exe, Options{}); got != 0 {
		t.Fatalf("RunCLI = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(FallbackDir(home), "f4")); err != nil {
		t.Fatalf("binary not installed into fallback dir: %v", err)
	}
}
