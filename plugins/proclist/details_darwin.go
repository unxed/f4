//go:build darwin

package proclist

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// errDetailUnsupportedDarwin explains the empty environment and open-files
// sections: reading another process' argv/envp needs sysctl
// KERN_PROCARGS2's raw, hand-parsed buffer layout, and a real open-files
// listing (with paths, not just descriptor numbers) needs a second,
// path-carrying libproc struct (vnode_fdinfowithpath) -- both are exactly
// the kind of hand-rolled, CI-unverifiable layout collector_other.go's own
// comment already declined to risk for the BSDs in part 2. proc_pidpath
// below carries none of that risk: libproc itself validates and
// NUL-terminates the string it returns.
var errDetailUnsupportedDarwin = errors.New("proclist: not available on macOS without an unverified sysctl/struct layout")

// collectProcDetails on macOS fills only the command line section's first
// (and only) line: the executable's own full path, from libproc's
// proc_pidpath -- loaded here separately from collector_darwin.go's own
// libproc handle (rather than sharing it) so this file cannot affect that
// already-shipped CPU/memory code. It is not the true command line
// (arguments are not included), matching the ticket's own "not necessarily
// on par with Linux" scope -- the same simplification details_windows.go
// makes for the same reason.
func collectProcDetails(pid int) procDetails {
	return procDetails{
		cmdline:   readDarwinExecutablePath(pid),
		environ:   procDetailsSection{err: errDetailUnsupportedDarwin},
		openFiles: procDetailsSection{err: errDetailUnsupportedDarwin},
	}
}

// procPidPathMaxSize is PROC_PIDPATHINFO_MAXSIZE from <libproc.h>: 4 *
// MAXPATHLEN.
const procPidPathMaxSize = 4096

func readDarwinExecutablePath(pid int) procDetailsSection {
	initDetailsLibproc()
	if !detailsLibprocOK {
		return procDetailsSection{err: errors.New("proclist: libproc is unavailable")}
	}
	buf := make([]byte, procPidPathMaxSize)
	// #nosec G115 -- pid comes from collect()'s own kinfo_proc-derived P_pid
	// (collector_darwin.go), already bounds-checked non-negative there.
	n := procPidPathFn(int32(pid), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n <= 0 {
		return procDetailsSection{err: errors.New("proc_pidpath: process not found or not permitted")}
	}
	return procDetailsSection{lines: []string{string(buf[:n])}}
}

// detailsLibprocOnce/detailsLibprocOK/procPidPathFn mirror
// collector_darwin.go's own libprocOnce/libprocOK/procPidinfo -- a separate,
// independent dlopen of the same already-cached libproc.dylib (macOS 11+
// keeps no on-disk file for it; dlopen still resolves it through the dyld
// shared cache either way), so a mistake here cannot affect that file's own,
// already-shipped CPU/memory collection.
var (
	detailsLibprocOnce sync.Once
	detailsLibprocOK   bool
	procPidPathFn      func(pid int32, buffer unsafe.Pointer, buffersize uint32) int32
)

func initDetailsLibproc() {
	detailsLibprocOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				detailsLibprocOK = false
			}
		}()
		h, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_LAZY)
		if err != nil || h == 0 {
			return
		}
		purego.RegisterLibFunc(&procPidPathFn, h, "proc_pidpath")
		detailsLibprocOK = true
	})
}
