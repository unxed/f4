package vfs

import (
	"strings"
	"testing"
)

// The names goffi's universal bridge uses. Spelled out here rather than
// imported: internal/update holds them as constants, and vfs cannot import
// that package (internal/update -> internal/unpack -> vfs). A rename in goffi
// therefore has to be followed in two places, and this test is the second one
// -- it fails only if the prefix stops covering the names, which is exactly
// when childEnv stops protecting the children.
const (
	goffiUniversalGuardVar = "GOFFI_UNIVERSAL_REEXEC"
	goffiUniversalExeVar   = "GOFFI_UNIVERSAL_EXE"
	goffiUniversalArgv0Var = "GOFFI_UNIVERSAL_ARGV0"
)

func TestGoffiUniversalVarsShareTheFilteredPrefix(t *testing.T) {
	for _, name := range []string{goffiUniversalGuardVar, goffiUniversalExeVar, goffiUniversalArgv0Var} {
		if !strings.HasPrefix(name, goffiUniversalEnvPrefix) {
			t.Errorf("%s is not covered by the filtered prefix %q", name, goffiUniversalEnvPrefix)
		}
	}
}

func TestChildEnvDropsTheUniversalReexecRecords(t *testing.T) {
	t.Setenv(goffiUniversalGuardVar, "1")
	t.Setenv(goffiUniversalExeVar, "4978:/home/user/bin/f4/f4")
	t.Setenv(goffiUniversalArgv0Var, "4978:/proc/self/fd/3")
	t.Setenv("F4_SUDO_CHILD_ENV_KEEPER", "kept")

	env := childEnv()

	keeper := false
	for _, kv := range env {
		if strings.HasPrefix(kv, goffiUniversalEnvPrefix) {
			t.Errorf("child environment still carries %q", kv)
		}
		if kv == "F4_SUDO_CHILD_ENV_KEEPER=kept" {
			keeper = true
		}
	}
	if !keeper {
		t.Error("child environment lost a variable that has nothing to do with the loader bridge")
	}
}
