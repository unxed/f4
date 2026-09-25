//go:build !linux || (!amd64 && !arm64)

package update

import "os"

// goffi's universal ("Profile U") mode is Linux-only -- it is a way to reach
// glibc or musl through whichever loader the host has, and no other platform
// poses that question -- and f4 builds it only for linux/amd64 and
// linux/arm64. Everywhere else this process starts the way it appears to
// start, so another copy of it is just its path.
//
// The other Linux architectures land here too, and must: goffi's ffi package
// does not build for them at all, so the universal variant cannot even be
// compiled there, let alone used.
//
// This is not a statement about goffi's platform support, which is wider than
// Linux (see docs/PLATFORMS.md there); it is about the one build mode that
// rewrites argv[0] and /proc/self/exe out from under the program.
func universalBuild() bool {
	return false
}

// executable is os.Executable where no build mode moves the executable out
// from under the program -- except the Android loader launch, which is not a
// build mode. Termux on 32-bit ARM (android/arm, #1380) lands in this file,
// because selfexec_linux.go is built only for amd64 and arm64, and it comes
// up through /system/bin/linker exactly as arm64 comes up through linker64:
// SelfCommand always starts the child that way (applySystemLinkerExec), and
// /proc/self/exe then names the loader. The same correction as in
// selfexec_linux.go applies, and outside Android it answers "no".
func executable() (string, error) {
	if p, ok := systemLinkerExecutable(); ok {
		return p, nil
	}
	return os.Executable()
}

// selfExecEnv is the environment for a copy of this process, which is ours.
func selfExecEnv() []string {
	return os.Environ()
}
