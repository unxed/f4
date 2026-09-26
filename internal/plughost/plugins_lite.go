//go:build lite

package plughost

// optionalVFSPlugins is empty in a lite build: no archive, cloud or
// ftp/sftp/scp VFS provider is registered, and none of their packages (nor
// the cloud SDKs and archive libraries they pull in) are even imported. See
// plugins_full.go for the real list and manager.go for where this is called.
func optionalVFSPlugins() []Plugin {
	return nil
}
