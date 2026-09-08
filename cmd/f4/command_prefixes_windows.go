//go:build windows

package main

func registerPlatformCommandPrefixes() {
	registerCommandPrefixOnce("builtin.registry", "reg", "reg")
}
