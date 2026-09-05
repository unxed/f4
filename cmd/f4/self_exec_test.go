package main

import (
	"reflect"
	"testing"
)

func TestLoaderArgv(t *testing.T) {
	got := loaderArgv("libc.so.6", "/proc/self/fd/3", []string{"--server", "/tmp/f4.sock"})
	want := []string{"--preload", "libc.so.6", "/proc/self/fd/3", "--server", "/tmp/f4.sock"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaderArgv() = %q, want %q", got, want)
	}
}

func TestLoaderArgvWithoutArguments(t *testing.T) {
	got := loaderArgv("libc.so.6", "/usr/bin/f4", nil)
	want := []string{"--preload", "libc.so.6", "/usr/bin/f4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaderArgv() = %q, want %q", got, want)
	}
}

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

// ManageSessions() reads the daemon's arguments by position, so the shift the
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
// started through goffi's bridge -- selfExecArgv must hand back exactly what
// the caller asked for.
func TestSelfExecArgvPlain(t *testing.T) {
	t.Setenv("GOFFI_UNIVERSAL_REEXEC", "")

	args := []string{"--server", "/tmp/f4.sock"}
	name, argv := selfExecArgv("/usr/bin/f4", args)
	if name != "/usr/bin/f4" {
		t.Errorf("program = %q, want %q", name, "/usr/bin/f4")
	}
	if !reflect.DeepEqual(argv, args) {
		t.Errorf("args = %q, want %q", argv, args)
	}
}
