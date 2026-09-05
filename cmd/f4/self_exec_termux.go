package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/unxed/vtui"
)

// termuxSelfExeEnv is where Termux records the path of the image that was
// launched. Going through the loader leaves /proc/self/exe naming linker64, so
// this is the only thing that still answers "where is f4".
const termuxSelfExeEnv = "TERMUX_EXEC__PROC_SELF_EXE"

// systemLinker is the loader an image of this architecture is launched through.
func systemLinker() string {
	if runtime.GOARCH == "arm" || runtime.GOARCH == "386" {
		return "/system/bin/linker"
	}
	return "/system/bin/linker64"
}

func isELF(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is the program f4 is about to exec.
	if err != nil {
		return false
	}
	defer f.Close()

	var magic [4]byte
	if n, err := f.Read(magic[:]); err != nil || n < 4 {
		return false
	}
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}

// setTermuxSelfExe replaces the inherited value rather than appending a second
// one: ours describes this process and would be read first.
func setTermuxSelfExe(env []string, exe string) []string {
	prefix := termuxSelfExeEnv + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + exe
			return env
		}
	}
	return append(env, prefix+exe)
}

// applySystemLinkerExec runs cmd's program through the system loader.
//
// Android refuses to execute a file in an app's own data directory once the app
// targets API 29 or later, so execve on $PREFIX/bin/f4 returns EACCES. Termux
// hides that by intercepting the exec() family through libtermux-exec.so, but
// that hook is in libc and Go issues execve directly with RawSyscall, so f4's
// own re-execs never reach it. Termux's Go package patches os.startProcess for
// the same reason; a binary built with an unpatched toolchain does it here.
//
// Anything that cannot be launched this way -- no loader, not an ELF -- is left
// alone, so the caller keeps whatever behaviour it had.
func applySystemLinkerExec(cmd *exec.Cmd) {
	if runtime.GOOS != "android" || cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return
	}

	linker := systemLinker()
	if _, err := os.Stat(linker); err != nil {
		vtui.DebugLog("SELFEXEC: no system linker at %s (%v); running %s directly", linker, err, cmd.Path)
		return
	}
	if !isELF(cmd.Path) {
		vtui.DebugLog("SELFEXEC: %s is not an ELF image; running it directly", cmd.Path)
		return
	}

	image := cmd.Path
	cmd.Path = linker
	cmd.Args = linkerArgv(cmd.Args[0], image, cmd.Args[1:])
	cmd.Env = setTermuxSelfExe(cmd.Env, image)

	vtui.DebugLog("SELFEXEC: launching %s through %s, argv=%q", image, linker, cmd.Args)
}

// systemLinkerExecutable is the path f4 was launched from when it came up
// through the loader. Relative values are made absolute because callers resolve
// files next to the binary.
func systemLinkerExecutable() (string, bool) {
	if runtime.GOOS != "android" {
		return "", false
	}
	exe := os.Getenv(termuxSelfExeEnv)
	if exe == "" {
		return "", false
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs, true
	}
	return exe, true
}
