//go:build windows

package proclist

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this build can collect a real process list.
func Supported() bool { return true }

// windowsTicksPerSec is the unit GetProcessTimes reports kernel/user time
// in: FILETIME ticks are always 100ns, regardless of platform or clock rate
// (unlike Linux's USER_HZ), so this is a fixed constant rather than
// something read from the system.
const windowsTicksPerSec = 1e7

// collect enumerates every process currently visible through a Toolhelp32
// snapshot and returns one sample per process. A process this one lacks the
// access right to open (a system process, or another user's, without
// elevation) still gets listed by PID and name -- the same way Task Manager
// shows a process it cannot report CPU/memory for -- with zeroed CPU% and
// memory rather than being dropped from the list.
func (c *collector) collect() ([]sample, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("proclist: CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("proclist: Process32First: %w", err)
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	seen := make(map[int]bool)
	var samples []sample
	for {
		pid := int(entry.ProcessID)
		name := windows.UTF16ToString(entry.ExeFile[:])
		seen[pid] = true

		info, ok := readWindowsProcess(pid)
		cpuPercent := 0.0
		if ok {
			if prev, hasPrev := c.prev[pid]; hasPrev && info.ticks >= prev.ticks {
				if dt := now.Sub(prev.at).Seconds(); dt > 0 {
					cpuPercent = float64(info.ticks-prev.ticks) / windowsTicksPerSec / dt * 100
				}
			}
			c.prev[pid] = tickSample{ticks: info.ticks, at: now}
		}
		samples = append(samples, sample{pid: pid, name: name, rssKiB: info.rssKiB, cpuPercent: cpuPercent, start: info.start})

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	// Bound memory: forget ticks for processes that no longer exist.
	for pid := range c.prev {
		if !seen[pid] {
			delete(c.prev, pid)
		}
	}
	return samples, nil
}

// windowsProcessInfo is what readWindowsProcess reads from an opened
// process: cumulative kernel+user time and the creation time, both in
// FILETIME's 100ns units, and the working-set size.
type windowsProcessInfo struct {
	ticks  uint64
	start  uint64
	rssKiB uint64
}

// readWindowsProcess opens pid just long enough to read its times and
// working-set size. ok is false only when the process could not even be
// opened -- which the caller treats as "no times available yet", not as a
// reason to drop the process from the list.
func readWindowsProcess(pid int) (info windowsProcessInfo, ok bool) {
	// #nosec G115 -- pid comes from Toolhelp32's own ProcessID (uint32) and is converted back to it here.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return windowsProcessInfo{}, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err == nil {
		info.ticks = filetimeTicks(kernel) + filetimeTicks(user)
		info.start = filetimeTicks(creation)
	}

	var counters processMemoryCounters
	counters.Cb = uint32(unsafe.Sizeof(counters))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Cb))
	if r != 0 {
		info.rssKiB = uint64(counters.WorkingSetSize) / 1024
	}
	return info, true
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// psapi.dll's GetProcessMemoryInfo has no typed wrapper in
// golang.org/x/sys/windows, so this loads it the same way mem_windows.go
// (internal/sysinfo) loads kernel32's GlobalMemoryStatusEx: a lazy DLL plus
// a hand-written struct matching the documented PROCESS_MEMORY_COUNTERS
// layout. SIZE_T is 8 bytes on both Windows targets this module builds for
// (amd64, arm64; see build.yml's matrix), so the struct needs no
// architecture split.
var (
	psapiDLL                 = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapiDLL.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}
