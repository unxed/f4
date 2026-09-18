//go:build !windows

package terminal

import "errors"

// ErrConsoleSpawnUnavailable is always returned off Windows: nothing outside
// Windows hands a child explicit console handles, so there is nothing to
// avoid. See console_spawn_windows.go for what this exists for.
var ErrConsoleSpawnUnavailable = errors.New("terminal: host console spawn not applicable")

// LegacyChildStdioEnvVar is declared here too so callers and docs can refer to
// it on any platform.
const LegacyChildStdioEnvVar = "F4_LEGACY_CHILD_STDIO"

// RunOnHostConsole never applies off Windows.
func RunOnHostConsole(dir, shell, flag, command string) error {
	return ErrConsoleSpawnUnavailable
}
