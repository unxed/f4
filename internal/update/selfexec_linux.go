//go:build linux && (amd64 || arm64)

package update

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// GoffiUniversalGuard is the variable goffi's universal ("Profile U") bridge
// adds to the environment of the copy of the process it re-execs through the
// host dynamic loader, as "<pid>:1". Its presence says this process reached a
// libc that way, so os.Executable() and os.Args[0] no longer name the file on
// disk. Before goffi v0.1.11 it was a plain "1" that a child took for its own,
// so a child started by path bound no libc and died before main.
//
// The name is goffi's, and a rename there would turn the checks below into
// no-ops: executable() would answer with the loader's path.
const GoffiUniversalGuard = "GOFFI_UNIVERSAL_REEXEC"

// GoffiUniversalExe is where goffi's universal bridge records the path of the
// binary it re-execed, as "<pid>:<path>". The pid is the process the record
// describes; the environment it lives in is inherited, so a child (a new pid)
// must not read it as being about itself.
//
// Recording it needs a goffi that does so. Against one that does not the
// variable is simply absent, and executable answers "unknown" instead of
// answering with the loader's path, which is the failure this is here to
// prevent.
const GoffiUniversalExe = "GOFFI_UNIVERSAL_EXE"

// F4ExeEnv passes this executable's path to the copies of f4 that SelfCommand
// starts. goffi's record cannot cover them: it is tagged with the pid of the
// process the bridge re-execed, and a child has a different one -- but the
// child is the same binary, so the parent's answer is the child's answer.
const F4ExeEnv = "F4_EXE"

var errExecutableUnknown = errors.New(
	"f4 cannot tell where its own executable is: this build reached its libc by " +
		"re-execing through the host dynamic loader, which leaves /proc/self/exe " +
		"pointing at ld.so and argv[0] at an in-memory copy")

// executable is os.Executable, corrected for the universal build.
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
func executable() (string, error) {
	// On Android f4 comes up through the system loader, so /proc/self/exe names
	// linker64 and os.Executable would point callers at /system/bin.
	if p, ok := systemLinkerExecutable(); ok {
		return p, nil
	}
	if !universalBuild() {
		return os.Executable()
	}
	if p, ok := recordedExecutable(os.Getenv(GoffiUniversalExe)); ok {
		return p, nil
	}
	if p := os.Getenv(F4ExeEnv); p != "" {
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

// goffiUniversalEnvPrefix names the variables goffi's universal bridge writes:
// the guard and the pid-tagged records of what the re-exec destroyed.
const goffiUniversalEnvPrefix = "GOFFI_UNIVERSAL_"

// selfExecEnv is the environment for a copy of this process: ours, without the
// bridge's variables, plus the path we know and the copy may not work out.
//
// The copy runs the bridge itself and writes its own. goffi v0.1.11 drops
// inherited ones and only trusts records tagged with its own pid, but an older
// universal f4 -- the binary an update replaces, say -- takes any guard for
// its own, binds no libc, and dies before main. Leaving them out costs
// nothing.
func selfExecEnv() []string {
	src := os.Environ()
	env := make([]string, 0, len(src)+1)
	for _, kv := range src {
		if strings.HasPrefix(kv, goffiUniversalEnvPrefix) {
			continue
		}
		env = append(env, kv)
	}
	if exe, err := executable(); err == nil && exe != "" {
		env = append(env, F4ExeEnv+"="+exe)
	}
	return env
}

// universalBuild reports whether this process came up through goffi's
// universal bridge, which leaves its guard in the environment.
func universalBuild() bool {
	return os.Getenv(GoffiUniversalGuard) != ""
}
