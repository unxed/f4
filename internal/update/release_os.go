package update

// releaseOS is the OS name in this build's release asset: the GOOS, except
// for the Windows 7/8/8.1 build (-tags win7, built with the go-legacy-win7
// toolchain), which is published as f4-windows7-<arch>.zip. The regular
// Windows build does not start on those systems, so the two must never
// update into each other.
func releaseOS(goos string) string {
	if goos == "windows" && windows7Build {
		return "windows7"
	}
	return goos
}
