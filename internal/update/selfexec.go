package update

import (
	"os"
	"os/exec"
)

// SelfCommand builds a command that starts another copy of this executable
// with args. self is the path the caller would otherwise have handed
// exec.Command: call sites disagree on whether that is os.Args[0] or
// os.Executable(), and this does not settle the argument for them.
//
// On an ordinary build that is the whole story. The universal Linux build --
// the single artifact that runs on glibc and musl alike, built with
// -tags goffi_universal -- re-execs itself through the host's dynamic loader
// before main, and that leaves both of the caller's candidates wrong:
//
//   - os.Executable() points at the loader. /proc/self/exe names the file the
//     kernel really execve'd, which is ld.so, not f4.
//   - os.Args[0] points at the image the loader was told to run: on glibc a
//     memfd copy of ourselves ("/proc/self/fd/N"), on musl the resolved path
//     of the binary.
//
// So a universal build starts the copy from the file on disk, the path
// executable() recovers. The copy is loaded by the kernel directly and runs
// goffi's bridge itself, exactly as the first launch did. That needs a goffi
// whose re-exec guard (GOFFI_UNIVERSAL_REEXEC) names the pid it was written
// for -- goffi v0.1.11 of the unxed fork -- so that the copy does not read its
// parent's guard as its own. The copy's environment drops the bridge's
// variables all the same (see selfExecEnv), which keeps an older universal
// binary, such as the one an update replaces, from being misled by them.
//
// Copies used to be started through the host loader by hand
// ("<ld.so> --preload <libs> /proc/self/fd/N"). glibc 2.31's loader refuses
// that image with "loader cannot load itself" (Debian 11, Ubuntu 20.04), so
// the copy never started there.
func SelfCommand(self string, args ...string) *exec.Cmd {
	// #nosec G204 -- the program is this executable; args are built by f4,
	// never taken from user input.
	cmd := exec.Command(selfExecPath(self), args...)
	// Callers that want more of their own add to cmd.Env rather than to
	// os.Environ(), so that what selfExecEnv puts there survives.
	cmd.Env = selfExecEnv()
	// On Android (Termux) the command has to go through the system loader; it needs
	// argv[0] and the environment, which selfExecPath does not return.
	applySystemLinkerExec(cmd)
	return cmd
}

// selfExecPath picks the program SelfCommand runs. Split out so the universal
// case can be checked without starting a process.
func selfExecPath(self string) string {
	if !universalBuild() {
		return self
	}
	// The caller's idea of "our path" is the loader (os.Executable()) or an
	// in-memory image (os.Args[0] on glibc); the file on disk is what the
	// first launch ran and what a copy should run too.
	if exe, err := executable(); err == nil && exe != "" {
		return exe
	}
	// Nothing recorded the path. os.Args[0] still names a loadable image: on
	// musl the binary itself, on glibc the memfd copy this process was loaded
	// from, which a child inherits and goffi's bridge handles.
	return os.Args[0]
}

// linkerArgv is the argument vector for running image through Android's system
// loader, which takes the image path as its first argument and hands the rest
// to the image. argv0 stays in front because the loader consumes one entry, so
// the image still finds its own arguments from argv[1] on -- the positions
// term.ManageSessions() indexes for "--server".
func linkerArgv(argv0, image string, args []string) []string {
	argv := make([]string, 0, len(args)+2)
	argv = append(argv, argv0, image)
	return append(argv, args...)
}
