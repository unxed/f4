package main

import (
	"os"
	"os/exec"
)

// selfCommand builds a command that starts another copy of this executable
// with args. self is the path the caller would otherwise have handed
// exec.Command: call sites disagree on whether that is os.Args[0] or
// os.Executable(), and this does not settle the argument for them.
//
// On an ordinary build that is the whole story. The universal Linux build --
// the single artifact that runs on glibc and musl alike, built with
// -tags goffi_universal -- is different, and re-executing it by path is fatal
// rather than merely wrong. Such a binary carries no PT_INTERP and no
// DT_NEEDED, so nothing maps a libc into it; goffi re-execs the process
// through the host's own dynamic loader with the host libc pre-loaded, before
// main, and only that second launch has a libc at all. What the re-exec leaves
// behind is:
//
//   - os.Executable() points at the loader. /proc/self/exe names the file the
//     kernel really execve'd, which is ld.so, not f4.
//   - os.Args[0] points at the image the loader was told to run: on glibc a
//     memfd copy of ourselves ("/proc/self/fd/N"), on musl the resolved path
//     of the binary.
//   - GOFFI_UNIVERSAL_REEXEC is in the environment, and every child inherits
//     it.
//
// That last item is what turns a wrong path into a dead process. The child
// inherits the guard, goffi's bridge reads it as "this launch already came
// through the loader", and nothing binds a libc -- so the child dies on the
// first libc symbol it touches, before main and before any of f4's logging
// exists. On glibc the loader says "symbol lookup error: undefined symbol:
// malloc"; on musl it is a jump to an unbound symbol.
//
// So when this process came through the loader, its children go through it
// too, with the same libc pre-loaded and the same image -- exactly the argv
// goffi's bridge would have built. The loader shifts argv the way it always
// does, so the child still sees os.Args[0] as the image and its own arguments
// from os.Args[1] on.
func selfCommand(self string, args ...string) *exec.Cmd {
	name, argv := selfExecArgv(self, args)
	// #nosec G204 -- the program is this executable and the loader it was
	// started through; args are built by f4, never taken from user input.
	cmd := exec.Command(name, argv...)
	// Callers that want more of their own add to cmd.Env rather than to
	// os.Environ(), so that what selfExecEnv puts there survives.
	cmd.Env = selfExecEnv()
	// On Android (Termux) the command has to go through the system loader; it needs
	// argv[0] and the environment, which selfExecArgv does not return.
	applySystemLinkerExec(cmd)
	return cmd
}

// selfExecArgv picks the program and arguments selfCommand runs. Split out so
// the universal case can be checked without starting a process.
func selfExecArgv(self string, args []string) (string, []string) {
	loader, libc, ok := universalHostLoader()
	if !ok {
		return self, args
	}
	// os.Args[0] rather than self: in a universal build the caller's idea of
	// "our path" is the loader (os.Executable()) or a name that no longer
	// resolves to a loadable image, while argv[0] is the image goffi handed
	// the loader and is the only thing that can be run again.
	return loader, loaderArgv(libc, os.Args[0], args)
}

// loaderArgv is the argument vector for running image through the host
// dynamic loader with libc pre-loaded, followed by args.
func loaderArgv(libc, image string, args []string) []string {
	argv := make([]string, 0, len(args)+3)
	argv = append(argv, "--preload", libc, image)
	return append(argv, args...)
}

// linkerArgv is the argument vector for running image through Android's system
// loader, which takes the image path as its first argument and hands the rest
// to the image. argv0 stays in front because the loader consumes one entry, so
// the image still finds its own arguments from argv[1] on -- the positions
// ManageSessions() indexes for "--server".
func linkerArgv(argv0, image string, args []string) []string {
	argv := make([]string, 0, len(args)+2)
	argv = append(argv, argv0, image)
	return append(argv, args...)
}
