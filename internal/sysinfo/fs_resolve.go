package sysinfo

import (
	"sync"
	"time"
)

// Windows' GetDiskFreeSpaceEx answers for the volume named by the path
// text, so a folder reached through a junction or symlink reported the
// disk holding the link instead of the disk the link leads to (Linux's
// statfs and Far both follow the link). resolveForVolume gives FS the
// path with links expanded — symlinks and, on Windows, junctions too
// (finalPath; f4#1835). The panel footer asks for the free space on
// every redraw, so the answer is kept for a moment rather than walking
// the path again each time.
const resolveCacheTTL = 2 * time.Second

var (
	evalSymlinks = finalPath
	resolveClock = time.Now

	resolveMu    sync.Mutex
	resolveCache = map[string]resolvedPath{}
)

type resolvedPath struct {
	path string
	at   time.Time
}

func resolveForVolume(path string) string {
	now := resolveClock()
	resolveMu.Lock()
	if c, ok := resolveCache[path]; ok && now.Sub(c.at) < resolveCacheTTL {
		resolveMu.Unlock()
		return c.path
	}
	resolveMu.Unlock()

	resolved := path
	if r, err := evalSymlinks(path); err == nil && r != "" {
		resolved = r
	}

	resolveMu.Lock()
	if len(resolveCache) > 64 {
		resolveCache = map[string]resolvedPath{}
	}
	resolveCache[path] = resolvedPath{path: resolved, at: now}
	resolveMu.Unlock()
	return resolved
}
