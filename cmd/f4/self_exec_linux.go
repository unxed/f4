//go:build linux && (amd64 || arm64)

package main

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/go-webgpu/goffi/ffi"
)

// goffiUniversalGuard is the variable goffi's universal ("Profile U") bridge
// adds to the environment of the copy of the process it re-execs through the
// host dynamic loader. Its presence says two things: this process reached a
// libc that way, and a child that inherits the variable will not be given one,
// because the bridge in the child reads the guard and returns instead of
// re-execing.
//
// The name is goffi's, and a rename there would turn the check below into a
// no-op rather than into a wrong answer: f4 would go back to starting children
// by path, which is the behaviour that made them die before main.
const goffiUniversalGuard = "GOFFI_UNIVERSAL_REEXEC"

// goffiUniversalExe is where goffi's universal bridge records the path of the
// binary it re-execed, as "<pid>:<path>". The pid is the process the record
// describes; the environment it lives in is inherited, so a child (a new pid)
// must not read it as being about itself.
//
// Recording it needs a goffi that does so. Against one that does not the
// variable is simply absent, and f4Executable answers "unknown" instead of
// answering with the loader's path, which is the failure this is here to
// prevent.
const goffiUniversalExe = "GOFFI_UNIVERSAL_EXE"

// f4ExeEnv passes this executable's path to the copies of f4 that selfCommand
// starts. goffi's record cannot cover them: it is tagged with the pid of the
// process the bridge re-execed, and a child has a different one -- but the
// child is the same binary, so the parent's answer is the child's answer.
const f4ExeEnv = "F4_EXE"

var errExecutableUnknown = errors.New(
	"f4 cannot tell where its own executable is: this build reached its libc by " +
		"re-execing through the host dynamic loader, which leaves /proc/self/exe " +
		"pointing at ld.so and argv[0] at an in-memory copy")

// f4Executable is os.Executable, corrected for the universal build.
//
// os.Executable reads /proc/self/exe, which after the bridge's re-exec names
// the loader rather than f4 -- so anything that installs, updates, re-runs or
// looks for files next to the binary gets an answer from somewhere in
// /usr/lib. That is not a cosmetic error: it is how the updater came to
// consider unpacking a release next to ld.so, with the sudo fallback in
// writeFileSafe standing ready to make it possible.
//
// The path is genuinely not recoverable from a re-execed process, so when
// nothing recorded it this reports that rather than guessing. Callers that can
// carry on without knowing (portable-config detection, say) already fall back
// to os.Args[0]; callers that cannot must refuse.
func f4Executable() (string, error) {
	// On Android f4 comes up through the system loader, so /proc/self/exe names
	// linker64 and os.Executable would point callers at /system/bin.
	if p, ok := systemLinkerExecutable(); ok {
		return p, nil
	}
	if os.Getenv(goffiUniversalGuard) == "" {
		return os.Executable()
	}
	if p, ok := recordedExecutable(os.Getenv(goffiUniversalExe)); ok {
		return p, nil
	}
	if p := os.Getenv(f4ExeEnv); p != "" {
		return p, nil
	}
	return "", errExecutableUnknown
}

// recordedExecutable returns the path from a "<pid>:<path>" record, and
// whether it describes this process.
func recordedExecutable(raw string) (string, bool) {
	tag, path, found := strings.Cut(raw, ":")
	if !found || path == "" {
		return "", false
	}
	pid, err := strconv.Atoi(tag)
	if err != nil || pid != os.Getpid() {
		return "", false
	}
	return path, true
}

// selfExecEnv is the environment for a copy of this process: ours, plus the
// path we know and it cannot work out.
func selfExecEnv() []string {
	env := os.Environ()
	if exe, err := f4Executable(); err == nil && exe != "" {
		env = append(env, f4ExeEnv+"="+exe)
	}
	return env
}

// universalHostLoader reports the host dynamic loader and libc SONAME that
// copies of this process must be started through, and whether that applies at
// all. It does only when this process itself came up that way; an ordinary
// dynamic or static build wants none of it.
func universalHostLoader() (loader, libc string, ok bool) {
	if os.Getenv(goffiUniversalGuard) == "" {
		return "", "", false
	}
	// The same table the bridge used, through goffi's public accessors: an
	// empty answer means the host runs a libc goffi does not recognise, and
	// then there was no re-exec to imitate.
	loader, libc = ffi.HostLoader(), ffi.HostLibC()
	if loader == "" || libc == "" {
		return "", "", false
	}
	return loader, libc, true
}
