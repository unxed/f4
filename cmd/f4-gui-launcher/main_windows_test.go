//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeInstall(t *testing.T) (dir string, executable func() (string, error)) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f4.exe"), []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, func() (string, error) { return filepath.Join(dir, "f4-gui.exe"), nil }
}

func TestRunStartsF4NextToTheLauncherInGuiMode(t *testing.T) {
	dir, executable := fakeInstall(t)
	var got *exec.Cmd
	code := run([]string{`C:\a b\file.txt`, "-x"}, executable,
		func(cmd *exec.Cmd) error { got = cmd; return nil },
		func(msg string) { t.Errorf("unexpected error box: %s", msg) })
	if code != 0 || got == nil {
		t.Fatalf("run = %d, command %v", code, got)
	}
	if got.Path != filepath.Join(dir, "f4.exe") {
		t.Errorf("started %q, want the f4.exe next to the launcher", got.Path)
	}
	if want := []string{got.Path, "--gui", `C:\a b\file.txt`, "-x"}; strings.Join(got.Args, "|") != strings.Join(want, "|") {
		t.Errorf("arguments %q, want %q", got.Args, want)
	}
	if got.SysProcAttr == nil || got.SysProcAttr.CreationFlags&detachedProcess == 0 {
		t.Error("the child must be started without a console (DETACHED_PROCESS)")
	}
}

func TestRunReportsWhatWentWrong(t *testing.T) {
	var msgs []string
	fail := func(m string) { msgs = append(msgs, m) }
	noStart := func(*exec.Cmd) error { t.Error("nothing should be started"); return nil }

	if code := run(nil, func() (string, error) { return "", errors.New("boom") }, noStart, fail); code != 2 {
		t.Errorf("an unknown launcher path gave %d", code)
	}
	empty := t.TempDir()
	if code := run(nil, func() (string, error) { return filepath.Join(empty, "f4-gui.exe"), nil }, noStart, fail); code != 2 {
		t.Errorf("a missing f4.exe gave %d", code)
	}
	if err := os.Mkdir(filepath.Join(empty, "f4.exe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := run(nil, func() (string, error) { return filepath.Join(empty, "f4-gui.exe"), nil }, noStart, fail); code != 2 {
		t.Errorf("a folder named f4.exe gave %d", code)
	}
	_, executable := fakeInstall(t)
	if code := run(nil, executable, func(*exec.Cmd) error { return errors.New("denied") }, fail); code != 1 {
		t.Errorf("a failed start gave %d", code)
	}
	if len(msgs) != 4 || !strings.Contains(msgs[1], "f4.exe was not found") || !strings.Contains(msgs[3], "denied") {
		t.Errorf("messages = %q", msgs)
	}
}

func TestShowErrorIsSilentWhenAskedTo(t *testing.T) {
	t.Setenv(quietEnv, "1")
	showError("this must not open a box") // would block the test if it did
}

// The point of the launcher, end to end: f4-gui.exe (linked -H windowsgui)
// starts the f4.exe beside it with --gui and its own arguments, and that
// process has no console, like the second copy of the binary it replaces.
func TestLauncherStartsF4WithoutAConsole(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two programs")
	}
	t.Setenv(quietEnv, "1") // the error box of the last step would wait for a click
	dir := t.TempDir()
	build := func(out string, args ...string) {
		t.Helper()
		cmd := exec.Command("go", append([]string{"build", "-o", out}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", args, err, output)
		}
	}
	build(filepath.Join(dir, "f4-gui.exe"), "-ldflags=-H windowsgui", ".")
	build(filepath.Join(dir, "f4.exe"), "./testdata/stub")

	out := filepath.Join(dir, "out.txt")
	cmd := exec.Command(filepath.Join(dir, "f4-gui.exe"), "one", "two words")
	cmd.Env = append(os.Environ(), "F4_STUB_OUT="+out)
	if err := cmd.Run(); err != nil {
		t.Fatalf("the launcher failed: %v", err)
	}
	var text string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil && strings.HasSuffix(string(data), "\n") {
			text = string(data)
			break
		}
	}
	if text == "" {
		t.Fatal("the stub f4.exe never reported")
	}
	if !strings.Contains(text, "args=--gui|one|two words\n") {
		t.Errorf("the stub got %q", text)
	}
	if !strings.Contains(text, "console=0\n") {
		t.Errorf("the stub had a console: %q", text)
	}

	// Without f4.exe next to it the launcher says so and exits with 2.
	if err := os.Remove(filepath.Join(dir, "f4.exe")); err != nil {
		t.Fatal(err)
	}
	err := exec.Command(filepath.Join(dir, "f4-gui.exe")).Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("without f4.exe the launcher exited with %v, want code 2", err)
	}
}
