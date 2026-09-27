//go:build !linux && !windows && !darwin

package proclist

import (
	"errors"

	"github.com/unxed/f4/vfs"
)

// Supported reports whether this build can collect a real process list.
//
// f4#312 part 2 of 4 added Windows (collector_windows.go, via
// golang.org/x/sys/windows' Toolhelp32/GetProcessTimes) and macOS
// (collector_darwin.go, via sysctl kern.proc.all for enumeration and
// libproc's proc_pidinfo for CPU/memory). The BSDs deliberately stayed on
// this stub for now: FreeBSD, NetBSD and OpenBSD each expose their own,
// differently laid out kinfo_proc/kinfo_proc2 (and golang.org/x/sys/unix,
// unlike its darwin support, defines none of them for this module's Go
// version), and none of the three has a GitHub-hosted runner -- not even
// through sandbox.yml's manual dispatch, whose os choices are limited to
// ubuntu/windows/macos. A hand-rolled struct layout for any of them would
// be build-checked by the cross-compile matrix (build.yml's separate
// "extra" job already builds freebsd/netbsd/openbsd/dragonfly/illumos/
// solaris binaries) but never actually executed anywhere in this project's
// CI, which is a materially different risk than Windows/macOS got: a wrong
// field offset would silently misreport CPU/memory instead of failing a
// test. Picking this back up needs either a real verification path or an
// explicit owner decision to accept that risk.
func Supported() bool { return false }

// newProcListPanel and (*Plugin).configure below are unreachable in
// practice: Plugin.Init (plugin.go, which has no build tag of its own and
// so must compile here too) checks Supported() before ever registering
// either as a panel provider's Open callback or a plugin command's Run
// handler. They still need real, correctly-typed bodies -- newProcListPanel
// matching panel.go's real one (f4#312 part 4 of 4 added the *settingsStore
// parameter to both), and configure matching config_dialog.go's -- so this
// file satisfies the same shape those do, the way
// plugins/ios/core_access_stub.go mirrors core_access_supported.go.
func newProcListPanel(vfs.PanelContext, *settingsStore) (vfs.PanelController, error) {
	return nil, errors.New("ProcList: the process list is not available on this platform yet")
}

func (p *Plugin) configure(vfs.App) {}
