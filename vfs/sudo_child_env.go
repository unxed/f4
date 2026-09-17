package vfs

import (
	"os"
	"strings"
)

// goffiUniversalEnvPrefix is the prefix of the variables goffi's universal
// ("Profile U") bridge puts in the environment of the process it re-execs
// through the host dynamic loader: GOFFI_UNIVERSAL_REEXEC (the guard),
// GOFFI_UNIVERSAL_EXE and GOFFI_UNIVERSAL_ARGV0 (what the re-exec destroyed).
// See goffi's docs/PROFILE_U.md and the longer account in
// internal/update/selfexec.go.
//
// The guard says "this launch already came through the loader, do not re-exec
// again". It is inherited, and nothing in it names the process it describes,
// so a child that inherits it is told something false about itself: the
// bridge in the child returns early, no libc is mapped, and the first libc
// symbol the child touches -- malloc, at the top of x_cgo_init -- is an
// unbound PLT entry. The child jumps to address 0 and dies of SIGSEGV before
// main, before the Go runtime installs its signal handlers, so there is no
// traceback and no f4 log entry, only a core dump whose instruction pointer
// is 0.
//
// A rename in goffi turns the filter below into a no-op rather than into a
// wrong answer: children would go back to inheriting the guard, which is the
// behaviour that killed them. The pinning test lives in sudo_child_env_test.go.
const goffiUniversalEnvPrefix = "GOFFI_UNIVERSAL_"

// childEnv is os.Environ() with the records that describe *this* process's
// trip through the host dynamic loader removed, so a child of it makes that
// trip itself instead of assuming it already has.
//
// Copies of f4 that f4 starts directly go through update.SelfCommand, which
// imitates the bridge: it runs the host loader on the image this process was
// loaded from. That recipe (goffi's own, in docs/PROFILE_U.md) cannot be used
// here. sudo takes one program path and no arguments -- neither for the
// command, which must stay "f4 --sudo-dispatcher <socket>" for sudoers rules
// to be written against, nor for SUDO_ASKPASS, which has no place to put
// "--preload libc.so.6" at all -- and the image the recipe would hand the
// loader is, on glibc, a memfd of this process that no child of sudo can
// open. So the children are started by path, from disk, and what they need is
// simply not to be lied to about having a libc already.
func childEnv() []string {
	src := os.Environ()
	env := make([]string, 0, len(src))
	for _, kv := range src {
		if strings.HasPrefix(kv, goffiUniversalEnvPrefix) {
			continue
		}
		env = append(env, kv)
	}
	return env
}
