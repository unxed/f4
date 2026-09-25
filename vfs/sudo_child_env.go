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
// again". It is inherited. Before goffi v0.1.11 nothing in it named the
// process it described, so a child that inherited it was told something
// false about itself: the bridge in the child returned early, no libc was
// mapped, and the first libc symbol the child touched -- malloc, at the top
// of x_cgo_init -- was an unbound PLT entry. The child jumped to address 0
// and died of SIGSEGV before main, with no traceback and no f4 log entry.
// v0.1.11 writes "<pid>:1" and a child runs the bridge itself, but the f4
// that sudo starts may be an older build, so the variables stay out.
//
// A rename in goffi turns the filter below into a no-op rather than into a
// wrong answer: children would go back to inheriting the guard, which is the
// behaviour that killed them. The pinning test lives in sudo_child_env_test.go.
const goffiUniversalEnvPrefix = "GOFFI_UNIVERSAL_"

// childEnv is os.Environ() with the records that describe *this* process's
// trip through the host dynamic loader removed, so a child of it makes that
// trip itself instead of assuming it already has.
//
// The children are started by path, from disk -- sudo takes one program path,
// both for the command, which must stay "f4 --sudo-dispatcher <socket>" for
// sudoers rules to be written against, and for SUDO_ASKPASS -- and what they
// need is simply not to be lied to about having a libc already.
// update.SelfCommand starts copies of f4 the same way.
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
