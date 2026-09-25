package update

import (
	"reflect"
	"testing"
)

func TestLinkerArgv(t *testing.T) {
	got := linkerArgv("f4", "/usr/bin/f4", []string{"--server", "/tmp/f4.sock"})
	want := []string{"f4", "/usr/bin/f4", "--server", "/tmp/f4.sock"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("linkerArgv() = %q, want %q", got, want)
	}
}

func TestLinkerArgvWithoutArguments(t *testing.T) {
	got := linkerArgv("f4", "/usr/bin/f4", nil)
	want := []string{"f4", "/usr/bin/f4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("linkerArgv() = %q, want %q", got, want)
	}
}

// term.ManageSessions() reads the daemon's arguments by position, so the shift the
// loader introduces has to land "--server" on os.Args[1] and the socket on
// os.Args[2]; otherwise a daemon comes up as an ordinary client.
func TestLinkerArgvKeepsServerArgumentPositions(t *testing.T) {
	sock := "/tmp/f4-new-1-2.sock"
	argv := linkerArgv("f4", "/usr/bin/f4", []string{"--server", sock})

	seen := argv[1:] // the loader consumes its own argv[0]
	if len(seen) < 3 {
		t.Fatalf("image would see %q, too short to carry --server", seen)
	}
	if seen[1] != "--server" {
		t.Errorf("os.Args[1] = %q, want %q", seen[1], "--server")
	}
	if seen[2] != sock {
		t.Errorf("os.Args[2] = %q, want %q", seen[2], sock)
	}
}

// Outside a universal build -- which is every test run that is not itself
// started through goffi's bridge -- selfExecPath must hand back exactly what
// the caller asked for.
func TestSelfExecPathPlain(t *testing.T) {
	t.Setenv("GOFFI_UNIVERSAL_REEXEC", "")

	if got := selfExecPath("/usr/bin/f4"); got != "/usr/bin/f4" {
		t.Errorf("program = %q, want %q", got, "/usr/bin/f4")
	}
}
