//go:build linux

package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPacmanPackageOwner(t *testing.T) {
	binDir := t.TempDir()
	pacman := filepath.Join(binDir, "pacman")
	script := "#!/bin/sh\n[ \"$1\" = -Qqo ] || exit 2\n[ \"$2\" = /usr/bin/f4 ] || exit 2\nprintf 'f4-bin\\n'\n"
	if err := os.WriteFile(pacman, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	manager, packageName := pacmanPackageOwner("/usr/bin/f4")
	if manager != "pacman" || packageName != "f4-bin" {
		t.Fatalf("pacmanPackageOwner() = %q, %q; want pacman, f4-bin", manager, packageName)
	}
}

func TestCheckRefusesPackageManagedExecutableBeforeNetwork(t *testing.T) {
	oldAPI, oldExecutable, oldOwner := APIURL, Executable, packageManagerOwner
	t.Cleanup(func() {
		APIURL, Executable, packageManagerOwner = oldAPI, oldExecutable, oldOwner
	})
	APIURL = "http://127.0.0.1:1/unreachable"
	Executable = func() (string, error) { return "/usr/bin/f4", nil }
	packageManagerOwner = func(exe string) (manager, packageName string) {
		if exe != "/usr/bin/f4" {
			t.Fatalf("packageManagerOwner path = %q", exe)
		}
		return "pacman", "f4-bin"
	}

	_, err := Check(context.Background(), Settings{Channel: ChannelStable}, Build{})
	if err == nil || !strings.Contains(err.Error(), `managed by pacman package "f4-bin"`) {
		t.Fatalf("Check() error = %v, want package-manager refusal", err)
	}
}
