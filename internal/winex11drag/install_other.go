//go:build !windows

package winex11drag

// Install is a no-op outside Windows, where there is no Wine GUI backend to
// wrap.
func Install() {}
