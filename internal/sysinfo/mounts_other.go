//go:build !linux && !windows

package sysinfo

// MountEntry mirrors the Linux type (mounts_linux.go) so drives_unix.go can
// share code across unices, but the mount-manager first slice (f4#415) is
// Linux-only by design -- the owner's scope explicitly starts with *nix and
// leaves the rest for later.
type MountEntry struct {
	Device     string
	MountPoint string
	FSType     string
}

// UserMounts returns no entries outside Linux, leaving darwin/BSD builds
// behaving exactly as they did before this feature existed.
func UserMounts() []MountEntry { return nil }
