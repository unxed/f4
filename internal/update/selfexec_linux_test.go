//go:build linux && (amd64 || arm64)

package update

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// With the guard set, a copy is started from the file on disk -- not through
// the host loader, whose glibc 2.31 build refuses the memfd image with "loader
// cannot load itself", and not from os.Args[0] or the loader's own path.
func TestSelfExecPathUniversalUsesRecordedExecutable(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, strconv.Itoa(os.Getpid())+":1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid())+":/usr/bin/f4")
	t.Setenv(F4ExeEnv, "")

	for _, self := range []string{"/proc/self/fd/3", "/lib64/ld-linux-x86-64.so.2", "f4"} {
		if got := selfExecPath(self); got != "/usr/bin/f4" {
			t.Errorf("selfExecPath(%q) = %q, want the recorded executable /usr/bin/f4", self, got)
		}
	}
}

// A copy started by a copy knows the path through F4_EXE.
func TestSelfExecPathUniversalUsesHandedDownExecutable(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, strconv.Itoa(os.Getpid())+":1")
	t.Setenv(GoffiUniversalExe, "")
	t.Setenv(F4ExeEnv, "/opt/f4/f4")

	if got := selfExecPath("/proc/self/fd/3"); got != "/opt/f4/f4" {
		t.Errorf("selfExecPath() = %q, want F4_EXE /opt/f4/f4", got)
	}
}

// With nothing recorded, os.Args[0] is the image this process was loaded
// from, and goffi's bridge brings a copy of it up as well.
func TestSelfExecPathUniversalFallsBackToArgv0(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, strconv.Itoa(os.Getpid())+":1")
	t.Setenv(GoffiUniversalExe, "")
	t.Setenv(F4ExeEnv, "")

	if got := selfExecPath("/lib64/ld-linux-x86-64.so.2"); got != os.Args[0] {
		t.Errorf("selfExecPath() = %q, want os.Args[0] %q", got, os.Args[0])
	}
}

// No loader in the command line, whatever the build: the program is f4 and the
// arguments are the caller's.
func TestSelfCommandUniversalHasNoLoaderDetour(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, strconv.Itoa(os.Getpid())+":1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid())+":/usr/bin/f4")

	cmd := SelfCommand("/proc/self/fd/3", "--server", "/tmp/f4.sock")
	if cmd.Path != "/usr/bin/f4" {
		t.Errorf("Path = %q, want /usr/bin/f4", cmd.Path)
	}
	want := []string{"/usr/bin/f4", "--server", "/tmp/f4.sock"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("Args = %q, want %q", cmd.Args, want)
	}
}

// The copy runs goffi's bridge itself. An older universal f4 would take an
// inherited guard for its own and die before main, so none of the bridge's
// variables reach it; the path it cannot work out does.
func TestSelfExecEnvDropsBridgeVariables(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, strconv.Itoa(os.Getpid())+":1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid())+":/usr/bin/f4")
	t.Setenv("GOFFI_UNIVERSAL_ARGV0", strconv.Itoa(os.Getpid())+":f4")
	t.Setenv(F4ExeEnv, "")

	var sawExe bool
	for _, kv := range selfExecEnv() {
		if strings.HasPrefix(kv, "GOFFI_UNIVERSAL_") {
			t.Errorf("selfExecEnv() carries %q", kv)
		}
		if kv == F4ExeEnv+"=/usr/bin/f4" {
			sawExe = true
		}
	}
	if !sawExe {
		t.Errorf("selfExecEnv() does not carry %s=/usr/bin/f4", F4ExeEnv)
	}
}

// The prefix is goffi's. Pin the variables f4 reads to it, so that a rename in
// goffi shows up here rather than as a filter that strips nothing.
func TestBridgeVariablesShareThePrefix(t *testing.T) {
	for _, name := range []string{GoffiUniversalGuard, GoffiUniversalExe} {
		if !strings.HasPrefix(name, goffiUniversalEnvPrefix) {
			t.Errorf("%s does not start with %s", name, goffiUniversalEnvPrefix)
		}
	}
}

// The path f4 installs to is not recoverable from a re-execed process, so a
// universal build with nothing recorded must say so rather than answer with
// the loader's own path -- which is what os.Executable() returns there, and
// what would have sent the updater to /usr/lib.
func TestF4ExecutableUnknownInUniversalBuild(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, "1")
	t.Setenv(GoffiUniversalExe, "")
	t.Setenv(F4ExeEnv, "")

	if got, err := executable(); err == nil {
		t.Errorf("executable() = %q, nil; want an error", got)
	}
}

func TestF4ExecutablePrefersGoffiRecord(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, "1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid())+":/usr/bin/f4")
	t.Setenv(F4ExeEnv, "/handed/down/f4")

	got, err := executable()
	if err != nil {
		t.Fatalf("executable() error: %v", err)
	}
	if want := "/usr/bin/f4"; got != want {
		t.Errorf("executable() = %q, want %q", got, want)
	}
}

// A record tagged with another pid was inherited from a parent. The parent
// hands its answer down deliberately instead, through F4_EXE.
func TestF4ExecutableIgnoresInheritedRecord(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, "1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid()+1)+":/usr/bin/some-other-program")
	t.Setenv(F4ExeEnv, "/usr/bin/f4")

	got, err := executable()
	if err != nil {
		t.Fatalf("executable() error: %v", err)
	}
	if want := "/usr/bin/f4"; got != want {
		t.Errorf("executable() = %q, want %q", got, want)
	}
}

// An ordinary build asks the operating system and is right.
func TestF4ExecutableWithoutGuard(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, "")
	t.Setenv(F4ExeEnv, "/should/be/ignored")

	want, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable here: %v", err)
	}
	got, err := executable()
	if err != nil {
		t.Fatalf("executable() error: %v", err)
	}
	if got != want {
		t.Errorf("executable() = %q, want %q", got, want)
	}
}

func TestSelfExecEnvCarriesThePath(t *testing.T) {
	t.Setenv(GoffiUniversalGuard, "1")
	t.Setenv(GoffiUniversalExe, strconv.Itoa(os.Getpid())+":/usr/bin/f4")
	t.Setenv(F4ExeEnv, "")

	var found bool
	for _, e := range selfExecEnv() {
		if e == F4ExeEnv+"=/usr/bin/f4" {
			found = true
		}
	}
	if !found {
		t.Errorf("selfExecEnv() does not carry %s=/usr/bin/f4", F4ExeEnv)
	}
}
