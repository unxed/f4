package proclist

import (
	"sync"
	"time"
)

// sample is one process's data for one collection pass. Every platform's
// collect() (collector_linux.go, collector_windows.go, collector_darwin.go)
// fills the same fields; only how it gets them differs.
type sample struct {
	pid        int
	name       string
	rssKiB     uint64
	cpuPercent float64
	// start is when the process started, in the platform's own unit: clock
	// ticks since boot on Linux, microseconds since the epoch on macOS,
	// FILETIME's 100ns units on Windows. Only equality is ever asked of it
	// (procKey), so the unit never has to be converted. Zero means the
	// platform could not say -- a process this one may not open on Windows.
	start uint64
}

// procKey tells one process from another across refreshes. A pid alone
// does not: the OS hands a freed pid to the next process it starts --
// Windows does so readily -- and a selection or a suspend toggle left on
// the pid would then act on a stranger. Where start is unknown (zero) the
// key degrades to the pid alone, which is what it was before start existed.
type procKey struct {
	pid   int
	start uint64
}

func (s sample) key() procKey { return procKey{pid: s.pid, start: s.start} }

// tickSample is what collector remembers about a process between two
// collect calls, so the next one can turn a platform's cumulative
// kernel+user CPU-time counter into a CPU% for the interval between them.
// The unit differs per platform (Linux: USER_HZ clock ticks; Windows:
// FILETIME's 100ns units; macOS: nanoseconds), so each collect
// implementation picks its own divisor; ticks itself is opaque here.
type tickSample struct {
	ticks uint64
	at    time.Time
}

// collector accumulates the CPU-time deltas collect needs across calls. Its
// zero value is not usable; construct one with newCollector. The
// accumulator itself does not vary by platform -- only the collect method
// that fills it does, one per collector_<os>.go.
type collector struct {
	mu   sync.Mutex
	prev map[int]tickSample
}

func newCollector() *collector {
	return &collector{prev: make(map[int]tickSample)}
}
