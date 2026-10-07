//go:build !windows

package vfs

// LaunchElevatedDispatcher is Windows-only: elsewhere sudo starts the
// dispatcher (SudoClient.Connect).
func LaunchElevatedDispatcher(exe, sock, token string) error {
	return ErrElevationLaunchUnsupported
}
