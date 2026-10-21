//go:build darwin

package proclist

import (
	"bytes"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Supported reports whether this build can collect a real process list.
func Supported() bool { return true }

// collect enumerates every process the kernel reports through
// kern.proc.all and returns one sample per process. kern.proc.all's
// kinfo_proc gives a reliable PID and name (that is its actual purpose),
// but its memory/CPU accounting fields are widely known to be stale
// BSD-compatibility leftovers on modern XNU -- x/sys/unix's own KinfoProc
// even names the embedded Vmspace fields "Dummy" -- so CPU% and memory come
// from libproc's proc_pidinfo(PROC_PIDTASKINFO) instead, the same
// documented API Activity Monitor and ps use. A process this one lacks the
// entitlement to query still gets listed by PID and name, with zeroed CPU%
// and memory, the same way collect_windows.go handles a process it could
// not open.
func (c *collector) collect() ([]sample, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("proclist: sysctl kern.proc.all: %w", err)
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	seen := make(map[int]bool, len(procs))
	samples := make([]sample, 0, len(procs))
	for _, kp := range procs {
		pid := int(kp.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		name := commString(kp.Proc.P_comm[:])
		seen[pid] = true

		info, ok := readDarwinTaskInfo(pid)
		cpuPercent := 0.0
		var rssKiB uint64
		if ok {
			rssKiB = info.ResidentSize / 1024
			// ticks is in nanoseconds here (libproc's own unit); the divisor
			// below reflects that, not USER_HZ or FILETIME's 100ns.
			nanos := info.TotalUser + info.TotalSystem
			if prev, hasPrev := c.prev[pid]; hasPrev && nanos >= prev.ticks {
				if dt := now.Sub(prev.at).Seconds(); dt > 0 {
					cpuPercent = float64(nanos-prev.ticks) / 1e9 / dt * 100
				}
			}
			c.prev[pid] = tickSample{ticks: nanos, at: now}
		}
		samples = append(samples, sample{pid: pid, name: name, rssKiB: rssKiB, cpuPercent: cpuPercent, start: startStamp(kp.Proc.P_starttime)})
	}

	// Bound memory: forget the running total for processes that no longer exist.
	for pid := range c.prev {
		if !seen[pid] {
			delete(c.prev, pid)
		}
	}
	return samples, nil
}

// startStamp folds kinfo_proc's p_starttime into sample.start's single
// number: microseconds since the epoch. A time before the epoch is no real
// process's and reads as unknown (zero).
func startStamp(tv unix.Timeval) uint64 {
	if tv.Sec < 0 || tv.Usec < 0 {
		return 0
	}
	// #nosec G115 -- both fields were just checked non-negative, and seconds since the epoch times 1e6 stays far below 2^63.
	return uint64(tv.Sec)*1_000_000 + uint64(tv.Usec)
}

// commString trims a null-terminated comm buffer (kinfo_proc's P_comm) to a
// Go string.
func commString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// procTaskInfo mirrors <sys/proc_info.h>'s struct proc_taskinfo: 6 uint64
// fields followed by 12 int32 fields, 96 bytes on both amd64 and arm64 (no
// padding: the leading uint64 run keeps 8-byte alignment throughout, and 12
// is even, so the struct's own size is already a multiple of 8). Only the
// fields this package reads are named individually; readDarwinTaskInfo
// checks the byte count proc_pidinfo actually filled against
// unsafe.Sizeof(procTaskInfo{}) before trusting any of them, so a future
// ABI change that shrinks or reorders the struct is refused rather than
// silently misread.
type procTaskInfo struct {
	VirtualSize   uint64
	ResidentSize  uint64
	TotalUser     uint64
	TotalSystem   uint64
	ThreadsUser   uint64
	ThreadsSystem uint64
	_             [12]int32
}

// procPidTaskInfo is PROC_PIDTASKINFO from <sys/proc_info.h>.
const procPidTaskInfo = 4

// procInfoCallPidinfo is PROC_INFO_CALL_PIDINFO, the __proc_info call
// number libproc's proc_pidinfo wraps.
const procInfoCallPidinfo = 2

// sysProcInfo is SYS_proc_info from <sys/syscall.h>, 336 on both amd64 and
// arm64 and unchanged since the call appeared in 10.5. x/sys/unix carries it
// too, but marks every darwin syscall number deprecated in favour of
// libSystem wrappers, and the libSystem wrapper is exactly the call this
// file stopped making (see readDarwinTaskInfo).
const sysProcInfo = 336

// readDarwinTaskInfo is libproc's proc_pidinfo(pid, PROC_PIDTASKINFO, 0,
// &info, sizeof info), made as the __proc_info system call it wraps rather
// than through a dlopen'ed libproc. Package syscall's Syscall6 traps straight
// into the kernel on darwin, so nothing foreign runs on the Go stack: the
// earlier FFI call into libproc crashed the darwin/amd64 test run
// intermittently with a nil dereference inside this function. A process
// this one lacks the entitlement to query fails with EPERM and is reported
// as having no task info.
func readDarwinTaskInfo(pid int) (procTaskInfo, bool) {
	var info procTaskInfo
	size := unsafe.Sizeof(info)
	// #nosec G103 G115 -- the pointer is passed in the call expression itself, as package syscall requires; pid comes from kinfo_proc's own P_pid (int32) and is bounds-checked non-negative by the caller.
	n, _, errno := unix.Syscall6(sysProcInfo, procInfoCallPidinfo, uintptr(pid), procPidTaskInfo, 0,
		uintptr(unsafe.Pointer(&info)), size)
	if errno != 0 || n != size {
		return procTaskInfo{}, false
	}
	return info, true
}
